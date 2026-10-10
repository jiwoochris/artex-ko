package server

import (
	"encoding/json"
	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"strings"
	"testing"
)

func TestDisplayAndResetCatalogPreserveLargeNumbers(t *testing.T) {
	const number = "9007199254740993"
	seed := agent.ToolSeed{Key: "precision_fixture", Schema: map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "integer", "description": "提示内容", "default": json.Number(number)}}}}
	s := &Server{toolPresentation: newToolPresentationCatalog([]agent.ToolSeed{seed})}
	raw, err := json.Marshal(seed.Schema)
	if err != nil {
		t.Fatal(err)
	}
	row := &db.Tool{Key: seed.Key, System: true, Kind: "builtin", Schema: raw}
	display := s.toolDisplaySchema(row)
	if !strings.Contains(string(display), `"default":`+number) {
		t.Fatalf("display rounded numeric default: %s", display)
	}
	snapshot, err := json.Marshal(s.toolPresentation.schemas[seed.Key])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(snapshot), `"default":`+number) {
		t.Fatalf("reset snapshot rounded numeric default: %s", snapshot)
	}
}
