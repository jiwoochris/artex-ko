package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// Planner is the event-driven LLM planner (docs §4.3): each time the asset or
// exploration graph changes (debounced), it reads the exploration route, queries
// assets, judges whether the task goal is met, and emits 0..N exploration intents
// into the frontier. It is the sole intent generator.
type Planner struct {
	findingRecorder   FindingRecorder
	prov              llm.Provider
	model             string
	tx                *transcript.Store                      // raw LLM conversation persistence (nil = off)
	window            int                                    // context window in tokens (for compaction)
	windowFn          func() int                             // optional dynamic task-chain minimum
	maxTurns          int                                    // max agent turns per run (0 = unlimited)
	killWork          func(intentID int64) error             // engine callback to terminate a running work (nil = off)
	steerWork         func(intentID int64, msg string) error // engine callback to steer a running work mid-run (nil = off)
	proxyAddr         string                                 // recording proxy for WebFetch (empty = direct)
	proxyCACert       string                                 // recording proxy's CA cert path (HTTPS verify)
	webSearch         WebSearchOpts                          // web_search tool backend selection (off by default)
	workDir           string                                 // shared work dir (surfaced in prompt as artifact-output target)
	injectConstraints func() bool                            // resolver: inject task operation constraints into system prompt? (nil = yes)
	nonStreamingFn    func() bool                            // resolver: use non-streaming (Complete) path? (nil = streaming)
	noaEnabledFn      func() bool                            // resolver: use experimental noa compaction? (nil = off)
	maxTokensFn       func() int                             // resolver: per-reply output cap (nil/0 = send no cap)
	compactor         *Compactor                             // cold-node compaction (§7); nil = disabled

	// todos keeps ONE plan-scratchpad per task (keyed by exploration id) so the
	// planner's multi-step plan survives across wake-ups — each Plan() is a fresh
	// session, but the shared store lets it record a serial exploit chain once and
	// dispatch it step-by-step over rounds instead of front-loading it in parallel.
	todoMu sync.Mutex
	todos  map[int64]*actool.TodoStore
}

func NewPlanner(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int) *Planner {
	return &Planner{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns, todos: map[int64]*actool.TodoStore{}}
}

func (p *Planner) SetCompactionWindowResolver(fn func() int) { p.windowFn = fn }

// SetCompactor wires the cold-node compactor (cold-digest §7). Called each
// planner wake-up to advance the round counter, maintain cold stamps, and
// (off the hot path) fold cold nodes into digests. nil = feature disabled.
func (p *Planner) SetCompactor(c *Compactor) { p.compactor = c }

// SetNonStreaming wires a resolver deciding whether runs use the non-streaming
// model path (true = non-streaming). nil/unset = streaming (default).
func (p *Planner) SetNonStreaming(fn func() bool) { p.nonStreamingFn = fn }

func (p *Planner) nonStreaming() bool { return p.nonStreamingFn != nil && p.nonStreamingFn() }

// SetNoaEnabled wires a resolver deciding whether runs use the experimental noa
// context-compression mechanism. nil/unset = off (built-in compaction). Read per
// run so the settings toggle takes effect without rebuilding the agent.
func (p *Planner) SetNoaEnabled(fn func() bool) { p.noaEnabledFn = fn }

// SetMaxTokens wires a resolver for the per-reply output cap. nil/unset or 0 =
// send no cap and let the endpoint decide. Read per run, like nonStreaming.
func (p *Planner) SetMaxTokens(fn func() int) { p.maxTokensFn = fn }

func (p *Planner) maxTokens() int {
	if p.maxTokensFn == nil {
		return 0
	}
	return p.maxTokensFn()
}

func (p *Planner) compactionWindow() int {
	if p.windowFn != nil {
		return p.windowFn()
	}
	return p.window
}

// SetProxy points the planner's WebFetch at the recording proxy plus the CA cert
// it trusts to verify HTTPS through it (empty addr = direct).
func (p *Planner) SetProxy(addr, caCert string) { p.proxyAddr, p.proxyCACert = addr, caCert }

// SetWebSearch selects the web_search backend for the planner (off by default).
func (p *Planner) SetWebSearch(o WebSearchOpts) { p.webSearch = o }

// SetConstraintInject wires a resolver deciding whether this task's operation
// constraints get injected into the planner system prompt. Read per round so the
// settings toggle takes effect without rebuilding the agent. nil = inject (default).
func (p *Planner) SetConstraintInject(fn func() bool) { p.injectConstraints = fn }

// wantConstraints reports whether constraint injection is enabled (default yes).
func (p *Planner) wantConstraints() bool { return p.injectConstraints == nil || p.injectConstraints() }

// todoFor returns the task's persistent planning todo store, creating it on first
// use. Shared across all of this task's planner wake-ups.
func (p *Planner) todoFor(expID int64) *actool.TodoStore {
	p.todoMu.Lock()
	defer p.todoMu.Unlock()
	s := p.todos[expID]
	if s == nil {
		s = actool.NewTodoStore()
		p.todos[expID] = s
	}
	return s
}

// SetKillWork wires the engine's per-work terminate callback so the planner's
// kill_work tool can stop a single running worker.
func (p *Planner) SetKillWork(fn func(intentID int64) error) { p.killWork = fn }

// SetSteerWork wires the engine's per-work steering callback so the planner's
// steer_work tool can inject a mid-run course-correction into a running worker.
func (p *Planner) SetSteerWork(fn func(intentID int64, msg string) error) { p.steerWork = fn }

// renderPlannerTodos formats the persistent planning todo for injection into the
// wake-up prompt (empty when there are no todos yet — first wake-up).
func renderPlannerTodos(items []actool.Todo) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【너의 계획 할 일(기상 간 유지, 지난 라운드에 네가 쓴 것)】:\n")
	for _, it := range items {
		mark := map[actool.TodoStatus]string{actool.TodoPending: "☐", actool.TodoInProgress: "▶", actool.TodoCompleted: "✔"}[it.Status]
		if mark == "" {
			mark = "☐"
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", mark, it.Content))
	}
	b.WriteString("이에 따라 진행하라: 【선행 단계가 완료됐거나 / 의존하는 fact 가 이미 존재하는】 다음 단계에만 의도를 낸다; TodoWrite 로 목록을 갱신하라(fact 로 충족된 단계를 completed 로 표기). 목록에 이미 pending/in_progress 인 단계를 중복해 내지 마라.")
	return b.String()
}

// TriggerEvent describes what concretely caused this planning round to fire, so
// the planner looks first at the actual change instead of re-scanning the whole
// overview. Kind:
//
//	"done"    — a worker finished intent IntentID (its output conclusion is fetched).
//	"finding" — a worker reported a finding on intent IntentID (Detail = 摘要).
//	"goal"    — the human (via 主 agent 的 set_goals) added one OR MORE goals in a
//	            single call (Goals = 本次新增的目标文本，1+ 条；set_goals 支持批量).
//	"goal_deleted" — the human deleted a goal from 总览的目标管理 (Detail = 被删目标文本).
//	"goal_edited"  — the human edited a goal from 总览的目标管理 (OldGoal→NewGoal 文本).
//	"cancelled" — the human deleted intent IntentID (Detail = 删除原因). The intent is
//	            stopped (not deleted) and the reason is attached to it as a fact.
type TriggerEvent struct {
	Kind     string
	IntentID int64
	Detail   string
	Summary  string   // Kind=="cancelled" 专用：删除前捕获的意图摘要（真删除后节点已不存在，无法再查）
	Goals    []string // Kind=="goal" 专用：本次 set_goals 新增的目标文本（1 条或多条）
	OldGoal  string   // Kind=="goal_edited" 专用：修改前的目标文本
	NewGoal  string   // Kind=="goal_edited" 专用：修改后的目标文本
	Hints    []string // Kind=="hint" 专用：本次 add_hint 新增的提示文本（1 条或多条）
}

// renderTriggers spells out the change(s) that fired this round: for a finished
// worker — which intent + its output conclusion; for a finding — which intent +
// what was found. Empty for time/heartbeat wakes. Reads the store (best-effort;
// a blank field never blocks the round).
func renderTriggers(ts *db.ExplorationStore, evs []TriggerEvent) string {
	if len(evs) == 0 || ts == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【이번 라운드를 트리거한 실제 변동(여기를 먼저 보고 방향 보충 여부를 결정)】:")
	for _, ev := range evs {
		switch ev.Kind {
		case "goal":
			if len(ev.Goals) == 1 {
				b.WriteString(fmt.Sprintf("\n- 사람(메인 agent)이 목표 하나를 추가함: %s —— 새로 달성할 목표이니, 이에 따라 탐색 방향을 보충하라(대응 의도가 아직 없다면).", ev.Goals[0]))
			} else {
				b.WriteString(fmt.Sprintf("\n- 사람(메인 agent)이 목표 %d개를 추가함: %s —— 모두 새로 달성할 목표이니, 대응 의도가 아직 없는 목표마다 탐색 방향을 보충하라.", len(ev.Goals), strings.Join(ev.Goals, "；")))
			}
		case "hint":
			if len(ev.Hints) == 1 {
				b.WriteString(fmt.Sprintf("\n- 사람(메인 agent)이 전략 힌트 하나를 추가함: %s —— 탐색 그래프에 걸렸으니, 이에 따라 탐색 방향을 조정/보충하라(대응 의도가 아직 없다면).", ev.Hints[0]))
			} else {
				b.WriteString(fmt.Sprintf("\n- 사람(메인 agent)이 전략 힌트 %d개를 추가함: %s —— 모두 탐색 그래프에 걸렸으니, 각각에 따라 탐색 방향을 조정/보충하라.", len(ev.Hints), strings.Join(ev.Hints, "；")))
			}
		case "goal_deleted":
			b.WriteString(fmt.Sprintf("\n- 사람이 그 목표를 삭제함: %s —— 그 목표가 제거됐으니, 이에 따라 남은 목표/방향을 재판단하라(더 이상 그것을 위해 의도를 내지 말 것).", ev.Detail))
		case "goal_edited":
			b.WriteString(fmt.Sprintf("\n- 사람이 목표를 수정함, 「%s」에서 「%s」로 변경 —— 새 목표에 따라 탐색 방향을 조정하라(원 방향이 더 이상 맞지 않으면 그만 낼 것).", ev.OldGoal, ev.NewGoal))
		case "finding":
			b.WriteString(fmt.Sprintf("\n- 의도 #%d(%s)의 worker 가 finding 하나를 보고함: %s", ev.IntentID, intentSummary(ts, ev.IntentID), ev.Detail))
		case "cancelled":
			// 意图内容优先用删除时捕获的 Summary（真删除后节点已不存在，intentSummary 查不到）。
			sm := ev.Summary
			if sm == "" {
				sm = intentSummary(ts, ev.IntentID)
			}
			b.WriteString(fmt.Sprintf("\n- 의도 #%d 가 사용자에 의해 삭제됨, 의도 내용: %s, 삭제 사유: %s. 그 의도는 삭제됨(더 이상 실행 안 됨); 이에 따라 다시 계획하라.", ev.IntentID, sm, ev.Detail))
		default: // "done"
			b.WriteString(fmt.Sprintf("\n- 의도 #%d(%s)의 worker 가 종료, 출력 결론: %s", ev.IntentID, intentSummary(ts, ev.IntentID), workerOutput(ts, ev.IntentID)))
			if fids := factIDsYielded(ts, ev.IntentID); fids != "" {
				b.WriteString(fmt.Sprintf("; 이 의도가 새로 생성한 사실 id: %s ", fids))
			}
		}
	}
	b.WriteString("\n(자세한 내용은 node_detail / get_worker_output / list_findings 로 추가 조회 가능.)")
	return b.String()
}

// factIDsYielded lists the fact ids an intent produced this run as "#12、#15", so the
// planner can jump straight to the round's incremental facts. Empty (best-effort) when
// the intent yielded no facts or the lookup fails.
func factIDsYielded(ts *db.ExplorationStore, id int64) string {
	ids, err := ts.FactsYielded(id)
	if err != nil || len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, fid := range ids {
		parts[i] = fmt.Sprintf("#%d", fid)
	}
	return strings.Join(parts, "、")
}

// intentSummary reads an intent node's one-line summary (best-effort, "?" on miss).
func intentSummary(ts *db.ExplorationStore, id int64) string {
	n, err := ts.GetNode(id)
	if err != nil || n == nil {
		return "?"
	}
	var p map[string]any
	if json.Unmarshal(n.Payload, &p) == nil {
		if s, ok := p["summary"].(string); ok && s != "" {
			return s
		}
	}
	return "?"
}

// workerOutput returns the finished worker's conclusion for an intent — the last
// 'result' (else 'text') activity's full detail, truncated. Same source get_worker_output uses.
func workerOutput(ts *db.ExplorationStore, id int64) string {
	acts, _, err := ts.ActivityList(&id, 0, 1000)
	if err != nil {
		return "(출력 가져오기 실패)"
	}
	var pick *db.Activity
	for i := range acts {
		if acts[i].Kind == "result" {
			pick = &acts[i]
		} else if acts[i].Kind == "text" && pick == nil {
			pick = &acts[i]
		}
	}
	if pick == nil {
		return "(이 work 는 아직 출력 기록이 없음)"
	}
	out, _ := ts.ActivityDetail(pick.ID)
	if out == "" {
		out = pick.Summary
	}
	return truncOutput(out, 800)
}

// truncOutput caps a worker-output blob so the trigger context doesn't bloat the
// system prompt every round; full text is one get_worker_output call away.
func truncOutput(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + " …(잘림, 전체는 get_worker_output 참고)"
}

// renderGraphOverview folds the pre-computed graph_overview snapshot into the
// wake-up prompt so the planner starts each round with the full situation in
// hand — saving the round-trip it would otherwise spend calling the tool. It is
// the exact same JSON graph_overview would return; deeper detail is still one
// tool call away (node_detail / list_facts / …).
func renderGraphOverview(data map[string]any) string {
	b, err := json.Marshal(data)
	if err != nil {
		return "" // fall back to the model calling graph_overview itself
	}
	return "\n\n【이번 라운드 상황(graph_overview 선취, 그 도구를 호출한 반환과 동일; 세부는 필요 시 node_detail/list_facts 등 호출)】:\n" + string(b)
}

// plannerDefaultTmpl is the built-in EDITABLE body (段 [A]) of the planner prompt,
// seeded into agent_prompts. Goal is a {{.Goal}} template var; the 中间产物输出规约
// tail is code-owned (artifactSpec) and appended by plannerSystem after rendering.
const plannerDefaultTmpl = `너는 사이버보안 플랫폼의 승인된 침투 테스트 시스템의 "계획자"로, 자주 깨어난다(그래프가 바뀔 때마다 깨어남). 역할: 상황 읽기 → 목표 판정 → **아직 다루지 않은 새 방향이 확실할 때만** 탐색 의도를 보충. 너는 계획자이지 실행자가 아니다: 이번 라운드의 모든 산출물은 【의도 생성/명확화】이거나 【목표 판정】뿐이며, 절대 plan 안에서 직접 일을 처리하지 않는다.

과제 목표: {{.Goal}}

**이번 라운드에 의도를 몇 개 낼 것인가(이것부터 분명히 하라)**:
- **하드 하한선(최우선)**: 【목표 미달성】이고 【현재 open 또는 running 의도가 하나도 없음】(frontier_open=0 이고 running_intents 가 비어 있음)이면, 이번 라운드에 목표로 전진하는 의도를 【반드시】 최소 하나 내야 한다 — 기다릴 실행 중 work 도, 대기 중 방향도 없을 때 의도 0개 = 과제 정지다; 알려진 방향이 recent_done 에만 있더라도, 아래의 done/exhausted/blocked 판단에 따라 새로 열거나 이어서 내려야 한다.
- 하드 하한선 외에는, **의도 0개를 내는 것도 정상 결과지만 정당한 이유가 있어야 한다**(적게 내는 게 더 안전해서가 아니다): ①**이미 커버됨** — 네가 떠올린 방향이 모두 아직 open/running 인 의도로 처리되고 있음(이미 존재하는 의도를 표현만 바꿔 다시 생성하는 것은 심각한 오류다); ②**의존 대기** — 다음 단계가 실행 중 work 의 산출에 의존하는데 아직 안 나옴(이때 억지로 내면 하위가 선행을 못 받아 헛돌므로, 다음에 깨어나 그래프가 갱신된 뒤에 내야 한다).
- 반대로: 【미커버이고 실행 중 work 에 의존하지 않는】 새 방향이 확실히 있거나, 목표 미달성이고 범위 내에 아직 테스트 안 한 면이 있으면 내야 한다 — 의도 0개를 게으름의 기본값으로 삼지 마라.

**매 기상 시 의사결정 흐름**:

1. **완전한 상황이 이 프롬프트 아래에 첨부되어 있다**(바로 graph_overview 의 반환이며, 다시 호출할 필요 없음): task(원 제목 + 목표/루트 노드), 자산 수, goals + 상태, open/running/recent_done 의도, sites_without_endpoints(엔드포인트 없는 사이트, 탐색해 볼 방향 암시), facts(탐색 사실 수, 취약점과는 다른 범주), recent_facts({id,summary,confidence?}).
   - **범위**: 탐색 노드(goals/의도/facts/findings)는 본 과제만 포함; **자산 그래프는 전역 공유**(여러 과제가 같은 것, 자산 수는 전역 범위 내의 것이지 본 과제 고유가 아님) — 본 과제와 무관한 자산이 나오면 무시한다.
   - **혈연**: 각 의도는 parents(상위: 어떤 사실/의도에서 파생됐는지)와 yields(하위: 어떤 사실/발견을 낳았는지)를 가지며, recent_facts 는 각 항목에 from_intent 를 가진다; 이로써 "어떤 사실이 어느 방향에서 왔는지, 종합해 새 방향을 낼 수 있는지"를 이해한다.
   - **부정/의심 관찰**(recent_facts 의 "포트 닫힘/주입 불가" 등)은 worker 의 관찰이지 정설이 아니다: 채택 전에 node_detail(id) 로 evidence 를 보라 — evidence 가 탄탄하고 confidence=observed 이며 수단을 다 썼을 때만 그 방향이 잠정 봉쇄된 것으로 보고; evidence 가 없거나 단지 "그래 보임/한 번만 탐색"이거나 confidence=inferred 이면 【아직 미탐색】으로 처리하고, 범위 내이며 다른 의도가 커버하지 않으면 기본적으로 복핵 의도 하나를 내어 입증하거나 반증한다(**같은 부정 방향은 최대 한 번만 복핵**; 복핵 후에도 부정이고 증거가 합리적이면 그 결론을 존중하고 더 내지 않는다).
   - **더 깊은 세부는 필요할 때만 호출**: list_facts(페이지, 최신 우선, 기본 20, q 필터·before 페이지 넘김 가능, total/has_more 포함), list_findings(모든 취약점), node_detail(id)(완전한 증거/상세; 목록/recent_facts 는 요약만), list_assets(pull: q 검색, type/company_id/task_id 필터, 페이지, 또는 id/ids 직취), asset_neighbors. 자산은 전역 공유이니 기본으로 전량을 당기지 마라.

2. **목표 판정(핵심 역할)**: goals 필드에 목표와 상태가 이미 있다; 어떤 발견/사실로 증명된 미달성 목표에 대해 prove_goal(goal_id, evidence_id, reason) 을 호출해 met 로 표기한다. **네가 표기한 것이 마지막 미완료 목표일 때 시스템이 과제 전체를 자동으로 완료 판정한다** — 마무리는 오직 prove_goal 을 하나씩 하는 것으로만 이루어지며, 다른 "원클릭 완료" 수단은 없다.
   - ⚠️ **정량 검수 대조(조기 승인 엄금)**: 목표에 정량 조건(커버리지 X% 달성, flag N개 획득, 특정 권한 획득)이 포함되면, prove_goal 전에 위 graph_overview 의 실측값(coverage.pct, findings_total 카운트 등)을 【반드시】 대조하라: 미달이면 prove_goal 을 【금지】하고 의도를 내어 차이를 메워라; "대체로 달성/핵심은 확보"를 이유로 조기 met 표기 금지. 예: 커버리지 100% 요구인데 실측 coverage.pct=40% → 미달성, 보충 테스트 의도를 계속 낸다.

3. **(선택, 개시 때만, 극히 경량) 이해를 위한 탐지**: 그래프에 fact 가 거의 없고(recent_facts 가 거의 비었고 과제가 막 시작됨), 상황만으로 초기 의도를 구체화할 수 없을 때만, Bash 등으로 목표에 극소량의 읽기 전용 탐지(예: curl 1~2회로 첫 페이지/핑거프린트 확인)를 한다. **유일한 합법 산출물은 더 정확한 의도 설명 한 문장**이다 — 결코 취약점의 발견/검증/공략이 아니고, 엔드포인트/디렉터리/파라미터의 열거 결과도 아니다(그것들은 worker 의 일이니 의도로 써서 내려보낸다). 세 가지 하드 경계:
   - 그래프에 worker 가 낸 fact 가 이미 있으면(facts>0 / recent_facts 비어 있지 않음) → 스스로 탐지하는 것을 【금지】, 모든 판단은 기존 fact 에 기반하고, 이번 라운드 산출물은 "새 의도 내기" 또는 "종료"뿐이다; 어떤 단서를 더 파고 싶으면 → 의도를 내어 worker 가 조사하게 하라, 네가 curl 하지 말고.
   - 개시 때라도 최대 ≤3회만 탐지하고 손을 떼며, 초기 의도를 명확히 하기 위해서만 한다; 자신이 "방향을 빠르게 정하기"가 아니라 "깊이 확인하기"를 하고 있다고 느끼는 즉시(엔드포인트/디렉터리 하나씩 열거, id 하나씩 시도, 디코딩 체인, 같은 인터페이스 반복 탐지, 어떤 주입/권한우회/취약점 테스트 검증 — 전부 worker 의 중노동), 즉시 멈추고 의도로 쓴다.
   - 기존 사실/상황으로 판단할 수 있는 것은 탐지할 필요가 아예 없다.

4. **어떤 새 방향을 보충할지 결정**: **여기서의 "절제"는 【이미 존재하는 의도를 반복하지 않음】만을 뜻하며, "적게 낼수록 좋다"가 아니다** — 목표 미달성 시 기본 질문은 "목표에 다가가기 위해 더 깊고, 더 독한, 아직 미커버인 공략이 무엇인가"이지 "마무리해도 되는가"가 아니다. 의도는 【열린 탐색 방향】이며(고정 유형/메뉴가 아님), 알려진 사실·자산·목표를 결합해 스스로 방향을 판단하고, open + running + recent_done 과 하나씩 대조한다:
   - 이미 open/running 이 커버함 → 다시 생성하지 않음(처리 중).
   - recent_done 에 나타난 적 있음 → **먼저 그 의도의 state(각 항목이 가짐)로 어떻게 멈췄는지 분별한 뒤 결정**:
     · **done(정상 완료)**: 이미 커버됨 → 그대로 다시 내지 않음; 막다른 길인지는 그것이 yields 한 fact 결론으로 보고 state 로 보지 않음; 【실질적 새 메커니즘】(새 사실/자산/파라미터/명백히 다른 공략)이 나타날 때만 다시 내되 summary 에 지난번과의 차이를 명확히; 표현 변경, "다시 해보면 될지도"는 안 되며 재시도 금지.
     · **exhausted(예산 소진, 절반 탐색 중 끊겨 일부만 기록) / blocked(모델 또는 네트워크 실패, 사실상 탐색 못 함)**: 둘 다 도중에 제대로 끝나지 못해 정보 불완전 — 먼저 get_worker_trace / get_worker_output 으로 실제 어디까지 했고 어디서 막혔는지 본 뒤 아래에서 고른다: 돌파 직전 예산에 끊김 → "지난 진행을 이어서 계속" 의도; 순수 외부 장애로 못 돌아감(blocked 가 흔히) → 같은 방향 바로 재발; 매번 같은 곳에서 막힘 → 공략/방향 변경. 근거는 언제나 trace 의 실제 진행이지 state 자체가 아니다.
   - 어떤 의도에도 전혀 커버되지 않은 완전히 새 방향 → 생성.
   - 알려진 모든 방향이 아직 open/running 인 의도로 커버됨 → 생성하지 않고 바로 종료(실행 중/대기 중 work 가 있으니 그것들이 진행하길 기다림); 다만 recent_done 커버만 남고 open/running 이 없는데 목표 미달성이면 → 상단 하드 하한선에 따라 반드시 새로 열거나 이어서 낸다.
   - **깊이가 커버리지보다 우선**: coverage 는 하한/검수 항목이지 탐색 목표 자체가 아니다; 고가치 입구(RCE/권한상승/데이터 유출로 통할 수 있는)를 발견한 뒤에는 그 길을 【끝까지 뚫는】 의도를 우선 내고, 커버리지를 맞추려 넓게 펼쳐 자산을 하나씩 얕게 테스트하지 마라.
   - **노선 다양성을 유지하고 너무 일찍 수렴하지 마라**: 목표 미달성 시 기존 의도가 모두 같은 노선/입구에 몰려 있는데 【본질적으로 다른】 미커버 방향(다른 입구 면/다른 유형 자산/다른 공략 체인)이 있으면, 같은 노선에 동의어 의도를 더하지 말고 그 분기 방향을 우선 보충하라(표현이 아니라 실질 차이를 봄); 그 분기 방향이 이미 기존 의도에 커버됐으면 그래도 생성하지 않음. 이상적인 것은 메커니즘이 다른 2~3개 노선의 병존(예: "업로드 체인으로 공략"과 "인증 우회로 공략")이며, 어떤 노선이 【목표 근접】 증거를 내놓은 뒤에 자원을 그쪽으로 집중한다. **다만 다양성은 언제나 상단 【작업 제약】에 복종한다**: 제약으로 배제된 입구 면/포트/호스트/작업은 본질적으로 달라도 절대 의도를 생성하지 않는다.

   **직렬 공략 체인: 단계별로 내고 병렬로 쪼개지 마라.** 강한 의존의 직렬 체인(①→②→③, 뒷 단계가 앞 단계의 실제 산출에 의존)은 한 번에 병렬로 내려보내지 마라(하위가 아직 없는 선행을 못 받으면 중복/헛돌기만 함); TodoWrite 로 체인 전체를 할 일로 기록하고(단계마다 하나), 이번 라운드에는 "선행이 충족된" 그 단계(보통 1단계)만 내고, 그것이 fact 를 낸 뒤 다음에 기상할 때(프롬프트에 할 일 목록이 붙어 옴) 다음 단계를 내며 충족된 것을 completed 로 표기한다. "같은 일"을 둘로 쪼개지 마라("유발점 확인"과 "유발점 유발"은 같은 단계다); 【평행하고 서로 의존하지 않는】 차원(예: 서로 무관한 여러 엔드포인트 열거)만 여러 의도로 병렬 처리한다.

5. **제출**: 【한 번의】 add_intent 로 추려낸 새 방향을 일괄 제출한다(intents 배열, 최고 가치 최대 4개, 하나씩 여러 번 호출하지 마라):
   - **summary**: 그 방향을 자연어 한 문장으로 기술(테스트 목표의 완전한 주소 + 무엇을 + 왜), 고정 분류에 얽매이지 않음; 중복 제거는 주로 이것과 기존 의도의 대조로 한다.
   - **asset_ids**: 이 방향에서 테스트/공격할 대상 자산 id(가능하면 전달, 0/1/다수, list_assets 에서 옴) — 방향이 구체적 자산(사이트/인터페이스/파라미터/호스트)을 중심으로 하면 반드시 전달, 커버리지 중복 제거와 자산 링크 연결에 쓰이며, 여러 자산에 걸치면 모두 전달; 순수 전역 정찰로 구체적 자산이 없을 때만 비워 둔다.
   - **parent_ids**: 이 방향이 어떤 상위 노드를 종합해 나왔는지(선택, 0/1/다수) — 여러 사실이 결합해 하나의 의도를 낳으면 모두 전달, 어떤 상위 의도/발견에서 파생됐으면 그 id 도 전달, 최상위의 완전히 새로운 방향은 비워 둔다.

반복하지 말고, 억지로 채우지 마라; 다만 목표 미달성이고 미커버이면서 더 깊은 공략이 있으면 내야 할 때 내라. 간결하고, 집중되며, 효율적으로.`

func plannerSystem(goal, dataDir, workDir string) string {
	body := renderSystem("planner", plannerDefaultTmpl, PlannerVars{Goal: goal, DataDir: dataDir, Now: nowStr()})
	return body + artifactSpec(workDir) + langDirective()
}

// Plan runs one planning round. emit, if non-nil, receives the planner's execution
// steps (so users can see how it reads the situation and judges goals — the
// planner is the intent generator and was previously a black box). Returns whether
// the planner judged the goal met.
// triggers carries the concrete change(s) that fired this round — worker(s) done
// and/or finding(s) reported (may be several — the engine debounces a burst; empty
// for time/heartbeat wakes). They are spelled out at the top of the prompt so the
// planner looks first at the actual change (which intent, its output/finding).
func (p *Planner) Plan(ctx context.Context, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, goal string, triggers []TriggerEvent, emit func(db.Activity)) (met bool, reason string, err error) {
	// cold-digest §2.3/§7: advance this task's planner-round counter, maintain the
	// cold_since_round stamps, and (if a threshold is hit) kick off background
	// compaction. Synchronous part is cheap (a few queries); the LLM compaction
	// runs in a detached goroutine so it never adds latency to this round.
	p.compactor.OnPlannerRound(ctx, ts)
	tsx := NewToolSet(ts, "planner")
	tsx.SetFindingRecorder(p.findingRecorder)
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetTaskID(taskID)
	tsx.SetCoverageEnabled(as == nil || as.CoverageEnabled(taskID))
	tsx.killWork = p.killWork   // enable kill_work tool (nil = unavailable)
	tsx.steerWork = p.steerWork // enable steer_work tool (nil = unavailable)
	if origin, _ := ts.OriginFactID(); origin > 0 {
		tsx.SetOwnerNode(origin) // planner-side anchors default to the task root (origin fact)
	}
	// 领域工具 + 基础默认工具集（Read/Write/Edit/MultiEdit/LS/Glob/Grep/Bash）
	// 资产覆盖度功能关闭时剔除 add_task_scope/list_untested_assets（不入 prompt）。
	base := append(tsx.DropCoverageTools(tsx.PlannerTools()), actool.DefaultTools()...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts)})
	tools, def, cleanup := AugmentTools(ctx, "planner", base)
	defer cleanup()
	// 关键态势（刚完成的意图 + 预取的完整图）改放【本轮 user 输入】(见下方 input)，system
	// 只留静态规划正文。move-out 让 system 每轮稳定、更利于缓存；代价是若单轮变长，态势可能
	// 被 compaction 压缩（planner 单轮通常短，风险低）。situational 会拼进下方 input。
	situational := renderTriggers(ts, triggers) + renderGraphOverview(tsx.graphOverviewData())
	// 任务级 deadline / 终局模式(经 ctx 注入,见 taskclock.go)。终局那一轮把任务超时
	// planner 收尾词作为【本轮操作指令】拼进本轮 user 输入(随 situational),让它只做最后
	// 目标判定、不产新意图。
	tc := taskClockFrom(ctx)
	if tc.Final {
		situational += "\n\n【과제 최종 마무리(이번 라운드 특수 지시, 위의 정규 계획 흐름을 덮음)】:" + resolveTaskTimeoutWrapup("planner")
	}
	// 本任务的工作目录 <workDir>/tasks/<taskID>，先建好。
	taskDir := ensureRunDir(p.workDir, taskID, 0)
	ctx = intercept.WithReviewContext(ctx, taskDir, intercept.ReviewBackground{})
	sysBody := plannerSystem(goal, p.workDir, taskDir)
	if p.wantConstraints() {
		sysBody += constraintBlock(ts) // 操作约束(若有)注入系统提示,框定探索边界
	}
	system, boundary := deferredSystem(sysBody, def)
	// planner 无自身墙钟预算;有 deadline 时把 MaxDuration 夹逼到剩余,让在跑的规划轮在
	// 任务到点时进收尾(因超时→任务超时词,因步数→per-run 词)。
	maxDur, clamped := clampMaxDuration(tc.DeadlineUnix, 0)
	settle := wrapupSettlement("planner", nil)
	if tc.DeadlineUnix > 0 {
		settle = wrapupSettlementForTask("planner", nil, clamped)
	}
	opts := agentcore.Options{
		Provider:        p.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		EnableWebFetch:  true, // 走记录代理留痕；载入代理 CA 验证 MITM 重签的 HTTPS 证书
		WebFetchProxy:   p.proxyAddr,
		WebFetchCACert:  p.proxyCACert,
		// 联网搜索(可选)。ddgs 无需 key；brave-free 需 BraveKey；tavily 需 TavilyKey。
		// WebSearchProxy 是独立出口代理(http/https/socks5)，与记录流量的 MITM 代理无关；空则直连。
		EnableWebSearch:       p.webSearch.Enabled,
		WebSearchBackend:      p.webSearch.Backend,
		BraveSearchAPIKey:     p.webSearch.BraveKey,
		TavilySearchAPIKey:    p.webSearch.TavilyKey,
		DeepSeekSearchBaseURL: p.webSearch.DeepSeekBaseURL,
		DeepSeekSearchAPIKey:  p.webSearch.DeepSeekAPIKey,
		DeepSeekSearchModel:   p.webSearch.DeepSeekModel,
		WebSearchProxy:        p.webSearch.Proxy,
		BashEnv:               proxyEnv(p.proxyAddr, p.proxyCACert), // Bash 子命令默认走代理+信任 CA
		WorkingDir:            taskDir,                              // 本任务工作目录 <workDir>/tasks/<taskID>
		ToolOutputDir:         cmdOutDir(taskDir),
		MaxTurns:              p.maxTurns, // 0 = unlimited (configurable in agent management)
		MaxDuration:           maxDur,     // 0=不限;有 deadline 时=距 deadline 剩余
		Compaction:            compactionConfig(p.compactionWindow()),
		// 跨唤醒共享的规划待办：让串行链在多轮之间保留（session 是新的，store 不是）。
		Todos: p.todoFor(ts.ID()),
		// 命中【本轮】步数预算→ SDK 跑收尾:把本轮已想清楚的结论落地(该派的 add_intent、
		// 能证的 prove_goal、串行链记 TodoWrite),而非停止规划——planner 之后仍会被反复唤醒。
		// clamped(被任务 deadline 夹逼)时改用 PromptByReason(见 wrapupSettlementForTask)。
		Settlement:   settle,
		NonStreaming: p.nonStreaming(), // 该 profile 选非流式时走 Provider.Complete
		MaxTokens:    p.maxTokens(),    // 0 = 不发上限,由服务端默认值决定
	}
	if p.tx != nil { // persist raw LLM conversation; one accumulating file per task's planner
		opts.Transcript = p.tx
		opts.SessionID = fmt.Sprintf("exp%d-planner", ts.ID())
	}
	// 实验功能:开启后由 noa 接管上下文压缩(归档集中在 <workDir>/noa/<SessionID> 下,持久)。
	noaSession := fmt.Sprintf("exp%d-planner", ts.ID())
	enableNoa(&opts, p.noaEnabledFn, p.workDir, noaSession, noaWarn(noaSession))
	// 态势（刚完成的意图 + 完整图）现在拼进本轮 user 输入（见下方 input）。user 里还有
	// 指令 + 跨唤醒待办（todo 是模型自己的规划便签，可再生，放 user 即可）。
	// 开场白按「本轮有无具体变动」分两种：有变动 → 指向下方【实际变动】块；无变动
	// (心跳定时巡检 / hint / 恢复等) → 别谎称"图发生了变化",转而提示顺带复查在跑意图。
	lead := "방금 구체적 변동이 있었다(아래 【이번 라운드를 트리거한 실제 변동】 참고), 이에 따라 다음 단계를 계획하라:"
	if len(triggers) == 0 {
		lead = "이번 라운드는 **정기 순회(하트비트 도달)/구체적 변동 신호 없음**의 기상이다 —— 그래프에 새 변동이 없을 수도 있다. 겸해서 실행 중 의도를 재점검하라: 오래 진전이 없거나 빗나간 것은 steer_work 로 교정, 방향 자체가 틀린 것은 kill_work 로 손절; 그 뒤 목표를 판정하고 방향 보충 여부를 결정하라:"
		// 心跳/无变动唤醒时,若全图已无任何 open 或 running 意图 → 探索已停摆(没 worker 在跑、
		// 也没排队方向)。明确告知 planner 并强制其本轮补出新方向,别只复查在跑意图后空转一轮。
		if active, err := ts.HasActiveIntent(); err == nil && !active {
			lead = "이번 라운드는 **정기 순회(하트비트 도달)**의 기상이고, 현재 **open 또는 running 의도가 하나도 없다** —— 실행 중 worker 도, 대기 중 방향도 없어 탐색이 정지됐다. 너는 이번 라운드에 목표로 전진하면서 그래프의 기존 의도와 **중복되지 않는** 새 의도를 하나 이상 **반드시** 내야 한다(의도 0개 금지); 먼저 아래 상황으로 목표 달성 여부를 판정하고, 미달성이면 즉시 방향을 보충하라:"
		}
	}
	input := lead + situational + "\n\n위 상황에 따라 목표를 판정하라. 목표가 【진짜로 달성】됨(목표 성과를 얻음/목표 취약점을 확인함)이면 prove_goal 로 하나씩 표기하라. **하드 하한선: 목표가 아직 미달성이고 현재 open 또는 running 의도가 하나도 없으면(frontier_open=0 이고 running_intents 가 비어 있음), 이번 라운드에 목표로 전진하는 의도를 최소 하나 반드시 내야 한다 —— 이때 기다릴 실행 중 work 도, 대기 중 방향도 없으니 의도 0개 = 과제 정지다. 이미 open/running 의도가 진행 중이거나 목표가 달성된 경우에만 이번 라운드에 새 의도를 내지 않아도 된다.**" +
		renderPlannerTodos(opts.Todos.List())
	// MaxDuration 现在会在墙钟到点打断在跑工具并就地进收尾(在活 ctx 上),单轮卡死不再
	// 绕过收尾,无需外部硬 ctx 兜底。ctx 只承载 pause / kill / shutdown。
	_, _, err = captureRun(ctx, opts, input,
		func(r db.Activity) {
			if emit != nil {
				r.Worker = "planner" // planner activity has no intent_id (it generates them)
				emit(r)
			}
		})
	return tsx.GoalMet, tsx.Reason, err
}
