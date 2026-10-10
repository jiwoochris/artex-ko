package agent

// 사용자 대면 출력 언어(展示语言) 선택. artex-ko 는 영어·한국어·중국어·스페인어
// 네 언어 중 하나로 에이전트가 사용자에게 보이는 자연어를 쓰도록 강제한다. 에이전트의
// "뇌"(벤치마크된 중국어 추론 본문 段[A])는 언어와 무관하게 그대로 둔다. 여기서 정하는
// 것은 오직 사용자가 읽는 표면의 언어다.

// OutputLanguage 는 서버가 설정(config 의 ARTEX_LANG, 런타임 settings KV)에서 고른
// 출력 언어 코드("en"/"ko"/"zh"/"es")를 돌려주도록 서버가 배선하는 훅이다. nil 이거나
// 빈 값이면 아래 기본값으로 떨어진다. PromptOverride 와 같은 방식으로 서버가 주입한다.
var OutputLanguage func() string

// 서버가 훅을 배선하지 않은 경우(예: 에이전트 패키지 단독 테스트)의 기본 출력 언어.
// 기본값은 한국어다(이 저장소는 한국어판이고, 설정 계층의 기본값도 ko). 영어·중국어·
// 스페인어는 설정으로 전환하는 추가 언어다.
const defaultOutputLang = "ko"

// SupportedOutputLangs 는 전환할 수 있는 출력 언어 코드 목록이다.
var SupportedOutputLangs = []string{"en", "ko", "zh", "es"}

// normalizeLang 은 지원 목록 밖의 코드를 기본값으로 좁힌다.
func normalizeLang(code string) string {
	for _, c := range SupportedOutputLangs {
		if c == code {
			return code
		}
	}
	return defaultOutputLang
}

// resolveOutputLang 은 현재 적용할 출력 언어 코드를 돌려준다. 서버가 배선한 훅을 먼저
// 보고, 없으면 기본값을 쓴다.
func resolveOutputLang() string {
	if OutputLanguage != nil {
		if code := OutputLanguage(); code != "" {
			return normalizeLang(code)
		}
	}
	return defaultOutputLang
}

// langPhrase 는 (중국어로 쓰인) 지시문 안에서 "이 언어로 써라"라고 지목할 때 쓰는 표기다.
// 괄호 안에 자국어 표기를 함께 둬서 모델이 어떤 언어인지 혼동하지 않게 한다.
var langPhrase = map[string]string{
	"ko": "韩语（한국어）",
	"en": "英语（English）",
	"zh": "中文（简体中文）",
	"es": "西班牙语（Español）",
}

// langBare 는 지시문 안에서 반복 지목할 때 쓰는 짧은 언어명이다.
var langBare = map[string]string{
	"ko": "韩语",
	"en": "英语",
	"zh": "中文",
	"es": "西班牙语",
}
