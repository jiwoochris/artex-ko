package db

import (
	"fmt"
	"testing"
	"time"
)

// InsertSettingIfAbsent 는 auth.password_hash 의 안전망이다: 호출자의 GetSetting 검사는
// 데이터베이스 오류로 무력해질 수 있고, 동시 요청에 새치기당할 수도 있다(bcrypt 가 수십
// 밀리초를 쓴다). 그래서 "최초 1회만 설정" 의 보장은 if 판단이 아니라 기본키 제약에 있어야
// 한다.
func TestInsertSettingIfAbsentDoesNotOverwrite(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
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
