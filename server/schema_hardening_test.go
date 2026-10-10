package server

import (
	"encoding/json"
	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/traffic"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sync"
	"testing"
)

// Architectural safety guard: package initialization must not instantiate runtime
// tools. Behavioral catalog tests below cover publication and instance isolation.
func TestPresentationNoRuntimeConstructorsAtPackageInit(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "presentation_ko.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		g, ok := decl.(*ast.GenDecl)
		if !ok || g.Tok != token.VAR {
			continue
		}
		ast.Inspect(g, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					switch sel.Sel.Name {
					case "orchestrationTools", "platformTools", "findingRetestTools", "Tools":
						t.Errorf("runtime constructor %s at package init", sel.Sel.Name)
					}
				}
			}
			return true
		})
	}
}

func TestPresentationCatalogSnapshotsAndConcurrentReads(t *testing.T) {
	seed := agent.ToolSeed{Key: "fixture", Desc: "raw", Schema: map[string]any{"description": "提示内容"}, Agents: []string{"worker"}}
	s := &Server{toolPresentation: newToolPresentationCatalog([]agent.ToolSeed{seed})}
	row := &db.Tool{Key: "fixture", System: true, Kind: "builtin", Schema: json.RawMessage(`{"description":"提示内容"}`)}
	seed.Schema["description"] = "edited after publication"
	seed.Agents[0] = "edited after publication"
	if s.toolPresentation.bindings["fixture"][0] != "worker" {
		t.Fatal("catalog aliases seed bindings")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				shown := s.toolDTOs([]*db.Tool{row})[0].DisplaySchema
				if string(shown) != `{"description":"힌트 내용"}` {
					t.Errorf("catalog snapshot changed: %s", shown)
					return
				}
				if got := s.toolSchemaSubmission(row, shown); string(got) != string(row.Schema) {
					t.Errorf("concurrent normalization changed raw: %s", got)
				}
			}
		}()
	}
	wg.Wait()
	// Unconfigured receivers fail closed rather than constructing tools lazily.
	bare := &Server{}
	if bare.toolDisplaySchema(row) != nil || bare.toolDTOs([]*db.Tool{row})[0].Description != row.Description {
		t.Fatal("unconfigured server constructed defaults")
	}
}

func TestTrafficMetadataSpecsArePureAndFresh(t *testing.T) {
	first := traffic.ToolMetadataSpecs()
	second := traffic.ToolMetadataSpecs()
	if len(first) != 3 || len(second) != 3 {
		t.Fatal("traffic catalog incomplete")
	}
	for i, spec := range first {
		if spec.Run != nil {
			t.Fatal("metadata spec retained runtime handler")
		}
		if spec.Name != second[i].Name || spec.Description != second[i].Description || !reflect.DeepEqual(spec.Schema, second[i].Schema) {
			t.Fatal("traffic metadata changed")
		}
	}
	first[0].Schema["description"] = "mutation"
	if _, exists := second[0].Schema["description"]; exists {
		t.Fatal("traffic metadata snapshots alias")
	}
}

func TestSchemaWalkerStandardLocations(t *testing.T) {
	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies", "additionalProperties", "items", "prefixItems", "oneOf", "anyOf", "allOf", "not", "if", "then", "else", "contains", "propertyNames", "additionalItems"} {
		t.Run(key, func(t *testing.T) {
			child := map[string]any{"description": "提示内容", "help": "提示内容"}
			var value any = child
			switch key {
			case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies":
				value = map[string]any{"x": child}
			case "prefixItems", "oneOf", "anyOf", "allOf":
				value = []any{child}
			}
			schema := map[string]any{key: value}
			localizeSchemaHelp(schema)
			if child["description"] != "힌트 내용" || child["help"] != "힌트 내용" {
				t.Fatalf("%s help not localized: %v", key, child)
			}
		})
	}
}

func TestSchemaWalkerPreservesDataPayloads(t *testing.T) {
	schema := map[string]any{}
	for _, key := range []string{"examples", "default", "enum", "const", "x-custom"} {
		schema[key] = map[string]any{"description": "提示内容", "properties": map[string]any{"help": map[string]any{"description": "提示内容"}}}
	}
	schema["dependencies"] = map[string]any{"names": []any{"description", "提示内容"}}
	before := normalizedSchema(schema)
	localizeSchemaHelp(schema)
	if !reflect.DeepEqual(before, normalizedSchema(schema)) {
		t.Fatal("data payload translated")
	}
	tuple := map[string]any{"items": []any{map[string]any{"help": "提示内容"}}}
	localizeSchemaHelp(tuple)
	b, _ := json.Marshal(tuple)
	if string(b) != `{"items":[{"help":"힌트 내용"}]}` {
		t.Fatalf("legacy tuple: %s", b)
	}
}
