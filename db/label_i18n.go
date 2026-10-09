package db

import "github.com/Autumn-27/artex/config"

// 자산 가로채기 설명 라벨의 다국어 표. 이 문자열들은 가로채기 규칙에 걸렸을 때 에이전트에게
// 돌려주는 설명 메시지에 쓰이며, 에이전트는 이를 읽고 사용자에게 출력 언어로 다시 표현한다.
// 출력 언어와 일관되도록 config.ActiveLanguage() 를 따른다. 미배선/미지원 언어는 ko 로 떨어진다.

var assetInterceptKindLabels = map[string]map[string]string{
	"en": {"exact_domain": "domain (exact)", "exact_ip": "IP (exact)", "exact_url": "URL (exact)", "fuzzy_domain": "domain (fuzzy)", "fuzzy_ip": "IP (fuzzy)", "fuzzy_url": "URL (fuzzy)", "cidr": "CIDR range"},
	"ko": {"exact_domain": "도메인(정확히 일치)", "exact_ip": "IP(정확히 일치)", "exact_url": "URL(정확히 일치)", "fuzzy_domain": "도메인(부분 일치)", "fuzzy_ip": "IP(부분 일치)", "fuzzy_url": "URL(부분 일치)", "cidr": "CIDR 대역"},
	"zh": {"exact_domain": "域名(全等)", "exact_ip": "IP(全等)", "exact_url": "URL(全等)", "fuzzy_domain": "域名(模糊)", "fuzzy_ip": "IP(模糊)", "fuzzy_url": "URL(模糊)", "cidr": "CIDR 网段"},
	"es": {"exact_domain": "dominio (exacto)", "exact_ip": "IP (exacta)", "exact_url": "URL (exacta)", "fuzzy_domain": "dominio (difuso)", "fuzzy_ip": "IP (difusa)", "fuzzy_url": "URL (difusa)", "cidr": "rango CIDR"},
}

// dbLabelLang 은 활성 출력 언어를 돌려주되, 표에 없는 언어면 ko 로 좁힌다.
func dbLabelLang() string {
	lang := config.ActiveLanguage()
	if _, ok := assetInterceptKindLabels[lang]; ok {
		return lang
	}
	return "ko"
}

// interceptReasonFmt: "규칙 [kind: pattern]에 걸림" 류 설명 템플릿(%s=kind라벨, %s=pattern).
var interceptReasonFmt = map[string]string{
	"en": "matched asset intercept rule [%s: %s]",
	"ko": "자산 가로채기 규칙에 걸림 [%s: %s]",
	"zh": "命中资产拦截规则 [%s: %s]",
	"es": "coincide con la regla de intercepción de activos [%s: %s]",
}

// interceptNoteFmt: 규칙 비고를 덧붙이는 괄호 표기(%s=note).
var interceptNoteFmt = map[string]string{
	"en": " (%s)",
	"ko": " (%s)",
	"zh": "（%s）",
	"es": " (%s)",
}

// assetLabelFmt: 자산 짧은 식별자 "자산#id[type] target" 템플릿(%d=id, %s=type, %s=target).
var assetLabelFmt = map[string]string{
	"en": "asset #%d[%s] %s",
	"ko": "자산 #%d[%s] %s",
	"zh": "资产#%d[%s] %s",
	"es": "activo #%d[%s] %s",
}

func trAssetLabelFmt() string {
	if v, ok := assetLabelFmt[dbLabelLang()]; ok {
		return v
	}
	return assetLabelFmt["ko"]
}
