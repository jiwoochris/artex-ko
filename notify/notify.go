// Package notify 实现漏洞发现的 IM / 邮件推送渠道适配层。
//
// 分层：本包是**叶子包**，只依赖标准库。它不认识数据库、不认识 server。渠道配置
// 以 map[string]any 传入（对应 notification_channels.config 这一 JSONB 列），
// 待推送内容以 Message 传入。这样拆开的好处是：签名计算、UTF-8 截断、过滤匹配这些
// 真正容易出错的地方可以脱离 PostgreSQL 单测，宿主只需在 server 侧做编排。
//
// 并发约定：Channel 的实现必须**无状态**。同一个 Channel 实例会被多个渠道配置
// （甚至同一渠道的多个机器人实例）并发复用，所有凭据一律从 cfg 参数传入，
// 不允许把 webhook URL 之类的东西缓存进实现自身的字段。
package notify

// 渠道类型标识。取值同时是 notification_channels.kind 的合法集合，由 server 侧
// 白名单校验（与 findings.status 同理，不用 DB CHECK，方便后续加渠道）。
const (
	KindDingTalk = "dingtalk" // 钉钉自定义机器人
	KindFeishu   = "feishu"   // 飞书(含 Lark)自定义机器人
	KindWeCom    = "wecom"    // 企业微信群机器人
	KindWebhook  = "webhook"  // 通用 Webhook：自定义方法/头/JSON 模板
	KindTelegram = "telegram" // Telegram Bot API
	KindEmail    = "email"    // SMTP 邮件
)

// 事件类型，对应 notification_events.kind。
const (
	EventFindingCreated       = "finding_created"
	EventFindingStatusChanged = "finding_status_changed"
)

// InitKind 是 config 里为空的 kind 的兜底值。
const InitKind = KindDingTalk

// severityRank 把漏洞级别映射成可比较的序数。未知级别返回 0，因此任何
// min_severity 设置都会把未知级别挡在外面——存疑时不推，避免误报刷屏。
var severityRank = map[string]int{
	"low":      1,
	"medium":   2,
	"high":     3,
	"critical": 4,
}

// SeverityRank 返回级别的序数；未知级别返回 0。
func SeverityRank(severity string) int { return severityRank[severity] }

// Lang, when wired by the server, returns the active user-facing output language
// ("en"/"ko"/"zh"/"es") for the strings this leaf package renders into push /
// email messages (severity and status labels). Kept as an injected func so the
// package stays standard-library-only: it does not import config/db/server. nil
// (unit tests / standalone) → labelLang falls back to "ko", preserving the Korean
// localization golden tests.
var Lang func() string

func labelLang() string {
	if Lang != nil {
		switch v := Lang(); v {
		case "en", "ko", "zh", "es":
			return v
		}
	}
	return "ko"
}

// severityLabels / statusLabels hold the per-language display strings. "ko" keeps
// the exact Korean values the localization golden tests pin.
var severityLabels = map[string]map[string]string{
	"ko": {"critical": "🔴 심각", "high": "🟠 높음", "medium": "🟡 중간", "low": "🔵 낮음"},
	"en": {"critical": "🔴 Critical", "high": "🟠 High", "medium": "🟡 Medium", "low": "🔵 Low"},
	"zh": {"critical": "🔴 严重", "high": "🟠 高危", "medium": "🟡 中危", "low": "🔵 低危"},
	"es": {"critical": "🔴 Crítico", "high": "🟠 Alto", "medium": "🟡 Medio", "low": "🔵 Bajo"},
}

var statusLabels = map[string]map[string]string{
	"ko": {"pending": "처리 대기", "in_progress": "처리 중", "confirmed": "확인됨", "resolved": "처리됨", "fixed": "수정됨", "false_positive": "오탐", "ignored": "무시", "duplicate": "중복", "risk_accepted": "위험 수용"},
	"en": {"pending": "Pending", "in_progress": "In progress", "confirmed": "Confirmed", "resolved": "Resolved", "fixed": "Fixed", "false_positive": "False positive", "ignored": "Ignored", "duplicate": "Duplicate", "risk_accepted": "Risk accepted"},
	"zh": {"pending": "待处理", "in_progress": "处理中", "confirmed": "已确认", "resolved": "已处理", "fixed": "已修复", "false_positive": "误报", "ignored": "忽略", "duplicate": "重复", "risk_accepted": "接受风险"},
	"es": {"pending": "Pendiente", "in_progress": "En curso", "confirmed": "Confirmado", "resolved": "Resuelto", "fixed": "Corregido", "false_positive": "Falso positivo", "ignored": "Ignorado", "duplicate": "Duplicado", "risk_accepted": "Riesgo aceptado"},
}

// SeverityLabel 返回带 emoji 的级别名(按当前输出语言)，用于消息标题与卡片配色。
// 未知级别原样回显，不臆造。
func SeverityLabel(severity string) string {
	if v, ok := severityLabels[labelLang()][severity]; ok {
		return v
	}
	return severity
}

// StatusLabel 把处置状态翻译成当前输出语言，用于状态变更消息。未知状态原样回显。
func StatusLabel(status string) string {
	if v, ok := statusLabels[labelLang()][status]; ok {
		return v
	}
	return status
}

// AtLeast 判断 severity 是否达到 min 门槛。min 为空表示不设门槛，一律通过。
// 注意未知 severity 的序数为 0，会被任何非空 min 拒掉（见 severityRank 注释）。
func AtLeast(severity, min string) bool {
	if min == "" {
		return true
	}
	return SeverityRank(severity) >= SeverityRank(min)
}
