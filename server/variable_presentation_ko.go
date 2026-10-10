package server

import "github.com/Autumn-27/artex/db"

// Scoped by agent key, variable identifier AND exact default description. Never
// used by template validation/rendering, and never persisted to the catalog.
var koreanVariableDefaults = map[string]map[string][2]string{
	"goals": {
		"EngagementDescription": {"任务描述（测试对象/背景）", "작업 설명(테스트 대상/배경)"},
	},
	"planner": {
		"Goal":         {"任务总目标", "작업의 전체 목표"},
		"AssetSummary": {"资产计数/类型分布摘要(可选)", "자산 수/유형 분포 요약(선택 사항)"},
	},
	"mainagent": {
		"Goal":            {"当前任务目标", "현재 작업 목표"},
		"AssetSummary":    {"开局态势摘要(可选)", "초기 탐색 현황 요약(선택 사항)"},
		"FindingsSummary": {"已确认漏洞摘要(可选)", "확인된 취약점 요약(선택 사항)"},
	},
	"worker": {
		"ProxyAddr":  {"记录代理地址(驱动 if 双文案)", "기록 프록시 주소(if 조건에 따른 두 안내문 선택)"},
		"WorkerName": {"worker 自我标识(可选)", "worker 자기 식별 이름(선택 사항)"},
	},
}

func agentVariableDTOs(key string, vars []db.PromptVar) []db.PromptVar {
	out := append([]db.PromptVar(nil), vars...)
	for i := range out {
		if d, ok := koreanVariableDefaults[key][out[i].Name]; ok && out[i].Description == d[0] {
			out[i].Description = d[1]
		}
	}
	return out
}
