package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// 상류 a951e4a 이식(백로그: 인증 읽기 fail-closed + 최초 설정 원자화 + 비밀번호 상한)의
// 회귀 방어. GetSetting 은 "키 없음"과 "읽기 실패"의 반환값이 error 하나만 다르다(둘 다
// value=""). 비밀번호 핸들러가 error 를 "미설정"으로 취급하면 데이터베이스 장애 중 초기화
// 입구가 열린다. 아래 두 테스트는 커넥션 풀을 닫아 읽기 실패를 만들어 fail-closed(503)를
// 확인한다.
func TestAuthStatusFailsClosedWhenDataSourceUnavailable(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	if err := m.pg.Close(); err != nil {
		t.Fatal(err)
	}

	s := &Server{m: m}
	w := httptest.NewRecorder()
	s.authStatus(w, httptest.NewRequest("GET", "/api/auth/status", nil))

	if w.Code != 503 {
		t.Fatalf("status=%d, 503 을 기대했습니다 (읽기 실패를 미초기화로 보면 사용자가 /setup 으로 가서 비밀번호를 덮어씁니다); body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		Initialized *bool `json:"initialized"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err == nil && payload.Initialized != nil {
		t.Fatalf("읽기 실패 시 initialized 를 답하면 안 됩니다: %v", *payload.Initialized)
	}
}

func TestAuthInitFailsClosedWhenDataSourceUnavailable(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	if err := m.pg.Close(); err != nil {
		t.Fatal(err)
	}

	s := &Server{m: m}
	w := httptest.NewRecorder()
	body := strings.NewReader(`{"password":"correct horse battery","setup_token":"x"}`)
	s.authInit(w, httptest.NewRequest("POST", "/api/auth/init", body))

	if w.Code != 503 {
		t.Fatalf("status=%d, 503 을 기대했습니다 (읽기 실패 시 통과시키면 인증되지 않은 요청이 기존 비밀번호를 덮어씁니다); body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "token") {
		t.Fatalf("읽기 실패 시 token 을 발급하면 안 됩니다: %s", w.Body.String())
	}
}

// 비밀번호 바이트 상한(bcrypt 72바이트)을 서버가 강제하는지 확인한다. 73바이트는 400 으로
// 거부되어야 한다(이전에는 bcrypt 가 해시 단계에서 실패해 500 이 났다).
func TestAuthInitRejectsTooLongPasswordDB(t *testing.T) {
	rig := newAuthTestRig(t)
	tok := rig.s.currentSetupToken()

	code, body := rig.do("POST", "/api/auth/init", "", map[string]any{
		"password":    strings.Repeat("a", 73),
		"setup_token": tok,
	})
	rig.expect(400, code, body, authErrPasswordTooLong)
}
