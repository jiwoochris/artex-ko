package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/Autumn-27/artex/config"
)

// TestLogTTemplatesHaveTranslations pins that every logT("…") template in this
// package has an en/zh/es entry in config's log catalog. config.T falls back to
// the Korean source on a miss, so a key that drifts by one character (spacing,
// an escaped quote) would otherwise ship Korean console logs to non-Korean
// operators without any test noticing.
func TestLogTTemplatesHaveTranslations(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var keys []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "logT" {
				return true
			}
			// A plain literal is the template itself. A computed argument (e.g. a
			// map lookup) is checked through every Hangul literal it contains.
			found := 0
			ast.Inspect(call.Args[0], func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("%s: %v", fset.Position(lit.Pos()), err)
				}
				if strings.IndexFunc(s, func(r rune) bool { return unicode.Is(unicode.Hangul, r) }) >= 0 {
					keys = append(keys, s)
					found++
				}
				return true
			})
			if found == 0 {
				t.Errorf("%s: no Korean template literal found in logT argument", fset.Position(call.Pos()))
			}
			return true
		})
	}
	if len(keys) == 0 {
		t.Fatal("found no logT call sites; the scan is broken")
	}
	for _, lang := range []string{"en", "zh", "es"} {
		t.Setenv("ARTEX_LANG", lang)
		for _, k := range keys {
			if config.T(k) == k {
				t.Errorf("logT template has no %s translation: %q", lang, k)
			}
		}
	}
}
