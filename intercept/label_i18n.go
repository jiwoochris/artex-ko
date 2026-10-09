package intercept

import "github.com/Autumn-27/artex/config"

// 가로채기 판정 액션 라벨(allow/deny/ask)의 다국어 표. 판정 메시지에 쓰이며 출력 언어를
// 따른다(config.ActiveLanguage). 미배선/미지원 언어는 ko 로 떨어진다. enum 키(allow/deny/
// ask)와 소스 마커("[模型]")는 안정 식별자라 번역하지 않는다.
var judgeActionLabels = map[string]map[string]string{
	"en": {"allow": "allow", "deny": "deny", "ask": "ask"},
	"ko": {"allow": "허용", "deny": "차단", "ask": "확인 요청"},
	"zh": {"allow": "允许", "deny": "拦截", "ask": "询问"},
	"es": {"allow": "permitir", "deny": "bloquear", "ask": "preguntar"},
}

func judgeActionLang() string {
	lang := config.ActiveLanguage()
	if _, ok := judgeActionLabels[lang]; ok {
		return lang
	}
	return "ko"
}
