package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// Worker is an LLM work agent (docs §4.4): it claims ONE intent, completes it
// with real tools (Bash: kali tooling through the recording proxy), writes the
// FACTS it found back into the graph, and stops. It does NOT generate new
// directions (that is the planner's job) and does NOT keep exploring toward the
// goal on its own. Multiple workers run concurrently as goroutines.
// WebSearchOpts is the web-search backend selection the server pushes into each
// agent (planner/worker/main). Enabled=false leaves the web_search tool off.
// Backend is "ddgs" (no key), "brave-free" (BraveKey required), "tavily"
// (TavilyKey required), or "deepseek" (DeepSeek* required, filled from the
// active LLM profile). It maps directly onto agentcore.Options.
// Proxy is a dedicated egress proxy for the search request (http/https/socks5),
// independent of the traffic-recording MITM proxy — set it when the search endpoint
// is only reachable via a VPN/SOCKS proxy. Empty = direct.
//
// 注意 deepseek 后端与其它三个的性质不同：DeepSeek 没有可直接调用的搜索接口，
// 搜索只存在于其 Anthropic 兼容 messages 接口内部(web_search_20250305 server
// tool)，因此每次搜索会消耗一次模型调用，且搜索请求由 DeepSeek 服务端发出——
// 不经过本机 Proxy，也不会进流量留痕。
type WebSearchOpts struct {
	Enabled   bool
	Backend   string
	BraveKey  string
	TavilyKey string
	Proxy     string
	// DeepSeek* 来自当前激活的 LLM 配置(仅 anthropic 格式的 DeepSeek 官方端点)，
	// 不单独配置，随 LLM 配置切换而变。
	DeepSeekBaseURL string
	DeepSeekAPIKey  string
	DeepSeekModel   string
}

type Worker struct {
	findingRecorder FindingRecorder
	prov            llm.Provider
	model           string
	workDir         string
	proxyAddr       string
	proxyCACert     string            // recording proxy's CA cert path (for WebFetch HTTPS verify)
	webSearch       WebSearchOpts     // web_search tool backend selection (off by default)
	tx              *transcript.Store // raw LLM conversation persistence (nil = off)
	window          int               // context window in tokens (for compaction)
	windowFn        func() int        // optional dynamic task-chain minimum
	maxTurns        int               // max agent turns per run (0 = unlimited)
	// runTimeout is the wall-clock budget for the main exploration of one intent
	// (0 = unlimited). When it fires, the run is cut and a settlement round is
	// forced so already-identified facts get written back instead of being lost.
	runTimeout time.Duration
	// extraTools are host-provided tools (e.g. traffic query, oast) appended to
	// the worker's graph write-back tools.
	extraTools []actool.CoreTool
	// injectConstraints resolves whether this task's operation constraints get
	// injected into the worker system prompt. Read per run so the settings toggle
	// takes effect without rebuilding the agent. nil = inject (default).
	injectConstraints func() bool
	// nonStreamingFn resolves whether this run uses the non-streaming (Complete)
	// path. Read per run so a profile/task toggle takes effect without rebuilding
	// the agent. nil = streaming (default).
	nonStreamingFn func() bool
	// noaEnabledFn resolves whether this run uses the experimental noa context-
	// compression mechanism. Read per run, like nonStreaming. nil = off (built-in
	// compaction).
	noaEnabledFn func() bool
	// maxTokensFn resolves the per-reply output cap in tokens, on the same
	// per-run basis. nil or 0 = send no cap and let the endpoint decide.
	maxTokensFn func() int
}

// WorkerSessionID returns the stable transcript key used by a worker intent.
// Worker slots are reusable, so the intent id (rather than work#N) is the
// session identity. Keep this helper public so the Worker message API and UI
// can refer to exactly the conversation that will be resumed.
func WorkerSessionID(explorationID, intentID int64) string {
	return fmt.Sprintf("exp%d-worker-i%d", explorationID, intentID)
}

const workerChatMarkerPrefix = "<!-- ARTEX_WORKER_CHAT:"

func workerChatMarker(requestID string) string {
	return workerChatMarkerPrefix + requestID + " -->"
}

func hasWorkerChatMessage(messages []llm.Message, requestID string) bool {
	marker := workerChatMarker(requestID)
	for _, message := range messages {
		if message.Role == llm.RoleUser && strings.Contains(message.Text(), marker) {
			return true
		}
	}
	return false
}

// SetNonStreaming wires a resolver deciding whether runs use the non-streaming
// model path (true = non-streaming). nil/unset = streaming (default). Read per
// run so a profile or task-chain toggle takes effect without rebuilding.
func (w *Worker) SetNonStreaming(fn func() bool) { w.nonStreamingFn = fn }

func (w *Worker) nonStreaming() bool { return w.nonStreamingFn != nil && w.nonStreamingFn() }

// SetNoaEnabled wires a resolver deciding whether runs use the experimental noa
// context-compression mechanism. nil/unset = off (built-in compaction). Read per
// run so the settings toggle takes effect without rebuilding the agent.
func (w *Worker) SetNoaEnabled(fn func() bool) { w.noaEnabledFn = fn }

// SetMaxTokens wires a resolver for the per-reply output cap. nil/unset or 0 =
// send no cap and let the endpoint decide. Read per run, like nonStreaming.
func (w *Worker) SetMaxTokens(fn func() int) { w.maxTokensFn = fn }

func (w *Worker) maxTokens() int {
	if w.maxTokensFn == nil {
		return 0
	}
	return w.maxTokensFn()
}

// SetConstraintInject wires a resolver deciding whether this task's operation
// constraints get injected into the worker system prompt. nil = inject (default).
func (w *Worker) SetConstraintInject(fn func() bool) { w.injectConstraints = fn }

// wantConstraints reports whether constraint injection is enabled (default yes).
func (w *Worker) wantConstraints() bool { return w.injectConstraints == nil || w.injectConstraints() }

// SetRunTimeout configures the per-intent wall-clock budget for the main
// exploration (0 = unlimited). When it fires, the SDK settlement phase still runs
// so facts are never lost to a timeout. Safe to call before Execute.
func (w *Worker) SetRunTimeout(run time.Duration) {
	w.runTimeout = run
}

// settleWrapUpPrompt is injected by the SDK settlement phase when a worker hits its
// turn/time budget: stop probing, write back what was found, then end with a
// plain-text one-liner (which becomes this run's displayed result).
const settleWrapUpPrompt = "이번 실행이 예산 소진으로 곧 종료됩니다. 더 이상 어떤 명령이나 탐지도 실행하지 마십시오. 다음 순서대로 처리하십시오. (1) 위에서 이미 식별했지만 아직 기록하지 않은 내용을 하나씩 기록합니다. 새 자산은 insert_assets, 탐색 결론과 사실은 record_fact, 확인된 취약점은 report_finding 으로 기록합니다. (2) **맨 마지막에 한 문장짜리 순수 텍스트로만** 무엇을 했고 어떤 핵심 결론을 얻었는지 한국어로 요약합니다. 이 한 문장이 이번 실행의 결과로 사용자에게 표시되므로 반드시 출력해야 합니다."

func NewWorker(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int, extra ...actool.CoreTool) *Worker {
	return &Worker{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns, extraTools: extra}
}

// defaultToolsExcept returns actool.DefaultTools() minus the named tools (by
// CoreTool.Name()). Used to trim SDK default tools an agent shouldn't have.
func defaultToolsExcept(exclude ...string) []actool.CoreTool {
	drop := make(map[string]bool, len(exclude))
	for _, n := range exclude {
		drop[n] = true
	}
	all := actool.DefaultTools()
	out := make([]actool.CoreTool, 0, len(all))
	for _, t := range all {
		if !drop[t.Name()] {
			out = append(out, t)
		}
	}
	return out
}

func (w *Worker) SetCompactionWindowResolver(fn func() int) { w.windowFn = fn }

func (w *Worker) compactionWindow() int {
	if w.windowFn != nil {
		return w.windowFn()
	}
	return w.window
}

// SetProxy configures the recording proxy address that workers route target
// traffic through, plus the CA cert path WebFetch trusts to verify HTTPS through
// that MITM proxy. Empty addr disables the hint.
func (w *Worker) SetProxy(addr, caCert string) { w.proxyAddr, w.proxyCACert = addr, caCert }

// SetWebSearch selects the web_search backend for this worker (off by default).
func (w *Worker) SetWebSearch(o WebSearchOpts) { w.webSearch = o }

// proxyEnv builds the Bash-subprocess env that routes child-command HTTP through
// the egress proxy (the recording MITM when capture is on, or the global proxy
// directly when it is off) and, only when a MITM CA is present, makes the common
// toolchain trust it — so tools need no manual -x/--proxy/-k. Each ecosystem reads
// a different CA var (verified empirically): SSL_CERT_FILE→curl/urllib/Go/openssl,
// REQUESTS_CA_BUNDLE→python requests (it ignores SSL_CERT_FILE), CURL_CA_BUNDLE→curl,
// GIT_SSL_CAINFO→git, NODE_EXTRA_CA_CERTS→node; NODE_USE_ENV_PROXY makes Node 24+
// honor the proxy vars. ALL_PROXY is set too so a socks5 egress proxy (which curl
// only reads from ALL_PROXY, not HTTP(S)_PROXY) works in the capture-off path.
// Empty proxyAddr → nil (direct, unchanged env).
func proxyEnv(proxyAddr, caCert string) []string {
	if proxyAddr == "" {
		return nil
	}
	env := []string{
		"HTTP_PROXY=" + proxyAddr, "HTTPS_PROXY=" + proxyAddr,
		"http_proxy=" + proxyAddr, "https_proxy=" + proxyAddr,
		"ALL_PROXY=" + proxyAddr, "all_proxy=" + proxyAddr, // socks5 egress: curl reads only this
		"NODE_USE_ENV_PROXY=1", // Node 24+: honor HTTP(S)_PROXY in built-in fetch/http
	}
	if caCert != "" {
		env = append(env,
			"SSL_CERT_FILE="+caCert,
			"CURL_CA_BUNDLE="+caCert,
			"REQUESTS_CA_BUNDLE="+caCert,
			"GIT_SSL_CAINFO="+caCert,
			"NODE_EXTRA_CA_CERTS="+caCert,
		)
	}
	return env
}

// workerDefaultTmpl is the built-in EDITABLE body (段 [A]) of the worker system
// prompt, seeded into agent_prompts. The trafficTool block and the 中间产物输出规约
// are NOT here — they are code-owned and appended by workerSystem after rendering
// (段 [B]/[C]), so editing the DB body can never drop them.
const workerDefaultTmpl = `너는 사이버보안 플랫폼의 승인된 침투 테스트 시스템의 "실행자"(work agent)다. 너는 【의도 하나】(한 문장짜리 탐색 방향)를 받았고, 유일한 역할은 **이 의도 하나를 완수하고, 발견을 지식 그래프에 기록한 뒤, 멈추고 반환하는 것**이다.

**경계(레드라인)**:
1. **네가 받은 이 의도 하나만 수행한다**. **이 의도를 탐색하다가 이 의도 밖에서 더 파 볼 가치가 있는 단서를 발견하면**(오류로 누출된 경로, 다른 자산과 연동될 수 있는 지점, 또 다른 공략 체인의 입구로 의심되는 것), **fact 의 summary 에 한마디 적어 계획자에게 넘겨라**.
2. 처음 막힌 것(payload 필터됨 / 404 / 주입 회신 없음)은 다 탐색했다는 뜻이 아니다 — 이 의도의 모든 우회 수단을 다 쓴 뒤 결론을 내라;
3. 승인된 범위 안에서만 조작한다. 시스템 프롬프트 상단에 【작업 제약】이 붙어 있으면 그것이 최우선 레드라인이다: 모든 명령/탐지를 실행하기 전에 자기점검을 하고, 위반하면 하지 마라(네가 받은 의도 안에 있더라도).

**발견하면서 바로 기록하라**(그래프에 써야 유효하고, 머릿속/글로만 있는 것은 무효다; 결과를 하나 얻을 때마다 즉시 쓰고, 마지막까지 쌓아 두다 단계 소진으로 잃지 마라). 세 가지 기록 방식이 있으니 그래프를 섞지 마라:
- **새 자산/자원 → insert_assets(자산 그래프)**: 서브도메인 / service / endpoint / 핑거프린트 / 자격증명 등 모든 자산 【자체】. **여기엔 자산만 등록하고; 탐색 결론/판단은 여기 쓰지 말고 record_fact 를 써라.**
- **탐색 결론/사실 → record_fact(탐색 그래프, intent_id 전달)**: 전부 이것을 쓴다. **여러 관찰을 【하나의】 사실로 모아라**(summary 는 한 문장 요약 + detail 은 실제 실행 과정에 근거한 요약의 확장), 속성마다 한 건씩 쪼개지 말고, 한 의도는 보통 하나만 — 잘게 쪼개면 그래프가 무한히 커진다. **기본은 하나로 쓰고, detail 에 합칠 수 있는 건 다 합쳐라**; 【서로 완전히 독립적이고 병합 불가한】 결론이 확실히 있을 때만 facts 배열로 나누되, 이는 극소수 예외이지 상례가 아니다. **증분만 기록하라**: 이번에 【새로 얻은】 것만 기록하고, 기존 사실을 표현만 바꿔 다시 쓰지 마라(기존 것을 확인만 하고 새것이 없으면 기록할 필요 없다). **실제로 본 것만 써라**: evidence 를 주고(한 줄: 명령 + 가장 잘 증명하는 한두 줄 출력, 간결하게, 세부는 detail 에), confidence 를 표기하라(observed=직접 봄 / inferred=현상으로 추론).
- **확인된 취약점 → report_finding(탐색 그래프, PoC 포함, intent_id 전달)**: **이번에 실제로 유발해 재현 가능한 증거(요청/응답 또는 명령 출력)를 얻었을 때만 사용**. "버전/핑거프린트가 CVE 와 매칭" "파라미터가 주입 가능해 보임" "외부 취약점 DB/업데이트 로그/코드 diff 로 추론"을 확인으로 간주하는 것을 엄금하며, CVE DB 조회나 패치 버전 비교로 실제 유발을 대체하지도 마라. 유발 못 했으나 의심되면 → record_fact 로 inferred 사실 하나를 기록해(의심 지점 + 왜 유발 못 했는지) 계획자에게 넘기고, 억지로 finding 으로 기록하지 마라.


이 의도를 완수한 뒤 무엇을 했고 어떤 사실을 기록했는지 한 문장으로 요약하라.`

// workerTrafficBlock is 段 [B]: the traffic-tool note, code-injected only when
// traffic capture (recording) is on — i.e. the traffic_* tools actually exist.
// Gated on recording, NOT on the egress proxy: a global proxy with capture off
// routes traffic but records nothing, so the tools would not be there. Not stored,
// not editable.
func workerTrafficBlock(recording bool) string {
	if !recording {
		return ""
	}
	return "\n\n**트래픽 도구**:\n- traffic_search / traffic_get / traffic_blob: 응답을 되짚고 이미 접근한 자원을 찾는다, **먼저 트래픽을 조회하고 같은 URL 을 반복 curl 하지 마라**. traffic_search 는 **반드시 host 를 지정**해야 하며, 기본으로 극히 가벼운 인덱스 3건(id/method/url/status/resp_len, 응답 내용 없음)만 반환하니 더 필요하면 limit 을 명시적으로 키워라; body_contains 로 요청/응답 본문 전문 검색이 가능하다(최소 3자, 부분 문자열과 한글/한자 지원, 예: 비밀번호/키/에러/내부망 주소 찾기); 특정 항목 원문은 traffic_get(id) 로 보되, 초대형 본문은 @blob sha256:<hash> 로 표시되며 traffic_blob(hash) 로 나눠 전문을 가져온다."
}

// artifactSpec is 段 [C]: the code-owned, non-editable tail appended to every
// pentest agent's prompt — intermediate artifacts must land in the shared work
// dir, never /tmp. Guaranteed present regardless of how the DB body is edited.
func artifactSpec(dir string) string {
	return "\n\n**중간 산출물 출력 규약**: 스크립트·payload·캡처한 응답 본문·임시 데이터 등 모든 중간 산출물은 **일괄 본 과제 작업 디렉터리 " + dir + " 에 쓴다**(상대 경로가 곧 여기에 기록되며, 이 절대 경로를 써도 됨) —— **/tmp 나 다른 절대 경로에 쓰지 마라**。"
}

// workerArtifactSpec is the worker's 段 [C]: its per-intent run dir is pre-created
// by the engine (ensureRunDir), so it just writes relative paths there — no manual
// mkdir, no cross-worker name collisions.
func workerArtifactSpec(runDir string) string {
	return "\n\n**중간 산출물 출력 규약**: 스크립트·payload·캡처한 응답 본문·임시 데이터 등 모든 중간 산출물은 **일괄 이번 의도의 전용 작업 디렉터리 " + runDir + " 에 쓴다**(이미 자동 생성되어 있으니 상대 경로로 바로 여기에 쓰면 되고, 수동으로 디렉터리를 만들 필요 없음) —— **/tmp 나 다른 절대 경로에 쓰지 마라**。"
}

// ensureRunDir builds and creates an agent's working directory under base:
// <base>/tasks/<taskID> for planner/main; <base>/tasks/<taskID>/i<intentID> for a
// worker (intentID<=0 → task dir only). The "tasks/" segment groups per-task dirs
// symmetrically with the chat agent's "sessions/<sessionID>". Best-effort mkdir — on
// failure, writes fail the same way an unwritable CWD would.
func ensureRunDir(base string, taskID, intentID int64) string {
	dir := filepath.Join(base, "tasks", strconv.FormatInt(taskID, 10))
	if intentID > 0 {
		dir = filepath.Join(dir, "i"+strconv.FormatInt(intentID, 10))
	}
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// cmdOutDir is the SDK large-tool-output spill dir under an agent's run dir.
func cmdOutDir(dir string) string { return filepath.Join(dir, "cmd-output") }

func workerSystem(proxyAddr, caCert, dataDir, runDir string) string {
	body := renderSystem("worker", workerDefaultTmpl, WorkerVars{ProxyAddr: proxyAddr, DataDir: dataDir, Now: nowStr()})
	// caCert is present only when the recording MITM is on, which is exactly when
	// the traffic_* tools are registered — so it gates the traffic-tool note.
	// Optional finding guidance is added for every role after tool resolution.
	return body + workerTrafficBlock(caCert != "") + workerArtifactSpec(runDir) + langDirective()
}

// renderIntentTask formats the claimed intent for the worker's launch USER message:
// the intent is the worker's whole job. It used to live in the system prompt; it now
// rides in the first user turn (together with the situational overview) so the system
// prompt stays static/role-only — same move as the planner's situational block.
// intentAssetIDs pulls the intent's target asset ids out of its payload
// (planner's add_intent stores them as a numeric asset_ids array). nil on absence
// or malformed payload.
func intentAssetIDs(intent *db.Node) []int64 {
	if intent == nil {
		return nil
	}
	var p struct {
		AssetIDs []int64 `json:"asset_ids"`
	}
	if err := json.Unmarshal(intent.Payload, &p); err != nil {
		return nil
	}
	return p.AssetIDs
}

func renderIntentTask(intent *db.Node) string {
	return fmt.Sprintf("\n\n【네가 받은 의도(이번의 유일한 과제: 이 하나만, 사실만 생성, 완료 즉시 정지)】:\n%s\n의도 id: %d(record_fact / report_finding 기록 시 전달)", string(intent.Payload), intent.ID)
}

// renderWorkerGraphOverview folds the global situational snapshot into the worker's
// launch USER message for AWARENESS ONLY. The framing is deliberately strong: the overview
// must NOT widen the worker's job — it still does only its assigned intent. Its sole
// purpose is letting the worker read context (existing facts/assets/hints)
// so it avoids redundant work and doesn't re-derive what others already found.
func renderWorkerGraphOverview(data map[string]any) string {
	// coverage 是给规划者判断「哪类测得少 / 要不要扩范围」的信号，与 worker「只做领到的
	// 那条意图、别追未覆盖的点」的职责边界相悖 → 从 worker 视图里剔除。data 是本次 worker
	// 专属的新 map，删键不影响 planner。
	delete(data, "coverage")
	b, err := json.Marshal(data)
	if err != nil {
		return "" // fall back silently: the worker just won't have the global context
	}
	return "\n\n【전역 탐색 상황(읽기 전용, 네 의도를 큰 그림 안에서 보도록 도움)】:\n" +
		"아래는 과제 전체의 현재 탐색 개황이다. 용도는 둘: 하나는 남이 이미 발견한 것을 알아 중복을 피하는 것, 둘은 네 의도를 탐색할 때 그것과 전역의 관계를 연상하게 하는 것.\n" +
		"**발산은 좋은 것**: 네 의도를 탐색할 때 깊이 생각하고 많이 연상하라. 유일한 선은 —— 실제로 다른 의도를 실행하지 말 것(그건 다른 worker 의 일이며 계획자가 조율한다). 가치 있는 단서를 연상하면(자산 간 연동, 또 다른 공략 체인 입구로 의심되는 것, 전역 수준의 의심점), **반드시 fact 에 써서 계획자에게 넘겨라** —— 이것은 네 중요한 산출물이지 있어도 그만인 게 아니다. 혼자 삼키느니 하나 더 보고해 계획자가 판단하게 하라.\n" +
		string(b)
}

// Execute runs one intent. hooks (the per-task Guard) gates every tool call; may
// be nil. emit, if non-nil, receives one ActivityRecord per execution step.
// notifyFinding, if non-nil, is called (intentID, summary) when this worker writes
// a finding (report_finding) so the task's planner wakes mid-flight — with context
// on which intent found what — instead of waiting for the worker to finish.
// Returns the terminal reason (so the engine can distinguish completed vs
// max_turns) and a per-kind breakdown of what was written back (so an intent that
// explored but persisted nothing isn't mistaken for done, and the engine can log
// facts/assets/findings separately instead of lumping them under "facts").
func (w *Worker) Execute(ctx context.Context, name string, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, intent *db.Node, hooks harness.HookRunner, emit func(db.Activity), enr EnrichTrigger, notifyFinding func(int64, string)) (harness.TerminalReason, WriteCounts, error) {
	return w.execute(ctx, name, taskID, as, ts, intent, hooks, emit, enr, notifyFinding, "", "")
}

// ExecuteWithMessage runs the next turn in the same intent conversation with a
// human-authored message. The HTTP handler does not edit the transcript;
// agentcore records the message as a normal user turn when this Worker starts.
// This keeps Worker continuation identical to the regular agent chat flow.
func (w *Worker) ExecuteWithMessage(ctx context.Context, name string, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, intent *db.Node, hooks harness.HookRunner, emit func(db.Activity), enr EnrichTrigger, notifyFinding func(int64, string), requestID, message string) (harness.TerminalReason, WriteCounts, error) {
	return w.execute(ctx, name, taskID, as, ts, intent, hooks, emit, enr, notifyFinding, strings.TrimSpace(requestID), strings.TrimSpace(message))
}

func (w *Worker) execute(ctx context.Context, name string, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, intent *db.Node, hooks harness.HookRunner, emit func(db.Activity), enr EnrichTrigger, notifyFinding func(int64, string), requestID, message string) (harness.TerminalReason, WriteCounts, error) {
	tsx := NewToolSet(ts, name)
	tsx.SetFindingRecorder(w.findingRecorder)
	tsx.SetTaskID(taskID)
	coverageEnabled := as == nil || as.CoverageEnabled(taskID)
	tsx.SetCoverageEnabled(coverageEnabled)
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetOwnerNode(intent.ID)         // assets this worker discovers anchor to its intent → visible to the task
	tsx.SetEnrich(enr)                  // async DNS/HTTP auto-completion for assets this worker writes
	tsx.SetNotifyFinding(notifyFinding) // report_finding 落库时当场唤醒 planner，带上「哪个意图+finding」
	// base = built-in worker tools ∪ host tools (traffic) ∪ default tools (incl. Bash);
	// then augment with the agent's visible skills/MCP. During the SDK settlement
	// phase, Bash is hidden via Settlement.DisabledTools (no local gating needed).
	base := append(tsx.WorkerTools(), w.extraTools...)
	// worker 刻意不给 MultiEdit/Glob/Grep：文件精改用 Edit、检索走 Bash(grep/find)，
	// 收敛工具面、减少低价值调用。其余 SDK 默认工具(Read/Write/Edit/LS/Bash/Sleep)照常。
	base = append(base, defaultToolsExcept("MultiEdit", "Glob", "Grep")...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts), IntentID: intent.ID})
	tools, def, cleanup := AugmentTools(ctx, "worker", base)
	defer cleanup()

	// 意图是 worker 的【唯一职责、贯穿整个 run 的不变量】→ 连同启动指令、意图锚定的目标资产
	// 原始数据一起放进 system prompt：system 每次 run 都重新拼一遍、绝不会被 compaction 压掉，
	// 长 run 里意图永远在场，续跑时也不依赖 transcript 历史是否留住那条首消息。代价是 system
	// 混入 per-intent 易变数据、失去跨意图缓存复用；这是刻意的取舍（意图丢失比省 token 严重得多）。
	// 与 planner「态势块放 user turn」分叉是有意的：planner 本身是产意图的那个、没有单一 mandate，
	// worker 有。仅【全局态势 overview】留在启动 user 消息里——它可降级、容忍 stale，压掉无碍。
	// 本次意图的专属工作目录 <workDir>/tasks/<taskID>/i<intentID>，引擎侧先建好。
	runDir := ensureRunDir(w.workDir, taskID, intent.ID)
	// The run-wide intent is not the current tool action. Do not forward it or
	// inherit a parent run's background into the action reviewer.
	ctx = intercept.WithReviewContext(ctx, runDir, intercept.ReviewBackground{})
	overview := renderWorkerGraphOverview(tsx.graphOverviewData())
	sysBody := workerSystem(w.proxyAddr, w.proxyCACert, w.workDir, runDir)
	if w.wantConstraints() {
		sysBody += constraintBlock(ts) // 操作约束(若有)注入系统提示,worker 执行时严格遵守
	}
	// 意图块 → 意图锚定资产块 → 启动指令，依次追加到 system 尾部（与 constraintBlock 同一套追加法）。
	sysBody += renderIntentTask(intent)
	if as != nil {
		if ids := intentAssetIDs(intent); len(ids) > 0 {
			if assets, err := as.GetByIDs(ids); err == nil && len(assets) > 0 {
				if b, err := json.Marshal(assets); err == nil {
					sysBody += "\n\n이 의도의 asset_ids 에 대응하는 대상 자산:\n" + string(b)
				}
				// 意图明确针对的这些资产 → 自动纳入任务测试范围（与 insertAssets 同一套
				// 保守粒度）。upsertTaskScope 的 ON CONFLICT DO NOTHING + uq_task_scope
				// 唯一索引保证不会重复添加；重跑/重试同样是幂等 no-op。
				// 资产覆盖度功能关闭时不再累积测试范围(分母)。
				if coverageEnabled {
					for _, a := range assets {
						_ = as.AddAutoScope(taskID, a.Type, a.Domain, a.URL, a.IP)
					}
				}
			}
		}
	}
	sysBody += "\n\n위의 이 의도를 실행 시작: 그것만, 사실·assets·finding 만 생성, 완료 즉시 정지."
	system, boundary := deferredSystem(sysBody, def)
	// 任务级 deadline(经 ctx 注入)夹逼本 run 的墙钟预算 + 决定收尾词(见 taskclock.go)。
	tc := taskClockFrom(ctx)
	maxDur, clamped := clampMaxDuration(tc.DeadlineUnix, w.runTimeout)
	settle := wrapupSettlement("worker", []string{"Bash"})
	if tc.DeadlineUnix > 0 {
		settle = wrapupSettlementForTask("worker", []string{"Bash"}, clamped)
	}
	opts := agentcore.Options{
		Provider:        w.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		// WebFetch 走记录代理，其 HTTP 与 curl 一样被留痕；载入代理 CA 让经 MITM
		// 重签的 HTTPS 证书能【正常校验通过】（而非关掉校验）。proxy 空则直连。
		EnableWebFetch: true,
		WebFetchProxy:  w.proxyAddr,
		WebFetchCACert: w.proxyCACert,
		// 联网搜索(可选)。ddgs 无需 key；brave-free 需 BraveKey；tavily 需 TavilyKey。
		// WebSearchProxy 是独立的出口代理(http/https/socks5)，与记录流量的 MITM 代理无关；空则直连。
		EnableWebSearch:       w.webSearch.Enabled,
		WebSearchBackend:      w.webSearch.Backend,
		BraveSearchAPIKey:     w.webSearch.BraveKey,
		TavilySearchAPIKey:    w.webSearch.TavilyKey,
		DeepSeekSearchBaseURL: w.webSearch.DeepSeekBaseURL,
		DeepSeekSearchAPIKey:  w.webSearch.DeepSeekAPIKey,
		DeepSeekSearchModel:   w.webSearch.DeepSeekModel,
		WebSearchProxy:        w.webSearch.Proxy,
		// Bash 子命令的 HTTP 默认走记录代理 + 信任其 CA（工具无需 -x/-k）。
		BashEnv:    proxyEnv(w.proxyAddr, w.proxyCACert),
		WorkingDir: runDir,
		MaxTurns:   w.maxTurns, // 0 = unlimited (configurable in agent management)
		// 墙钟预算,轮边界判,不打断半路;0 = 不限。有任务级 deadline 时夹逼到 min(自身预算,
		// 距 deadline 剩余),让本 run 在任务到点时自然进收尾(见 taskclock.go)。
		MaxDuration: maxDur,
		// 命中预算(轮次 OR 时长)→ SDK 跑一轮收尾(隐藏 Bash),把已识别的写回,避免烂尾。
		// clamped(被任务 deadline 夹逼)时用 PromptByReason:因超时=任务到点→任务超时词,
		// 因步数=夹逼窗口内步数先耗尽→回落 per-run 词。非 clamped 维持纯 per-run。
		Settlement: settle,
		// large tool output spills to cmd-output/ with a head + pointer (SDK tool.Capture);
		// full output preserved on disk. 截断上限用 SDK 默认(30000 字符)。
		ToolOutputDir: cmdOutDir(runDir),
		Compaction:    compactionConfig(w.compactionWindow()), // long tool-heavy runs stay within the window
		Todos:         actool.NewTodoStore(),                  // 会话级临时待办（TodoWrite），纯规划用，退出即丢
		NonStreaming:  w.nonStreaming(),                       // 该 profile 选非流式时走 Provider.Complete
		MaxTokens:     w.maxTokens(),                          // 0 = 不发上限,由服务端默认值决定
	}
	if hooks != nil { // typed-nil guard: only set when concrete (avoids harness panic)
		opts.Hooks = hooks
	}
	if w.tx != nil { // persist raw LLM conversation; one file per worked intent
		opts.Transcript = w.tx
		opts.SessionID = WorkerSessionID(ts.ID(), intent.ID)
	}
	intentID := intent.ID
	emitWrap := func(r db.Activity) {
		if emit != nil {
			r.NodeID, r.Worker = &intentID, name
			emit(r)
		}
	}
	// 意图 / 启动指令 / 意图锚定资产已随 system prompt 下发（见上方 sysBody 组装）。
	// 这条启动 user 消息只承载【全局态势 overview】——可降级的了解大局信息，压掉无碍。
	// overview 罕见地 marshal 失败为空时，回退一句启动词，避免首轮出现空 user 消息。
	input := overview
	if strings.TrimSpace(input) == "" {
		input = "system 에서 받은 의도를 실행 시작: 그것만, 사실·assets·finding 만 생성, 완료 즉시 정지."
	}

	// 实验功能:开启后由 noa 接管上下文压缩(归档集中在 <workDir>/noa/<SessionID> 下,持久)。
	noaSession := WorkerSessionID(ts.ID(), intent.ID)
	enableNoa(&opts, w.noaEnabledFn, w.workDir, noaSession, noaWarn(noaSession))
	ctx = attachSideCapture(ctx, &opts)
	s := agentcore.NewSession(opts)
	defer s.Close() // release the session's background-task manager (temp dir + processes)

	// Resume prior conversation if this intent was paused/blocked/exhausted and is
	// being re-run. The transcript ID is deterministic per intent, so if a prior
	// session exists the worker continues from where it left off instead of
	// restarting from scratch.
	alreadyRecorded := false
	if w.tx != nil {
		_ = s.Resume(opts.SessionID)
		alreadyRecorded = requestID != "" && hasWorkerChatMessage(s.Messages(), requestID)
		if len(s.Messages()) > 0 && message == "" {
			seedUnlockFromHistory(s.Messages(), def.UnlockSkill)
			input = "계속 실행."
		} else if len(s.Messages()) > 0 {
			seedUnlockFromHistory(s.Messages(), def.UnlockSkill)
		}
	}
	if message != "" {
		if alreadyRecorded {
			input = "지난번 수동 대화 입력의 새 의도를 계속 실행. 이미 완료한 동작을 반복하지 마라."
		} else if len(s.Messages()) > 0 {
			input = workerChatMarker(requestID) + "\n【수동 대화 입력의 새 의도】\n" + message +
				"\n\n이 수동 입력을 즉시 실행하고, 완료 후 컨텍스트에 따라 원 과제를 계속할지 결정하라."
		} else {
			input += "\n\n" + workerChatMarker(requestID) + "\n【수동 대화 입력의 새 의도】\n" + message +
				"\n\n이 수동 입력을 우선 실행하라."
		}
	}

	// Budgets + settlement are owned by the SDK (MaxTurns/MaxDuration + Settlement):
	// on hit it runs a wrap-up turn and finishes with ReasonMaxTurns/ReasonTimeout.
	// MaxDuration now interrupts an in-flight tool at the wall-clock deadline and
	// enters the wrap-up phase on the live ctx, so a run whose tool overran the budget
	// still settles (no external hard-timeout backstop needed). ctx itself carries only
	// pause / planner kill / shutdown, which the engine distinguishes and re-queues/stops.
	_, reason, err := captureRunSession(ctx, s, input, emitWrap)
	return reason, tsx.Writes(), err
}
