package server

import "github.com/Autumn-27/artex/config"

// 사용자 대면 백엔드 메시지(한국어 원문)를 활성 출력 언어로 치환하는 카탈로그. 이 저장소의
// writeErr 응답 문구·일부 writeJSON 에러 필드는 한국어 상수·리터럴로 작성되어 있다(F3b/F34).
// 여기서는 그 한국어 원문을 키로, 영어·중국어·스페인어 번역을 값으로 둔다. 한국어(ko)는
// 키 자체이므로 저장하지 않고, 조회 실패·ko 일 때는 원문을 그대로 돌려준다.
//
// 키는 소스의 한국어 상수/리터럴에서 바이트 그대로 추출해 생성한다(scripts 보조). 손으로
// 타이핑하지 않으므로 writeErr 에 들어오는 런타임 값과 키가 어긋나 조용히 누락되는 일이 없다.
//
// 포맷 문자열 상수(%d·%s·%w 등)는 포매팅한 결과가 키와 달라지므로, 호출부에서 fmt.Sprintf/
// fmt.Errorf 에 넘기기 전에 trMsg(템플릿) 로 템플릿 자체를 먼저 치환한다.

// trMsg 는 한국어 원문 메시지를 활성 출력 언어로 치환한다. 카탈로그에 없거나 활성 언어가
// 한국어이면 원문을 그대로 돌려준다. config.ActiveLanguage 가 런타임 설정(설정 테이블)을
// 반영하므로, 사용자가 고른 언어가 즉시 적용된다.
func trMsg(ko string) string {
	if m, ok := errCatalog[ko]; ok {
		if v, ok2 := m[config.ActiveLanguage()]; ok2 && v != "" {
			return v
		}
	}
	return ko
}

// logT translates an operator-facing console log template (config.T) so server
// log.Printf sites can wrap a template without each file importing config.
func logT(ko string) string { return config.T(ko) }
