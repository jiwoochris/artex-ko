package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 첫 실행 관리자 계정 선점(/api/auth/init 무인증 호출) 제보에 대한 수정을 DB 없이
// 지키는 단위 테스트다. 설정 토큰 비교, 비밀번호 해시에 묶인 토큰 서명, CORS 허용
// 목록, 실패 시도 제한, 루프백 바인딩 판정을 각각 확인한다. DB 를 거치는 전체 흐름은
// auth_init_db_test.go 가 맡는다.

func TestSigningKeyBoundToPasswordHash(t *testing.T) {
	s := &Server{jwtKey: []byte(strings.Repeat("k", 32))}
	if !bytes.Equal(s.signingKey(""), s.jwtKey) {
		t.Fatal("비밀번호가 없을 때는 파일 키를 그대로 써야 합니다(DB 없는 테스트 호환)")
	}
	tok, err := signJWT(s.signingKey("hash-A"))
	if err != nil {
		t.Fatal(err)
	}
	if !verifyJWT(tok, s.signingKey("hash-A")) {
		t.Fatal("같은 해시로 파생한 키로는 검증이 되어야 합니다")
	}
	if verifyJWT(tok, s.signingKey("hash-B")) {
		t.Fatal("비밀번호(해시)가 바뀌면 이전 토큰은 무효가 되어야 합니다")
	}
	if verifyJWT(tok, s.signingKey("")) {
		t.Fatal("해시 파생 키로 서명한 토큰이 원본 키로 검증되면 안 됩니다")
	}
}

func TestVerifyTokenInvalidatedByPasswordChange(t *testing.T) {
	s := &Server{jwtKey: []byte(strings.Repeat("v", 32))}
	s.setPassHash("hash-A")
	tok, err := signJWT(s.signingKey("hash-A"))
	if err != nil {
		t.Fatal(err)
	}
	if !s.verifyToken(tok) {
		t.Fatal("현재 해시로 서명한 토큰은 통과해야 합니다")
	}
	s.setPassHash("hash-B")
	if s.verifyToken(tok) {
		t.Fatal("비밀번호 변경 뒤에도 이전 토큰이 통과하면 안 됩니다")
	}
	if s.verifyToken("") {
		t.Fatal("빈 토큰은 통과하면 안 됩니다")
	}
}

func TestSetupTokenFromEnvAndRandom(t *testing.T) {
	t.Setenv(setupTokenEnv, "fixed-setup-token")
	s := &Server{}
	if !s.checkSetupToken("  fixed-setup-token \n") {
		t.Fatal("환경 변수 토큰은 앞뒤 공백을 무시하고 일치해야 합니다")
	}
	if s.checkSetupToken("fixed-setup-tokeN") || s.checkSetupToken("") {
		t.Fatal("다른 값이나 빈 값은 거부해야 합니다")
	}

	t.Setenv(setupTokenEnv, "")
	s = &Server{}
	tok := s.currentSetupToken()
	if len(tok) != setupTokenLen {
		t.Fatalf("무작위 설정 토큰 길이 = %d, 기대 %d", len(tok), setupTokenLen)
	}
	if s.currentSetupToken() != tok {
		t.Fatal("한 번 만든 토큰은 지우기 전까지 같아야 합니다")
	}
	if !s.checkSetupToken(tok) || s.checkSetupToken(tok[:len(tok)-1]) {
		t.Fatal("무작위 토큰은 정확히 같을 때만 통과해야 합니다")
	}
	s.clearSetupToken()
	if s.currentSetupToken() == tok {
		t.Fatal("초기화가 끝나 지운 뒤에는 새 토큰이 나와야 합니다")
	}
}

func TestAuthLimiterCooldown(t *testing.T) {
	l := newAuthLimiter()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	const key = "203.0.113.9"

	for i := 0; i < authFreeFailures; i++ {
		if !l.allow(key) {
			t.Fatalf("%d번째 실패 전에는 허용되어야 합니다", i+1)
		}
		l.fail(key)
	}
	if l.allow(key) {
		t.Fatalf("%d회 실패 뒤에는 냉각 시간 동안 차단되어야 합니다", authFreeFailures)
	}
	now = now.Add(authCooldownBase + time.Second)
	if !l.allow(key) {
		t.Fatal("첫 냉각 시간이 지나면 다시 허용되어야 합니다")
	}
	l.fail(key)
	now = now.Add(authCooldownBase + time.Second)
	if l.allow(key) {
		t.Fatal("추가 실패마다 냉각 시간이 배로 늘어야 합니다")
	}
	now = now.Add(authCooldownBase)
	if !l.allow(key) {
		t.Fatal("두 배 냉각 시간이 지나면 허용되어야 합니다")
	}
	l.reset(key)
	l.fail(key)
	if !l.allow(key) {
		t.Fatal("성공으로 초기화한 뒤 한 번 실패는 자유 허용 범위입니다")
	}
	for i := 0; i < 20; i++ {
		l.fail(key)
	}
	if got := l.clients[key].until.Sub(now); got > authCooldownMax {
		t.Fatalf("냉각 시간 상한 초과: %v > %v", got, authCooldownMax)
	}
	now = now.Add(authFailureWindow + time.Second)
	if !l.allow(key) {
		t.Fatal("실패 기록 창이 지나면 기록이 지워져 허용되어야 합니다")
	}
	if l.allow("198.51.100.1") != true {
		t.Fatal("다른 클라이언트는 영향을 받지 않아야 합니다")
	}
}

func TestCORSAllowlist(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := corsWith(map[string]bool{"http://localhost:5173": true}, ok)

	call := func(method, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/auth/init", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}

	if rec := call(http.MethodPost, ""); rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("Origin 헤더가 없는(동일 출처) 요청에는 CORS 헤더를 붙이지 않아야 합니다")
	}
	rec := call(http.MethodPost, "https://evil.example")
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("허용 목록에 없는 출처에 Access-Control-Allow-Origin 을 주면 안 됩니다")
	}
	if rec.Header().Get("Vary") != "Origin" {
		t.Fatal("출처별로 응답이 달라지므로 Vary: Origin 이 있어야 합니다")
	}
	if rec := call(http.MethodOptions, "https://evil.example"); rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("허용되지 않은 출처의 사전 요청: code=%d ACAO=%q", rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
	}
	rec = call(http.MethodPost, "http://LOCALHOST:5173")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://LOCALHOST:5173" {
		t.Fatalf("허용된 출처는 대소문자 무시로 그대로 되돌려야 합니다: %q", got)
	}
	if !strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatal("허용된 출처에는 Authorization 헤더 사용을 허용해야 합니다")
	}

	wild := corsWith(map[string]bool{"*": true}, ok)
	r := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	r.Header.Set("Origin", "https://anything.example")
	w := httptest.NewRecorder()
	wild.ServeHTTP(w, r)
	if w.Header().Get("Access-Control-Allow-Origin") != "https://anything.example" {
		t.Fatal("\"*\" 를 명시하면 모든 출처를 허용(이전 동작)해야 합니다")
	}
}

func TestCORSAllowedOriginsEnv(t *testing.T) {
	t.Setenv(corsOriginsEnv, "")
	got := corsAllowedOrigins()
	for _, o := range defaultCORSOrigins {
		if !got[o] {
			t.Fatalf("기본 허용 목록에 %s 가 없습니다", o)
		}
	}
	t.Setenv(corsOriginsEnv, " https://UI.example.com , http://10.0.0.2:5173,, ")
	got = corsAllowedOrigins()
	if !got["https://ui.example.com"] || !got["http://10.0.0.2:5173"] || len(got) != 2 {
		t.Fatalf("환경 변수 목록 파싱 결과가 다릅니다: %v", got)
	}
	if got["http://localhost:5173"] {
		t.Fatal("환경 변수를 주면 기본값은 더 이상 포함되지 않아야 합니다")
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	cases := map[string]bool{
		":8787":           false,
		"0.0.0.0:8787":    false,
		"[::]:8787":       false,
		"10.0.0.5:8787":   false,
		"127.0.0.1:8787":  true,
		"127.0.0.1":       true,
		"localhost:8787":  true,
		"[::1]:8787":      true,
		"192.168.0.10:80": false,
	}
	for addr, want := range cases {
		if got := isLoopbackAddr(addr); got != want {
			t.Errorf("isLoopbackAddr(%q) = %v, 기대 %v", addr, got, want)
		}
	}
}

func TestClientKeyIgnoresForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.RemoteAddr = "198.51.100.7:40000"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if got := clientKey(r); got != "198.51.100.7" {
		t.Fatalf("제한 키는 위조 가능한 X-Forwarded-For 가 아니라 TCP 상대 주소여야 합니다: %q", got)
	}
	if id := clientLogID(r); !strings.Contains(id, "198.51.100.7:40000") || !strings.Contains(id, "1.2.3.4") {
		t.Fatalf("로그 식별자에는 두 값이 모두 있어야 합니다: %q", id)
	}
}
