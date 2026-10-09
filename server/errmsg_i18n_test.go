package server

import (
	"testing"
	"unicode"

	"github.com/Autumn-27/artex/config"
)

// TestTrMsgSwitchesLanguage pins the backend output-language switching: a
// user-facing Korean message is returned verbatim under ko (and when unwired),
// and translated to a non-Korean string under en/zh/es. This is the backend
// analogue of the web i18n catalog — it proves writeErr responses follow the
// configured language, not Korean only.
func TestTrMsgSwitchesLanguage(t *testing.T) {
	t.Cleanup(func() { config.CurrentLanguage = nil })

	hasHangul := func(s string) bool {
		for _, r := range s {
			if unicode.Is(unicode.Hangul, r) {
				return true
			}
		}
		return false
	}

	// A representative catalog key (an existing server error constant value).
	const koMsg = errAssetIDRequired
	if !hasHangul(koMsg) {
		t.Fatalf("precondition: source message should be Korean, got %q", koMsg)
	}

	// Unwired → ko passthrough.
	config.CurrentLanguage = nil
	if got := trMsg(koMsg); got != koMsg {
		t.Fatalf("unwired: trMsg must pass through Korean, got %q", got)
	}

	// ko → passthrough (the key itself).
	config.CurrentLanguage = func() string { return "ko" }
	if got := trMsg(koMsg); got != koMsg {
		t.Fatalf("ko: trMsg must return the Korean source, got %q", got)
	}

	// en/zh/es → a translated, non-empty string. en and es must carry no Hangul.
	for _, lang := range []string{"en", "zh", "es"} {
		config.CurrentLanguage = func() string { return lang }
		got := trMsg(koMsg)
		if got == "" || got == koMsg {
			t.Fatalf("%s: trMsg must translate away from the Korean source, got %q", lang, got)
		}
		if (lang == "en" || lang == "es") && hasHangul(got) {
			t.Fatalf("%s: translation must not contain Hangul, got %q", lang, got)
		}
	}

	// Unknown language code → ko fallback (config.NormalizeLanguage rejects it).
	config.CurrentLanguage = func() string { return "fr" }
	if got := trMsg(koMsg); got != koMsg {
		t.Fatalf("unknown lang: trMsg must fall back to Korean, got %q", got)
	}

	// A string not in the catalog (propagated lower-layer error) passes through.
	config.CurrentLanguage = func() string { return "en" }
	const notInCatalog = "some-lower-layer-error: connection refused"
	if got := trMsg(notInCatalog); got != notInCatalog {
		t.Fatalf("non-catalog string must pass through unchanged, got %q", got)
	}
}
