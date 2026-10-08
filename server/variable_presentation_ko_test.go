package server

import (
	"encoding/json"
	"github.com/Autumn-27/artex/db"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"testing"
	"unicode"
)

// Pin both HTTP paths rather than depending on a live database or mutating it.
func TestAgentVariableHTTPBoundary(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "server_mgmt.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"pgGetAgent", "pgPromptVars"} {
		found := false
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == endpoint {
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if call, ok := n.(*ast.CallExpr); ok {
						if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "agentVariableDTOs" {
							found = true
						}
					}
					return true
				})
			}
		}
		if !found {
			t.Errorf("%s emits untranslated default catalog", endpoint)
		}
	}
}

func TestAgentVariablePresentationDefaults(t *testing.T) {
	cases := map[string][]db.PromptVar{
		"goals":     {{Name: "EngagementDescription", Description: "任务描述（测试对象/背景）", Example: "测试 example.com 站点", Source: "exploration"}},
		"planner":   {{Name: "Goal", Description: "任务总目标", Example: "拿下 example.com 的管理员权限", Source: "exploration"}, {Name: "AssetSummary", Description: "资产计数/类型分布摘要(可选)", Source: "distilled"}},
		"mainagent": {{Name: "Goal", Description: "当前任务目标", Source: "exploration"}, {Name: "AssetSummary", Description: "开局态势摘要(可选)", Source: "distilled"}, {Name: "FindingsSummary", Description: "已确认漏洞摘要(可选)", Source: "distilled"}},
		"worker":    {{Name: "ProxyAddr", Description: "记录代理地址(驱动 if 双文案)", Source: "runtime"}, {Name: "WorkerName", Description: "worker 自我标识(可选)", Source: "runtime"}},
	}
	// Exercise the same boundary used by both handlers through its catalog metadata.
	for key, vars := range cases {
		before, _ := json.Marshal(vars)
		shown := agentVariableDTOs(key, vars)
		for i, v := range shown {
			korean := false
			for _, r := range v.Description {
				if unicode.Is(unicode.Hangul, r) {
					korean = true
				}
			}
			if !korean {
				t.Errorf("%s.%s lacks Korean help", key, v.Name)
			}
			v.Description = vars[i].Description
			if !reflect.DeepEqual(v, vars[i]) {
				t.Fatal("variable identifier/example/source changed")
			}
		}
		after, _ := json.Marshal(vars)
		if string(before) != string(after) {
			t.Fatal("raw variable catalog mutated")
		}
	}
}

func TestAgentVariableCodeDefaultCoverage(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "../db/db.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	literal := func(e ast.Expr) string {
		b, ok := e.(*ast.BasicLit)
		if !ok {
			t.Fatal("expected string literal")
		}
		s, err := strconv.Unquote(b.Value)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	count := 0
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) == 0 || spec.Names[0].Name != "builtinAgents" {
			return true
		}
		for _, entry := range spec.Values[0].(*ast.CompositeLit).Elts {
			agent := entry.(*ast.CompositeLit)
			key := literal(agent.Elts[0])
			vars, ok := agent.Elts[4].(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, entry := range vars.Elts {
				fields := entry.(*ast.CompositeLit).Elts
				v := db.PromptVar{Name: literal(fields[0]), Description: literal(fields[1]), Example: literal(fields[2]), Source: literal(fields[3])}
				shown := agentVariableDTOs(key, []db.PromptVar{v})[0]
				if shown.Description == v.Description {
					t.Errorf("%s.%s default missing translation", key, v.Name)
				}
				if shown.Name != v.Name || shown.Example != v.Example || shown.Source != v.Source {
					t.Fatal("non-description field changed")
				}
				count++
			}
		}
		return false
	})
	if count == 0 {
		t.Fatal("no code-default catalog variables inspected")
	}
	t.Logf("covered %d code-default variable descriptions (plus %d already-Korean globals)", count, len(globalPromptVars))
}

func TestAgentVariableCustomPreservation(t *testing.T) {
	for key, defaults := range koreanVariableDefaults {
		for name, d := range defaults {
			for _, v := range []db.PromptVar{
				{Name: name, Description: d[0] + " (사용자 수정)", Example: "自定义示例", Source: "runtime"},
				{Name: "custom_" + name, Description: d[0], Example: "自定义示例", Source: "runtime"},
			} {
				if got := agentVariableDTOs(key, []db.PromptVar{v}); !reflect.DeepEqual(got, []db.PromptVar{v}) {
					t.Fatal("custom variable changed")
				}
			}
			raw := []db.PromptVar{{Name: name, Description: d[0]}}
			if got := agentVariableDTOs("custom_agent", raw); !reflect.DeepEqual(got, raw) {
				t.Fatal("custom agent catalog changed")
			}
		}
	}
}
