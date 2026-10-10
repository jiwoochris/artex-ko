package server

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// A database/sql boundary double exercises the actual HTTP handler and db methods
// without a Postgres connection, global driver registration, or runtime tool calls.
type toolMemoryConnector struct {
	row    *db.Tool
	writes int
}

func (c *toolMemoryConnector) Connect(context.Context) (driver.Conn, error) {
	return &toolMemoryConn{c}, nil
}
func (c *toolMemoryConnector) Driver() driver.Driver { return toolMemoryDriver{} }

type toolMemoryDriver struct{}

func (toolMemoryDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector; network disabled")
}

type toolMemoryConn struct{ c *toolMemoryConnector }

func (*toolMemoryConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*toolMemoryConn) Close() error              { return nil }
func (*toolMemoryConn) Begin() (driver.Tx, error) { return nil, errors.New("unexpected transaction") }
func (c *toolMemoryConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if !strings.Contains(q, "FROM tools WHERE key=$1") || len(args) != 1 || args[0].Value != c.c.row.Key {
		return nil, errors.New("unexpected query: " + q)
	}
	row := c.c.row
	return &toolMemoryRows{values: []driver.Value{row.Key, row.System, row.Description, []byte(row.Schema), []byte(submissionJSON(row.Agents)), row.Enabled, row.Kind, []byte(`{}`), row.Deferred}}, nil
}
func (c *toolMemoryConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if strings.Contains(q, "INSERT INTO tools") && strings.Contains(q, "ON CONFLICT (key) DO UPDATE") && len(args) == 4 {
		c.c.writes++
		c.c.row.Key = args[0].Value.(string)
		c.c.row.Description = args[1].Value.(string)
		c.c.row.Schema = append(json.RawMessage(nil), args[2].Value.([]byte)...)
		_ = json.Unmarshal(args[3].Value.([]byte), &c.c.row.Agents)
		c.c.row.Enabled = true
		return driver.RowsAffected(1), nil
	}
	if !strings.Contains(q, "UPDATE tools SET description=$2, schema=$3, agents=$4, enabled=$5") || len(args) != 5 || args[0].Value != c.c.row.Key {
		return nil, errors.New("unexpected exec: " + q)
	}
	c.c.writes++
	c.c.row.Description = args[1].Value.(string)
	c.c.row.Schema = append(json.RawMessage(nil), args[2].Value.([]byte)...)
	_ = json.Unmarshal(args[3].Value.([]byte), &c.c.row.Agents)
	c.c.row.Enabled = args[4].Value.(bool)
	return driver.RowsAffected(1), nil
}

type toolMemoryRows struct {
	values []driver.Value
	done   bool
}

func (*toolMemoryRows) Columns() []string {
	return []string{"key", "system", "description", "schema", "agents", "enabled", "kind", "exec", "deferred"}
}
func (*toolMemoryRows) Close() error { return nil }
func (r *toolMemoryRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	copy(dest, r.values)
	r.done = true
	return nil
}

func TestPgResetToolUsesRawCatalogForAllBuiltins(t *testing.T) {
	for _, entry := range []struct{ key, binding string }{{"traffic_get", "worker"}, {"get_finding_retest_context", db.FindingRetestAgentKey}, {"graph_overview", "planner"}, {"record_fact", "worker"}} {
		t.Run(entry.key, func(t *testing.T) {
			s := newPresentationTestServer()
			row := &db.Tool{Key: entry.key, System: true, Kind: "builtin", Description: "edited", Schema: json.RawMessage(`{}`)}
			c := &toolMemoryConnector{row: row}
			sqlDB := sql.OpenDB(c)
			defer sqlDB.Close()
			s.m.pg = &db.DB{DB: sqlDB}
			r := httptest.NewRequest("POST", "/api/tools/"+entry.key+"/reset", nil)
			r.SetPathValue("key", entry.key)
			w := httptest.NewRecorder()
			s.pgResetTool(w, r)
			if w.Code != 200 || c.writes != 1 {
				t.Fatalf("reset status=%d writes=%d body=%s", w.Code, c.writes, w.Body.String())
			}
			persisted, err := s.m.pg.GetTool(entry.key)
			if err != nil {
				t.Fatal(err)
			}
			if persisted.Description != s.toolPresentation.descriptions[entry.key] || !persisted.Enabled {
				t.Fatalf("reset wrote display metadata: %+v", persisted)
			}
			assertJSONEqual(t, persisted.Schema, submissionJSON(s.toolPresentation.schemas[entry.key]))
			found := false
			for _, binding := range persisted.Agents {
				if binding == entry.binding {
					found = true
				}
			}
			if !found {
				t.Fatalf("wrong reset bindings: %v", persisted.Agents)
			}
		})
	}
}

func TestPgUpdateToolNormalizesDisplaySchema(t *testing.T) {
	s, row := schemaFixture()
	raw := append(json.RawMessage(nil), row.Schema...)
	row.Description = "runtime description"
	c := &toolMemoryConnector{row: row}
	sqlDB := sql.OpenDB(c)
	defer sqlDB.Close()
	s.m = &Manager{pg: &db.DB{DB: sqlDB}}
	shown := s.toolDTOs([]*db.Tool{row})[0].DisplaySchema
	body := submissionJSON(map[string]any{"description": row.Description, "schema": shown, "agents": []string{"worker"}, "enabled": false})
	r := httptest.NewRequest("PUT", "/api/tools/fixture", strings.NewReader(string(body)))
	r.SetPathValue("key", "fixture")
	w := httptest.NewRecorder()
	s.pgUpdateTool(w, r)
	if w.Code != 200 || c.writes != 1 {
		t.Fatalf("handler: status=%d writes=%d body=%s", w.Code, c.writes, w.Body.String())
	}
	// Read back through the real catalog query rather than trusting Exec success.
	persisted, err := s.m.pg.GetTool(row.Key)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, persisted.Schema, raw)
	if persisted.Description != "runtime description" || persisted.Enabled || len(persisted.Agents) != 1 || persisted.Agents[0] != "worker" {
		t.Fatalf("other update fields changed: %+v", persisted)
	}
}
