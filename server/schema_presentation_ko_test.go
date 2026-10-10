package server

import (
	"bytes"
	"encoding/json"
	"github.com/Autumn-27/artex/db"
	"strings"
	"testing"
	"unicode"
)

// Independent auditor: descend JSON generically (not through the production
// walker's location allowlist), excluding instance-data/annotation payloads.
// Property/definition dictionaries are containers, not schema nodes themselves.
func schemaDescriptions(v any, visit func(string)) {
	switch x := v.(type) {
	case map[string]any:
		for key, value := range x {
			switch key {
			case "description", "help":
				if s, ok := value.(string); ok {
					visit(s)
				}
			case "examples", "default", "enum", "const", "required", "dependentRequired", "type":
				// Instance data and scalar annotations are never help locations.
			case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies":
				if entries, ok := value.(map[string]any); ok {
					for _, child := range entries {
						schemaDescriptions(child, visit)
					}
				}
			default:
				schemaDescriptions(value, visit)
			}
		}
	case []any:
		for _, child := range x {
			schemaDescriptions(child, visit)
		}
	}
}

func TestCoverageAuditorDetectsHiddenChinese(t *testing.T) {
	for _, key := range []string{"patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies", "additionalProperties", "prefixItems", "oneOf", "anyOf", "allOf", "not", "if", "then", "else", "contains", "propertyNames"} {
		schema := map[string]any{key: map[string]any{"description": "中文遗漏"}}
		switch key {
		case "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies":
			schema[key] = map[string]any{"x": schema[key]}
		case "prefixItems", "oneOf", "anyOf", "allOf":
			schema[key] = []any{schema[key]}
		}
		found := false
		schemaDescriptions(schema, func(s string) {
			if s == "中文遗漏" {
				found = true
			}
		})
		if !found {
			t.Errorf("coverage auditor missed Chinese at %s", key)
		}
	}
}

// Separate keyword audit fails closed on future schema vocabulary. It does not
// reuse the production walker's allowlist and skips annotation/instance data.
func unknownSchemaKeywords(v any) []string {
	known := strings.Fields(`$schema $id id $ref $anchor $dynamicRef $dynamicAnchor $recursiveRef $recursiveAnchor $vocabulary $comment title description help type enum const default examples readOnly writeOnly deprecated format contentEncoding contentMediaType contentSchema multipleOf maximum exclusiveMaximum minimum exclusiveMinimum maxLength minLength pattern maxItems minItems uniqueItems maxContains minContains maxProperties minProperties required dependentRequired properties patternProperties additionalProperties unevaluatedProperties propertyNames dependencies dependentSchemas items additionalItems prefixItems unevaluatedItems contains oneOf anyOf allOf not if then else $defs definitions`)
	allowed := map[string]bool{}
	for _, key := range known {
		allowed[key] = true
	}
	var out []string
	var scan func(any)
	scan = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for key, child := range x {
				if !allowed[key] {
					out = append(out, key)
				}
				switch key {
				case "default", "examples", "enum", "const", "required", "dependentRequired", "type":
				case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies":
					if entries, ok := child.(map[string]any); ok {
						for _, schema := range entries {
							scan(schema)
						}
					}
				default:
					scan(child)
				}
			}
		case []any:
			for _, child := range x {
				scan(child)
			}
		}
	}
	scan(v)
	return out
}

func TestCoverageAuditorUnknownSchemaKeyword(t *testing.T) {
	v := map[string]any{"properties": map[string]any{"default": map[string]any{"futureSchema": map[string]any{"description": "中文遗漏"}}}, "default": map[string]any{"futureSchema": map[string]any{"description": "用户数据"}}}
	found := false
	schemaDescriptions(v, func(s string) {
		if s == "中文遗漏" {
			found = true
		}
		if s == "用户数据" {
			t.Fatal("auditor interpreted instance data as schema help")
		}
	})
	if !found {
		t.Fatal("auditor missed unknown schema location")
	}
	keys := unknownSchemaKeywords(v)
	if len(keys) != 1 || keys[0] != "futureSchema" {
		t.Fatalf("unknown schema keyword sentinel missed: %v", keys)
	}
}

func TestDisplaySchemaDefaultCoverage(t *testing.T) {
	count := 0
	for _, seed := range presentationSeeds() {
		raw, _ := json.Marshal(seed.Schema)
		row := &db.Tool{Key: seed.Key, Kind: "builtin", System: true, Schema: raw}
		payload, _ := json.Marshal(newPresentationTestServer().toolDTOs([]*db.Tool{row})[0])
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
		if keys := unknownSchemaKeywords(v); len(keys) > 0 {
			t.Errorf("%s unknown schema keys: %v", seed.Key, keys)
		}
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
			dto := newPresentationTestServer().toolDTOs([]*db.Tool{&row})[0]
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
		first := newPresentationTestServer().toolDTOs([]*db.Tool{&base})[0]
		if len(first.DisplaySchema) > 0 {
			first.DisplaySchema[0] = '!'
		}
		second := newPresentationTestServer().toolDTOs([]*db.Tool{&base})[0]
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
		dto := newPresentationTestServer().toolDTOs([]*db.Tool{{Key: seed.Key, Kind: "builtin", System: true, Schema: raw}})[0]
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
