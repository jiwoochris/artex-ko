package server

import (
	"bytes"
	"encoding/json"
	"github.com/Autumn-27/artex/db"
	"strings"
	"testing"
	"unicode"
)

func schemaDescriptions(v any, visit func(string)) {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if k == "description" || k == "help" {
				if s, ok := v.(string); ok {
					visit(s)
				}
			} else if k == "properties" {
				if props, ok := v.(map[string]any); ok {
					for _, p := range props {
						schemaDescriptions(p, visit)
					}
				}
			} else if k == "items" {
				schemaDescriptions(v, visit)
			}
		}
	case []any:
		for _, v := range x {
			schemaDescriptions(v, visit)
		}
	}
}

func TestDisplaySchemaDefaultCoverage(t *testing.T) {
	count := 0
	for _, seed := range presentationSeeds() {
		raw, _ := json.Marshal(seed.Schema)
		row := &db.Tool{Key: seed.Key, Kind: "builtin", System: true, Schema: raw}
		payload, _ := json.Marshal(toolDTOs([]*db.Tool{row})[0])
		var dto map[string]json.RawMessage
		_ = json.Unmarshal(payload, &dto)
		if len(dto["display_schema"]) == 0 {
			t.Errorf("%s missing display_schema", seed.Key)
			continue
		}
		var shown any
		_ = json.Unmarshal(dto["display_schema"], &shown)
		schemaDescriptions(shown, func(s string) {
			count++
			korean := false
			for _, r := range s {
				if unicode.Is(unicode.Hangul, r) {
					korean = true
				}
			}
			if !korean {
				t.Errorf("%s untranslated: %q", seed.Key, s)
			}
		})
		if !bytes.Equal(raw, row.Schema) || !bytes.Equal(raw, dto["schema"]) {
			t.Fatalf("%s raw schema changed", seed.Key)
		}
	}
	t.Logf("covered %d schema description paths across %d tools", count, len(presentationSeeds()))
}

func TestDefaultSchemaDictionaryAudit(t *testing.T) {
	seen := map[string]bool{}
	for _, seed := range presentationSeeds() {
		b, _ := json.Marshal(seed.Schema)
		var v any
		_ = json.Unmarshal(b, &v)
		schemaDescriptions(v, func(s string) {
			seen[s] = true
			if _, ok := koreanSchemaDescriptions[s]; !ok {
				t.Errorf("missing default help: %s", s)
			}
		})
	}
	for key := range koreanSchemaDescriptions {
		if !seen[key] {
			t.Errorf("dictionary entry is not a rendered default: %s", key)
		}
	}
	t.Logf("%d unique default descriptions, %d dictionary entries", len(seen), len(koreanSchemaDescriptions))
	intentional := 0
	for _, translated := range koreanSchemaDescriptions {
		withoutExamples := strings.ReplaceAll(strings.ReplaceAll(translated, "重点挖认证后接口", ""), "内网测试", "")
		hasHan := false
		for _, r := range translated {
			if unicode.Is(unicode.Han, r) {
				hasHan = true
			}
		}
		if hasHan {
			intentional++
		}
		for _, r := range withoutExamples {
			if unicode.Is(unicode.Han, r) {
				t.Errorf("untranslated Chinese help outside preserved examples: %s", translated)
				break
			}
		}
	}
	t.Logf("%d translated descriptions intentionally retain Chinese example literals", intentional)
}

func TestDisplaySchemaCustomPreservation(t *testing.T) {
	for _, seed := range presentationSeeds() {
		raw, _ := json.Marshal(seed.Schema)
		base := db.Tool{Key: seed.Key, Kind: "builtin", System: true, Schema: raw}
		custom := base
		custom.System = false
		command := base
		command.Kind = "command"
		unknown := base
		unknown.Key = "custom_" + seed.Key
		modified := base
		var schema map[string]any
		_ = json.Unmarshal(raw, &schema)
		schema["description"] = "自定义说明"
		schema["examples"] = []any{"用户示例"}
		modified.Schema, _ = json.Marshal(schema)
		invalid := base
		invalid.Schema = json.RawMessage(`{`)
		for _, row := range []db.Tool{custom, command, unknown, modified, invalid} {
			before, _ := json.Marshal(row)
			dto := toolDTOs([]*db.Tool{&row})[0]
			if dto.DisplaySchema != nil {
				t.Fatalf("%s custom/modified schema translated", seed.Key)
			}
			if !bytes.Equal(dto.Schema, row.Schema) {
				t.Fatal("raw schema changed")
			}
			after, _ := json.Marshal(row)
			if !bytes.Equal(before, after) {
				t.Fatal("row mutated")
			}
		}
		// Mutating the display copy cannot change either the raw or code defaults.
		first := toolDTOs([]*db.Tool{&base})[0]
		if len(first.DisplaySchema) > 0 {
			first.DisplaySchema[0] = '!'
		}
		second := toolDTOs([]*db.Tool{&base})[0]
		if !json.Valid(second.DisplaySchema) || !bytes.Equal(base.Schema, raw) {
			t.Fatal("presentation aliases default/runtime metadata")
		}
	}
}

func stripSchemaHelp(v any) {
	switch x := v.(type) {
	case map[string]any:
		delete(x, "description")
		delete(x, "help")
		if props, ok := x["properties"].(map[string]any); ok {
			for _, p := range props {
				stripSchemaHelp(p)
			}
		}
		stripSchemaHelp(x["items"])
	case []any:
		for _, i := range x {
			stripSchemaHelp(i)
		}
	}
}

func TestDisplaySchemaOnlyHelpChanges(t *testing.T) {
	for _, seed := range presentationSeeds() {
		raw, _ := json.Marshal(seed.Schema)
		dto := toolDTOs([]*db.Tool{{Key: seed.Key, Kind: "builtin", System: true, Schema: raw}})[0]
		var original, display any
		_ = json.Unmarshal(raw, &original)
		_ = json.Unmarshal(dto.DisplaySchema, &display)
		stripSchemaHelp(original)
		stripSchemaHelp(display)
		a, _ := json.Marshal(original)
		b, _ := json.Marshal(display)
		if !bytes.Equal(a, b) {
			t.Fatalf("%s changed schema fields other than help", seed.Key)
		}
	}
}

func TestSchemaHelpNestedArrays(t *testing.T) {
	v := map[string]any{"properties": map[string]any{"description": map[string]any{"type": "array", "description": "提示内容", "items": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"note": map[string]any{"help": "提示内容", "examples": []any{"提示内容"}, "default": "提示内容"}}}}}}}
	localizeSchemaHelp(v)
	prop := v["properties"].(map[string]any)["description"].(map[string]any)
	if prop["description"] != "힌트 내용" {
		t.Fatal("array help not translated")
	}
	note := prop["items"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["note"].(map[string]any)
	if note["help"] != "힌트 내용" || note["default"] != "提示内容" || note["examples"].([]any)[0] != "提示内容" {
		t.Fatal("nested help missing or example/default translated")
	}
}
