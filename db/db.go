// Package db is the PostgreSQL data source for ARTEX (取代旧 graph 单文件 SQLite)。
// 它打开连接、应用 schema、并 seed 内置 agent 与变量目录。
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Autumn-27/artex/config"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // pgx database/sql driver ("pgx")
)

//go:embed schema.sql
var schemaSQL string

const schemaMigrationLockKey int64 = 7337741001

var schemaDeadlockRetryDelays = [...]time.Duration{
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
}

type schemaExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func isPostgresDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

func applySchemaWithRetry(ctx context.Context, execer schemaExecer, sleep func(time.Duration)) error {
	for attempt := 0; ; attempt++ {
		if _, err := execer.ExecContext(ctx, schemaSQL); err != nil {
			if !isPostgresDeadlock(err) || attempt >= len(schemaDeadlockRetryDelays) {
				return err
			}
			sleep(schemaDeadlockRetryDelays[attempt])
			continue
		}
		return nil
	}
}

// withSchemaMigrationLock pins the session-level lock to one checked-out
// connection. Running pg_advisory_lock through *sql.DB is incorrect because a
// later schema or unlock call may use a different pooled PostgreSQL session.
func withSchemaMigrationLock(ctx context.Context, sqlDB *sql.DB, action func(*sql.Conn) error) (err error) {
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, schemaMigrationLockKey); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	defer func() {
		if _, unlockErr := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, schemaMigrationLockKey); unlockErr != nil && err == nil {
			err = fmt.Errorf("advisory unlock: %w", unlockErr)
		}
	}()
	return action(conn)
}

// coordinateWithSchemaMigration makes long, multi-table archive transactions
// mutually exclusive with startup DDL while allowing ordinary runtime queries
// to continue normally.
func coordinateWithSchemaMigration(tx *sql.Tx) error {
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, schemaMigrationLockKey); err != nil {
		return fmt.Errorf("coordinate with schema migration: %w", err)
	}
	return nil
}

// DSN resolves the PostgreSQL connection string and reports where it came from.
// Precedence: env ARTEX_PG_DSN > config file (config.json). There is no
// built-in default — it errors if neither source is configured.
func DSN() (dsn, source string, err error) {
	return config.PostgresDSN()
}

// DB wraps the shared *sql.DB. PG handles its own connection pool + concurrency
// (MVCC), so unlike the old SQLite store there is no process-wide write mutex.
type DB struct{ *sql.DB }

// ensureDatabase connects to the postgres system database and creates the target
// database if it does not exist. dsn must be a postgres:// URL.
func ensureDatabase(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil // unparseable DSN — let the normal Open fail with a clear error
	}
	dbName := strings.TrimPrefix(u.Path, "/")
	if dbName == "" || dbName == "postgres" {
		return nil
	}
	// connect to the postgres maintenance database instead
	adminDSN := *u
	adminDSN.Path = "/postgres"
	admin, err := sql.Open("pgx", adminDSN.String())
	if err != nil {
		return nil // best-effort; let Open surface the real error
	}
	defer admin.Close()
	if err := admin.Ping(); err != nil {
		return nil
	}
	var exists bool
	_ = admin.QueryRow(`SELECT true FROM pg_database WHERE datname=$1`, dbName).Scan(&exists)
	if !exists {
		if _, err := admin.Exec(`CREATE DATABASE "` + dbName + `"`); err != nil {
			return fmt.Errorf("create database %q: %w", dbName, err)
		}
	}
	return nil
}

// Open connects, applies the schema (idempotent), and seeds builtin rows.
func Open(dsn string) (*DB, error) {
	if err := ensureDatabase(dsn); err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping postgres (%s): %w", config.Redact(dsn), err)
	}
	d := &DB{sqlDB}
	// pgx runs multi-statement Exec via the simple protocol when there are no args.
	// Keep the dedicated lock connection checked out until both DDL and seeding
	// finish so concurrent application instances cannot initialize out of order.
	err = withSchemaMigrationLock(context.Background(), sqlDB, func(conn *sql.Conn) error {
		if err := applySchemaWithRetry(context.Background(), conn, time.Sleep); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
		if err := d.seedBuiltins(); err != nil {
			return fmt.Errorf("seed builtins: %w", err)
		}
		return nil
	})
	if err != nil {
		sqlDB.Close()
		return nil, err
	}
	return d, nil
}

// builtinAgent describes one of the fixed agents and its prompt-variable catalog.
type builtinAgent struct {
	key, name, role, desc string
	vars                  []promptVar
	interactiveShell      bool // 建行时的默认交互式 shell 开关；ON CONFLICT 不覆盖用户后续手动开关
	runSeconds            *int // 建行时的单次 run 墙钟上限(秒)；nil=用种子默认(1200)，0=不限时
}

type promptVar struct{ name, desc, example, source string }

// intp 返回 v 的指针，用于给 builtinAgent 可选字段(如 runSeconds)显式取值。
func intp(v int) *int { return &v }

// builtinAgents mirrors docs §5(a). 内置工具不入库；这里只 seed agent + 变量目录。
// 注：planner/worker/mainagent/auto 的交互式 shell 默认由下方 interactive_shell_default_v1
// 块统一置 true（尊重后续 toggle）；这里的 interactiveShell 只给需要「建行即默认开」的新 agent。
var builtinAgents = []builtinAgent{
	{"goals", "목표 분해", "goals", "침투 작업 목표를 독립적이고 검증 가능한 여러 하위 목표로 분해합니다.", []promptVar{
		{"EngagementDescription", "작업 설명(테스트 대상/배경)", "example.com 사이트 테스트", "exploration"},
		// Now 是全局 runtime 变量(见 server.globalPromptVars),不再在各 agent 目录里
		// 重复定义,否则 withGlobalVars 追加时会与全局项撞名。
	}, false, nil},
	{"planner", "계획", "planner", "상황을 읽고 목표를 판정하며, 아직 다루지 않은 새 방향이 있을 때만 탐색 의도를 보충합니다(작업당 계획 루프 1회).", []promptVar{
		{"Goal", "작업 총목표", "example.com 관리자 권한 확보", "exploration"},
		{"AssetSummary", "자산 수/유형 분포 요약(선택)", "domain:3 ip:5 site:2", "distilled"},
	}, false, nil},
	{"mainagent", "메인", "main", "인간-기계 인터페이스: 진행 상황을 관찰하고 사용자의 의도를 힌트 또는 고우선순위 의도로 반영합니다.", []promptVar{
		{"Goal", "현재 작업 목표", "example.com 관리자 권한 확보", "exploration"},
		{"AssetSummary", "초기 상황 요약(선택)", "domain:3 ip:5", "distilled"},
		{"FindingsSummary", "확인된 취약점 요약(선택)", "high:1 medium:2", "distilled"},
	}, false, nil},
	{"worker", "실행", "worker", "의도를 하나 받아 실행하고, 발견한 사실/취약점을 지식 그래프에 기록한 뒤 종료합니다.", []promptVar{
		{"ProxyAddr", "프록시 주소 기록(if 이중 문구 구동)", "127.0.0.1:8080", "runtime"},
		{"WorkerName", "worker 자기 식별자(선택)", "worker-1", "runtime"},
	}, false, nil},
	// Auto:内置「平台操作」agent。不参与渗透编排循环,经对话页驱动,用工具操作平台。
	{"auto", "Auto", "assistant", "플랫폼 운영 도우미: 도구로 작업(생성/조회/일시정지/힌트 제공)과 자산을 관리하고, skill·사용자 정의 도구·MCP를 생성/수정할 수 있습니다.", nil, false, nil},
	// 침투 테스트:内置「独立渗透」agent。经对话页驱动,一人从侦察到收尾走完整条渗透链,自己계획自己执行自己验证。默认开启交互式 shell。
	{"pentest", "침투 테스트", "assistant", "독립 침투 에이전트: 정찰→공격 표면 탐색→심층 활용→검증→마무리까지 한 사람이 전 과정을 수행하며 스스로 계획·실행·대항적 검증합니다.", nil, true, intp(0)},
}

// seedBuiltins inserts the fixed built-in agents and their variable catalog (idempotent).
func (d *DB) seedBuiltins() error {
	for _, a := range builtinAgents {
		var agentID int64
		err := d.QueryRow(`
INSERT INTO agents(key, name, description, role, builtin, enabled, interactive_shell, run_seconds)
VALUES ($1, $2, NULLIF($3,''), $4, true, true, $5, COALESCE($6, 1200))
ON CONFLICT (key) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description
RETURNING id`, a.key, a.name, a.desc, a.role, a.interactiveShell, a.runSeconds).Scan(&agentID)
		if err != nil {
			return fmt.Errorf("agent %s: %w", a.key, err)
		}
		for _, v := range a.vars {
			if _, err := d.Exec(`
INSERT INTO agent_prompt_vars(agent_id, var_name, description, example, source)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (agent_id, var_name) DO UPDATE
  SET description = EXCLUDED.description, example = EXCLUDED.example, source = EXCLUDED.source`,
				agentID, v.name, v.desc, v.example, v.source); err != nil {
				return fmt.Errorf("agent %s var %s: %w", a.key, v.name, err)
			}
		}
	}
	// Drop catalog entries for variables that were renamed, so the white-list no
	// longer advertises a name templates can't resolve (EngagementTitle→Description).
	// 'Now' 从各 agent 目录提升为全局 runtime 变量后,旧库里 goals 仍残留一条 'Now'
	// 会与全局项撞名(前端变量列表 key 重复);一并清掉。
	if _, err := d.Exec(`DELETE FROM agent_prompt_vars WHERE var_name IN ('EngagementTitle', 'CoverageGaps', 'Now')`); err != nil {
		return fmt.Errorf("cleanup renamed vars: %w", err)
	}
	// Default-on interactive_shell for the runtime agents (planner/worker/mainagent/auto)
	// ONCE — respects a later user toggle-off (guarded by a settings flag). goals(one-shot
	// decomposer) stays off. Runs after the column exists (schema applied before seed).
	if v, _, _ := d.GetSetting("interactive_shell_default_v1"); v != "true" {
		if _, err := d.Exec(`UPDATE agents SET interactive_shell=true WHERE key IN ('planner','worker','mainagent','auto')`); err != nil {
			return fmt.Errorf("seed interactive_shell defaults: %w", err)
		}
		_ = d.SetSetting("interactive_shell_default_v1", "true")
	}
	// Seed the built-in browser (Playwright) MCP once — DISABLED by default (用户
	// 需要时自行启用), no proxy by default. The traffic-capture toggle injects/strips
	// the recording proxy + CA at runtime (server.Manager.syncBrowserMCPProxy).
	// Insert only if absent so we never clobber user edits (args/env/enabled/
	// visibility) on restart.
	if _, err := d.Exec(`
INSERT INTO mcp_servers(name, transport, command, args, env, enabled)
VALUES ('browser', 'stdio', 'npx', $1, '{}', false)
ON CONFLICT (name) DO NOTHING`,
		`["@playwright/mcp","--headless"]`); err != nil {
		return fmt.Errorf("seed browser mcp: %w", err)
	}
	// NOTE: the placeholder ScopeSentry data-source MCP (empty URL + empty X-API-Key,
	// disabled) is seeded directly in schema.sql §F so a raw `psql < schema.sql` init
	// also gets it. schema.sql is Exec'd on every startup, so it stays idempotent.
	if err := d.seedBuiltinSkillVisibility(); err != nil {
		return fmt.Errorf("seed skill visibility: %w", err)
	}
	if err := d.seedDefaultInterceptRules(); err != nil {
		return fmt.Errorf("seed intercept rules: %w", err)
	}
	if err := d.seedDefaultInterceptRulesV2(); err != nil {
		return fmt.Errorf("seed intercept rules v2: %w", err)
	}
	if err := d.seedDefaultInterceptRulesV3(); err != nil {
		return fmt.Errorf("seed intercept rules v3: %w", err)
	}
	if err := d.seedDefaultInterceptRulesV4(); err != nil {
		return fmt.Errorf("seed intercept rules v4: %w", err)
	}
	if err := d.seedDefaultAssetInterceptRulesV2(); err != nil {
		return fmt.Errorf("seed asset intercept rules v2: %w", err)
	}
	return nil
}

// Korean text for the built-in intercept rules seeded by v1-v3, keyed by the
// rule's stable regex pattern. Names and numeric ids are not reliable keys:
// ids differ per install and a name may be reworded between versions, while the
// pattern is what actually distinguishes one rule from another.
var builtinInterceptRuleTextKo = []struct {
	pattern string
	name    string
	message string
}{
	{pattern: `(?i)\brm\b.{0,80}(?:-[a-z]*r[a-z]*f[a-z]*|-[a-z]*f[a-z]*r[a-z]*|--recursive|--no-preserve-root)`, name: "[내장] 재귀 강제 삭제 rm -rf", message: "재귀 강제 삭제 실행 금지 (rm -rf / rm --recursive) — 시스템이나 대상 환경을 영구적으로 손상시킬 수 있습니다"},
	{pattern: `\brm\b[^"'\n]{0,60}["'\s](/|/etc|/bin|/usr|/boot|/var|/lib|/sys|/proc|/dev|/sbin|/root)`, name: "[내장] 시스템 핵심 디렉터리 삭제", message: "시스템 핵심 경로 삭제 금지"},
	{pattern: `\bmkfs\b`, name: "[내장] 디스크 포맷 mkfs", message: "디스크 포맷 금지 (mkfs)"},
	{pattern: `\bdd\b[^|\n]{0,100}\bof=\s*/dev/[a-zA-Z]`, name: "[내장] 디스크 장치 덮어쓰기 dd", message: "dd 로 디스크 장치 덮어쓰기 금지"},
	{pattern: `:\(\)\s*\{[^}]*:\|:`, name: "[내장] 포크 폭탄", message: "포크 폭탄 실행 금지"},
	{pattern: `\b(?:shutdown|reboot|halt|poweroff|init\s+[06])\b`, name: "[내장] 종료 / 재부팅", message: "종료 또는 재부팅 명령 실행 금지"},
	{pattern: `\bkill\s+-9\s+-1\b|\bkillall\s+-9\b`, name: "[내장] 모든 프로세스 종료", message: "kill -9 -1 또는 killall -9 금지 (모든 프로세스 종료)"},
	{pattern: `\b(?:shred|wipe)\b[^|\n]{0,80}/dev/[a-zA-Z]`, name: "[내장] 디스크 지우기 shred / wipe", message: "디스크 장치에 shred/wipe 지우기 실행 금지"},
	{pattern: `\biptables\s+(?:-F|--flush)\b|\bnft\s+flush\s+ruleset\b`, name: "[내장] 방화벽 규칙 초기화", message: "방화벽 규칙 초기화 금지 (iptables -F / nft flush)"},
	{pattern: `(?i)\bDROP\s+(?:DATABASE|TABLE|SCHEMA|INDEX|VIEW|TABLESPACE|USER|ROLE)\b`, name: "[내장] SQL DROP DATABASE / TABLE / SCHEMA", message: "DROP 작업 실행 금지 — 데이터베이스 객체를 되돌릴 수 없이 파괴할 수 있습니다"},
	{pattern: `(?i)\bTRUNCATE\s+(?:TABLE\s+)?\w`, name: "[내장] SQL TRUNCATE", message: "TRUNCATE 실행 금지 — 데이터 테이블의 모든 데이터를 비울 수 있습니다"},
	{pattern: `(?i)\.(?:dropDatabase|dropCollection|drop)\s*\(`, name: "[내장] MongoDB drop / dropDatabase", message: "MongoDB drop 작업 실행 금지"},
	{pattern: `(?i)\b(?:FLUSHALL|FLUSHDB)\b`, name: "[내장] Redis FLUSHALL / FLUSHDB", message: "Redis FLUSHALL / FLUSHDB 실행 금지 — 전체 캐시 데이터를 비울 수 있습니다"},
	{pattern: `(?i)\bcurl\b[^|\n&;"]{0,300}(?:-X\s*DELETE|--request\s+DELETE|-XDELETE)|\bwget\b[^|\n&;"]{0,300}--method[=\s]+DELETE`, name: "[내장] curl / wget 로 DELETE 요청 전송", message: "curl/wget 로 HTTP DELETE 요청 전송 금지 — 대상 시스템 데이터를 삭제할 수 있습니다"},
	{pattern: `(?i)\b(?:requests|httpx|aiohttp|urllib\.request)\.delete\s*\(|session\.delete\s*\(|client\.delete\s*\(`, name: "[내장] Python HTTP 클라이언트 DELETE (requests/httpx/aiohttp)", message: "Python HTTP 클라이언트로 DELETE 요청 전송 금지"},
	{pattern: `(?i)axios\.delete\s*\(|method\s*[:=]\s*['"]DELETE['"]`, name: "[내장] 스크립트 내 HTTP DELETE 메서드 선언 (JS/범용)", message: "스크립트에서 HTTP DELETE 요청을 선언·전송하는 것 금지"},
	{pattern: `(?i)/(?:clear|wipe|flush|purge|truncate|drop|destroy|factory[-_]reset|reset[-_]all)(?:[/?#"'\s]|$)`, name: "[내장] 일괄 초기화 / 삭제 인터페이스 경로", message: "일괄 초기화·파괴류 인터페이스 호출 금지 (/clear /wipe /flush /purge 등)"},
}

// seedDefaultInterceptRulesV4 backfills Korean text for the built-in intercept
// rules that earlier seeds inserted in Chinese. Those seeds use
// `ON CONFLICT DO NOTHING` behind one-shot setting flags, so an install that
// already ran them keeps the Chinese rows permanently — fixing the literals
// only reaches fresh installs. This migration matches on `pattern` and rewrites
// only the display columns, leaving user toggles (enabled, priority,
// timeout_action) and user-created rules untouched.
func (d *DB) seedDefaultInterceptRulesV4() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v4"); v == "done" {
		return nil
	}
	for _, r := range builtinInterceptRuleTextKo {
		if _, err := d.Exec(`
UPDATE intercept_rules SET name = $2, message = $3
WHERE pattern = $1 AND (name LIKE '%[内置]%' OR message ~ '[一-龥]')`,
			r.pattern, r.name, r.message); err != nil {
			return fmt.Errorf("rule text %q: %w", r.name, err)
		}
	}
	return d.SetSetting("intercept_default_rules_v4", "done")
}

// seedDefaultAssetInterceptRulesV2 backfills Korean text for the four built-in
// asset blocklist entries seeded in Chinese by v1. Same one-shot-flag problem
// as the intercept rules above.
func (d *DB) seedDefaultAssetInterceptRulesV2() error {
	if v, _, _ := d.GetSetting("asset_intercept_default_rules_v2"); v == "done" {
		return nil
	}
	for _, r := range []struct{ pattern, note string }{
		{".gov", "[내장] 정부 웹사이트 (.gov)"},
		{".gov.cn", "[내장] 정부 웹사이트 (.gov.cn)"},
		{".edu", "[내장] 교육 웹사이트 (.edu)"},
		{".edu.cn", "[내장] 교육 웹사이트 (.edu.cn)"},
	} {
		if _, err := d.Exec(`
UPDATE asset_intercept_rules SET note = $2
WHERE pattern = $1 AND note ~ '[一-龥]'`, r.pattern, r.note); err != nil {
			return fmt.Errorf("asset rule note %q: %w", r.note, err)
		}
	}
	return d.SetSetting("asset_intercept_default_rules_v2", "done")
}

// seedDefaultAssetInterceptRules inserts the built-in asset blocklist (fuzzy
// domain matches for government / education sites) once on first startup. Gated
// by a settings flag so a user's later disable/delete is never resurrected on
// restart — same policy as the intercept-rule seed.
func (d *DB) seedDefaultAssetInterceptRules() error {
	if v, _, _ := d.GetSetting("asset_intercept_default_rules_v1"); v == "done" {
		return nil
	}
	rules := []struct {
		kind    string
		pattern string
		note    string
	}{
		{"fuzzy_domain", ".gov", "[내장] 정부 웹사이트 (.gov)"},
		{"fuzzy_domain", ".gov.cn", "[내장] 정부 웹사이트 (.gov.cn)"},
		{"fuzzy_domain", ".edu", "[내장] 교육 웹사이트 (.edu)"},
		{"fuzzy_domain", ".edu.cn", "[내장] 교육 웹사이트 (.edu.cn)"},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO asset_intercept_rules(enabled, kind, pattern, note, builtin)
VALUES (true, $1, $2, $3, true)
ON CONFLICT DO NOTHING`, r.kind, r.pattern, r.note); err != nil {
			return fmt.Errorf("asset rule %q: %w", r.pattern, err)
		}
	}
	return d.SetSetting("asset_intercept_default_rules_v1", "done")
}

// builtinSkillVisibility maps a shipped skill's directory name → the built-in
// agent keys that should see it by default. The skill FILES themselves live on the
// filesystem (SkillDir, loaded by norma at runtime); DB only carries this visibility
// binding. Skills omitted here (e.g. playwright-cli, scopesentry) ship invisible by
// default — the user turns them on per-agent when needed. scopesentry additionally
// declares `mcps: ScopeSentry`, which only takes effect once it's made visible and
// that MCP is enabled/configured.
var builtinSkillVisibility = map[string][]string{
	"api-recon": {"auto", "pentest", "worker"},
}

// seedBuiltinSkillVisibility binds the shipped built-in skills to their default
// agents. Insert-if-absent (ON CONFLICT DO NOTHING) so a user's later toggle-off is
// never resurrected on restart — matches the browser-MCP / intercept-rule seed policy.
func (d *DB) seedBuiltinSkillVisibility() error {
	for skillName, agentKeys := range builtinSkillVisibility {
		for _, key := range agentKeys {
			if _, err := d.Exec(`
INSERT INTO agent_skill_visibility(agent_id, skill_name, enabled)
SELECT id, $2, true FROM agents WHERE key=$1
ON CONFLICT (agent_id, skill_name) DO NOTHING`, key, skillName); err != nil {
				return fmt.Errorf("skill %s → agent %s: %w", skillName, key, err)
			}
		}
	}
	return nil
}

// seedDefaultInterceptRules inserts built-in safety intercept rules once on
// first startup. The seed is gated by a settings flag so user edits (disable,
// delete, re-order) are never overwritten on subsequent restarts.
func (d *DB) seedDefaultInterceptRules() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v1"); v == "done" {
		return nil
	}
	type rule struct {
		name     string
		target   string // tool_name | tool_input
		typ      string // string | regex
		pattern  string
		action   string
		message  string
		priority int
	}
	rules := []rule{
		// ── 系统破坏性命令 (priority 100) ──────────────────────────────────
		{
			name:     "[내장] 재귀 강제 삭제 rm -rf",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\brm\b.{0,80}(?:-[a-z]*r[a-z]*f[a-z]*|-[a-z]*f[a-z]*r[a-z]*|--recursive|--no-preserve-root)`,
			action:   "deny",
			message:  "재귀 강제 삭제 실행 금지 (rm -rf / rm --recursive) — 시스템이나 대상 환경을 영구적으로 손상시킬 수 있습니다",
			priority: 100,
		},
		{
			name:     "[내장] 시스템 핵심 디렉터리 삭제",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\brm\b[^"'\n]{0,60}["'\s](/|/etc|/bin|/usr|/boot|/var|/lib|/sys|/proc|/dev|/sbin|/root)`,
			action:   "deny",
			message:  "시스템 핵심 경로 삭제 금지",
			priority: 100,
		},
		{
			name:     "[내장] 디스크 포맷 mkfs",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\bmkfs\b`,
			action:   "deny",
			message:  "디스크 포맷 금지 (mkfs)",
			priority: 100,
		},
		{
			name:     "[내장] 디스크 장치 덮어쓰기 dd",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\bdd\b[^|\n]{0,100}\bof=\s*/dev/[a-zA-Z]`,
			action:   "deny",
			message:  "dd 로 디스크 장치 덮어쓰기 금지",
			priority: 100,
		},
		{
			name:     "[내장] 포크 폭탄",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `:\(\)\s*\{[^}]*:\|:`,
			action:   "deny",
			message:  "포크 폭탄 실행 금지",
			priority: 100,
		},
		{
			name:     "[내장] 종료 / 재부팅",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\b(?:shutdown|reboot|halt|poweroff|init\s+[06])\b`,
			action:   "deny",
			message:  "종료 또는 재부팅 명령 실행 금지",
			priority: 100,
		},
		{
			name:     "[내장] 모든 프로세스 종료",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\bkill\s+-9\s+-1\b|\bkillall\s+-9\b`,
			action:   "deny",
			message:  "kill -9 -1 또는 killall -9 금지 (모든 프로세스 종료)",
			priority: 100,
		},
		{
			name:     "[내장] 디스크 지우기 shred / wipe",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\b(?:shred|wipe)\b[^|\n]{0,80}/dev/[a-zA-Z]`,
			action:   "deny",
			message:  "디스크 장치에 shred/wipe 지우기 실행 금지",
			priority: 100,
		},
		{
			name:     "[내장] 방화벽 규칙 초기화",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\biptables\s+(?:-F|--flush)\b|\bnft\s+flush\s+ruleset\b`,
			action:   "deny",
			message:  "방화벽 규칙 초기화 금지 (iptables -F / nft flush)",
			priority: 100,
		},
		// ── 数据库破坏性操作 (priority 90) ─────────────────────────────────
		{
			name:     "[내장] SQL DROP DATABASE / TABLE / SCHEMA",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bDROP\s+(?:DATABASE|TABLE|SCHEMA|INDEX|VIEW|TABLESPACE|USER|ROLE)\b`,
			action:   "deny",
			message:  "DROP 작업 실행 금지 — 데이터베이스 객체를 되돌릴 수 없이 파괴할 수 있습니다",
			priority: 90,
		},
		{
			name:     "[내장] SQL TRUNCATE",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bTRUNCATE\s+(?:TABLE\s+)?\w`,
			action:   "deny",
			message:  "TRUNCATE 실행 금지 — 데이터 테이블의 모든 데이터를 비울 수 있습니다",
			priority: 90,
		},
		{
			name:     "[내장] MongoDB drop / dropDatabase",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\.(?:dropDatabase|dropCollection|drop)\s*\(`,
			action:   "deny",
			message:  "MongoDB drop 작업 실행 금지",
			priority: 90,
		},
		{
			name:     "[내장] Redis FLUSHALL / FLUSHDB",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\b(?:FLUSHALL|FLUSHDB)\b`,
			action:   "deny",
			message:  "Redis FLUSHALL / FLUSHDB 실행 금지 — 전체 캐시 데이터를 비울 수 있습니다",
			priority: 90,
		},
		// ── HTTP 破坏性请求 (priority 80) ──────────────────────────────────
		// Agent 发送 DELETE 请求的三种常见方式：
		//   1. curl -X DELETE / --request DELETE（Bash 工具直接执行或写入脚本）
		//   2. Python HTTP 客户端 .delete() 方法
		//   3. JS/通用脚本里的 method: 'DELETE' / method="DELETE"
		{
			name:     "[내장] curl / wget 로 DELETE 요청 전송",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bcurl\b[^|\n&;"]{0,300}(?:-X\s*DELETE|--request\s+DELETE|-XDELETE)|\bwget\b[^|\n&;"]{0,300}--method[=\s]+DELETE`,
			action:   "deny",
			message:  "curl/wget 로 HTTP DELETE 요청 전송 금지 — 대상 시스템 데이터를 삭제할 수 있습니다",
			priority: 80,
		},
		{
			name:     "[내장] Python HTTP 클라이언트 DELETE (requests/httpx/aiohttp)",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\b(?:requests|httpx|aiohttp|urllib\.request)\.delete\s*\(|session\.delete\s*\(|client\.delete\s*\(`,
			action:   "deny",
			message:  "Python HTTP 클라이언트로 DELETE 요청 전송 금지",
			priority: 80,
		},
		{
			name:     "[내장] 스크립트 내 HTTP DELETE 메서드 선언 (JS/범용)",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)axios\.delete\s*\(|method\s*[:=]\s*['"]DELETE['"]`,
			action:   "deny",
			message:  "스크립트에서 HTTP DELETE 요청을 선언·전송하는 것 금지",
			priority: 80,
		},
		{
			name:     "[내장] 일괄 초기화 / 삭제 인터페이스 경로",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)/(?:clear|wipe|flush|purge|truncate|drop|destroy|factory[-_]reset|reset[-_]all)(?:[/?#"'\s]|$)`,
			action:   "deny",
			message:  "일괄 초기화·파괴류 인터페이스 호출 금지 (/clear /wipe /flush /purge 등)",
			priority: 80,
		},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
VALUES ($1, true, $2, $3, $4, $5, $6, $7, false, 60, 'deny')
ON CONFLICT DO NOTHING`,
			r.name, r.priority, r.target, r.typ, r.pattern, r.action, r.message,
		); err != nil {
			return fmt.Errorf("rule %q: %w", r.name, err)
		}
	}
	return d.SetSetting("intercept_default_rules_v1", "done")
}

// seedDefaultInterceptRulesV2 migrates the two safety patterns that used to be
// hard-coded in guard.go (destructive shell + data-exfil pipe) into ordinary
// intercept rules. Gated by its own flag so it also lands on DBs that already ran
// v1. Unlike the old guard.go floor, these are plain [内置] rules — the user can
// disable or delete them. The exfil rule ships DISABLED by default (its
// curl/wget/nc pipe pattern mis-fires on legitimate CTF/pentest reverse-shell and
// data-transfer pipes); enable it manually when exfil gating is actually wanted.
func (d *DB) seedDefaultInterceptRulesV2() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v2"); v == "done" {
		return nil
	}
	rules := []struct {
		name     string
		pattern  string
		action   string
		message  string
		enabled  bool
		priority int
	}{
		{
			name:     "[내장] 파괴적 시스템 명령",
			pattern:  `(?i)\b(rm\s+-rf\s+/|mkfs|dd\s+if=|:\(\)\s*\{|shutdown|reboot|>\s*/dev/sd)`,
			action:   "deny",
			message:  "파괴적 명령 거부됨 (rm -rf / / mkfs / dd / 포크 폭탄 / 종료·재부팅 / 디스크 장치 덮어쓰기)",
			enabled:  true,
			priority: 100,
		},
		{
			name:     "[내장] 데이터 유출 파이프",
			pattern:  `(?i)(curl|wget|nc|ncat)\b[^|]*\b(\|\s*(curl|wget|nc))`,
			action:   "deny",
			message:  "데이터 유출 의심 파이프 거부됨 (명령 출력을 curl/wget/nc 로 외부 전송)",
			enabled:  false,
			priority: 80,
		},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
VALUES ($1, $2, $3, 'tool_input', 'regex', $4, $5, $6, false, 60, 'deny')
ON CONFLICT DO NOTHING`,
			r.name, r.enabled, r.priority, r.pattern, r.action, r.message,
		); err != nil {
			return fmt.Errorf("rule %q: %w", r.name, err)
		}
	}
	return d.SetSetting("intercept_default_rules_v2", "done")
}

// seedDefaultInterceptRulesV3 adds the delete-endpoint path rule. The v1 HTTP rules
// only catch the DELETE *method* (curl -X DELETE, requests.delete(, method:'DELETE'),
// and v1's path rule covers only /clear /wipe /flush /purge /truncate /drop /destroy
// /factory-reset /reset-all — so a plain `curl 'http://t/api/user/delete?id=1'` (a
// delete endpoint reached with GET/POST, which is how most web apps expose deletion)
// slipped through every built-in rule. Own flag so it also lands on DBs that already
// ran v1/v2, where editing the v1 seed would have no effect.
//
// The pattern deliberately requires a separator after the verb so /delivery,
// /details, /delta and /delegate do not match, while /deleteAll, /delete_user and
// /delete-user do. destroy is re-covered here because v1's rule does not allow a
// suffix (/destroyAll was missed).
//
// Exported as a package const only so the seeded regex is unit-testable without a DB.
const deleteEndpointPathPattern = `(?i)/(?:(?:delete|remove|unlink|erase|destroy)[-\w]*|del)(?:[/?#"'\s]|$)`

func (d *DB) seedDefaultInterceptRulesV3() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v3"); v == "done" {
		return nil
	}
	const name = "[내장] 삭제류 인터페이스 경로"
	if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
SELECT $1, true, 80, 'tool_input', 'regex', $2, 'deny', $3, false, 60, 'deny'
WHERE NOT EXISTS (SELECT 1 FROM intercept_rules WHERE name = $1)`,
		name,
		deleteEndpointPathPattern,
		"삭제류 인터페이스 호출 금지 (/delete /remove /unlink /erase 등) — HTTP 메서드와 무관합니다. 대부분의 애플리케이션은 삭제 인터페이스를 GET/POST 로도 트리거할 수 있고, 이 역시 대상 데이터를 실제로 삭제합니다",
	); err != nil {
		return fmt.Errorf("rule %q: %w", name, err)
	}
	return d.SetSetting("intercept_default_rules_v3", "done")
}
