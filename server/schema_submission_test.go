package server

import (
	"bytes"
	"encoding/json"
	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"testing"
)

func schemaFixture() (*Server, *db.Tool) {
	schema := json.RawMessage(`{"type":"object","properties":{"x":{"description":"提示内容","help":"提示内容","default":7},"nested":{"items":{"description":"提示内容"}}},"examples":[{"description":"提示内容"}]}`)
	var v any
	_ = json.Unmarshal(schema, &v)
	return &Server{toolPresentation: newToolPresentationCatalog([]agent.ToolSeed{{Key: "fixture", Schema: v.(map[string]any)}})}, &db.Tool{Key: "fixture", Kind: "builtin", System: true, Schema: schema}
}

func assertJSONEqual(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	var g, w any
	if json.Unmarshal(got, &g) != nil || json.Unmarshal(want, &w) != nil || !bytes.Equal(submissionJSON(g), submissionJSON(w)) {
		t.Fatalf("got %s; want %s", got, want)
	}
}
func submissionJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestSchemaSubmissionFullDisplay(t *testing.T) {
	s, row := schemaFixture()
	shown := s.toolDTOs([]*db.Tool{row})[0].DisplaySchema
	got := s.toolSchemaSubmission(row, shown)
	assertJSONEqual(t, got, row.Schema)
}

func TestSchemaSubmissionMixedHelpAndEdits(t *testing.T) {
	s, row := schemaFixture()
	submitted := json.RawMessage(`{"type":"object","properties":{"x":{"description":"힌트 내용","help":"사용자가 편집한 설명","default":null},"nested":{"items":{"description":"提示内容","default":{"description":"힌트 내용"}}}},"examples":[{"description":"힌트 내용"}],"required":["nested"],"x-note":"keep"}`)
	want := json.RawMessage(`{"type":"object","properties":{"x":{"description":"提示内容","help":"사용자가 편집한 설명","default":null},"nested":{"items":{"description":"提示内容","default":{"description":"힌트 내용"}}}},"examples":[{"description":"힌트 내용"}],"required":["nested"],"x-note":"keep"}`)
	assertJSONEqual(t, s.toolSchemaSubmission(row, submitted), want)
}

func TestSchemaSubmissionEditedCurrentDefaultsRetainProvenance(t *testing.T) {
	s, row := schemaFixture()
	row.Schema = json.RawMessage(`{"type":"object","properties":{"x":{"description":"提示内容","help":"사용자 설명","default":99},"nested":{"items":{"description":"提示内容"}}},"examples":[]}`)
	if s.toolDisplaySchema(row) != nil {
		t.Fatal("customized schema unexpectedly presented")
	}
	submitted := json.RawMessage(`{"type":"object","properties":{"x":{"description":"힌트 내용","help":"사용자 설명"},"nested":{"items":{"description":"힌트 내용"}}}}`)
	want := json.RawMessage(`{"type":"object","properties":{"x":{"description":"提示内容","help":"사용자 설명"},"nested":{"items":{"description":"提示内容"}}}}`)
	assertJSONEqual(t, s.toolSchemaSubmission(row, submitted), want)
}

func TestSchemaSubmissionStandardLocations(t *testing.T) {
	for _, key := range []string{"properties", "patternProperties", "additionalProperties", "items", "prefixItems", "oneOf", "anyOf", "allOf", "$defs", "definitions", "not", "if", "then", "else", "contains", "propertyNames", "dependentSchemas", "dependencies", "additionalItems", "unevaluatedProperties", "unevaluatedItems", "contentSchema"} {
		t.Run(key, func(t *testing.T) {
			leaf := map[string]any{"description": "提示内容"}
			var child any = leaf
			switch key {
			case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies":
				child = map[string]any{"description": leaf}
			case "prefixItems", "oneOf", "anyOf", "allOf":
				child = []any{leaf}
			}
			schema := map[string]any{key: child}
			raw := submissionJSON(schema)
			leaf["description"] = "힌트 내용"
			submitted := submissionJSON(schema)
			var defaults map[string]any
			_ = json.Unmarshal(raw, &defaults)
			s := &Server{toolPresentation: newToolPresentationCatalog([]agent.ToolSeed{{Key: "fixture", Schema: defaults}})}
			row := &db.Tool{Key: "fixture", Kind: "builtin", System: true, Schema: raw}
			assertJSONEqual(t, s.toolSchemaSubmission(row, submitted), raw)
		})
	}
}

func TestSchemaSubmissionCatalogRoundtrip(t *testing.T) {
	s := newPresentationTestServer()
	for _, seed := range presentationSeeds() {
		raw := submissionJSON(seed.Schema)
		row := &db.Tool{Key: seed.Key, Kind: "builtin", System: true, Schema: raw}
		shown := s.toolDTOs([]*db.Tool{row})[0].DisplaySchema
		assertJSONEqual(t, s.toolSchemaSubmission(row, shown), raw)
		if !bytes.Equal(row.Schema, raw) {
			t.Fatalf("%s raw row mutated", seed.Key)
		}
	}
}

func TestSchemaSubmissionPreservesMissingNullAndLargeDefaults(t *testing.T) {
	s, row := schemaFixture()
	submitted := json.RawMessage(`{"properties":{"x":{"description":"힌트 내용","help":null,"default":9007199254740993},"nested":{"items":{"default":null}}},"const":{"description":"힌트 내용"}}`)
	want := json.RawMessage(`{"properties":{"x":{"description":"提示内容","help":null,"default":9007199254740993},"nested":{"items":{"default":null}}},"const":{"description":"힌트 내용"}}`)
	got := s.toolSchemaSubmission(row, submitted)
	// Compare raw number tokens independently; float64 test decoding would hide loss.
	if !bytes.Contains(got, []byte(`9007199254740993`)) {
		t.Fatalf("large default rounded: %s", got)
	}
	assertJSONEqual(t, got, want)
	var input map[string]json.RawMessage
	_ = json.Unmarshal(got, &input)
	if _, ok := input["type"]; ok {
		t.Fatal("missing schema field restored")
	}
}

func TestSchemaSubmissionSameLocationOnly(t *testing.T) {
	s, row := schemaFixture()
	row.Schema = json.RawMessage(`{"properties":{"x":{"description":"other raw text"},"new":{"description":"提示内容"}}}`)
	submitted := json.RawMessage(`{"properties":{"x":{"description":"힌트 내용"},"new":{"description":"힌트 내용"}}}`)
	if got := s.toolSchemaSubmission(row, submitted); !bytes.Equal(got, submitted) {
		t.Fatalf("reversed non-default or new-path text: %s", got)
	}
}

func TestSchemaSubmissionPreservesNonDefaultsAndInvalidInputs(t *testing.T) {
	s, base := schemaFixture()
	for _, mode := range []string{"custom", "command", "unknown", "edited-help", "nil-current", "invalid-current"} {
		t.Run(mode, func(t *testing.T) {
			row := *base
			switch mode {
			case "custom":
				row.System = false
			case "command":
				row.Kind = "command"
			case "unknown":
				row.Key = "unknown"
			case "edited-help":
				row.Schema = json.RawMessage(`{"description":"사용자 원문"}`)
			case "nil-current":
				row.Schema = nil
			case "invalid-current":
				row.Schema = json.RawMessage(`{`)
			}
			submitted := json.RawMessage(`{"description":"힌트 내용","default":null}`)
			if got := s.toolSchemaSubmission(&row, submitted); !bytes.Equal(got, submitted) {
				t.Fatalf("changed custom schema: %s", got)
			}
		})
	}
	for _, submitted := range []json.RawMessage{nil, json.RawMessage(`{`), json.RawMessage(`null`), json.RawMessage(`false`), json.RawMessage(`[]`)} {
		if got := s.toolSchemaSubmission(base, submitted); !bytes.Equal(got, submitted) {
			t.Fatalf("altered invalid/nil/non-object input: %s", got)
		}
	}
	// A Korean user value at the same path is not a generated code-default help.
	base.Schema = json.RawMessage(`{"type":"object","properties":{"x":{"description":"힌트 내용"}}}`)
	submitted := json.RawMessage(`{"type":"object","properties":{"x":{"description":"힌트 내용"}}}`)
	if got := s.toolSchemaSubmission(base, submitted); !bytes.Equal(got, submitted) {
		t.Fatal("Korean user edit erased")
	}
}
