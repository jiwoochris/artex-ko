package server

import (
	"encoding/json"
	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/traffic"
	"reflect"
)

var koreanToolDescriptions = map[string]string{
	"traffic_search":               "기록 프록시가 수집한 트래픽을 조회합니다. host는 필수이며 호스트, 호스트:포트 또는 전체 URL을 지원합니다. 지정한 포트의 서비스만 조회하며 URL 부분 문자열 또는 body_contains로 요청/응답 헤더와 본문을 검색합니다(최소 3자, 부분 문자열과 중국어 지원). 응답 내용 없이 id/method/url/status/resp_len 인덱스만 반환합니다. 결과가 있으면 traffic_get으로 각각 검증하고 현재 취약점을 실제로 뒷받침하는 ID만 bind_finding_traffic에 전달하세요. 기본 3개, 페이지당 최대 10개이며 page로 다음 페이지를 조회합니다.",
	"traffic_get":                  "id로 수집된 트래픽의 요청/응답 원문을 조회합니다(큰 내용은 잘립니다). traffic_search와 함께 사용하여 같은 URL에 curl을 반복하지 않도록 합니다.",
	"traffic_blob":                 "큰 요청/응답 본문 원문을 구간별로 읽습니다. traffic_get에서 '…[truncated] @blob sha256:<hash>'로 표시된 부분은 해당 hash로 조회할 수 있습니다. 한 번에 최대 8KB를 반환하며 offset으로 이어서 읽습니다(결과에 전체 길이 포함). 인라인 한도를 넘는 백업 파일, 유출 소스 코드, 대형 JSON 내보내기 등에 사용합니다.",
	"graph_overview":               "탐색 현황 요약: 자산 수, 인터페이스가 없는 사이트, 대기 중인 계획, 발견 사항과 전략 힌트를 확인합니다.",
	"list_findings":                "현재 작업 및 직접 연결된 작업의 확인된 취약점을 조회합니다. 연결된 작업의 항목은 읽기 전용입니다.",
	"list_facts":                   "탐색 사실과 결론을 최신순으로 조회합니다. 키워드 검색과 커서 기반 페이지 나누기를 지원합니다.",
	"node_detail":                  "탐색 그래프 노드의 전체 내용을 조회합니다. 상속된 노드는 읽기 전용이며 자산 조회에는 사용할 수 없습니다.",
	"expand_digest":                "압축된 요약의 구성 항목을 펼쳐 id, 요약, 상태와 신뢰도를 조회합니다.",
	"get_worker_output":            "실행 계획의 최종 결론을 조회합니다. 중단되거나 실패한 실행은 마지막 출력을 반환합니다.",
	"get_worker_trace":             "실행 계획의 단계별 요약을 조회하고 키워드로 검색하거나 지정한 단계의 전체 내용을 확인합니다(최대 5개).",
	"search_all_worker_traces":     "다른 실행 계획의 실행 기록을 키워드로 검색합니다. 자신의 실행 기록은 제외하며 일치하는 단계의 요약을 반환합니다.",
	"add_hint":                     "다음 계획 수립에 반영할 전략 힌트를 탐색 그래프에 추가합니다. 여러 힌트를 일괄 제출할 수 있습니다.",
	"add_intent":                   "새 탐색 방향을 실행 대기열과 탐색 그래프에 추가합니다. 여러 실행 계획을 일괄 제출할 수 있습니다.",
	"steer_work":                   "실행 중인 계획에 실시간 방향 수정 지시를 전달합니다. 실행을 중단하거나 기존 진행 내용을 버리지 않습니다.",
	"set_goals":                    "작업에 검증 가능한 최종 목표를 추가합니다. 공격 단계가 아닌 결과를 등록하며 여러 목표를 일괄 제출할 수 있습니다.",
	"set_constraints":              "작업의 허용 또는 금지 작업 경계를 등록합니다. 작업 목표나 설명에 명시된 제약만 추가합니다.",
	"insert_assets":                "새로 발견한 자산을 유형별 필수 필드에 맞춰 일괄 등록합니다. 인증 정보, 기술과 매개변수는 기존 값에 추가 병합합니다.",
	"add_company_scope":            "기업의 자산 범위에 도메인, IP, CIDR, ICP 등록 정보 또는 키워드를 추가합니다. 소속 근거가 필요하며 지나치게 넓은 범위는 거부합니다.",
	"list_assets":                  "현재 작업 및 직접 연결된 작업 범위의 자산을 DSL 검색식이나 id/ids로 조회합니다. 조건 없는 전체 조회는 허용하지 않습니다.",
	"report_finding":               "검증 가능한 증거와 함께 확인된 취약점을 등록합니다. finding_id는 독립 취약점 ID이며 finding_node_id는 탐색 노드 ID입니다.",
	"record_fact":                  "실제 도구 출력에서 관찰한 탐색 사실과 결론을 증거 및 신뢰도와 함께 기록합니다. 한 번의 탐색에서 얻은 관찰은 하나의 사실로 요약합니다.",
	"add_task_scope":               "작업의 승인된 테스트 범위에 기업, 도메인, IP, CIDR, ICP 등록 정보 또는 키워드를 추가합니다. 범위 확대 근거를 기록해야 합니다.",
	"list_untested_assets":         "현재 작업 및 직접 연결된 작업 범위에서 아직 사실로 검증되지 않은 자산을 조회합니다. 유형 필터와 페이지 나누기를 지원합니다.",
	"list_goals":                   "현재 작업의 목표 노드와 달성 상태(open/met)를 조회합니다.",
	"prove_goal":                   "발견 사항이나 사실이 목표 달성을 입증하면 증거 노드를 목표에 연결하고 met로 표시합니다.",
	"goal_met":                     "모든 목표가 실제로 달성된 경우에만 전체 작업을 즉시 종료합니다. 개별 목표 달성이나 계획 회차 종료에는 사용하지 않습니다.",
	"kill_work":                    "실행 중인 계획을 종료하고 stopped로 표시합니다. 자동으로 다시 배정되지 않으며 종료 전에 실행 출력을 확인해야 합니다.",
	"list_companies":               "기업 목록, 자산 범위 및 소속 자산 수를 조회합니다. 기업 이름으로 검색할 수 있습니다.",
	"list_tasks":                   "전체 작업의 ID, 설명, 목표, 상태, 실행 시간, 상위 작업과 LLM 설정을 조회합니다.",
	"list_llm_profiles":            "사용 가능한 LLM 설정의 ID, 이름, 모델, 형식과 활성 여부를 조회합니다. API 키는 포함하지 않습니다.",
	"spawn_task":                   "하위 작업을 생성하고 탐색 엔진을 시작합니다. task_id를 반환하며 parent_ref로 상위 작업을 연결할 수 있습니다.",
	"pause_task":                   "지정한 작업의 계획 및 실행 루프를 일시 중지합니다.",
	"get_task_graph":               "지정한 작업의 탐색 그래프 현황, 자산 수, 실행 대기열, 발견 사항과 검증 범위를 조회합니다.",
	"list_task_findings":           "지정한 작업의 확인된 취약점과 flag/PoC, 분류, 심각도, 요약 및 상태를 조회합니다.",
	"add_task_hint":                "지정한 작업의 다음 계획 수립에 반영할 전략 힌트를 추가합니다. 일괄 제출을 지원합니다.",
	"get_task_worker_trace":        "지정한 작업의 실행 계획 기록을 조회합니다. 단계 요약을 확인한 뒤 최대 5개 단계의 전체 내용을 요청할 수 있습니다.",
	"list_task_worker_traces":      "지정한 작업에서 실행된 계획과 각 계획의 단계 수를 조회합니다.",
	"search_task_worker_traces":    "지정한 작업의 모든 실행 기록을 키워드로 검색하여 단계 요약과 intent_id를 반환합니다.",
	"get_task_node_detail":         "지정한 작업의 탐색 노드 전체 내용을 조회합니다. 취약점 보고서 작성 전에 증거와 PoC를 확인할 수 있습니다.",
	"update_finding_report":        "등록된 취약점의 상세 Markdown 보고서를 작성하거나 갱신합니다. 기존 보고서 전체를 덮어씁니다.",
	"get_finding_traffic":          "취약점에 연결된 실제 HTTP 트래픽 증거를 조회합니다. 독립 취약점 ID를 사용하며 본문을 구간별로 읽을 수 있습니다.",
	"bind_finding_traffic":         "검증된 실제 HTTP 트래픽을 기존 취약점에 연결합니다. 독립 취약점 ID를 사용하며 일괄 연결은 모두 성공하거나 모두 실패합니다.",
	"create_skill":                 "agentskills.io 규격의 SKILL.md를 작성하여 새 스킬을 생성합니다. 이름에는 소문자, 숫자와 하이픈을 사용합니다.",
	"update_skill_file":            "스킬의 파일을 작성하거나 덮어씁니다(기본 SKILL.md). 스킬 내용, 스크립트 또는 참조 파일을 수정할 수 있습니다.",
	"create_custom_tool":           "설치한 도구를 플랫폼에서 사용할 수 있도록 사용자 정의 도구(shell/command/script/http)를 생성합니다. shell은 key, description, agents만 필요합니다.",
	"update_custom_tool":           "key로 지정한 기존 사용자 정의 도구를 수정합니다.",
	"create_mcp":                   "MCP 서버(stdio/http/sse)를 생성합니다. 생성 후 에이전트별 도구 접근 권한을 설정해야 합니다.",
	"update_mcp":                   "id로 지정한 기존 MCP 서버를 수정합니다.",
	"delete_assets_by_host":        "host와 정확히 일치하는 자산 및 관련 서비스와 인터페이스를 삭제합니다. 루트 도메인은 하위 도메인도 삭제하며 전역 자산 저장소에서 복구할 수 없습니다.",
	"get_finding_retest_context":   "현재 재검증 대화에 연결된 취약점 증거, 재검증 상태, 보충 설명과 작업 제약을 조회합니다. 현재 대화만 조회할 수 있습니다.",
	"record_finding_retest_result": "현재 재검증 대화의 단일 결론을 저장하며 기존 증거와 보고서는 보존합니다. 성공적으로 종료된 대화의 결론이 fixed이면 취약점을 수정됨으로 표시합니다.",
}

// Metadata-only constructors: none of the tools are called here.
var toolPresentationSchemas = make(map[string]any)

var toolPresentationDefaults = func() map[string]string {
	defaults := make(map[string]string)
	for _, seed := range agent.BuiltinToolSeeds() {
		defaults[seed.Key] = seed.Desc
		toolPresentationSchemas[seed.Key] = normalizedSchema(seed.Schema)
	}
	s := &Server{}
	for _, tool := range append(append(s.orchestrationTools(), s.platformTools()...), append(s.findingRetestTools(), (&traffic.Traffic{}).Tools()...)...) {
		defaults[tool.Name()] = tool.Description()
		toolPresentationSchemas[tool.Name()] = normalizedSchema(tool.InputSchema())
	}
	return defaults
}()

// toolDTOs copies catalog rows at the HTTP boundary; the runtime keeps raw rows.
type ToolDTO struct {
	db.Tool
	DisplaySchema json.RawMessage `json:"display_schema,omitempty"`
}

func normalizedSchema(schema any) any {
	b, _ := json.Marshal(schema)
	var v any
	_ = json.Unmarshal(b, &v)
	return v
}

// Only a complete code-default schema qualifies; any customization is left raw.
// JSON decoding creates an independent tree, never an alias of runtime metadata.
func toolDisplaySchema(row *db.Tool) json.RawMessage {
	if !row.System || row.Kind != "builtin" {
		return nil
	}
	expected, ok := toolPresentationSchemas[row.Key]
	if !ok {
		return nil
	}
	var copy any
	if json.Unmarshal(row.Schema, &copy) != nil || !reflect.DeepEqual(copy, expected) {
		return nil
	}
	localizeSchemaHelp(copy)
	b, _ := json.Marshal(copy)
	return b
}

func localizeSchemaHelp(v any) {
	switch x := v.(type) {
	case map[string]any:
		for _, key := range []string{"description", "help"} {
			if text, ok := x[key].(string); ok {
				if ko, found := koreanSchemaDescriptions[text]; found {
					x[key] = ko
				}
			}
		}
		if props, ok := x["properties"].(map[string]any); ok {
			for _, prop := range props {
				localizeSchemaHelp(prop)
			}
		}
		localizeSchemaHelp(x["items"])
	case []any:
		for _, item := range x {
			localizeSchemaHelp(item)
		}
	}
}

func toolDTOs(in []*db.Tool) []ToolDTO {
	out := make([]ToolDTO, 0, len(in))
	for _, row := range in {
		dto := ToolDTO{Tool: *row, DisplaySchema: toolDisplaySchema(row)}
		if korean, ok := koreanToolDescriptions[row.Key]; ok && row.System && row.Kind == "builtin" && row.Description == toolPresentationDefaults[row.Key] {
			dto.Description = korean
		}
		out = append(out, dto)
	}
	return out
}

// Presentation-only exact matches: never write these values back to the database
// or use them when assembling agent prompts or LLM tool schemas.
var koreanAgentDefaults = map[string]struct {
	name, description, koreanName, koreanDescription string
	builtin                                          bool
}{
	"goals":     {"目标拆解", "把渗透任务目标拆解成若干独立、可验证的子目标。", "목표 분해", "침투 테스트 목표를 독립적으로 검증할 수 있는 하위 목표로 나눕니다.", true},
	"planner":   {"规划", "读取态势、判定目标，只在确有未覆盖的新方向时补充探索意图（每任务一个规划循环）。", "계획", "현황과 목표를 검토하고, 아직 다루지 않은 새로운 방향이 있을 때만 탐색 계획을 추가합니다(작업별 계획 루프).", true},
	"mainagent": {"主", "人机接口：观察进展，把人的意图落成 hint 或高优先级意图。", "메인", "진행 상황을 확인하고 사용자의 의도를 힌트 또는 우선순위가 높은 실행 계획으로 전달합니다.", true},
	"worker":    {"执行", "领取一条意图执行，把发现的事实/漏洞写回知识图谱后停止。", "실행", "실행 계획 하나를 수행하고 발견한 사실과 취약점을 지식 그래프에 기록한 뒤 종료합니다.", true},
	"auto":      {"Auto", "平台操作助手：用工具管理任务(建/看/暂停/给提示)与资产，并可创建/修改 skill、自定义工具、MCP。", "Auto", "도구로 작업(생성·조회·일시 중지·힌트)과 자산을 관리하고 스킬, 사용자 정의 도구, MCP를 생성하거나 수정하는 플랫폼 도우미입니다.", true},
	"pentest":   {"渗透测试", "独立渗透 agent：一人从侦察→找攻击面→深入利用→验证→收尾走完整条链，自己规划、自己执行、自己对抗式验证。", "침투 테스트", "정찰부터 공격 표면 탐색, 심층 활용, 검증, 마무리까지 독립적으로 계획하고 실행하며 대항 검증하는 에이전트입니다.", true},
	"reporter":  {"报告撰写", "漏洞详细报告撰写：发现漏洞时自动触发，查取证据与执行过程后写 Markdown 报告并回写。", "보고서 작성", "취약점 발견 시 자동으로 실행되어 증거와 실행 과정을 확인하고 상세 Markdown 보고서를 작성해 저장합니다.", false},
	"retester":  {"漏洞复测", "从漏洞详情手动启动，读取原证据并保存独立复测结论。", "취약점 재검증", "취약점 상세 화면에서 수동으로 실행하여 기존 증거를 확인하고 독립적인 재검증 결과를 저장합니다.", false},
}

// Only HTTP serialization translates shared validation errors; internal tool
// results and logs keep their original text.
func httpErrorPresentation(msg string) string {
	switch msg {
	case "纠偏消息不能为空":
		return "방향 수정 메시지는 비워 둘 수 없습니다"
	case "offset / length 不能为负数":
		return "offset / length는 음수일 수 없습니다"
	case "offset 超出正文长度":
		return "offset이 본문 길이를 초과했습니다"
	case "任务不存在":
		return "작업을 찾을 수 없습니다"
	case "当前任务不可读取该漏洞":
		return "현재 작업에서 이 취약점을 조회할 수 없습니다"
	case "继承漏洞的流量证据只读，请到来源任务修改":
		return "상속된 취약점의 트래픽 증거는 읽기 전용입니다. 원본 작업에서 수정하세요"
	case "finding_id 必须为独立漏洞记录 ID；不是探索节点 ID":
		return "finding_id는 탐색 노드 ID가 아닌 독립 취약점 기록 ID여야 합니다"
	}
	return msg
}

// An unchanged localized form must not overwrite execution metadata in storage.
// A genuinely edited value still passes through unchanged.
func presentationSubmission(raw, shown, submitted string) string {
	if submitted == shown {
		return raw
	}
	return submitted
}

func agentPresentation(a AgentDTO) AgentDTO {
	if d, ok := koreanAgentDefaults[a.Key]; ok && a.Builtin == d.builtin {
		if a.Name == d.name {
			a.Name = d.koreanName
		}
		if a.Description == d.description {
			a.Description = d.koreanDescription
		}
	}
	return a
}
