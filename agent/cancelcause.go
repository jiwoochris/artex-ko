package agent

import (
	"context"
	"errors"
	"fmt"
)

// AbortCause names why an agent run's context was cancelled. Every cancellation
// site should attach one so the activity trace can report the real initiator.
type AbortCause struct {
	Code  string
	Short string
	Text  string
}

func (c *AbortCause) Error() string { return c.Text }

func cause(code, short, text string) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: text}
}

// Causef builds a cause that includes runtime-specific detail.
func Causef(code, short, format string, args ...any) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: fmt.Sprintf(format, args...)}
}

var (
	// Task-level execution context.
	AbortPausedByUser = cause("paused_by_user", "사용자가 과제를 일시정지함",
		"사용자가 과제 제어 API(POST /api/tasks/{id}/control, action=pause)로 과제를 일시정지했습니다. 이번 Planner/Worker 실행이 능동 취소됨; 실행 중 의도는 frontier(open)로 되돌아가며, 과제 복구 후 다시 받아 처음부터 실행됩니다")
	AbortPausedByOrchestrator = cause("paused_by_orchestrator", "오케스트레이션 Agent 가 과제를 일시정지함",
		"오케스트레이션 Agent 가 pause_task 도구로 이 과제를 일시정지했습니다. 이번 Planner/Worker 실행이 능동 취소됨; 실행 중 의도는 frontier(open)로 되돌아가 복구 후 다시 실행됩니다")
	AbortTaskDeleted = cause("task_deleted", "과제가 삭제됨",
		"과제가 삭제되는 중(DELETE /api/tasks/{id})이며, 삭제 배리어가 이 과제의 실행 중 Planner·Worker·메인 Agent 를 취소했습니다; 이번 실행 결과는 더 이상 사용되지 않습니다")
	AbortPausedOnReload = cause("paused_on_reload", "백엔드가 과제의 일시정지 상태를 복원함",
		"백엔드가 기동 시 DB 에 저장된 상태에 따라 과제 일시정지를 복원했습니다. 이번 실행이 취소됨; 정상적으로는 복원 단계에 실행 중인 Agent 가 없습니다")
	AbortGoalMet = cause("goal_met", "계획자가 과제 목표 달성으로 판정함",
		"계획자가 과제 목표 달성으로 판정함并将任务置为 done，随后取消仍在运行的 Worker；这些意图会标记为 stopped，而不是失败")
	AbortSettleDrainTimeout = cause("settle_drain_timeout", "과제 타임아웃 마무리 대기 시간이 소진됨",
		"과제가 timeout 에 도달한 뒤 실행 중 Worker 의 우아한 마무리를 기다렸으나 90초 drain 유예로도 부족해 하드 취소를 수행했습니다; 의도는 exhausted 로 표시되고, 마무리 단계에서 이미 기록된 사실과 자산은 보존됩니다")

	// Per-work context.
	AbortKilledByPlanner = cause("killed_by_planner", "계획자가 이 의도를 종료함",
		"계획자가 kill_work 로 이 의도를 능동 종료했습니다, 보통 방향이 빗나갔거나 계속할 가치가 없음을 뜻함; 의도는 stopped 로 표시되고 자동으로 다시 수행되지 않습니다")
	AbortWorkPausedByUser = cause("work_paused_by_user", "사용자가 이 Worker 의도를 일시정지함",
		"사용자가 실행 중 Worker 를 일시정지했습니다. 이번 호출이 취소되고 의도는 paused 로 전환됨; 이미 등록된 의도·사실·취약점·활동 기록은 전부 보존되며 복구 후 처음부터 다시 실행됩니다")
	AbortWorkCancelledByUser = cause("work_cancelled_by_user", "사용자가 이 Worker 의도를 삭제함",
		"사용자가 실행 중 Worker 를 삭제했습니다. 이번 호출이 취소됨; Worker 가 쓰기 구역을 벗어난 뒤 서버가 사용자가 고른 삭제 모드로 이 의도를 처리함 —— 가짜 삭제는 삭제로만 표시하고 모든 산출을 보존, 진짜 삭제는 이 의도와 그것만으로 지탱되는 하위 노드를 연쇄 제거합니다")
	AbortWorkFinished = cause("work_finished", "Worker 가 정상 종료하고 context 를 해제함",
		"Worker 가 정상 종료했고 엔진이 detachWork 에서 그 context 자원을 해제했습니다. 이는 실행 중단이 아님; 중단 메시지에 나타난다면 취소와 종료 이벤트가 경쟁 상태였음을 뜻합니다")
	AbortPausedRaceGuard = cause("paused_race_guard", "과제 일시정지 중 새 실행 시작을 거부함",
		"과제가 일시정지 상태일 때 엔진이 새 실행 context 발급을 거부합니다, claim 과 일시정지 사이의 경쟁으로 Worker 가 계속 시작되는 것을 막기 위함; 이미 받은 의도는 frontier 로 되돌아갑니다")

	// Main Agent and standalone conversation contexts.
	AbortChatStoppedByUser = cause("chat_stopped_by_user", "사용자가 이번 대화를 중지함",
		"사용자가 중지를 눌러 이번 메인 Agent 또는 대화 Agent 실행을 능동 중단했습니다. 이미 생성된 활동 기록은 보존되며 다음 메시지를 계속 보낼 수 있습니다")
	AbortChatPausedWithTask = cause("chat_paused_with_task", "과제 일시정지로 메인 Agent 대화가 중단됨",
		"사용자가 과제를 일시정지할 때 실행 중이던 메인 Agent 대화도 함께 취소되었습니다. 이미 생성된 활동 기록은 보존됨; 과제 복구 후 이번 메시지를 자동으로 재생하지 않습니다")
	AbortChatTurnFinished = cause("chat_turn_finished", "이번 대화가 정상 종료하고 context 를 해제함",
		"이번 대화가 정상 종료했고 서버가 그 라운드의 context 자원을 해제하는 중입니다. 이는 실행 중단이 아님; 중단 메시지에 나타난다면 취소와 종료 이벤트가 경쟁 상태였음을 뜻합니다")

	// Process-level and per-run hard backstop.
	AbortShutdown = cause("shutdown", "백엔드 프로세스가 종료되는 중",
		"백엔드 프로세스가 SIGINT 또는 SIGTERM 을 받아 재시작·업데이트·종료 중입니다. 실행 중인 모든 Agent 가 취소됨; 재시작 후 남은 running 의도는 open 으로 리셋되어 다시 실행됩니다")
	AbortRunHardTimeout = cause("run_hard_timeout", "단일 실행의 하드 타임아웃 폴백이 발동됨",
		"단일 실행이 소프트 벽시계 예산과 추가 유예를 초과했습니다, 모델 요청이나 어떤 도구가 오래 반환하지 않아 정상적인 라운드 경계 마무리를 수행할 수 없었음을 뜻합니다. 중단 전 마지막으로 반환되지 않은 도구 호출을 중점 확인하세요")
)

// AbortReason resolves the named cause attached to a cancelled run context.
func AbortReason(ctx context.Context) (code, short, text string, ok bool) {
	c := context.Cause(ctx)
	if c == nil {
		return "", "", "", false
	}
	var ac *AbortCause
	if errors.As(c, &ac) {
		return ac.Code, ac.Short, ac.Text, true
	}
	switch {
	case errors.Is(c, context.DeadlineExceeded):
		return "deadline_exceeded", "상위 context 가 deadline 에 도달함",
			"상위 context 가 deadline 에 도달함，但设置方没有通过 WithTimeoutCause 附加具名原因: " + c.Error(), true
	case errors.Is(c, context.Canceled):
		return "canceled_no_cause", "취소한 쪽이 명시적 원인을 붙이지 않음",
			"상위 context 가 취소되었으나 취소한 쪽이 context.WithCancelCause 로 명시적 원인을 붙이지 않았습니다; agent/cancelcause.go 에 원인을 등록하고 그 취소 지점에 연결하세요", true
	default:
		return "other", firstLine(c.Error(), 80), c.Error(), true
	}
}
