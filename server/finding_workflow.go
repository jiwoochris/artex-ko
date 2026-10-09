package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/traffic"
	actool "github.com/Autumn-27/norma/tool"
)

// 현지화 보존 판정(artex-ko): 이 파일의 중국어 문자열은 전부 에이전트(두뇌) 입력이고
// HTTP 사용자 응답으로 나가지 않는다. BRIEF 경계 #1(두뇌는 번역하지 않는다 — 성능
// 보존)에 따라 원문을 그대로 둔다. 이 파일에 writeErr 는 0 — 사용자 노출 경로가 없다.
// 싱크는 세 갈래다(행 번호 대신 심볼로 가리킨다. 편집으로 밀려도 유효하게).
//
//	(1) 도구 설명·스키마 description: report_finding 의 evidence_hint_id·hints.text,
//	    bind_finding_traffic 의 wrTool 설명·finding_id strParam. LLM 이 읽는 도구
//	    정의라 두뇌 입력이다.
//	(2) 마이그레이션 매칭 문자열: seedFindingWorkflowTools 의 legacy 변수(옛 traffic_search
//	    설명)는 traffic.TrafficSearchDescription(그 자체도 중국어 도구 설명)으로 올리는
//	    UPDATE … WHERE description=$2 의 비교값이다. 번역하면 기존 행과 매칭되지 않아
//	    업그레이드가 멈춘다.
//	(3) actool.Errorf 도구 결과: agentFindingTrafficAccess 의 오류들은 유일 호출처
//	    toolBindFindingTraffic(이 파일)과 get_finding_traffic(finding_traffic.go) 두
//	    도구에서 모두 actool.Errorf(err.Error()) 로 감싸 에이전트에게 되돌아가고,
//	    toolBindFindingTraffic 의 바인딩 비활성·빈 traffic_refs 문구는 직접 actool.Errorf 다.
//
// 다음 기여자가 "마저 번역"하다 벤치마크된 두뇌 입력을 바꾸지 않도록 둔다(F16 계열).
func (s *Server) seedFindingWorkflowTools() {
	const hostSearchDescriptionFlag = "finding_workflow_tools_v3_host_search_description"
	if value, _, _ := s.m.pg.GetSetting(hostSearchDescriptionFlag); value != "true" {
		// Only replace the original built-in text. A user-edited description is
		// authoritative and must survive upgrades.
		legacy := "기록 프록시가 이미 수집한 대상 트래픽을 조회합니다(host 필수 지정, URL 하위 문자열이나 본문 키워드로 추가 필터 가능). body_contains 는 수집된 요청/응답 헤더와 본문에서 전문 검색을 수행하며 임의 하위 문자열과 한글을 지원합니다(최소 3자). 응답 속 비밀번호·키·오류·내부 주소 등을 찾는 데 쓸 수 있습니다. 매우 가벼운 색인(id/method/url/status/resp_len)만 반환하고 응답 내용은 포함하지 않습니다. 기본 3건, 페이지당 최대 10건을 반환하며 결과가 많으면 page 로 페이징합니다(page=0 부터). 특정 건의 요청/응답 원문을 보려면 traffic_get(id) 를 사용하세요. 이미 방문한 리소스를 다시 보거나 엔드포인트를 찾을 때 먼저 이것을 써서 같은 URL 을 중복 curl 하는 것을 피하세요."
		if _, err := s.m.pg.Exec(`UPDATE tools SET description=$1,updated_at=now() WHERE key='traffic_search' AND system AND description=$2`, traffic.TrafficSearchDescription, legacy); err != nil {
			// Log and leave the flag unset so the next startup retries; do not
			// return, or a transient error here would also skip the reporter
			// migration below — the two are independent.
			log.Printf("[evidence] upgrade traffic_search description: %v", err)
		} else {
			_ = s.m.pg.SetSetting(hostSearchDescriptionFlag, "true")
		}
	}
	const flag = "finding_workflow_tools_v2_reporter"
	if value, _, _ := s.m.pg.GetSetting(flag); value == "true" {
		return
	}
	for _, key := range []string{"report_finding", "add_hint", "add_task_hint"} {
		row, err := s.m.pg.GetTool(key)
		if err != nil {
			log.Printf("[evidence] load %s: %v", key, err)
			return
		}
		if row == nil || !row.System {
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(row.Schema, &schema); err != nil {
			log.Printf("[evidence] invalid schema for %s: %v", key, err)
			return
		}
		if schema == nil {
			log.Printf("[evidence] missing object schema for %s", key)
			return
		}
		props := objectProperty(schema, "properties")
		if key == "report_finding" {
			if _, exists := props["evidence_hint_id"]; !exists {
				props["evidence_hint_id"] = map[string]any{"type": "integer", "description": "선택: 이 작업에서 이 취약점에 대응하는 hint ID; 그 힌트에 저장된 traffic_refs 를 읽어 함께 연결합니다. 힌트가 없으면 생략하세요"}
			}
		} else {
			if _, exists := props["traffic_refs"]; !exists {
				props["traffic_refs"] = agent.HintTrafficSchema()
			}
			hints := objectProperty(props, "hints")
			if _, exists := hints["type"]; !exists {
				hints["type"] = "array"
			}
			items := objectProperty(hints, "items")
			if _, ok := items["type"]; !ok {
				items["type"] = "object"
			}
			itemProps := objectProperty(items, "properties")
			for name, value := range map[string]any{"text": strParam("힌트 내용"), "asset_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}, "traffic_refs": agent.HintTrafficSchema()} {
				if _, exists := itemProps[name]; !exists {
					itemProps[name] = value
				}
			}
		}
		raw, _ := json.Marshal(schema)
		result, err := s.m.pg.Exec(`UPDATE tools SET schema=$2::jsonb,updated_at=now() WHERE key=$1 AND system AND schema=$3::jsonb`, key, string(raw), string(row.Schema))
		if err != nil {
			log.Printf("[evidence] upgrade %s: %v", key, err)
			return
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return
		} // preserve concurrent user edits
	}
	// Upgrade only the original default binding. Customized lists and enabled
	// flags survive; the one-time flag also preserves future user unbinding.
	readers := `["worker","reporter"]`
	for _, key := range []string{"traffic_search", "traffic_get", "traffic_blob"} {
		if _, err := s.m.pg.Exec(`UPDATE tools SET agents=$2::jsonb WHERE key=$1 AND system AND (agents='["worker"]'::jsonb OR (agents @> '["worker","planner","mainagent","auto","pentest"]'::jsonb AND jsonb_array_length(agents)=5))`, key, readers); err != nil {
			return
		}
	}
	if _, err := s.m.pg.Exec(`UPDATE tools SET agents=$1::jsonb WHERE key='get_finding_traffic' AND system AND agents @> '["auto","reporter"]'::jsonb AND jsonb_array_length(agents)=2`, `["auto","reporter","worker","planner","mainagent","pentest"]`); err != nil {
		return
	}
	// Replace the previous code default only; preserve customized binding lists.
	if _, err := s.m.pg.Exec(`UPDATE tools SET agents='["reporter"]'::jsonb WHERE key='bind_finding_traffic' AND system AND agents @> '["worker","planner","mainagent","auto","pentest"]'::jsonb AND jsonb_array_length(agents)=5`); err != nil {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("reporter", []string{"bind_finding_traffic"}); err != nil {
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

func objectProperty(parent map[string]any, key string) map[string]any {
	value, ok := parent[key].(map[string]any)
	if !ok {
		value = map[string]any{}
		parent[key] = value
	}
	return value
}

func (s *Server) agentFindingTrafficAccess(ctx context.Context, id int64, write bool) error {
	if id <= 0 {
		return errors.New("finding_id 는 독립 취약점 기록 ID 여야 합니다. 탐색 노드 ID 가 아닙니다")
	}
	f, err := s.m.pg.GetFinding(id)
	if err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("%w: finding_id=%d. 증거 도구는 독립 취약점 기록 ID 를 사용합니다. list_task_findings / get_task_node_detail 의 finding_id 필드에서 읽고, id / finding_node_id 를 전달하지 마세요", db.ErrFindingNotFound, id)
	}
	if ri := agent.RunInfoFrom(ctx); ri.TaskID > 0 {
		task := s.m.ResolveTask(strconv.FormatInt(ri.TaskID, 10))
		if task == nil {
			return errors.New("작업이 존재하지 않습니다")
		}
		_, inherited, allowed := findingProvenanceInTask(task, f.TaskID)
		if !allowed {
			return errors.New("현재 작업은 해당 취약점을 읽을 수 없습니다")
		}
		if write && inherited {
			return errors.New("상속된 취약점의 트래픽 증거는 읽기 전용입니다. 원본 작업에서 수정하세요")
		}
	}
	return nil
}

func (s *Server) toolBindFindingTraffic() actool.CoreTool {
	return wrTool("bind_finding_traffic", "이미 등록된 취약점에 확인된 실제 HTTP 트래픽을 추가 연결합니다. finding_id 는 독립 취약점 기록 ID 를 사용하며 탐색 노드 ID 를 전달하지 마세요. 같은 배치의 참조는 전부 성공하거나 전부 실패하며, 중복 참조는 기존 설명을 덮어쓰지 않습니다. 추가 연결은 기존 보고서를 갱신 대기로 표시합니다. 패킷 보충을 위해 재탐지하거나 취약점을 중복 생성하지 마세요.",
		objSchema(map[string]any{"finding_id": strParam("독립 취약점 기록 ID 로, list_task_findings / get_task_node_detail 의 finding_id 필드에서 읽습니다"), "traffic_refs": agent.HintTrafficSchema()}, "finding_id", "traffic_refs"),
		func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
			if !s.m.pg.GetBool(settingAgentTrafficBinding, false) {
				return actool.Errorf("Agent 자동 트래픽 연결이 꺼져 있습니다. 시스템 설정에서 켜거나 페이지에서 수동으로 연결하세요."), nil
			}
			var args struct {
				FindingID json.RawMessage `json:"finding_id"`
				Refs      []db.TrafficRef `json:"traffic_refs"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			id := parseProfileID(args.FindingID)
			if err := s.agentFindingTrafficAccess(ctx, id, true); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if len(args.Refs) == 0 {
				return actool.Errorf("재연결에는 최소 한 개의 확인된 traffic_refs 가 필요합니다. 트래픽이 없으면 이 도구를 호출할 필요가 없습니다"), nil
			}
			list, err := s.evidenceStore().Bind(ctx, id, args.Refs)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(trafficSummary(list))
		})
}
