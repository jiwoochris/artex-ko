package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/artex/retestplan"
)

func assuranceDB(t *testing.T) (*DB, *Task, int64) {
	t.Helper()
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	task, err := d.CreateTask("assurance fixture", "local synthetic validation", nil, 0, 0)
	if err != nil {
		d.Close()
		t.Fatal(err)
	}
	fid, err := d.AddFinding(task.ID, 0, "authorization", "Scoped finding", "high", "fixture claim", "fixture evidence", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.Exec(`DELETE FROM findings WHERE task_id=$1`, task.ID)
		d.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID)
		d.Exec(`DELETE FROM explorations WHERE id=$1`, task.ExplorationID)
		d.Close()
	})
	return d, task, fid
}
func assuranceWorkspace(t *testing.T, d *DB, taskID int64) *TaskAssurance {
	t.Helper()
	w, e := d.GetTaskAssurance(t.Context(), taskID)
	if e != nil {
		t.Fatal(e)
	}
	return w
}
func assuranceInit(t *testing.T, d *DB, taskID int64) *AssuranceSnapshot {
	t.Helper()
	s, e := d.AppendAssuranceSnapshot(t.Context(), taskID, AssuranceSnapshotInput{Environment: "local-test", Revision: "build-a", Nodes: []retestplan.Node{{ID: "route", DependsOn: []string{"policy"}}, {ID: "policy"}}, Versions: map[string]string{"route": "r1", "policy": "p1"}, AvailablePrerequisites: []string{"test-user"}})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func assuranceMapping(generation, baseline int64) AssuranceConclusionInput {
	return AssuranceConclusionInput{ExpectedGeneration: generation, BaselineGeneration: baseline, Dependencies: []string{"route"}, MappingComplete: true, Impact: 5, EffortUnits: 3, Prerequisites: []string{"test-user"}}
}
func assuranceBind(t *testing.T, d *DB, fid int64) string {
	t.Helper()
	s := TrafficEvidenceSnapshot{SourceTrafficID: fmt.Sprintf("fixture-%d", fid), URL: "https://fixture.invalid/local", Method: "GET", Status: 200, ReqHash: strings.Repeat("a", 64), RespHash: strings.Repeat("b", 64)}
	s.ID = TrafficSnapshotID(s)
	err := d.WithEvidenceTx(t.Context(), func(tx *sql.Tx) error {
		if err := LockFindingEvidenceTx(tx, fid, nil); err != nil {
			return err
		}
		return AddFindingTrafficTx(tx, fid, []PreparedTrafficEvidence{{Ref: TrafficRef{Role: "proof"}, Snapshot: s}})
	})
	if err != nil {
		t.Fatal(err)
	}
	return s.ID
}
func assuranceConclusion(t *testing.T, w *TaskAssurance, id string) AssuranceConclusion {
	t.Helper()
	for _, c := range w.Conclusions {
		if c.ID == id {
			return c
		}
	}
	t.Fatal("missing conclusion", id)
	return AssuranceConclusion{}
}

func TestAssuranceLegacyProvenanceAndIdempotentMigration(t *testing.T) {
	d, task, fid := assuranceDB(t)
	cid := fmt.Sprintf("finding:%d", fid)
	w := assuranceWorkspace(t, d, task.ID)
	if w.Current != nil || w.Summary.Candidate != 1 || w.Summary.Unknown != 1 {
		t.Fatalf("legacy workspace %+v", w)
	}
	ref := assuranceBind(t, d, fid)
	w = assuranceWorkspace(t, d, task.ID)
	c := assuranceConclusion(t, w, cid)
	if c.Assurance != "runtime_observed" || c.MappingComplete || !reflect.DeepEqual(c.EvidenceRefs, []string{ref}) {
		t.Fatalf("bound traffic misclassified %+v", c)
	}
	assuranceInit(t, d, task.ID)
	w = assuranceWorkspace(t, d, task.ID)
	before := w.Current.Fingerprint
	other, e := Open(testDSN(t))
	if e != nil {
		t.Fatal(e)
	}
	other.Close()
	w = assuranceWorkspace(t, d, task.ID)
	if w.Current.Fingerprint != before || w.Current.Generation != 1 {
		t.Fatal("migration changed assurance snapshot")
	}
	f, _ := d.GetFinding(fid)
	if f.Status != FindingPending {
		t.Fatal("assurance changed remediation", f.Status)
	}
}

func TestAssuranceCASAndScope(t *testing.T) {
	d, task, fid := assuranceDB(t)
	assuranceInit(t, d, task.ID)
	var wg sync.WaitGroup
	var mu sync.Mutex
	success, conflicts := 0, 0
	for i := 0; i < 6; i++ {
		wg.Go(func() {
			_, err := d.AppendAssuranceSnapshot(t.Context(), task.ID, AssuranceSnapshotInput{ExpectedGeneration: 1, Environment: "local-test", Revision: "build-b"})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				success++
			} else if errors.Is(err, ErrAssuranceConflict) {
				conflicts++
			} else {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if success != 1 || conflicts != 5 {
		t.Fatalf("CAS success=%d conflicts=%d", success, conflicts)
	}
	_, other, _ := assuranceDB(t)
	_, err := d.UpsertAssuranceConclusion(t.Context(), task.ID, fmt.Sprintf("finding:%d", fid+100000000), assuranceMapping(2, 1))
	if !errors.Is(err, ErrAssuranceNotFound) {
		t.Fatalf("foreign finding accepted: %v", err)
	}
	if _, err = d.GetAssurancePlan(t.Context(), other.ID, 123456789); !errors.Is(err, ErrAssuranceNotFound) {
		t.Fatal(err)
	}
	if _, err = d.AppendAssuranceSnapshot(t.Context(), task.ID, AssuranceSnapshotInput{ExpectedGeneration: 2, Environment: "production", Revision: "b"}); !errors.Is(err, ErrAssuranceInvalid) {
		t.Fatal("environment mutation accepted", err)
	}
}

func TestAssuranceEvidenceAndConstraintChangesInvalidate(t *testing.T) {
	d, task, fid := assuranceDB(t)
	ref := assuranceBind(t, d, fid)
	assuranceInit(t, d, task.ID)
	cid := fmt.Sprintf("finding:%d", fid)
	if _, err := d.UpsertAssuranceConclusion(t.Context(), task.ID, cid, assuranceMapping(1, 1)); err != nil {
		t.Fatal(err)
	}
	w := assuranceWorkspace(t, d, task.ID)
	c := assuranceConclusion(t, w, cid)
	if c.Freshness != "supported_for_snapshot" {
		t.Fatalf("unchanged mapped finding %+v", c)
	}
	review := AssuranceReviewInput{ExpectedGeneration: w.Current.Generation, ExpectedEvidenceVersion: c.EvidenceVersion, ExpectedEvidenceHash: c.EvidenceHash, State: "operator_verified", Provenance: "operator", Reviewer: "fixture operator", Rationale: "Compared scoped baseline and proof", EvidenceRefs: []string{ref}}
	if _, err := d.RecordAssuranceReview(t.Context(), task.ID, cid, review); err != nil {
		t.Fatal(err)
	}
	w = assuranceWorkspace(t, d, task.ID)
	if assuranceConclusion(t, w, cid).Assurance != "operator_verified" {
		t.Fatal("operator attestation missing")
	}
	traffic, err := d.GetFindingTraffic(t.Context(), fid)
	if err != nil {
		t.Fatal(err)
	}
	note := "changed evidence explanation"
	if err = d.EditFindingTraffic(t.Context(), fid, traffic.Bindings[0].ID, traffic.Version, nil, &note, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = d.RecordAssuranceReview(t.Context(), task.ID, cid, review); !errors.Is(err, ErrAssuranceConflict) {
		t.Fatal("stale review published", err)
	}
	w = assuranceWorkspace(t, d, task.ID)
	c = assuranceConclusion(t, w, cid)
	if c.Freshness != "stale" || c.Assurance == "operator_verified" || len(c.Reviews) != 1 {
		t.Fatalf("evidence invalidation %+v", c)
	}
	previous := w.Current.Generation
	if _, err = d.Exploration(task.ExplorationID).AddConstraint("deny", "no destructive writes", "human"); err != nil {
		t.Fatal(err)
	}
	w = assuranceWorkspace(t, d, task.ID)
	if w.Current.Generation <= previous {
		t.Fatal("constraint change did not advance generation")
	}
	c = assuranceConclusion(t, w, cid)
	found := false
	for _, r := range c.Reasons {
		if slicesLast(r.Path) == assuranceConstraintsNode {
			found = true
		}
	}
	if !found {
		t.Fatal("constraint provenance missing", c.Reasons)
	}
	f, _ := d.GetFinding(fid)
	if f.Status != FindingPending {
		t.Fatal("review changed triage")
	}
}
func slicesLast(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

func TestAssuranceCoveragePlanningAndImmutableHistory(t *testing.T) {
	d, task, fid := assuranceDB(t)
	assuranceInit(t, d, task.ID)
	cid := fmt.Sprintf("finding:%d", fid)
	if _, e := d.UpsertAssuranceConclusion(t.Context(), task.ID, cid, assuranceMapping(1, 1)); e != nil {
		t.Fatal(e)
	}
	add := func(outcome, identity string, impact, cost int, prereqs []string) string {
		w := assuranceWorkspace(t, d, task.ID)
		m := assuranceMapping(w.Current.Generation, 1)
		m.Impact = impact
		m.EffortUnits = cost
		m.Prerequisites = prereqs
		c, e := d.AddCoverageCheck(t.Context(), task.ID, AssuranceCoverageInput{AssuranceConclusionInput: m, Surface: "GET /orders/{id}", Identity: identity, Workflow: "read own order", RiskCategory: "object authorization", Outcome: outcome, Rationale: "Synthetic explicit observation"})
		if e != nil {
			t.Fatal(e)
		}
		return c.ID
	}
	negative := add("observed_not_vulnerable", "user", 4, 2, nil)
	blocked := add("blocked", "admin", 3, 1, []string{"test-admin"})
	inconclusive := add("inconclusive", "anonymous", 2, 1, nil)
	w := assuranceWorkspace(t, d, task.ID)
	if assuranceConclusion(t, w, negative).Assurance != "operator_reported" {
		t.Fatal("operator check mislabeled runtime verified")
	}
	if assuranceConclusion(t, w, blocked).Freshness == "supported_for_snapshot" || assuranceConclusion(t, w, inconclusive).Freshness == "supported_for_snapshot" {
		t.Fatal("blocked/inconclusive supported")
	}
	if _, e := d.AppendAssuranceSnapshot(t.Context(), task.ID, AssuranceSnapshotInput{ExpectedGeneration: w.Current.Generation, Environment: "local-test", Revision: "build-b", Nodes: []retestplan.Node{{ID: "route", DependsOn: []string{"policy"}}, {ID: "policy"}}, Versions: map[string]string{"route": "r1", "policy": "p2"}, AvailablePrerequisites: []string{"test-user"}}); e != nil {
		t.Fatal(e)
	}
	w = assuranceWorkspace(t, d, task.ID)
	p, e := d.CreateAssurancePlan(t.Context(), task.ID, AssurancePlanInput{ExpectedGeneration: w.Current.Generation, BudgetUnits: 3})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Result.Items) != 4 || p.Result.AllocatedUnits != 3 {
		t.Fatalf("budget/coverage %+v", p.Result)
	}
	var document AssurancePlanDocument
	if err := json.Unmarshal(p.Input, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 1 || len(document.Conclusions) != 4 || len(document.Baselines) != 1 {
		t.Fatalf("normalized replay input %+v", document)
	}
	freshness := map[string]string{}
	for _, entry := range document.Conclusions {
		baseline, ok := document.Baselines[fmt.Sprint(entry.BaselineGeneration)]
		if !ok {
			t.Fatal("missing immutable baseline")
		}
		replayed, err := retestplan.Build(retestplan.Input{SchemaVersion: 1, Baseline: baseline, Current: document.Current, Nodes: document.Nodes, Conclusions: []retestplan.Conclusion{entry.Conclusion}, AvailablePrerequisites: document.AvailablePrerequisites, BudgetUnits: document.BudgetUnits})
		if err != nil {
			t.Fatal(err)
		}
		freshness[entry.Conclusion.ID] = replayed.Items[0].Freshness
	}
	for _, item := range p.Result.Items {
		if freshness[item.ID] != item.Freshness {
			t.Fatal("immutable input did not reproduce freshness", item.ID)
		}
	}
	decisions := map[string]string{}
	for _, item := range p.Result.Items {
		decisions[item.ID] = item.Decision
		if item.ID == negative && item.Freshness != "stale" {
			t.Fatal("negative result did not invalidate")
		}
	}
	if decisions[cid] != "selected" || decisions[negative] != "deferred_budget" || decisions[blocked] != "blocked" {
		t.Fatal(decisions)
	}
	before, e := d.GetAssurancePlan(t.Context(), task.ID, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	m := assuranceMapping(w.Current.Generation, w.Current.Generation)
	m.MappingComplete = false
	if _, e = d.UpsertAssuranceConclusion(t.Context(), task.ID, cid, m); e != nil {
		t.Fatal(e)
	}
	saved, e := d.GetAssurancePlan(t.Context(), task.ID, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(saved, before) {
		t.Fatal("saved plan mutated")
	}
	w = assuranceWorkspace(t, d, task.ID)
	if w.Current.Fingerprint == p.Fingerprint {
		t.Fatal("mapping edit did not invalidate dispatch fingerprint")
	}
}

func TestAssuranceReviewRejectsInventedAndCrossTaskEvidence(t *testing.T) {
	d, task, fid := assuranceDB(t)
	_, other, otherFID := assuranceDB(t)
	_ = other
	foreign := assuranceBind(t, d, otherFID)
	assuranceInit(t, d, task.ID)
	w := assuranceWorkspace(t, d, task.ID)
	c := w.Conclusions[0]
	in := AssuranceReviewInput{ExpectedGeneration: 1, ExpectedEvidenceVersion: c.EvidenceVersion, ExpectedEvidenceHash: c.EvidenceHash, State: "operator_verified", Provenance: "operator", Reviewer: "tester", Rationale: "explicit", EvidenceRefs: []string{foreign}}
	if _, e := d.RecordAssuranceReview(t.Context(), task.ID, fmt.Sprintf("finding:%d", fid), in); !errors.Is(e, ErrAssuranceInvalid) {
		t.Fatal("foreign evidence accepted", e)
	}
	in.EvidenceRefs = nil
	if _, e := d.RecordAssuranceReview(t.Context(), task.ID, c.ID, in); !errors.Is(e, ErrAssuranceInvalid) {
		t.Fatal("unsubstantiated review accepted", e)
	}
	in.State = "rejected"
	in.Provenance = "agent"
	if _, e := d.RecordAssuranceReview(t.Context(), task.ID, c.ID, in); !errors.Is(e, ErrAssuranceInvalid) {
		t.Fatal("agent masqueraded as operator", e)
	}
}

func TestAssuranceSnapshotLocksConstraintWriters(t *testing.T) {
	d, task, _ := assuranceDB(t)
	assuranceInit(t, d, task.ID)
	tx, e := d.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = AssuranceSnapshotTx(t.Context(), tx, task.ID, 1); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		_, err := d.Exploration(task.ExplorationID).AddConstraint("deny", "later constraint", "human")
		done <- err
	}()
	select {
	case e := <-done:
		t.Fatal("constraint write bypassed publication lock", e)
	case <-time.After(80 * time.Millisecond):
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("constraint writer did not resume")
	}
}

func TestAssuranceInputLimits(t *testing.T) {
	d, task, _ := assuranceDB(t)
	_, e := d.AppendAssuranceSnapshot(context.Background(), task.ID, AssuranceSnapshotInput{Environment: "test", Revision: "a", Nodes: []retestplan.Node{{ID: "artex:forged"}}, Versions: map[string]string{"artex:forged": "v"}})
	if !errors.Is(e, ErrAssuranceInvalid) {
		t.Fatal("reserved dependency accepted", e)
	}
	assuranceInit(t, d, task.ID)
	if _, e = d.CreateAssurancePlan(t.Context(), task.ID, AssurancePlanInput{ExpectedGeneration: 1, BudgetUnits: -1}); !errors.Is(e, ErrAssuranceInvalid) {
		t.Fatal("negative budget accepted", e)
	}
}

func TestAssuranceFindingDocumentsAndCoverageEvidence(t *testing.T) {
	d, task, fid := assuranceDB(t)
	assuranceInit(t, d, task.ID)
	w := assuranceWorkspace(t, d, task.ID)
	c := w.Conclusions[0]
	if c.Assurance != "candidate" || len(c.EvidenceRefs) != 1 || c.EvidenceRefKinds[c.EvidenceRefs[0]] != "finding_document" {
		t.Fatalf("narrative evidence promoted to runtime observation %+v", c)
	}
	review := AssuranceReviewInput{ExpectedGeneration: 1, ExpectedEvidenceVersion: c.EvidenceVersion, ExpectedEvidenceHash: c.EvidenceHash, State: "operator_verified", Provenance: "operator", Reviewer: "operator", Rationale: "Manually inspected document evidence", EvidenceRefs: c.EvidenceRefs}
	if _, err := d.RecordAssuranceReview(t.Context(), task.ID, c.ID, review); err != nil {
		t.Fatal(err)
	}
	check, err := d.AddCoverageCheck(t.Context(), task.ID, AssuranceCoverageInput{AssuranceConclusionInput: assuranceMapping(1, 1), Surface: "CLI fixture", Identity: "local user", Workflow: "read fixture", RiskCategory: "authorization", Outcome: "observed_not_vulnerable", Rationale: "Explicit operator statement", EvidenceRefs: c.EvidenceRefs})
	if err != nil {
		t.Fatal(err)
	}
	w = assuranceWorkspace(t, d, task.ID)
	if assuranceConclusion(t, w, c.ID).Assurance != "operator_verified" {
		t.Fatal("unrelated mapping edit erased review")
	}
	if len(check.EvidenceDependencies) != 1 {
		t.Fatal("document evidence dependency not captured")
	}
	if _, err = d.Exec(`UPDATE findings SET evidence='replacement document' WHERE id=$1`, fid); err != nil {
		t.Fatal(err)
	}
	w = assuranceWorkspace(t, d, task.ID)
	if assuranceConclusion(t, w, check.ID).Freshness != "stale" {
		t.Fatal("negative check survived replacement evidence")
	}
	review.ExpectedGeneration = w.Current.Generation
	if _, err = d.RecordAssuranceReview(t.Context(), task.ID, c.ID, review); !errors.Is(err, ErrAssuranceConflict) {
		t.Fatal("old document reference accepted", err)
	}
}

func TestAssuranceExportIncludesUntruncatedHistory(t *testing.T) {
	d, task, fid := assuranceDB(t)
	assuranceInit(t, d, task.ID)
	if _, err := d.UpsertAssuranceConclusion(t.Context(), task.ID, fmt.Sprintf("finding:%d", fid), assuranceMapping(1, 1)); err != nil {
		t.Fatal(err)
	}
	w := assuranceWorkspace(t, d, task.ID)
	p, err := d.CreateAssurancePlan(t.Context(), task.ID, AssurancePlanInput{ExpectedGeneration: w.Current.Generation, BudgetUnits: 0})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Exec(`INSERT INTO assurance_snapshots(task_id,generation,fingerprint,snapshot) SELECT task_id,n,fingerprint,jsonb_set(snapshot,'{generation}',to_jsonb(n)) FROM assurance_snapshots CROSS JOIN generate_series(3::bigint,110::bigint) n WHERE task_id=$1 AND generation=2`, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Exec(`INSERT INTO assurance_plans(task_id,generation,fingerprint,input,result) SELECT task_id,generation,fingerprint,input,result FROM assurance_plans CROSS JOIN generate_series(1,55) WHERE id=$1`, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	w = assuranceWorkspace(t, d, task.ID)
	if len(w.History) != 100 || !w.HistoryTruncated || len(w.Plans) != 50 || !w.PlansTruncated {
		t.Fatalf("truncation undisclosed history=%d plans=%d flags=%t/%t", len(w.History), len(w.Plans), w.HistoryTruncated, w.PlansTruncated)
	}
	export, err := d.GetTaskAssuranceExport(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(export.History) != 110 || len(export.Plans) != 56 || export.HistoryTruncated || export.PlansTruncated || len(export.MappingHistory) != 1 {
		t.Fatalf("incomplete export history=%d plans=%d mappings=%d", len(export.History), len(export.Plans), len(export.MappingHistory))
	}
	for _, plan := range export.Plans {
		if len(plan.Input) == 0 {
			t.Fatal("export dropped immutable plan input")
		}
	}
}

func TestAssuranceAcceptedRetestsDoNotInvalidateOtherPlanItems(t *testing.T) {
	d, task, fid := assuranceDB(t)
	second, err := d.AddFinding(task.ID, 0, "authorization", "Second fixture", "high", "claim", "evidence", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	assuranceInit(t, d, task.ID)
	for _, id := range []int64{fid, second} {
		w := assuranceWorkspace(t, d, task.ID)
		if _, err = d.UpsertAssuranceConclusion(t.Context(), task.ID, fmt.Sprintf("finding:%d", id), assuranceMapping(w.Current.Generation, 1)); err != nil {
			t.Fatal(err)
		}
	}
	w := assuranceWorkspace(t, d, task.ID)
	if _, err = d.AppendAssuranceSnapshot(t.Context(), task.ID, AssuranceSnapshotInput{ExpectedGeneration: w.Current.Generation, Environment: "local-test", Revision: "build-b", Nodes: []retestplan.Node{{ID: "route", DependsOn: []string{"policy"}}, {ID: "policy"}}, Versions: map[string]string{"route": "r1", "policy": "p2"}, AvailablePrerequisites: []string{"test-user"}}); err != nil {
		t.Fatal(err)
	}
	w = assuranceWorkspace(t, d, task.ID)
	p, err := d.CreateAssurancePlan(t.Context(), task.ID, AssurancePlanInput{ExpectedGeneration: w.Current.Generation, BudgetUnits: 6})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{fid, second} {
		cid := fmt.Sprintf("finding:%d", id)
		r, c, created, e := d.CreateAssuranceRetest(t.Context(), task.ID, p.ID, cid, "local synthetic run")
		if e != nil || !created {
			t.Fatalf("dispatch %s created=%t err=%v", cid, created, e)
		}
		t.Cleanup(func() { d.Exec(`DELETE FROM conversations WHERE id=$1`, c.ID) })
		if _, e = d.StartFindingRetest(t.Context(), r.ID); e != nil {
			t.Fatal(e)
		}
		if e = d.RecordFindingRetestResult(t.Context(), c.ID, "reproduced", "fixture result", "fixture runtime report"); e != nil {
			t.Fatal(e)
		}
		if e = d.FinishFindingRetest(r.ID, "completed", ""); e != nil {
			t.Fatal(e)
		}
		w = assuranceWorkspace(t, d, task.ID)
		if w.Current.Generation != p.Generation || w.Current.Fingerprint != p.Fingerprint {
			t.Fatal("accepted result invalidated other plan work")
		}
		conclusion := assuranceConclusion(t, w, cid)
		if conclusion.Freshness != "supported_for_snapshot" || conclusion.Assurance != "retest_reported" || conclusion.LastRetestID != r.ID {
			t.Fatalf("completed runtime result not reflected %+v", conclusion)
		}
	}
}
