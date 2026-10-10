package db

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// requireDBEnv 는 이 테스트에만 적용하는 스위치다. 저장소 관례는 DB 가 없으면 t.Skipf
// 이지만, 그 관례만 두면 CI 의 go-db 작업에서 기동 경로 오류(스키마 적용·seed 실패)까지
// skip 되어 초록으로 보인다. ci.yml 의 go-db 잡이 이 변수를 켜서, DB 가 붙어 있어야 하는
// 작업에서 skip 이 초록을 대신하지 못하게 한다(server 패키지의 같은 이름 헬퍼와 짝).
const requireDBEnv = "ARTEX_REQUIRE_DB"

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

// InsertSettingIfAbsent 는 auth.password_hash 의 안전망이다: 호출자의 GetSetting 검사는
// 데이터베이스 오류로 무력해질 수 있고, 동시 요청에 새치기당할 수도 있다(bcrypt 가 수십
// 밀리초를 쓴다). 그래서 "최초 1회만 설정" 의 보장은 위쪽 if 판단이 아니라 기본키 제약에
// 있어야 한다.
func TestInsertSettingIfAbsentDoesNotOverwrite(t *testing.T) {
	d, err := Open(testDSN(t))
	requireDBOrSkip(t, err)
	defer d.Close()

	// 테스트 전용 키를 써서 개발 데이터베이스의 실제 auth.password_hash 는 건드리지 않는다.
	key := fmt.Sprintf("test.insert_if_absent.%d", time.Now().UnixNano())
	defer func() { _, _ = d.Exec(`DELETE FROM settings WHERE key=$1`, key) }()

	inserted, err := d.InsertSettingIfAbsent(key, "first")
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("최초 쓰기는 inserted=true 여야 합니다")
	}

	inserted, err = d.InsertSettingIfAbsent(key, "second")
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Fatal("키가 이미 있으면 inserted=false 여야 합니다")
	}

	got, ok, err := d.GetSetting(key)
	if err != nil || !ok {
		t.Fatalf("GetSetting: ok=%v err=%v", ok, err)
	}
	if got != "first" {
		t.Fatalf("값이 %q 로 덮였습니다. %q 를 유지해야 합니다", got, "first")
	}
}
