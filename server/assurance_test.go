package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/retestplan"
	"github.com/Autumn-27/norma/llm"
)

func assuranceServerFixture(t *testing.T) (*Server, *db.Task, int64, *db.AssurancePlan) {
	t.Helper()
	s, fid := newRetestServer(t)
	task, err := s.m.pg.CreateTask("assurance server fixture", "synthetic local verification", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.m.pg.Exec(`UPDATE findings SET task_id=$2 WHERE id=$1`, fid, task.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		waitRetestIdle(t, s)
		_, _ = s.m.pg.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID)
		_, _ = s.m.pg.Exec(`DELETE FROM explorations WHERE id=$1`, task.ExplorationID)
	})
	snapshot, err := s.m.pg.AppendAssuranceSnapshot(t.Context(), task.ID, db.AssuranceSnapshotInput{
		Environment: "local-fixture", Revision: "build-a",
		Nodes:    []retestplan.Node{{ID: "route", DependsOn: []string{"policy"}}, {ID: "policy"}},
		Versions: map[string]string{"route": "r1", "policy": "p1"}, AvailablePrerequisites: []string{"test-user"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.m.pg.UpsertAssuranceConclusion(t.Context(), task.ID, fmt.Sprintf("finding:%d", fid), db.AssuranceConclusionInput{
		ExpectedGeneration: snapshot.Generation, BaselineGeneration: snapshot.Generation, Dependencies: []string{"route"}, MappingComplete: true,
		Impact: 5, EffortUnits: 2, Prerequisites: []string{"test-user"},
	})
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.m.pg.GetTaskAssurance(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.m.pg.AppendAssuranceSnapshot(t.Context(), task.ID, db.AssuranceSnapshotInput{
		ExpectedGeneration: w.Current.Generation, Environment: "local-fixture", Revision: "build-b",
		Nodes:    []retestplan.Node{{ID: "route", DependsOn: []string{"policy"}}, {ID: "policy"}},
		Versions: map[string]string{"route": "r1", "policy": "p2"}, AvailablePrerequisites: []string{"test-user"},
	})
	if err != nil {
		t.Fatal(err)
	}
	w, err = s.m.pg.GetTaskAssurance(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.m.pg.CreateAssurancePlan(t.Context(), task.ID, db.AssurancePlanInput{ExpectedGeneration: w.Current.Generation, BudgetUnits: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Result.Items) != 1 || plan.Result.Items[0].Decision != "selected" || plan.Result.Items[0].Freshness != "stale" {
		t.Fatalf("bad fixture plan %+v", plan)
	}
	return s, task, fid, plan
}

func assuranceHTTPRequest(t *testing.T, s *Server, method, path string, body any, auth bool) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if auth {
		s.jwtKey = []byte("local-assurance-test-signing-key")
		s.setPassHash("")
		token, err := signJWT(s.signingKey(""))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestAssuranceHTTPWorkspaceScopeAndExport(t *testing.T) {
	s, task, fid, plan := assuranceServerFixture(t)
	base := fmt.Sprintf("/api/tasks/%d/assurance", task.ID)
	if w := assuranceHTTPRequest(t, s, "GET", base, nil, false); w.Code != 401 {
		t.Fatalf("workspace must require auth: %d", w.Code)
	}
	w := assuranceHTTPRequest(t, s, "GET", base, nil, true)
	if w.Code != 200 {
		t.Fatalf("workspace: %d %s", w.Code, w.Body)
	}
	var workspace db.TaskAssurance
	if err := json.Unmarshal(w.Body.Bytes(), &workspace); err != nil {
		t.Fatal(err)
	}
	if workspace.Summary.Stale != 1 || len(workspace.Plans) != 1 {
		t.Fatalf("workspace loses uncertainty %+v", workspace)
	}
	other, err := s.m.pg.CreateTask("other assurance task", "local", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.m.pg.DeleteTask(other.ID) })
	wrong := assuranceHTTPRequest(t, s, "GET", fmt.Sprintf("/api/tasks/%d/assurance/plans/%d", other.ID, plan.ID), nil, true)
	if wrong.Code != 404 {
		t.Fatalf("cross-task plan leaked: %d %s", wrong.Code, wrong.Body)
	}
	bad := assuranceHTTPRequest(t, s, "PUT", base+fmt.Sprintf("/conclusions/finding:%d", fid), map[string]any{"expected_generation": workspace.Current.Generation, "forged_state": "operator_verified"}, true)
	if bad.Code != 400 {
		t.Fatalf("unknown field accepted: %d", bad.Code)
	}
	ex := assuranceHTTPRequest(t, s, "GET", base+"/export", nil, true)
	if ex.Code != 200 || !strings.Contains(ex.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("export: %d %s", ex.Code, ex.Body)
	}
	var exported map[string]json.RawMessage
	if err := json.Unmarshal(ex.Body.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema_version", "task_status", "complete", "coverage_basis", "history", "conclusions", "plans", "retests", "mapping_history"} {
		if _, ok := exported[key]; !ok {
			t.Errorf("export missing %s", key)
		}
	}
	if string(exported["complete"]) != "false" {
		t.Fatal("incomplete security assessment exported as complete")
	}
	if !bytes.Contains(exported["plans"], []byte(`"input"`)) {
		t.Fatal("export omitted reproducible plan input")
	}
}

func TestAssuranceRetestDispatchIsAtomicAndIdempotent(t *testing.T) {
	s, task, fid, plan := assuranceServerFixture(t)
	cid := fmt.Sprintf("finding:%d", fid)
	var wg sync.WaitGroup
	results := make(chan *db.FindingRetest, 2)
	errs := make(chan error, 2)
	var creates atomic.Int32
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _, created, err := s.m.pg.CreateAssuranceRetest(context.Background(), task.ID, plan.ID, cid, "")
			if err != nil {
				errs <- err
				return
			}
			if created {
				creates.Add(1)
			}
			results <- r
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var id int64
	for r := range results {
		if id != 0 && r.ID != id {
			t.Fatal("duplicate conversations")
		}
		id = r.ID
	}
	if creates.Load() != 1 {
		t.Fatalf("created %d runs", creates.Load())
	}
	runs, err := s.m.pg.ListFindingRetests(fid)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	if runs[0].AssurancePlanID != plan.ID || runs[0].AssuranceGeneration != plan.Generation {
		t.Fatal("snapshot binding missing")
	}
	if _, _, _, err := s.m.pg.CreateAssuranceRetest(t.Context(), task.ID, plan.ID, "check:1", ""); !errors.Is(err, db.ErrAssuranceInvalid) {
		t.Fatalf("negative check incorrectly dispatched: %v", err)
	}
}

func TestAssuranceRetestCannotRebindManualRun(t *testing.T) {
	s, task, fid, plan := assuranceServerFixture(t)
	r, _, _, err := s.m.pg.CreateFindingRetest(t.Context(), fid, "")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = s.m.pg.CreateAssuranceRetest(t.Context(), task.ID, plan.ID, fmt.Sprintf("finding:%d", fid), "")
	if !errors.Is(err, db.ErrAssuranceConflict) {
		t.Fatal("manual run rebound", err)
	}
	runs, _ := s.m.pg.ListFindingRetests(fid)
	if len(runs) != 1 || runs[0].ID != r.ID || runs[0].AssurancePlanID != 0 {
		t.Fatal("manual snapshot changed")
	}
}

func TestAssuranceChangedEvidencePreventsStartOrFixedPublication(t *testing.T) {
	for _, beforeStart := range []bool{true, false} {
		t.Run(strconv.FormatBool(beforeStart), func(t *testing.T) {
			s, task, fid, plan := assuranceServerFixture(t)
			r, c, _, err := s.m.pg.CreateAssuranceRetest(t.Context(), task.ID, plan.ID, fmt.Sprintf("finding:%d", fid), "")
			if err != nil {
				t.Fatal(err)
			}
			if !beforeStart {
				if ok, err := s.m.pg.StartFindingRetest(t.Context(), r.ID); err != nil || !ok {
					t.Fatalf("start %v %v", ok, err)
				}
			}
			if _, err := s.m.pg.Exec(`UPDATE findings SET evidence='new evidence after dispatch' WHERE id=$1`, fid); err != nil {
				t.Fatal(err)
			}
			if beforeStart {
				if ok, err := s.m.pg.StartFindingRetest(t.Context(), r.ID); err != nil || ok {
					t.Fatalf("stale start %v %v", ok, err)
				}
			} else {
				if err := s.m.pg.RecordFindingRetestResult(t.Context(), c.ID, "fixed", "fixture verdict", "fixture execution record"); err != nil {
					t.Fatal(err)
				}
				if err := s.m.pg.FinishFindingRetest(r.ID, "completed", ""); err != nil {
					t.Fatal(err)
				}
			}
			f, err := s.m.pg.GetFinding(fid)
			if err != nil {
				t.Fatal(err)
			}
			if f.Status == db.FindingFixed {
				t.Fatal("stale result marked new evidence fixed")
			}
			runs, err := s.m.pg.ListFindingRetests(fid)
			if err != nil || !runs[0].AssuranceStale || runs[0].Error == "" {
				t.Fatalf("historical status missing %+v %v", runs, err)
			}
		})
	}
}

func TestAssuranceFreshCompletionAdvancesReportedBaseline(t *testing.T) {
	s, task, fid, plan := assuranceServerFixture(t)
	cid := fmt.Sprintf("finding:%d", fid)
	r, c, _, err := s.m.pg.CreateAssuranceRetest(t.Context(), task.ID, plan.ID, cid, "")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.m.pg.StartFindingRetest(t.Context(), r.ID); err != nil || !ok {
		t.Fatalf("start %v %v", ok, err)
	}
	if err := s.m.pg.RecordFindingRetestResult(t.Context(), c.ID, "reproduced", "fixture verdict", "fixture execution record"); err != nil {
		t.Fatal(err)
	}
	if err := s.m.pg.FinishFindingRetest(r.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	w, err := s.m.pg.GetTaskAssurance(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Conclusions) != 1 || w.Conclusions[0].Freshness != "supported_for_snapshot" || w.Conclusions[0].Assurance != "retest_reported" {
		t.Fatalf("successful result did not advance provenance %+v", w.Conclusions)
	}
	if err := s.m.pg.FinishFindingRetest(r.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	again, err := s.m.pg.GetTaskAssurance(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Current.Generation != w.Current.Generation {
		t.Fatal("finalization replay changed baseline history")
	}
}

func TestAssuranceOneCompletionDoesNotInvalidateOtherSelectedWork(t *testing.T) {
	s, task, firstID, _ := assuranceServerFixture(t)
	secondID, err := s.m.pg.AddFinding(task.ID, 0, "authorization", "Second fixture", "high", "claim", "proof", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.m.pg.DeleteFinding(secondID) })
	w, err := s.m.pg.GetTaskAssurance(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.m.pg.UpsertAssuranceConclusion(t.Context(), task.ID, fmt.Sprintf("finding:%d", secondID), db.AssuranceConclusionInput{
		ExpectedGeneration: w.Current.Generation, BaselineGeneration: w.Current.Generation, Dependencies: []string{"route"}, MappingComplete: true, Impact: 4, EffortUnits: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	w, err = s.m.pg.GetTaskAssurance(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.m.pg.AppendAssuranceSnapshot(t.Context(), task.ID, db.AssuranceSnapshotInput{
		ExpectedGeneration: w.Current.Generation, Environment: "local-fixture", Revision: "build-c",
		Nodes:    []retestplan.Node{{ID: "route", DependsOn: []string{"policy"}}, {ID: "policy"}},
		Versions: map[string]string{"route": "r1", "policy": "p3"}, AvailablePrerequisites: []string{"test-user"},
	})
	if err != nil {
		t.Fatal(err)
	}
	w, err = s.m.pg.GetTaskAssurance(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.m.pg.CreateAssurancePlan(t.Context(), task.ID, db.AssurancePlanInput{ExpectedGeneration: w.Current.Generation, BudgetUnits: 4})
	if err != nil {
		t.Fatal(err)
	}
	for _, fid := range []int64{firstID, secondID} {
		r, c, created, err := s.m.pg.CreateAssuranceRetest(t.Context(), task.ID, plan.ID, fmt.Sprintf("finding:%d", fid), "")
		if err != nil || !created {
			t.Fatalf("dispatch item %d: %v created=%v", fid, err, created)
		}
		if ok, err := s.m.pg.StartFindingRetest(t.Context(), r.ID); err != nil || !ok {
			t.Fatalf("start %v %v", ok, err)
		}
		if err := s.m.pg.RecordFindingRetestResult(t.Context(), c.ID, "reproduced", "fixture", "fixture observation"); err != nil {
			t.Fatal(err)
		}
		if err := s.m.pg.FinishFindingRetest(r.ID, "completed", ""); err != nil {
			t.Fatal(err)
		}
	}
	w, err = s.m.pg.GetTaskAssurance(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if w.Current.Generation != plan.Generation || w.Summary.Stale != 0 {
		t.Fatalf("completion changed target or left work stale %+v", w)
	}
	if r, _, created, err := s.m.pg.CreateAssuranceRetest(t.Context(), task.ID, plan.ID, fmt.Sprintf("finding:%d", firstID), ""); err != nil || created || r.Status != "completed" {
		t.Fatalf("completed retry: %+v %v %v", r, created, err)
	}
}

func TestAssuranceUnresolvedRunsDoNotAdvanceBaseline(t *testing.T) {
	for _, outcome := range []struct{ status, verdict string }{
		{"failed", "fixed"}, {"stopped", "reproduced"},
		{"completed", "inconclusive"}, {"completed", ""},
	} {
		t.Run(outcome.status+"_"+outcome.verdict, func(t *testing.T) {
			s, task, fid, plan := assuranceServerFixture(t)
			before, err := s.m.pg.GetTaskAssurance(t.Context(), task.ID)
			if err != nil {
				t.Fatal(err)
			}
			r, c, _, err := s.m.pg.CreateAssuranceRetest(t.Context(), task.ID, plan.ID, fmt.Sprintf("finding:%d", fid), "")
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := s.m.pg.StartFindingRetest(t.Context(), r.ID); err != nil || !ok {
				t.Fatalf("start %v %v", ok, err)
			}
			if outcome.verdict != "" {
				if err := s.m.pg.RecordFindingRetestResult(t.Context(), c.ID, outcome.verdict, "fixture", "fixture observation"); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.m.pg.FinishFindingRetest(r.ID, outcome.status, "fixture termination"); err != nil {
				t.Fatal(err)
			}
			after, err := s.m.pg.GetTaskAssurance(t.Context(), task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Conclusions[0].BaselineGeneration != before.Conclusions[0].BaselineGeneration || after.Conclusions[0].Freshness != "stale" || after.Conclusions[0].Assurance == "retest_reported" {
				t.Fatalf("unresolved result advanced support: %+v", after.Conclusions[0])
			}
			f, err := s.m.pg.GetFinding(fid)
			if err != nil || f.Status == db.FindingFixed {
				t.Fatalf("unresolved result changed triage: %+v %v", f, err)
			}
		})
	}
}

func TestAssuranceHTTPDispatchRunsExistingToolsWithTaskAttribution(t *testing.T) {
	s, task, fid, plan := assuranceServerFixture(t)
	var calls atomic.Int32
	setRetestProvider(s, retestProvider{complete: func(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		if ri := agent.RunInfoFrom(ctx); ri.TaskID != task.ID || ri.ExplorationID != task.ExplorationID {
			t.Errorf("task attribution lost %+v", ri)
		}
		n := calls.Add(1)
		if n > 2 {
			return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("local fixture complete")}}, "end_turn", llm.Usage{}, nil
		}
		name, input := "get_finding_retest_context", `{}`
		if n == 2 {
			name, input = "record_finding_retest_result", `{"verdict":"inconclusive","summary":"missing test identity","evidence":"local fixture did not contact any target"}`
		}
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: strconv.Itoa(int(n)), Name: name, Input: json.RawMessage(input)}}}, "tool_use", llm.Usage{}, nil
	}})
	path := fmt.Sprintf("/api/tasks/%d/assurance/plans/%d/items/finding:%d/retest", task.ID, plan.ID, fid)
	w := assuranceHTTPRequest(t, s, "POST", path, map[string]string{"notes": "local fixture"}, true)
	if w.Code != 202 {
		t.Fatalf("dispatch %d %s", w.Code, w.Body)
	}
	waitRetestIdle(t, s)
	runs, err := s.m.pg.ListFindingRetests(fid)
	if err != nil || len(runs) != 1 || runs[0].Status != "completed" || runs[0].Verdict != "inconclusive" {
		t.Fatalf("runs %+v %v", runs, err)
	}
	workspace, err := s.m.pg.GetTaskAssurance(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Summary.Stale != 1 {
		t.Fatal("inconclusive result restored freshness")
	}
	if calls.Load() != 3 {
		t.Fatalf("calls=%d", calls.Load())
	}
	dup := assuranceHTTPRequest(t, s, "POST", path, map[string]string{}, true)
	if dup.Code != 200 || !strings.Contains(dup.Body.String(), `"created":false`) {
		t.Fatalf("duplicate %d %s", dup.Code, dup.Body)
	}
	time.Sleep(10 * time.Millisecond)
	if calls.Load() != 3 {
		t.Fatal("duplicate replay launched another model turn")
	}
}
