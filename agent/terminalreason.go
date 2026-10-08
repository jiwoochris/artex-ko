package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Autumn-27/norma/harness"
)

// runTrace retains the latest tool call so an interrupted run can identify the
// operation that was still in flight.
type runTrace struct {
	startedAt time.Time
	id        string
	name      string
	input     string
	at        time.Time
	pending   bool
}

func (t *runTrace) start(id, name, input string) {
	t.id, t.name, t.input, t.at, t.pending = id, name, input, time.Now(), true
}

func (t *runTrace) done(id string) {
	if id == t.id {
		t.pending = false
	}
}

var reasonHint = map[harness.TerminalReason]string{
	harness.ReasonCompleted:         "모델이 이번 라운드를 정상 종료했지만 글로 된 요약을 남기지 않았습니다. 사실과 자산은 이번 라운드의 도구 호출 기록을 기준으로 합니다",
	harness.ReasonMaxTurns:          "단계 상한(MaxTurns) 도달: SDK 가 마무리를 수행해 사실과 자산을 기록했고, 의도는 exhausted 로 표시되어 계획자가 방향을 바꿔 이어가도록 하며 실패로 처리하지 않습니다",
	harness.ReasonTimeout:           "단일 실행의 벽시계 예산(MaxDuration) 도달: 시간이 되면 실행 중인 도구를 중단하고 그 자리에서 마무리에 들어가 식별된 사실과 자산을 기록하며, 의도는 exhausted 로 표시됩니다",
	harness.ReasonModelError:        "모델 또는 API 호출 실패(네트워크·인증·레이트리밋·공급자 5xx 등). 재시도 소진 후 의도는 blocked 로 표시됩니다 — 전송 계층 장애라 이 의도는 사실상 제대로 탐색되지 못했습니다. 실행 과정(get_worker_trace)을 확인한 뒤 재배정할지 방법을 바꿀지 결정하세요",
	harness.ReasonBlockingLimit:     "컨텍스트 길이가 하드 상한에 도달해 요청이 전송 전에 차단되었습니다. 의도 입자를 좁히거나 도구 반환을 압축해야 합니다",
	harness.ReasonPromptTooLong:     "프롬프트가 너무 길고 컨텍스트 압축 재시도도 소진되어 더 이상 진행할 수 없습니다",
	harness.ReasonImageError:        "현재 모델이 이번 라운드의 멀티모달 콘텐츠를 지원하지 않습니다. 비전 지원 모델로 전환하거나 도구가 이미지를 반환하지 않도록 하세요",
	harness.ReasonStopHookPrevented: "Stop 훅이 이번 라운드 종료를 막았고 이후 이어가지 못했습니다. 과제 Guard 규칙이 지나치게 엄격한지 확인하세요",
	harness.ReasonHookStopped:       "도구나 훅이 실행 계속을 능동적으로 중단했습니다(예: 범위를 벗어난 목표나 금지된 명령). 마지막 tool_result 의 차단 설명을 확인하세요",
	harness.ReasonAbortedStreaming:  "모델 출력 스트리밍 생성 단계에서 실행이 취소되었습니다",
	harness.ReasonAbortedTools:      "도구 실행 단계에서 실행이 취소되었습니다",
}

// terminalText renders a terminal event with no final text into a compact summary
// and a Markdown detail block.
func terminalText(ctx context.Context, term *harness.Terminal, tr *runTrace) (string, string) {
	reason := term.Reason
	aborted := reason == harness.ReasonAbortedStreaming || reason == harness.ReasonAbortedTools
	// Prompt may return ctx.Err directly without a terminal event. Preserve the
	// cancellation cause instead of falling back to an empty/unknown terminal reason.
	if reason == "" && ctx.Err() != nil {
		aborted = true
	}

	var sum string
	if aborted {
		_, short, _, ok := AbortReason(ctx)
		if !ok {
			short = "취소 원인을 가져오지 못함"
		}
		stage := "실행 도중"
		switch reason {
		case harness.ReasonAbortedStreaming:
			stage = "모델 출력 단계"
		case harness.ReasonAbortedTools:
			stage = "도구 실행 단계"
		}
		sum = "(실행 중단됨: " + short + "; " + stage + "에서 멈춤" + progressSuffix(term, tr) + ", 미완료)"
	} else if reason == harness.ReasonMaxTurns || reason == harness.ReasonTimeout {
		sum = "(실행 예산 상한(" + string(reason) + ") 도달, 마무리하여 사실 기록함" + progressSuffix(term, tr) + "; 이번에는 글 요약 없음)"
	} else {
		hint := terminalReasonHint(reason)
		sum = "(글 요약 없음, 종료 상태 " + terminalReasonLabel(reason) + ": " + firstLine(hint, 80) + ")"
	}

	var b strings.Builder
	b.WriteString(sum)
	b.WriteString("\n\n")
	displayReason := terminalReasonLabel(reason)
	fmt.Fprintf(&b, "- **종료 상태**: `%s` - %s\n", displayReason, terminalReasonHint(reason))
	if aborted {
		code, _, why, ok := AbortReason(ctx)
		if ok {
			fmt.Fprintf(&b, "- **중단 원인** (`%s`): %s\n", code, why)
		} else {
			b.WriteString("- **중단 원인**: 가져올 수 없음; 취소한 쪽이 context.WithCancelCause 로 명시적 원인을 붙이지 않았을 수 있습니다\n")
		}
	}
	if term.Err != nil {
		fmt.Fprintf(&b, "- **하위 계층 오류**: `%v`\n", term.Err)
	}
	if aborted && strings.TrimSpace(term.Text) != "" {
		b.WriteString("- **취소 전 이미 생성된 일부 출력**:\n\n")
		b.WriteString(term.Text)
		b.WriteString("\n\n")
	}
	if term.Turns > 0 {
		fmt.Fprintf(&b, "- **실행함**: 모델 라운드 %d 회\n", term.Turns)
	}
	if !tr.startedAt.IsZero() {
		fmt.Fprintf(&b, "- **이번 실행 소요 시간**: %s\n", roundDur(time.Since(tr.startedAt)))
	}
	if u := term.Usage; u.InputTokens+u.OutputTokens+u.CacheReadTokens+u.CacheWriteTokens > 0 {
		fmt.Fprintf(&b, "- **누적 token**: 입력 %d / 출력 %d / 캐시 읽기 %d / 캐시 쓰기 %d\n",
			u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
	}
	if tr.name == "" {
		b.WriteString("- **도구 호출**: 이번 실행은 도구를 한 번도 호출하지 않고 끝났습니다\n")
	} else if tr.pending {
		fmt.Fprintf(&b, "- **중단 시점에 실행 중이던 도구**: `%s`(%s 동안 실행, **결과 미반환**)\n\n  ```json\n  %s\n  ```\n",
			tr.name, roundDur(time.Since(tr.at)), firstLine(tr.input, 300))
	} else {
		fmt.Fprintf(&b, "- **중단 전 마지막 도구**: `%s`(정상 반환됨)\n", tr.name)
	}
	return sum, b.String()
}

func terminalReasonLabel(reason harness.TerminalReason) string {
	if reason == "" {
		return "context_canceled"
	}
	return string(reason)
}

func terminalReasonHint(reason harness.TerminalReason) string {
	if hint := reasonHint[reason]; hint != "" {
		return hint
	}
	if reason == "" {
		return "실행 context 가 취소되었으나 하위 계층에서 Terminal 이벤트가 생성되지 않았습니다"
	}
	return "알 수 없는 종료 상태; harness 에 TerminalReason 이 새로 추가되었을 수 있으니 reasonHint 를 보충하세요"
}

func progressSuffix(term *harness.Terminal, tr *runTrace) string {
	var parts []string
	if term.Turns > 0 {
		parts = append(parts, fmt.Sprintf("%d 라운드", term.Turns))
	}
	if !tr.startedAt.IsZero() {
		parts = append(parts, roundDur(time.Since(tr.startedAt)))
	}
	if len(parts) == 0 {
		return ""
	}
	return ", 실행 " + strings.Join(parts, " / ")
}

func roundDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return d.Round(100 * time.Millisecond).String()
	case d < time.Hour:
		return d.Round(time.Second).String()
	default:
		return d.Round(time.Minute).String()
	}
}
