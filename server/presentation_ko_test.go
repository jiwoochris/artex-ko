package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/traffic"
	"net/http/httptest"
	"reflect"
	"testing"
	"unicode"
)

func newPresentationTestServer() *Server {
	m := &Manager{tasks: map[string]*Task{}}
	s := &Server{m: m, engine: NewEngine(m), ctx: context.Background()}
	s.initToolPresentation()
	return s
}

func presentationSeeds() []agent.ToolSeed {
	seeds := agent.BuiltinToolSeeds()
	s := newPresentationTestServer()
	for _, tool := range append(append(s.orchestrationTools(), s.platformTools()...), append(s.findingRetestTools(), traffic.SeedToolMetas()...)...) {
		seeds = append(seeds, agent.ToolSeed{Key: tool.Name(), Desc: tool.Description(), Schema: tool.InputSchema()})
	}
	return seeds
}

func TestToolPresentationKoreanDefaults(t *testing.T) {
	for _, seed := range presentationSeeds() {
		t.Run(seed.Key, func(t *testing.T) {
			schema, _ := json.Marshal(seed.Schema)
			raw := &db.Tool{Key: seed.Key, System: true, Kind: "builtin", Description: seed.Desc, Schema: schema, Agents: []string{"worker"}, Enabled: true, Calls: 7}
			before, _ := json.Marshal(raw)
			dto := newPresentationTestServer().toolDTOs([]*db.Tool{raw})[0]
			hasKorean := false
			for _, r := range dto.Description {
				if unicode.Is(unicode.Hangul, r) {
					hasKorean = true
				}
			}
			if !hasKorean {
				t.Errorf("no Korean presentation for %s: %q", seed.Key, seed.Desc)
			}
			after, _ := json.Marshal(raw)
			if string(before) != string(after) || string(dto.Schema) != string(schema) {
				t.Fatal("presentation mutated raw schema or metadata")
			}
			if dto.Key != raw.Key || dto.Calls != 7 || !reflect.DeepEqual(dto.Agents, raw.Agents) {
				t.Fatal("non-presentation metadata changed")
			}
			raw.Description = "用户编辑的自定义说明"
			if newPresentationTestServer().toolDTOs([]*db.Tool{raw})[0].Description != raw.Description {
				t.Fatal("custom description translated")
			}
			raw.Description = seed.Desc
			raw.System = false
			raw.Kind = "command"
			if newPresentationTestServer().toolDTOs([]*db.Tool{raw})[0].Description != raw.Description {
				t.Fatal("custom tool translated")
			}
		})
	}
}

func TestPresentationPreservesInternalLLMMetadata(t *testing.T) {
	s := newPresentationTestServer()
	tools := append(append(s.orchestrationTools(), s.platformTools()...), s.findingRetestTools()...)
	for _, tool := range tools {
		schema, _ := json.Marshal(tool.InputSchema())
		name, description, prompt := tool.Name(), tool.Description(), tool.Prompt()
		row := &db.Tool{Key: name, System: true, Kind: "builtin", Description: description, Schema: schema}
		_ = newPresentationTestServer().toolDTOs([]*db.Tool{row})
		var schemaMap map[string]any
		_ = json.Unmarshal(row.Schema, &schemaMap)
		llmTool := agent.DecorateTool(tool, row.Description, schemaMap)
		if llmTool.Name() != name || llmTool.Description() != description || llmTool.Prompt() != prompt {
			t.Fatalf("LLM metadata changed for %s", name)
		}
		after, _ := json.Marshal(llmTool.InputSchema())
		if !bytes.Equal(schema, after) {
			t.Fatalf("LLM schema changed for %s", name)
		}
	}
	// No tool is invoked; constructors only expose metadata.
	before := []string{agent.DefaultAssistantPrompt, agent.ReporterDefaultPrompt, agent.RetesterDefaultPrompt}
	_ = agentDTOs([]*db.Agent{{Key: "reporter", Name: "报告撰写"}, {Key: "retester", Name: "漏洞复测"}})
	if !reflect.DeepEqual(before, []string{agent.DefaultAssistantPrompt, agent.ReporterDefaultPrompt, agent.RetesterDefaultPrompt}) {
		t.Fatal("internal agent prompts changed")
	}
}

func TestPresentationHTTPEnvelopes(t *testing.T) {
	for _, seed := range presentationSeeds() {
		schema, _ := json.Marshal(seed.Schema)
		row := &db.Tool{Key: seed.Key, System: true, Kind: "builtin", Description: seed.Desc, Schema: schema}
		rec := httptest.NewRecorder()
		writeJSON(rec, 200, map[string]any{"tools": newPresentationTestServer().toolDTOs([]*db.Tool{row})})
		var response struct {
			Tools []db.Tool `json:"tools"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if rec.Code != 200 || len(response.Tools) != 1 || response.Tools[0].Description != koreanToolDescriptions[seed.Key] {
			t.Fatalf("HTTP tool presentation: %s", rec.Body.String())
		}
		if !bytes.Equal(schema, response.Tools[0].Schema) {
			t.Fatal("HTTP tool schema changed")
		}
	}
	rec := httptest.NewRecorder()
	writeJSON(rec, 200, map[string]any{"agents": agentDTOs([]*db.Agent{{Key: "goals", Name: "目标拆解", Builtin: true}})})
	var response struct {
		Agents []AgentDTO `json:"agents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(response.Agents) != 1 || response.Agents[0].Name != "목표 분해" {
		t.Fatalf("HTTP agent presentation: %s", rec.Body.String())
	}
}

func TestPresentationSubmissionPreservesRawDefaults(t *testing.T) {
	for _, seed := range presentationSeeds() {
		raw := &db.Tool{Key: seed.Key, System: true, Kind: "builtin", Description: seed.Desc}
		shown := newPresentationTestServer().toolDTOs([]*db.Tool{raw})[0]
		if got := presentationSubmission(raw.Description, shown.Description, shown.Description); got != seed.Desc {
			t.Errorf("%s roundtrip changed LLM description: %q", seed.Key, got)
		}
		custom := "사용자가 직접 수정한 설명"
		if got := presentationSubmission(raw.Description, shown.Description, custom); got != custom {
			t.Fatal("user edit was discarded")
		}
	}
	for key, seed := range koreanAgentDefaults {
		a := &db.Agent{Key: key, Name: seed.name, Description: seed.description, Builtin: seed.builtin}
		shown := agentDTO(a)
		if got := presentationSubmission(a.Name, shown.Name, shown.Name); got != a.Name {
			t.Errorf("%s name changed on roundtrip", key)
		}
		if got := presentationSubmission(a.Description, shown.Description, shown.Description); got != a.Description {
			t.Errorf("%s description changed on roundtrip", key)
		}
	}
}

// API error classification must remain status-based regardless of localized text.
// This checks the wire contract without asserting frontend source strings.
func TestLocalizedHTTPErrorStatusesRemainStable(t *testing.T) {
	for _, raw := range []string{"纠偏消息不能为空", "offset / length 不能为负数", "offset 超出正文长度", "任务不存在", "当前任务不可读取该漏洞", "继承漏洞的流量证据只读，请到来源任务修改", "finding_id 必须为独立漏洞记录 ID；不是探索节点 ID"} {
		for _, status := range []int{400, 401, 403, 404} {
			w := httptest.NewRecorder()
			writeErr(w, status, raw)
			var body map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != status || body["error"] == raw || body["error"] != httpErrorPresentation(raw) {
				t.Fatalf("localized error altered wire classification: %d %s", w.Code, w.Body.String())
			}
		}
	}
}

func TestHTTPErrorPresentationKorean(t *testing.T) {
	for raw, want := range map[string]string{
		"纠偏消息不能为空":                          "방향 수정 메시지는 비워 둘 수 없습니다",
		"offset / length 不能为负数":             "offset / length는 음수일 수 없습니다",
		"offset 超出正文长度":                     "offset이 본문 길이를 초과했습니다",
		"任务不存在":                             "작업을 찾을 수 없습니다",
		"当前任务不可读取该漏洞":                       "현재 작업에서 이 취약점을 조회할 수 없습니다",
		"继承漏洞的流量证据只读，请到来源任务修改":              "상속된 취약점의 트래픽 증거는 읽기 전용입니다. 원본 작업에서 수정하세요",
		"finding_id 必须为独立漏洞记录 ID；不是探索节点 ID": "finding_id는 탐색 노드 ID가 아닌 독립 취약점 기록 ID여야 합니다",
		"用户自定义错误":                           "用户自定义错误",
	} {
		rec := httptest.NewRecorder()
		writeErr(rec, 400, raw)
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if rec.Code != 400 || body["error"] != want {
			t.Errorf("HTTP error = %d %s; want %q", rec.Code, rec.Body.String(), want)
		}
	}
}

func TestAgentPresentationKoreanDefaults(t *testing.T) {
	cases := []struct {
		key, name, description, wantName, wantDescription string
		builtin                                           bool
	}{
		{"goals", "目标拆解", "把渗透任务目标拆解成若干独立、可验证的子目标。", "목표 분해", "침투 테스트 목표를 독립적으로 검증할 수 있는 하위 목표로 나눕니다.", true},
		{"planner", "规划", "读取态势、判定目标，只在确有未覆盖的新方向时补充探索意图（每任务一个规划循环）。", "계획", "현황과 목표를 검토하고, 아직 다루지 않은 새로운 방향이 있을 때만 탐색 계획을 추가합니다(작업별 계획 루프).", true},
		{"mainagent", "主", "人机接口：观察进展，把人的意图落成 hint 或高优先级意图。", "메인", "진행 상황을 확인하고 사용자의 의도를 힌트 또는 우선순위가 높은 실행 계획으로 전달합니다.", true},
		{"worker", "执行", "领取一条意图执行，把发现的事实/漏洞写回知识图谱后停止。", "실행", "실행 계획 하나를 수행하고 발견한 사실과 취약점을 지식 그래프에 기록한 뒤 종료합니다.", true},
		{"auto", "Auto", "平台操作助手：用工具管理任务(建/看/暂停/给提示)与资产，并可创建/修改 skill、自定义工具、MCP。", "Auto", "도구로 작업(생성·조회·일시 중지·힌트)과 자산을 관리하고 스킬, 사용자 정의 도구, MCP를 생성하거나 수정하는 플랫폼 도우미입니다.", true},
		{"pentest", "渗透测试", "独立渗透 agent：一人从侦察→找攻击面→深入利用→验证→收尾走完整条链，自己规划、自己执行、自己对抗式验证。", "침투 테스트", "정찰부터 공격 표면 탐색, 심층 활용, 검증, 마무리까지 독립적으로 계획하고 실행하며 대항 검증하는 에이전트입니다.", true},
		{"reporter", "报告撰写", "漏洞详细报告撰写：发现漏洞时自动触发，查取证据与执行过程后写 Markdown 报告并回写。", "보고서 작성", "취약점 발견 시 자동으로 실행되어 증거와 실행 과정을 확인하고 상세 Markdown 보고서를 작성해 저장합니다.", false},
		{"retester", "漏洞复测", "从漏洞详情手动启动，读取原证据并保存独立复测结论。", "취약점 재검증", "취약점 상세 화면에서 수동으로 실행하여 기존 증거를 확인하고 독립적인 재검증 결과를 저장합니다.", false},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			a := &db.Agent{ID: 42, Key: c.key, Name: c.name, Description: c.description, Builtin: c.builtin, Role: "assistant", Enabled: true}
			before := *a
			got := agentDTO(a)
			if got.Name != c.wantName || got.Description != c.wantDescription {
				t.Fatalf("presentation = %q / %q; want %q / %q", got.Name, got.Description, c.wantName, c.wantDescription)
			}
			if !reflect.DeepEqual(before, *a) {
				t.Fatal("presentation mutated persisted agent")
			}
			if got.Key != a.Key || got.Role != a.Role || got.ID != "42" {
				t.Fatal("presentation changed identifiers")
			}
			custom := *a
			custom.Name = "用户自定义"
			custom.Description = "用户修改的说明"
			dto := agentDTO(&custom)
			if dto.Name != custom.Name || dto.Description != custom.Description {
				t.Fatal("custom metadata translated")
			}
			unknown := *a
			unknown.Key = "custom_agent"
			unknown.Builtin = false
			dto = agentDTO(&unknown)
			if dto.Name != unknown.Name || dto.Description != unknown.Description {
				t.Fatal("unknown key translated")
			}
		})
	}
}
