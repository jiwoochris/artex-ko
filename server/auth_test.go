package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// 이 파일은 "비밀번호 관련 읽기 실패를 '아직 설정하지 않음' 으로 취급하지 않는다" 는
// 계약을 고정한다. GetSetting 은 "키가 없음" 과 "읽기 실패" 의 반환값이 error 하나만
// 다르다(두 경우 모두 value 가 빈 문자열이다). 핸들러가 error 를 버리면 데이터베이스가
// 흔들리는 동안 초기화 입구가 열린다 — authInit 은 인증되지 않은 요청을 통과시켜 이미
// 설정된 관리자 비밀번호를 덮어쓸 수 있고, authStatus 는 200 + initialized:false 로 답해
// 프런트를 /setup 으로 보내 사용자가 그 일을 하도록 만든다.
//
// 읽기 실패는 커넥션 풀을 닫아 만든다(이후 GetSetting 이 sql.ErrNoRows 가 아니라 error 를
// 돌려준다).

// requireDBEnv 는 이 파일과 db/settings_insert_test.go 가 추가한 테스트에만 적용하는
// 스위치다. 저장소 관례는 DB 가 없으면 t.Skipf 이지만, 그 관례만 두면 CI 의 go-db 작업에서
// **기동 경로 오류(스키마 적용·seed 실패)** 까지 skip 되어 초록으로 보인다. ci.yml 의
// go-db 잡은 이 변수를 켜서 "DB 가 붙어 있어야 하는 작업" 에서 skip 이 초록을 대신하지
// 못하게 한다. 이 PR 이 추가한 테스트에만 적용하고, 나머지 136곳의 관례는 건드리지 않는다.
const requireDBEnv = "ARTEX_REQUIRE_DB"

// requireDBOrSkip 은 데이터베이스 준비 실패를 관례대로 skip 하되, requireDBEnv 가 켜진
// 환경(= CI 의 go-db 작업)에서는 실패로 올린다.
func requireDBOrSkip(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if os.Getenv(requireDBEnv) != "" {
		t.Fatalf("데이터베이스 기동 실패(%v): %s 가 설정된 환경에서는 skip 하지 않습니다", err, requireDBEnv)
	}
	t.Skipf("postgres unavailable (%v)", err)
}

// newClosedPoolServer 는 DB 설정으로 연결한 뒤 풀을 닫아, 이후 조회가 전부 error 를
// 돌려주는 Server 를 만든다.
func newClosedPoolServer(t *testing.T) *Server {
	t.Helper()
	dsn, _, err := db.DSN()
	requireDBOrSkip(t, err)
	d, err := db.Open(dsn)
	requireDBOrSkip(t, err)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	return &Server{m: &Manager{pg: d}, jwtKey: []byte(strings.Repeat("s", 32))}
}

func TestAuthStatusFailsClosedWhenDataSourceUnavailable(t *testing.T) {
	s := newClosedPoolServer(t)

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
	s := newClosedPoolServer(t)

	w := httptest.NewRecorder()
	body := strings.NewReader(`{"password":"correct horse battery"}`)
	s.authInit(w, httptest.NewRequest("POST", "/api/auth/init", body))

	if w.Code != 503 {
		t.Fatalf("status=%d, 503 을 기대했습니다 (읽기 실패 시 통과시키면 인증되지 않은 요청이 기존 비밀번호를 덮어씁니다); body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "token") {
		t.Fatalf("읽기 실패 시 token 을 발급하면 안 됩니다: %s", w.Body.String())
	}
}

// TestValidatePassword 는 서버측 비밀번호 정책의 경계를 고정한다. 하한은 문자(룬) 수,
// 상한은 바이트 수다(bcrypt 제약이 바이트 기준이라 72바이트를 넘으면 해시 자체가 실패한다).
func TestValidatePassword(t *testing.T) {
	for _, tc := range []struct {
		name, pw string
		wantErr  bool
	}{
		{"빈 문자열", "", true},
		{"7자", "1234567", true},
		{"8자", "12345678", false},
		{"한글 8자는 글자 수로 세므로 통과", "비밀번호여덟글자", false},
		{"한글 3자는 9바이트지만 3자", "비밀강", true},
		{"72바이트", strings.Repeat("a", 72), false},
		{"73바이트는 bcrypt 상한 초과", strings.Repeat("a", 73), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validatePassword(tc.pw); (got != "") != tc.wantErr {
				t.Fatalf("validatePassword(%q)=%q, wantErr=%v", tc.pw, got, tc.wantErr)
			}
		})
	}
}
