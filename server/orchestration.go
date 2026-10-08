package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/traffic"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// jsonResult marshals v to a JSON tool result.
func jsonResult(v any) (actool.Result, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	return actool.Text(string(b)), nil
}

// 本文件实现 P2「跨任务编排工具集」(docs/跑分编排 §2 P2)。这些是 host 工具——需要
// 访问 Manager(任意任务的 Store)、Engine(暂停)、以及建任务流程,所以住在 server 层。
// 读类工具把「现有 per-task 工具」重定向到目标任务的 store 上跑(建一个临时 ToolSet
// 并 Call 其对应工具),从而复用完全相同的逻辑;控制类(spawn/pause)直接调 Manager/Engine。
// 它们像流量工具一样 seed 进 tools 表、按 agent 绑定(只绑给编排 agent 才可见)。

// hostTools is the runtime host-tool provider fed to ToolAugment: traffic tools
// (gated by capture) + cross-task orchestration tools + user-defined custom tools.
// The second return is the names of custom tools flagged `deferred` (schema
// withheld, routed via SearchExtraTools/ExecuteExtraTool). Per-agent binding still
// decides who actually sees any of them.
//
//nolint:unused // used as the hostTools provider in wireAgentAugment
func (s *Server) hostTools() ([]actool.CoreTool, map[string][]string) {
	tools := append(s.m.HostTools(), s.orchestrationTools()...)
	tools = append(tools, s.findingRetestTools()...)
	tools = append(tools, s.platformTools()...) // 平台操作工具(建改 skill/工具/MCP，给 Auto 用)
	custom, err := s.customTools()
	if err != nil {
		log.Printf("[custom-tool] 加载失败: %v", err)
		return tools, nil
	}
	tools = append(tools, custom...)
	// deferred custom tools → name -> its bound agent keys. ToolAugment turns a
	// name into a deferred entry only for agents it's actually bound to (so we don't
	// advertise a tool the per-agent binding will drop from the callable set).
	deferred := map[string][]string{}
	rows, _ := s.m.pg.ListCustomTools()
	for _, t := range rows {
		if t.Deferred && t.Enabled {
			deferred[t.Key] = t.Agents
		}
	}
	return tools, deferred
}

// orchestrationTools returns the cross-task tool set. Bound per-agent via the
// tools table (default: no binding — opt-in for orchestration agents).
func (s *Server) orchestrationTools() []actool.CoreTool {
	return []actool.CoreTool{
		s.toolListTasks(),
		s.toolListLLMProfiles(),
		s.toolSpawnTask(),
		s.toolPauseTask(),
		s.toolGetTaskGraph(),
		s.toolListTaskFindings(),
		s.toolAddHint(),
		s.toolGetWorkerTrace(),
		s.toolListWorkerTraces(),
		s.toolSearchWorkerTraces(),
		s.toolGetTaskNodeDetail(),
		s.toolUpdateFindingReport(),
		s.toolGetFindingTraffic(),
		s.toolBindFindingTraffic(),
	}
}

// --- schema helpers ---

func strParam(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// parseProfileID reads an LLM profile id from a tool arg that may arrive as a JSON
// number (5) or a numeric string ("5"); returns 0 when absent/unparseable.
func parseProfileID(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		v, _ := strconv.ParseInt(strings.TrimSpace(str), 10, 64)
		return v
	}
	return 0
}

func objSchema(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		m["required"] = req
	}
	return m
}

func roTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: name, Description: desc, Schema: schema,
		ReadOnly:   func(json.RawMessage) bool { return true },
		Concurrent: func(json.RawMessage) bool { return true },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return run(ctx, in)
		},
	})
}

func wrTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: name, Description: desc, Schema: schema,
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return run(ctx, in)
		},
	})
}

// delegateToTask resolves the `task_id` in the input, builds a ToolSet bound to
// that task's store, strips task_id, and calls the chosen per-task tool — so the
// cross-task read reuses the exact in-task logic against another task.
func (s *Server) delegateToTask(ctx context.Context, in json.RawMessage, pick func(*agent.ToolSet) actool.CoreTool) (actool.Result, error) {
	var head struct {
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal(in, &head)
	if strings.TrimSpace(head.TaskID) == "" {
		return actool.Errorf("task_id 는 필수"), nil
	}
	t, ok := s.m.Task(head.TaskID)
	if !ok {
		return actool.Errorf("task 가 존재하지 않음: " + head.TaskID), nil
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(in, &m)
	delete(m, "task_id")
	inner, _ := json.Marshal(m)
	tsx := agent.NewToolSet(t.Store, "orchestrator")
	if s.m.Assets() != nil {
		tsx.SetAssetStore(s.m.Assets(), s.m.Assets().Companies())
	}
	tsx.SetNotify(t.Notify)         // 通用唤醒（无专用回调的写操作走它；读工具为 no-op）
	tsx.SetNotifyHint(t.NotifyHint) // add_hint → 记一条「人新增了 N 条战略提示：…」触发并唤醒 planner
	return pick(tsx).Call(ctx, inner, nil)
}

// --- tools ---

func (s *Server) toolListTasks() actool.CoreTool {
	return roTool("list_tasks",
		"모든 과제(id/설명/목표/상태/실행 시간/부모 과제/LLM 설정)를 나열합니다. 오케스트레이션 agent 가 전체를 파악하고, 어떤 과제가 너무 오래 멈춰 있는지, 각자 어떤 LLM 을 쓰는지 보는 데 사용. 실행 시간: 실행 중=생성→현재, 종료 상태=생성→마지막 활동(초). llm_profile: 과제의 planner/worker 가 쓰는 설정 이름, (활성 설정)=전역 활성 설정을 따름.",
		objSchema(map[string]any{}),
		func(context.Context, json.RawMessage) (actool.Result, error) {
			lastAct, _ := s.m.PG().LastActivityAll()
			// id -> name to resolve each task's pinned LLM profile.
			profName := map[int64]string{}
			if profs, err := s.m.pg.ListProfiles(); err == nil {
				for _, p := range profs {
					profName[p.ID] = p.Name
				}
			}
			out := make([]map[string]any, 0)
			for _, t := range s.m.List() {
				status := s.deriveTaskStatus(t)
				end := lastAct[t.ExpID]
				if live := s.engine.LastActivity(t.ID); live > end {
					end = live
				}
				dur := int64(0)
				if status == "running" {
					dur = time.Now().Unix() - t.CreatedAt
				} else if end > t.CreatedAt {
					dur = end - t.CreatedAt
				}
				row := map[string]any{"id": t.ID, "description": t.Description, "goal": t.Goal, "status": status, "run_seconds": dur}
				if t.ParentRef != "" {
					row["parent_ref"] = t.ParentRef
				}
				llmState := t.llmStateSnapshot()
				if llmState.ProfileID == nil {
					row["llm_profile"] = "(활성 설정)"
				} else if n, ok := profName[*llmState.ProfileID]; ok {
					row["llm_profile"] = n
				} else {
					row["llm_profile"] = fmt.Sprintf("#%d(삭제됨)", *llmState.ProfileID)
				}
				out = append(out, row)
			}
			return jsonResult(out)
		})
}

// toolListLLMProfiles lists the available LLM profiles (name/model/active) so an
// orchestration agent can pick one for spawn_task's llm_profile. Never leaks keys.
func (s *Server) toolListLLMProfiles() actool.CoreTool {
	return roTool("list_llm_profiles",
		"사용 가능한 LLM 설정(profile)을 나열: id, 이름, 모델, 형식, 현재 활성 설정 여부. 이 id 를 spawn_task 의 llm_profile_id 파라미터에 주어 하위 과제 전용 LLM 을 지정합니다(예: 정찰엔 저렴한 모델, 공략엔 강한 모델). API Key 는 포함하지 않음.",
		objSchema(map[string]any{}),
		func(context.Context, json.RawMessage) (actool.Result, error) {
			profs, err := s.m.pg.ListProfiles()
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			out := make([]map[string]any, 0, len(profs))
			for _, p := range profs {
				out = append(out, map[string]any{
					"id": p.ID, "name": p.Name, "model": p.Model, "format": p.Format, "is_active": p.IsDefault,
				})
			}
			return jsonResult(map[string]any{"profiles": out})
		})
}

func (s *Server) toolSpawnTask() actool.CoreTool {
	return wrTool("spawn_task",
		"하위 과제를 새로 만들고 탐색 엔진을 시작하며 task_id 를 반환합니다. 하나의 일(예: 한 문제/하나의 목표)을 독립 과제로 분할하는 데 사용. parent_ref 선택: 현재 오케스트레이션과 연관된 부모 과제 id 를 넣어 부모-자식 연관을 만듭니다.",
		objSchema(map[string]any{
			"description":            strParam("과제 설명(짧은 제목)"),
			"goal":                   strParam("과제 목표(무엇을 달성할지)"),
			"parent_ref":             strParam("선택: 부모 과제 id(부모-자식 연관)"),
			"source_task_ids":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": fmt.Sprintf("선택: 읽기 전용으로 상속할 출처 과제 id 목록(최대 %d 개). 하위 과제는 이 과제들이 이미 탐색한 자산/결론을 시작점으로 읽기 전용 참조할 수 있습니다; parent_ref 의 순수 부모-자식 포인터와 달리 이것은 내용 상속입니다.", db.MaxTaskSourceCount)},
			"llm_profile_id":         map[string]any{"type": "integer", "description": "선택: 이 하위 과제의 planner/worker 가 쓸 LLM 설정 id 지정(list_llm_profiles 참고); 비우면 부모 과제를 상속하고 다시 전역 활성 설정으로 폴백"},
			"timeout_seconds":        map[string]any{"type": "integer", "description": "선택: 과제 수준 타임아웃(초). 시간이 되면 우아한 마무리를 트리거하고 timeout 종료 상태로 진입; 비우거나 0 = 무제한"},
			"plan_heartbeat_seconds": map[string]any{"type": "integer", "description": "선택: planner 하트비트 트리거 간격(초). 지난 계획 종료/과제 시작으로부터 이 값만큼 지나고 그동안 트리거가 없으면 → 한 라운드 계획을 트리거(데드락 방지 + 진행 중인 worker 감독을 위해 깨움). 비우거나 0 = 기본 600(10분);"},
			"seed_first_intent":      map[string]any{"type": "boolean", "description": "선택: 간단한 과제에서 켤 수 있으며, 생성 시 시드 의도(내용=설명+목표) 하나를 바로 내려 worker 가 첫 라운드 planner 를 기다리지 않고 바로 테스트를 시작하게 함; 기본 false(표준대로 먼저 계획 후 실행)."},
		}, "description", "goal"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Description          string          `json:"description"`
				Goal                 string          `json:"goal"`
				ParentRef            string          `json:"parent_ref"`
				SourceTaskIDs        []string        `json:"source_task_ids"`
				LLMProfileID         json.RawMessage `json:"llm_profile_id"`
				TimeoutSeconds       int             `json:"timeout_seconds"`
				PlanHeartbeatSeconds int             `json:"plan_heartbeat_seconds"`
				SeedFirstIntent      bool            `json:"seed_first_intent"`
			}
			_ = json.Unmarshal(in, &a)
			if strings.TrimSpace(a.Description) == "" {
				a.Description = "이름 없는 과제"
			}
			if strings.TrimSpace(a.Goal) == "" {
				return actool.Errorf("goal 은 필수"), nil
			}
			if a.TimeoutSeconds < 0 {
				a.TimeoutSeconds = 0
			}
			// 只读继承来源任务：数量上限 + 每个 id 有效/去重/存在，校验规则与 HTTP 建任务一致。
			if len(a.SourceTaskIDs) > db.MaxTaskSourceCount {
				return actool.Errorf(fmt.Sprintf("연관 과제는 최대 %d 개 선택", db.MaxTaskSourceCount)), nil
			}
			sourceIDs := make([]int64, 0, len(a.SourceTaskIDs))
			seenSources := map[int64]bool{}
			for _, raw := range a.SourceTaskIDs {
				id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
				if err != nil || id <= 0 || seenSources[id] {
					return actool.Errorf("연관 과제 id 가 유효하지 않거나 중복"), nil
				}
				if _, ok := s.m.Task(strconv.FormatInt(id, 10)); !ok {
					return actool.Errorf(fmt.Sprintf("연관 과제 #%d 가 존재하지 않음", id)), nil
				}
				seenSources[id] = true
				sourceIDs = append(sourceIDs, id)
			}
			// LLM profile resolution: explicit id > inherit parent's pin > active(nil).
			var pin *int64
			if id := parseProfileID(a.LLMProfileID); id > 0 {
				if _, ok := s.loadProfileConfig(id); !ok {
					return actool.Errorf(fmt.Sprintf("LLM 설정 #%d 가 존재하지 않거나 API Key 가 설정되지 않음", id)), nil
				}
				pin = &id
			} else if a.ParentRef != "" {
				if pt, ok := s.m.Task(a.ParentRef); ok {
					pin = pt.LLMProfileID
				}
			}
			var llmIDs []int64
			if pin != nil {
				llmIDs = []int64{*pin}
			}
			t, err := s.m.CreateTaskWithOptions(a.Description, a.Goal, db.TaskCreateOptions{
				SourceTaskIDs:        sourceIDs,
				LLMProfileIDs:        llmIDs,
				TimeoutSeconds:       a.TimeoutSeconds,
				PlanHeartbeatSeconds: a.PlanHeartbeatSeconds,
			})
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if a.ParentRef != "" {
				t.ParentRef = a.ParentRef
				if id, e := strconv.ParseInt(t.ID, 10, 64); e == nil {
					_ = s.m.PG().SetParentRef(id, a.ParentRef)
				}
			}
			// 共享的建后流程,与 HTTP 建任务(server.go createTask)复用同一段 launchTask:
			// seed + 后台可见地做目标分解(第0轮/LLM步骤/逐条goal) + engine.Run。
			// seed_first_intent 默认 false(标准先规划再执行);简单任务可开启直接下发一 work 测试。
			s.launchTask(t, a.Description+" "+a.Goal, a.SeedFirstIntent)
			return actool.Text(fmt.Sprintf("task created: %s", t.ID)), nil
		})
}

func (s *Server) toolPauseTask() actool.CoreTool {
	return wrTool("pause_task", "지정한 과제를 일시정지(그 planner/worker 루프를 멈춤).",
		objSchema(map[string]any{"task_id": strParam("일시정지할 과제 id")}, "task_id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				TaskID string `json:"task_id"`
			}
			_ = json.Unmarshal(in, &a)
			t, ok := s.m.Task(a.TaskID)
			if !ok {
				return actool.Errorf("task 가 존재하지 않음: " + a.TaskID), nil
			}
			if _, err := s.applyTaskControlWithCause(t, "pause", agent.AbortPausedByOrchestrator); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("task paused: " + a.TaskID), nil
		})
}

func (s *Server) toolGetTaskGraph() actool.CoreTool {
	return roTool("get_task_graph", "지정한 과제의 탐색 그래프 개요를 읽습니다(graph_overview 와 동일: 자산 수/frontier/발견/커버리지 등), task_id 로 과제 지정.",
		objSchema(map[string]any{"task_id": strParam("과제 id")}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).GraphOverviewTool)
		})
}

func (s *Server) toolListTaskFindings() actool.CoreTool {
	return roTool("list_task_findings", "지정한 과제의 확인된 취약점을 읽습니다(flag/PoC 포함; 각 항목에 id/task_id/intent_id/vulnclass/severity/요약/상태), task_id 로 과제 지정.",
		objSchema(map[string]any{"task_id": strParam("과제 id")}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).ListFindingsTool)
		})
}

func (s *Server) toolAddHint() actool.CoreTool {
	return wrTool("add_task_hint", "지정한 과제에 전략 힌트를 주입합니다(그 과제의 planner 가 다음 라운드에 의도를 생성할 때 읽음).\n"+
		"★배치 우선: 여러 힌트를 hints 배열에 담아 한 번에 제출(ids 배열 반환, hints 와 길이·순서 동일, 실패 항목 id=0); 단건이면 hints 를 생략하고 최상위 text 에 바로 주세요.",
		objSchema(map[string]any{
			"task_id":      strParam("과제 id"),
			"hints":        map[string]any{"type": "array", "description": "【이것을 우선 사용】힌트 배열, 각 요소 필드는 최상위와 동일(text/asset_ids/traffic_refs).", "items": objSchema(map[string]any{"text": strParam("힌트 내용"), "asset_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}, "traffic_refs": agent.HintTrafficSchema()})},
			"text":         strParam("[단건] 힌트 내용"),
			"traffic_refs": agent.HintTrafficSchema(),
			"asset_ids":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "앵커할 자산 id(선택, 0/1/다수; 해당 과제 내의 자산 id)"},
		}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).AddHintTool)
		})
}

func (s *Server) toolGetWorkerTrace() actool.CoreTool {
	return roTool("get_task_worker_trace",
		"지정한 과제에서 어떤 work(의도)의 실행 과정을 봅니다: get_task_worker_trace(task_id, intent_id) 로 단계 요약을 보고; step_ids=[...] 를 추가해 그 단계들의 전체 내용을 가져옵니다(한 번에 최대 5개, 더 전달하면 앞 5개만 반환).",
		objSchema(map[string]any{
			"task_id":   strParam("과제 id"),
			"intent_id": map[string]any{"type": "integer", "description": "의도 id(해당 과제 내의 work)"},
			"step_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "선택: 전체 내용을 가져올 단계 id(한 번에 최대 5개, 더 전달하면 앞 5개만 반환, 나머지는 omitted_step_ids 에 나열)"},
		}, "task_id", "intent_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).GetWorkerTraceTool)
		})
}

func (s *Server) toolListWorkerTraces() actool.CoreTool {
	return roTool("list_task_worker_traces", "지정한 과제에서 어떤 work(의도)가 수행되었는지 + 각자의 단계 수를 나열하여, 들여다볼 만한 work 를 찾는 데 사용(이어서 get_task_worker_trace).",
		objSchema(map[string]any{"task_id": strParam("과제 id")}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).ListWorkerTracesTool)
		})
}

func (s *Server) toolSearchWorkerTraces() actool.CoreTool {
	return roTool("search_task_worker_traces", "지정한 과제에서 키워드로 모든 work 의 실행 과정을 검색(걸린 단계 요약 + intent_id 반환).",
		objSchema(map[string]any{"task_id": strParam("과제 id"), "q": strParam("검색 키워드")}, "task_id", "q"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).SearchWorkerTracesTool)
		})
}

func (s *Server) toolGetTaskNodeDetail() actool.CoreTool {
	return roTool("get_task_node_detail",
		"지정한 과제에서 어떤 탐색 그래프 노드의 전체 내용을 읽습니다(발견/사실/의도/목표: 요약 + 상세/증거/PoC). id 는 탐색 노드 id(예: report_finding 반환값, 또는 list_task_findings 의 id). 취약점 보고서를 쓰기 전에 이것으로 해당 취약점의 전체 증거를 가져오세요.",
		objSchema(map[string]any{
			"task_id": strParam("과제 id"),
			"id":      map[string]any{"type": "integer", "description": "탐색 그래프 노드 id(자산 id 아님)"},
		}, "task_id", "id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).NodeDetailTool)
		})
}

// toolUpdateFindingReport writes/overwrites a finding's detailed Markdown report.
// finding_id is the id report_finding returned ("finding recorded: <id>", the
// finding node id). The write (SetFindingReportByNodeID) is keyed by node_id and
// task-agnostic, so this host tool needs no task_id / exploration store.
func (s *Server) toolUpdateFindingReport() actool.CoreTool {
	return wrTool("update_finding_report",
		"이미 등록된 취약점에 【상세 보고서】를 작성/갱신합니다(Markdown 전문, 기존 내용을 통째로 덮어씀). finding_id 에는 report_finding 이 반환한 id(\"finding recorded: <id>\" 안의 숫자)를 전달. 보고서에는 취약점 개요, 영향과 위험, 재현 단계, 증거/PoC, 수정 권고를 포함할 것을 권장합니다.",
		objSchema(map[string]any{
			"finding_id":       map[string]any{"type": "integer", "description": "대상 취약점 id(report_finding 이 반환한 id)"},
			"report":           strParam("상세 보고서 전문, Markdown 형식"),
			"evidence_version": map[string]any{"type": "integer", "description": "get_finding_traffic 가 반환한 증거 version; 보고서가 새 증거 변경을 덮어쓰는 것을 방지"},
		}, "finding_id", "report"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				EvidenceVersion *int64          `json:"evidence_version"`
				FindingID       json.RawMessage `json:"finding_id"`
				Report          string          `json:"report"`
			}
			_ = json.Unmarshal(in, &a)
			nodeID := parseProfileID(a.FindingID) // 复用「数字或数字字符串」解析
			if nodeID <= 0 {
				return actool.Errorf("finding_id 가 유효하지 않음"), nil
			}
			n, err := s.m.pg.SetFindingReportVersionByNodeID(ctx, nodeID, a.Report, a.EvidenceVersion)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if n == 0 {
				return actool.Errorf(fmt.Sprintf("finding_id=%d 에 해당하는 취약점 레코드를 찾지 못함(먼저 report_finding 으로 등록하세요)", nodeID)), nil
			}
			return actool.Text(fmt.Sprintf("finding %d report updated (%d chars)", nodeID, len(a.Report))), nil
		})
}

// deriveTaskStatus mirrors listTasks' status derivation for the list_tasks tool.
func (s *Server) deriveTaskStatus(t *Task) string {
	lifecycle := t.lifecycleSnapshot()
	switch {
	case isTerminalStatus(lifecycle.Status):
		return lifecycle.Status
	case lifecycle.Paused || s.engine.IsPaused(t.ID):
		return "paused"
	case s.engine.ReadyFor(t) && s.engine.Started(t.ID):
		return "running"
	}
	return "created"
}

// orchestrationToolSeeds seeds the cross-task tools into the tools table so they
// are bindable per-agent (default: bound to nobody — opt-in for orchestration
// agents). First-insert only, like the traffic seeds.
func (s *Server) seedOrchestrationTools() {
	// task-op + platform tools default-bind to the built-in Auto agent (它天生用来
	// 操作平台)。SeedTool 首插入生效;老库已 seed 的行由 seedAutoDefaultBindings 补绑。
	autoAgents, _ := json.Marshal([]string{"auto"})
	for _, t := range s.orchestrationTools() {
		schema, _ := json.Marshal(t.InputSchema())
		bindings := autoAgents
		if t.Name() == "bind_finding_traffic" {
			bindings = json.RawMessage(`["reporter"]`)
		}
		_ = s.m.PG().SeedTool(t.Name(), t.Description(), schema, bindings)
	}
	for _, t := range s.platformTools() {
		schema, _ := json.Marshal(t.InputSchema())
		_ = s.m.PG().SeedTool(t.Name(), t.Description(), schema, autoAgents)
	}
	s.refreshBuiltinToolSchemas()
	s.seedAutoDefaultBindings()
	s.seedPlannerDefaultBindings()
	s.seedPlannerListAssetsBinding()
	s.seedCompanyScopeRebind()
	s.seedWorkerReadToolsUnbind() // list_facts/list_companies/list_worker_traces 从 worker 默认解绑(一次性)
	s.seedWorkerReadbackRebind()  // 修复旧迁移误删：把 search_all_worker_traces/get_worker_trace/node_detail 补绑回 worker(一次性)
	s.seedAutoReportFindingBinding()
	s.unbindGoalMetDefault()
	s.reseedGoalsPrompt()             // goals 提示词加入「抽操作约束」步 → 旧库追加一版新默认(一次性)
	s.reseedMainAgentPrompt()         // mainagent 提示词加入「目标达成后 add_intent 反问是否建目标」(一次性)
	s.reseedPlannerPrompt()           // planner 提示词:重写「0 意图」正当理由 + 加量化验收核对(一次性)
	s.reseedWorkerPrompt()            // worker 提示词:加否定结论证据门槛(一次性)
	s.localizeKoResync()              // 내장 도구 설명·스키마 + 에이전트 프롬프트를 한국어 코드 기본값으로 재동기화(1회성)
	s.seedReporterAgent()             // 预置「보고서 작성」agent + 工具绑定 + finding 触发器(一次性)
	s.upgradeReporterTriggerMessage() // 老库补迁移:让 reporter 回传 evidence_version(一次性)
	s.seedFindingTrafficTools()       // 增加可选证据参数及只读证据工具，保留用户配置
	s.seedFindingWorkflowTools()
	// 注：pentest 的默认工具绑定无需迁移——BuiltinToolSeeds 在全新初始化时就把
	// list_assets/insert_assets/report_finding/list_findings/list_companies 连同
	// pentest 一起 seed 好了（项目尚无旧库，不做迁移）。
}

// refreshBuiltinToolSchemas propagates code schema/description changes on the
// orchestration + platform tools into already-seeded rows ONCE per version flag —
// SeedTool is first-insert-only, so a new param (e.g. spawn_task 的 llm_profile) never
// reaches an old DB otherwise. Preserves each tool's agent binding + enabled flag.
// Bump the flag whenever these tools' schemas/descriptions change in code.
func (s *Server) refreshBuiltinToolSchemas() {
	const flag = "tool_schema_refresh_v7_list_facts_paging"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	tools := append(s.orchestrationTools(), s.platformTools()...)
	for _, t := range tools {
		schema, _ := json.Marshal(t.InputSchema())
		if err := s.m.pg.RefreshToolDefaults(t.Name(), t.Description(), schema); err != nil {
			log.Printf("[tools] refresh %s schema failed: %v", t.Name(), err)
		}
	}
	// 同时把部分内置 agent 工具刷成代码默认：
	//   - goal_met：旧库 seed 的描述带“结束本轮规划”的误导，会让 planner 把它当成
	//     “结束空轮”的手段、刚开跑就误判整个任务完成。
	//   - insert_assets：新增 related 入参(标记资产是否与当前任务相关、决定是否入覆盖度)，
	//     SeedTool 首插入only，旧库已 seed 的 schema 否则收不到这个新参数。
	//   - list_facts：改为分页，新增 limit/before/q 入参；旧库已 seed 的空 schema 否则
	//     在工具管理页显示「无参数」，模型也拿不到这几个参数说明。
	refreshBuiltin := map[string]bool{"goal_met": true, "insert_assets": true, "list_facts": true}
	for _, sd := range agent.BuiltinToolSeeds() {
		if !refreshBuiltin[sd.Key] {
			continue
		}
		schema, _ := json.Marshal(sd.Schema)
		if err := s.m.pg.RefreshToolDefaults(sd.Key, sd.Desc, schema); err != nil {
			log.Printf("[tools] refresh %s desc failed: %v", sd.Key, err)
		}
	}
	_ = s.m.pg.SetSetting(flag, "true")
	log.Printf("[tools] orchestration/platform 도구 스키마를 코드 기본값으로 새로고침(1회성)")
}

// unbindGoalMetDefault removes goal_met's default "planner" binding ONCE (guarded by
// a settings flag), so existing DBs match the new default of NO agent. goal_met bypasses
// per-goal prove_goal to declare the whole task done — powerful/risky and redundant with
// the prove_goal→auto-complete path — so it ships unbound; users can re-bind it per agent
// in the UI. A user's own binding to another agent is untouched (we only strip planner).
func (s *Server) unbindGoalMetDefault() {
	const flag = "goal_met_unbind_default_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.RemoveAgentFromTool("planner", "goal_met"); err != nil {
		log.Printf("[tools] goal_met 解绑 planner 失败: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// reseedGoalsPrompt 把 goals 目标拆解器的提示词刷成【当前代码默认】——因为默认正文新增了
// 「先抽操作约束(set_constraints)再拆目标」这一步,而 SeedPromptIfEmpty 首插入only,旧库
// 已有的 version 1 收不到这步。这里用版本管理【追加一个新版本】并切过去(ResetPromptToDefault),
// 旧的版本仍保留在历史里,用户若自定义过可从版本记录找回。settings flag 守卫 → 只做一次;
// 以后默认再变就 bump 这个 flag。全新库无需处理(SeedPromptIfEmpty 已 seed 最新默认)。
func (s *Server) reseedGoalsPrompt() {
	const flag = "goals_prompt_constraint_step_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 无论成功与否只尝试一次
	a, err := s.m.pg.GetAgentByKey("goals")
	if err != nil || a == nil {
		return // 全新库尚未建 agent 行时,seedPrompts 会直接 seed 最新默认,无需此迁移
	}
	tmpl := agent.BuiltinPromptSeeds()["goals"]
	if tmpl == "" {
		return
	}
	// 全新库 seedPrompts 已 seed 最新默认 → 当前版本已等于代码默认,不必再追加重复版本。
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] goals 提示词重刷为新默认失败: %v", err)
		return
	}
	log.Printf("[prompts] goals 提示词已追加新默认版本(加入抽操作约束步,一次性)")
}

// reseedMainAgentPrompt 把 mainagent 提示词刷成【当前代码默认】——默认正文新增了「目标全部
// 达成后 add_intent 直投意图时,反问人是否登记为正式目标」这段引导,而 SeedPromptIfEmpty 首插入
// only,旧库已有版本收不到。用版本管理【追加一个新版本】并切过去(ResetPromptToDefault),旧版本仍
// 保留在历史里,用户若自定义过可从版本记录找回。settings flag 守卫 → 只做一次。全新库无需处理
// (SeedPromptIfEmpty 已 seed 最新默认)。与 reseedGoalsPrompt 完全同构。
func (s *Server) reseedMainAgentPrompt() {
	const flag = "mainagent_prompt_goalless_intent_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 无论成功与否只尝试一次
	a, err := s.m.pg.GetAgentByKey("mainagent")
	if err != nil || a == nil {
		return // 全新库尚未建 agent 行时,seedPrompts 会直接 seed 最新默认,无需此迁移
	}
	tmpl := agent.BuiltinPromptSeeds()["mainagent"]
	if tmpl == "" {
		return
	}
	// 全新库 seedPrompts 已 seed 最新默认 → 当前版本已等于代码默认,不必再追加重复版本。
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] mainagent 提示词重刷为新默认失败: %v", err)
		return
	}
	log.Printf("[prompts] mainagent 提示词已追加新默认版本(加入目标达成后反问建目标,一次性)")
}

// reseedPlannerPrompt 把 planner 提示词刷成【当前代码默认】——默认正文做了精简重构,并把「克制」降级为
// 仅去重、新增「深度优先于覆盖度」「硬底线:目标未达成且无在跑意图必须产出」、给否定结论复核加上界。
// 每次默认有实质变更就 bump 下面的 flag(当前 v2)让存量旧库再刷一次。SeedPromptIfEmpty 首插入only,旧库已有版本收不到,故用版本管理
// 【追加一个新版本】并切过去(ResetPromptToDefault),旧版本仍保留在历史里,用户若自定义过可从版本记录
// 找回。settings flag 守卫 → 只做一次。全新库无需处理(SeedPromptIfEmpty 已 seed 最新默认)。与
// reseedGoalsPrompt 完全同构。
func (s *Server) reseedPlannerPrompt() {
	const flag = "planner_prompt_compact_realistic_v2"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 无论成功与否只尝试一次
	a, err := s.m.pg.GetAgentByKey("planner")
	if err != nil || a == nil {
		return // 全新库尚未建 agent 行时,seedPrompts 会直接 seed 最新默认,无需此迁移
	}
	tmpl := agent.BuiltinPromptSeeds()["planner"]
	if tmpl == "" {
		return
	}
	// 全新库 seedPrompts 已 seed 最新默认 → 当前版本已等于代码默认,不必再追加重复版本。
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] planner 提示词重刷为新默认失败: %v", err)
		return
	}
	log.Printf("[prompts] planner 提示词已追加新默认版本(精简重构+克制降级去重+深度优先+否定复核上界,一次性)")
}

// reseedWorkerPrompt 把 worker 提示词刷成【当前代码默认】——默认正文 record_fact 段删掉了「否定类结论
// 写观察+试探性读法」整句、并把 confidence(observed/inferred)与「是否穷尽本意图手段」解耦(这些易误导规划者),
// 同时把 facts 数组分条收紧为「彼此完全独立、无法归并」的极少数例外。bump flag 至 v3 让存量旧库再刷一次。
// SeedPromptIfEmpty 首插入only,旧库已有版本收不到,故用版本管理【追加一个新版本】并切过去,旧版本仍保留在历史里可找回。
// settings flag 守卫 → 只做一次。全新库无需处理。与 reseedGoalsPrompt 完全同构。
func (s *Server) reseedWorkerPrompt() {
	const flag = "worker_prompt_compact_v4"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 无论成功与否只尝试一次
	a, err := s.m.pg.GetAgentByKey("worker")
	if err != nil || a == nil {
		return // 全新库尚未建 agent 行时,seedPrompts 会直接 seed 最新默认,无需此迁移
	}
	tmpl := agent.BuiltinPromptSeeds()["worker"]
	if tmpl == "" {
		return
	}
	// 全新库 seedPrompts 已 seed 最新默认 → 当前版本已等于代码默认,不必再追加重复版本。
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] worker 提示词重刷为新默认失败: %v", err)
		return
	}
	log.Printf("[prompts] worker 提示词已追加新默认版本(查上下文段收敛为 list_assets/list_findings,去掉 list_facts/node_detail/asset_neighbors,一次性)")
}

// reporterToolCallMessage 必须无条件要求先读一次 get_finding_traffic 再写报告。
// 该工具是只读的、「不依赖捕获开关」,自动绑定关不关都能读到人工绑定的证据。若这里
// 写成「启用自动绑定才读」,默认关闭配置下 reporter 就不会传 evidence_version,
// SetFindingReportVersionByNodeID 便按 legacy 语义写 -1,漏洞详情与 Markdown 导出
// 从此常驻「证据已变更，报告待更新」,而 UI 上没有任何入口能把它清掉。
const reporterToolCallMessage = "방금 위에서 취약점 하나가 report_finding 으로 등록되었습니다. 반환 JSON 의 finding_id(독립 취약점 레코드 ID)와 finding_node_id(탐색 노드 ID)를 읽으세요," +
	"먼저 get_finding_traffic(finding_id) 로 현재 증거 목록과 그 version 을 읽으세요(빈 목록은 정상이며 평소대로 보고서를 쓰면 됩니다);" +
	"실행 지침에서 자동 연결이 켜져 있으면, 읽기 전에 이번 취약점의 트래픽을 먼저 확인·연결하세요. 노드 상세는 finding_node_id 를 사용합니다." +
	"마지막으로 update_finding_report(finding_id=finding_node_id, report, evidence_version=실제 읽은 버전)를 호출해 저장하세요," +
	"evidence_version 은 반드시 전달해야 하며, 그렇지 않으면 보고서가 영구히 '갱신 필요'로 표시됩니다. 두 가지 번호를 혼용하지 마세요."

// 旧版触发消息(0.3.8 及更早)。只有仍与它逐字相同的记录才会被迁移覆盖，用户改过的保持原样。
const reporterToolCallMessageV1 = "방금 위에서 취약점 하나가 report_finding 으로 등록되었습니다. 트리거 컨텍스트에서 finding_id 를 꺼내세요" +
	"(도구가 반환한 \"finding recorded: <id>\" 안의 숫자)와 과제 id 를 가지고, 당신의 역할에 따라 해당 취약점의 상세 보고서를 작성하세요," +
	"마지막으로 update_finding_report(finding_id, report)를 호출해 저장하세요."

// upgradeReporterTriggerMessage 把老库里仍是默认文案的 reporter 触发消息刷成新版本。
// seedReporterAgent 受 reporter_agent_seed_v1 守卫且只在新建 agent 时写触发器，所以
// 升级上来的库拿不到新文案 —— 工具 schema 由 seedFindingTrafficTools 补齐了
// evidence_version，但没有任何东西告诉 reporter 去用它。一次性，且只覆盖未被改动的文案。
func (s *Server) upgradeReporterTriggerMessage() {
	const flag = "reporter_trigger_evidence_version_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 只尝试一次
	triggers, err := s.m.pg.ListTriggersFor("reporter")
	if err != nil {
		log.Printf("[reporter] 读取触发器失败: %v", err)
		return
	}
	for _, t := range triggers {
		if !t.OnToolCall || t.ToolCallMessage != reporterToolCallMessageV1 {
			continue // 用户改过或不是 finding 触发器，不动。
		}
		t.ToolCallMessage = reporterToolCallMessage
		if err := s.m.pg.UpdateTrigger(t); err != nil {
			log.Printf("[reporter] 升级触发消息失败: %v", err)
			return
		}
		log.Printf("[reporter] 触发消息已升级为读取并回传 evidence_version")
	}
}

// reporterAgentName·reporterAgentDescription 은 시드되는 reporter 에이전트의 표시 전용
// 라벨이다(한국어화). 에이전트의 두뇌(段[A] agent.ReporterDefaultPrompt)와 트리거 주입
// 메시지(reporterToolCallMessage)는 모델 입력이라 중국어 원문을 보존하지만, 이 이름·설명은
// `agentDTO` 로 system/agents UI 에만 렌더되고(server_mgmt.go) 어떤 프롬프트·에이전트 선택에도
// 들어가지 않는 순수 UI 라벨이라 한국어화한다(BRIEF 사용자 노출 산출물 한국어화 범위). 트리거는
// report_finding 도구 호출 기반(OnToolCall·결정적)이라 reporter 는 key "reporter" 로 참조될 뿐
// 표시 이름으로 선택되지 않는다. 회귀 방어: reporter_seed_localized_test.go. [[F35]]
const reporterAgentName = "보고서 작성"
const reporterAgentDescription = "취약점 상세 보고서 작성: 취약점을 발견하면 자동으로 트리거되어, 증거와 실행 과정을 조회한 뒤 Markdown 보고서를 작성해 해당 취약점에 기록합니다."

// seedReporterAgent 预置一个「보고서 작성」自定义 agent(builtin=false，可在 UI 编辑/删除)：
// 绑定 update_finding_report + 任务查询工具，并挂一个「report_finding 被调用即触发」的
// 触发器 —— 每登记一个漏洞就唤起它写详细报告。一次性(settings flag 守卫)：用户删掉后不再重建。
// 依赖：orchestration 工具已在本函数上方 SeedTool 入库，故绑定得上。
func (s *Server) seedReporterAgent() {
	const flag = "reporter_agent_seed_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 无论成功与否只尝试一次

	if exist, _ := s.m.pg.GetAgentByKey("reporter"); exist != nil {
		return // key 已被占用(用户手建过)——不覆盖
	}
	a, err := s.m.pg.CreateAgent("reporter", reporterAgentName, reporterAgentDescription)
	if err != nil {
		log.Printf("[reporter] 创建 agent 失败: %v", err)
		return
	}
	if err := s.m.pg.SeedPromptIfEmpty(a.ID, agent.ReporterDefaultPrompt); err != nil {
		log.Printf("[reporter] seed prompt 失败: %v", err)
	}
	// 触发运行策略：parallel + none —— 一漏洞一报告、多个 finding 并发各写各的。
	// merge 必须为 none：否则(默认 all)一波 finding 会被合并成一次运行，并行就没意义。
	// maxParallel=5：同时最多 5 个报告会话，避免瞬时太多 LLM 调用。
	if err := s.m.pg.SetAgentTriggerBehavior("reporter", "parallel", "none", 5); err != nil {
		log.Printf("[reporter] 设置触发运行策略失败: %v", err)
	}
	// 绑定它需要的工具：写报告 + 读证据/执行过程/态势。
	if err := s.m.pg.AddAgentToToolBinding("reporter", []string{
		"update_finding_report", "get_task_node_detail", "list_task_findings",
		"get_task_worker_trace", "list_task_worker_traces", "search_task_worker_traces",
		"get_task_graph",
	}); err != nil {
		log.Printf("[reporter] 绑定工具失败: %v", err)
	}
	// 触发器：report_finding 被调用即触发（工具返回 "finding recorded: <id>" 带上 finding_id，
	// 과제 id 也在触发消息里）。
	if _, err := s.m.pg.CreateTrigger(&db.AgentTrigger{
		AgentKey:        "reporter",
		Enabled:         true,
		OnToolCall:      true,
		ToolNames:       []string{"report_finding"},
		ToolCallMessage: reporterToolCallMessage,
	}); err != nil {
		log.Printf("[reporter] 创建触发器失败: %v", err)
	}
	log.Printf("[reporter] 「%s」 agent + finding 트리거 사전 구성", reporterAgentName)
}

// seedAutoReportFindingBinding adds "auto" to report_finding's binding ONCE so
// conversation-context agents can call it without requiring an intent_id.
func (s *Server) seedAutoReportFindingBinding() {
	const flag = "auto_report_finding_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("auto", []string{"report_finding"}); err != nil {
		log.Printf("[auto] report_finding 默认绑定失败: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedPlannerDefaultBindings adds "planner" to report_finding's binding ONCE
// (guarded by a settings flag), so existing DBs — whose report_finding row was
// seeded as worker-only — also let the planner record findings. Fresh DBs already
// get it via PlannerTools(); this only backfills without overriding a user unbind.
func (s *Server) seedPlannerDefaultBindings() {
	const flag = "planner_report_finding_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("planner", []string{"report_finding"}); err != nil {
		log.Printf("[planner] report_finding 默认绑定失败: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedPlannerListAssetsBinding adds "planner" to list_assets's binding ONCE
// (guarded by a settings flag), so existing DBs — whose list_assets row was seeded
// as auto/pentest-only — also let the planner query the asset store by DSL. Fresh
// DBs already get it via PlannerTools(); this only backfills without overriding a
// user unbind.
func (s *Server) seedPlannerListAssetsBinding() {
	const flag = "planner_list_assets_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("planner", []string{"list_assets"}); err != nil {
		log.Printf("[planner] list_assets 默认绑定失败: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedCompanyScopeRebind changes add_company_scope's default binding ONCE on
// existing DBs (guarded by a settings flag): the tool moves off worker and onto
// planner — defining a company's asset scope is a planning/main/auto concern, not
// something a worker does mid-exploration. Fresh DBs already get planner via
// PlannerTools() and lack worker via WorkerTools(); this only backfills old rows.
// One-shot + flag-guarded so a user who later re-binds worker isn't overridden.
func (s *Server) seedCompanyScopeRebind() {
	const flag = "company_scope_rebind_v1" // worker→planner 默认绑定切换
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("planner", []string{"add_company_scope"}); err != nil {
		log.Printf("[planner] add_company_scope 默认绑定失败: %v", err)
		return
	}
	if err := s.m.pg.RemoveAgentFromTool("worker", "add_company_scope"); err != nil {
		log.Printf("[worker] add_company_scope 解绑失败: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedWorkerReadToolsUnbind strips the read-context tools off worker's default
// binding ONCE on existing DBs (guarded by a settings flag): a worker executes one
// intent and writes back — reading facts/companies and listing all workers' traces is
// a planning/main concern, not the executor's. Fresh DBs already lack these via
// WorkerTools(); this only backfills old rows without overriding a user who
// deliberately re-binds worker. Each RemoveAgentFromTool is per-tool +
// membership-guarded, so planner/mainagent bindings of the same tool are untouched.
//
// NOTE: search_all_worker_traces / get_worker_trace / node_detail are intentionally NOT
// unbound — worker owns them for cross-work look-back + node drill-down (see WorkerTools).
// They used to be in this list back when worker lacked them; seedWorkerReadbackRebind
// repairs DBs whose old run stripped them.
func (s *Server) seedWorkerReadToolsUnbind() {
	const flag = "worker_readtools_unbind_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	for _, k := range []string{
		"list_facts", "list_companies", "list_worker_traces",
	} {
		if err := s.m.pg.RemoveAgentFromTool("worker", k); err != nil {
			log.Printf("[worker] %s 从 worker 解绑失败: %v", k, err)
			return // 出错则不落 flag，下次启动重试
		}
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedWorkerReadbackRebind re-binds the cross-work look-back / drill-down tools onto
// worker ONCE (guarded by a settings flag): an earlier seedWorkerReadToolsUnbind wrongly
// stripped search_all_worker_traces / get_worker_trace / node_detail from worker after
// they had been added to WorkerTools(), so any DB that ran that migration lost them.
// Fresh DBs already have them via WorkerTools() and this is a harmless no-op there.
// One-shot + flag-guarded so a user who later deliberately unbinds them isn't overridden.
func (s *Server) seedWorkerReadbackRebind() {
	const flag = "worker_readback_rebind_v2" // v2: 追加 node_detail
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("worker", []string{
		"search_all_worker_traces", "get_worker_trace", "node_detail",
	}); err != nil {
		log.Printf("[worker] 回看/详情工具补绑失败: %v", err)
		return // 出错则不落 flag，下次启动重试
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedAutoDefaultBindings adds "auto" to the task-op + platform tools' bindings
// ONCE (guarded by a settings flag), so existing DBs whose tool rows were seeded
// before Auto existed still give Auto its default toolset — without re-adding it
// after a user deliberately unbinds.
func (s *Server) seedAutoDefaultBindings() {
	const flag = "auto_default_bindings_v3" // v3: 替换旧资产工具名，加入 insert_assets/add_company_scope
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	keys := make([]string, 0, len(platformToolKeys)+12)
	for _, t := range s.orchestrationTools() {
		keys = append(keys, t.Name())
	}
	keys = append(keys, platformToolKeys...)
	// 资产工具：Auto 操作平台常要看/登记资产、管理公司范围。
	keys = append(keys, "insert_assets", "add_company_scope", "list_assets")
	if err := s.m.pg.AddAgentToToolBinding("auto", keys); err != nil {
		log.Printf("[auto] 默认绑定失败: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// localizeKoResync propagates the Korean-localized built-in tool descriptions/schemas
// and agent prompt bodies into an already-seeded DB ONCE (guarded by a settings flag).
// SeedTool / SeedPromptIfEmpty are first-insert-only, so a DB seeded from the earlier
// Chinese defaults never picks up the translation otherwise. Fresh DBs seed the Korean
// defaults directly and skip the per-row work (current already equals the code default).
// Preserves each tool's agent binding + enabled flag and keeps old prompt versions in
// history. Bump the flag if the localized defaults change again.
func (s *Server) localizeKoResync() {
	const flag = "localize_ko_v3"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }()

	// 1) Built-in tool descriptions + parameter schemas (desc/param text only; binding kept).
	for _, t := range s.orchestrationTools() {
		schema, _ := json.Marshal(t.InputSchema())
		_ = s.m.pg.RefreshToolDefaults(t.Name(), t.Description(), schema)
	}
	for _, t := range s.platformTools() {
		schema, _ := json.Marshal(t.InputSchema())
		_ = s.m.pg.RefreshToolDefaults(t.Name(), t.Description(), schema)
	}
	for _, sd := range agent.BuiltinToolSeeds() {
		schema, _ := json.Marshal(sd.Schema)
		_ = s.m.pg.RefreshToolDefaults(sd.Key, sd.Desc, schema)
	}
	// Traffic + finding-evidence tools are seeded via their own paths (traffic.SeedToolMetas,
	// findingRetestTools, toolGetFindingTraffic, toolBindFindingTraffic), so refresh them too.
	koTools := traffic.SeedToolMetas()
	koTools = append(koTools, s.findingRetestTools()...)
	koTools = append(koTools, s.toolGetFindingTraffic(), s.toolBindFindingTraffic())
	for _, t := range koTools {
		schema, _ := json.Marshal(t.InputSchema())
		_ = s.m.pg.RefreshToolDefaults(t.Name(), t.Description(), schema)
	}

	// 2) Agent prompt bodies — append a new version from the Korean code default and
	//    switch to it (old versions stay in history). Skip if already equal.
	resetPrompt := func(key, tmpl string) {
		if tmpl == "" {
			return
		}
		a, err := s.m.pg.GetAgentByKey(key)
		if err != nil || a == nil {
			return
		}
		if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
			return
		}
		if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
			log.Printf("[prompts] localize_ko %s 재동기화 실패: %v", key, err)
		}
	}
	for key, tmpl := range agent.BuiltinPromptSeeds() {
		resetPrompt(key, tmpl)
	}
	resetPrompt(agent.ReporterSeedKey, agent.ReporterDefaultPrompt)
	resetPrompt(agent.RetesterSeedKey, agent.RetesterDefaultPrompt)
	log.Printf("[localize] 내장 도구·프롬프트를 한국어 코드 기본값으로 재동기화(1회성)")
}
