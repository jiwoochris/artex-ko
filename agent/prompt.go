package agent

import (
	"bytes"
	"text/template"
	"time"
)

// PromptOverride, if set, returns the stored system-prompt template for an agent
// key and whether one exists. The server wires it to the PG agent_prompts table.
// When nil or no override exists, agents use their built-in default prompt — so
// behavior is identical until a user edits a prompt in the UI.
var PromptOverride func(agentKey string) (string, bool)

// Prompt-variable structs — fields mirror each agent's catalog (docs §5a) so a
// user template referencing a catalog variable renders; referencing anything else
// fails template execution and falls back to the built-in default.
type PlannerVars struct{ Goal, Scope, AssetSummary, DataDir, Now string }
type WorkerVars struct{ ProxyAddr, WorkerName, DataDir, Now string }
type MainVars struct{ Goal, AssetSummary, FindingsSummary, DataDir, Now string }
type GoalsVars struct{ EngagementDescription, DataDir, Now string }

// nowStr is the server-local wall-clock string exposed as the universal {{.Now}}
// prompt variable. renderSystem runs on every agent turn/round, so this is fresh
// each run — a prompt can subtract it from a fixed start stamp to reason about
// elapsed time (e.g. a timed benchmark's "last N hours" window).
func nowStr() string { return time.Now().Format("2006-01-02 15:04:05 MST") }

// renderSystem returns the rendered system-prompt BODY (段 [A]) for agentKey.
// Precedence: the DB-stored template (if any) over the built-in default template
// (def). BOTH are Go templates now — the built-in default is seeded into the DB
// verbatim, so the two paths render identically until a user edits the prompt.
// Rendering always runs (def used to be pre-substituted plain text; it is now a
// {{.Var}} template like the DB one). On any render error we fall back to the
// default template, then to the raw default string — an agent never starts with a
// half-rendered prompt. Callers append the code-owned tail (trafficTool / 中间产物
// 输出规约) AFTER this, so those can't be edited away via the DB body.
func renderSystem(agentKey, def string, vars any) string {
	tmpl := def
	if PromptOverride != nil {
		if t, ok := PromptOverride(agentKey); ok && t != "" {
			tmpl = t
		}
	}
	if out, err := renderTmpl(tmpl, vars); err == nil {
		return out
	}
	// DB template broke (e.g. references an out-of-catalog var) → code default.
	if out, err := renderTmpl(def, vars); err == nil {
		return out
	}
	return def
}

// langDirective is the artex-ko output-language tail: a code-owned segment
// appended AFTER the rendered body and the artifact/traffic tails on every
// user-facing agent role, so a DB-edited prompt body can never drop it — the same
// guarantee artifactSpec gives. It does NOT translate the agent "brain": the
// benchmarked Chinese reasoning body (段 [A]) stays verbatim. It only constrains
// the LANGUAGE of what the agent SHOWS to the user. Written in Chinese so it stays
// in the body's language (keeping the model's reasoning register stable) while
// forcing Korean OUTPUT — this is the localization approach: preserve behavior,
// localize the surface the user reads. Raw technical strings (commands, payloads,
// code, URLs, log/response excerpts) are explicitly kept verbatim so evidence and
// reproduction steps are not mangled by translation.
//
// Two anti-drift clauses were added after the end-to-end run (L1): live models
// leaked (1) Chinese into the planner's situation summary — mirroring the Chinese
// brain body (段 [A]) — and (2) English into report_finding's structured fields —
// mirroring an English target app/evidence. The directive now names the planner
// situation summary as a user-facing field and explicitly forbids mirroring BOTH
// the Chinese instruction language AND the target/material language in the display
// fields, so only the listed verbatim technical fragments stay non-Korean.
func langDirective() string {
	return "\n\n**출력 언어 규약(현지화·최우선, 프롬프트 본문으로 덮을 수 없음)**: 【사용자에게 보이는】 모든 자연어 텍스트는 일괄 【한국어】로 쓴다 —— record_fact 의 summary/detail, report_finding 의 제목/설명/결론/수정 권고, 계획자(planner)의 상황/정황 요약, 마지막 한 문장 요약, 그리고 사용자에게 보내는 채팅 응답을 포함한다. 다만 【명령, payload, 코드, 파일 경로, URL, 파라미터 이름, 그리고 로그/요청/응답의 원문 조각】은 【원문 그대로 한 글자도 바꾸지 말고 보존】해야 하며 번역·개작하지 않는다(evidence 안의 명령줄과 출력은 특히 재현을 위해 원문 그대로 옮긴다). **설령 위의 시스템/역할 지시 일부에 다른 언어가 섞여 있더라도, 사용자에게 보이는 표시 언어는 오직 한국어이며, 사용자에게 보이는 텍스트에 다른 언어 문장이 나오게 하지 마라.** **대상 시스템, 그 페이지, 증거, 로그, 또는 어떤 참고 자료가 영어·중국어·기타 언어이더라도, 사용자에게 보이는 자연어 필드(제목/설명/결론/수정 권고/요약/상황 요약)는 여전히 한국어로 써야 한다 —— 그 표시 필드를 쓸 때 대상이나 자료의 언어를 그대로 미러링하거나 베끼지 마라; 위에 나열한 원문 기술 조각만 원형을 유지한다.** **너의 분석/계획/사고는 어떤 언어로 하든 상관없지만 그것은 【보이지 않는 내부 추론】이며 결코 본문으로 출력해선 안 된다: 사용자에게 보이는 응답은 첫 글자부터 한국어여야 하고, 앞에 다른 언어로 된 사고·설명·「먼저 어떻게 하겠다」는 서두를 깔지 마라; 명확화 질문, 파라미터 부족, 「계속할 수 없음」 같은 설명도 일괄 한국어로 바로 쓴다.** 한마디로: 내부로 어떻게 생각하든 자유지만, 사용자가 볼 수 있는 본문으로 나오는 것은 모두 한국어여야 한다(기술 원문 조각 제외)."
}

func renderTmpl(tmpl string, vars any) (string, error) {
	t, err := template.New("p").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, vars); err != nil {
		return "", err
	}
	return b.String(), nil
}
