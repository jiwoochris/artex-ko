package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/Autumn-27/artex/retestplan"
)

func assuranceArchiveFixture(t *testing.T) (*DB, *Task) {
	t.Helper()
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.EnsureLLMRecordsTable(); err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureLLMUsageTable(); err != nil {
		t.Fatal(err)
	}
	task, err := d.CreateTaskWithOptions("assurance archive guard", "preserve provenance", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM task_archives WHERE task_id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM conversations WHERE id IN (SELECT r.conversation_id FROM finding_retests r JOIN findings f ON f.id=r.finding_id WHERE f.task_id=$1)`, task.ID)
		_, _ = d.Exec(`DELETE FROM findings WHERE task_id=$1`, task.ID)
		_ = d.DeleteTask(task.ID)
	})
	if err := d.SetPaused(task.ID, true); err != nil {
		t.Fatal(err)
	}
	return d, task
}

func insertArchiveAssuranceSnapshot(t *testing.T, d *DB, taskID int64) {
	t.Helper()
	if _, err := d.Exec(`INSERT INTO assurance_snapshots(task_id,generation,fingerprint,snapshot)
VALUES($1,1,'archive-test','{"generation":1}')`, taskID); err != nil {
		t.Fatal(err)
	}
}

func TestTaskArchiveRefusesActiveRetest(t *testing.T) {
	d, task := assuranceArchiveFixture(t)
	fid, err := d.AddFinding(task.ID, 0, "archive-active", "active fixture", "high", "fixture", "fixture evidence", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	run, _, _, err := d.CreateFindingRetest(t.Context(), fid, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.QueueTaskArchive(task.ID); !errors.Is(err, ErrTaskArchiveRetestRunning) {
		t.Fatalf("queue with active retest: %v", err)
	}
	if archive, err := d.GetTaskArchiveByTask(task.ID); err != nil || archive != nil {
		t.Fatalf("rejected queue persisted a job: archive=%+v err=%v", archive, err)
	}
	if _, err := d.SnapshotTaskArchive(task.ID); !errors.Is(err, ErrTaskArchiveRetestRunning) {
		t.Fatalf("snapshot accepted active retest: %v", err)
	}
	if err := d.FinishFindingRetest(run.ID, "failed", "synthetic stop"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.QueueTaskArchive(task.ID); err != nil {
		t.Fatalf("terminal retest blocked archival: %v", err)
	}
	if _, _, _, err := d.CreateFindingRetest(t.Context(), fid, ""); !errors.Is(err, ErrTaskArchiveState) {
		t.Fatalf("retest admitted after archive queue: %v", err)
	}
}

func TestTaskArchiveRechecksAssuranceBeforeCompaction(t *testing.T) {
	d, task := assuranceArchiveFixture(t)
	findingID, err := d.AddFinding(task.ID, 0, "archive-guard", "preserve finding", "high", "synthetic", "synthetic evidence", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.DeleteFinding(findingID) })
	archive, err := d.QueueTaskArchive(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Claim exactly this synthetic job so unrelated fixture jobs cannot affect it.
	if _, err := d.Exec(`UPDATE task_archives SET state=$2 WHERE id=$1`, archive.ID, Archiving); err != nil {
		t.Fatal(err)
	}
	snapshot, err := d.SnapshotTaskArchive(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate history that appeared after the package snapshot: the destructive
	// commit must recheck even if an older caller bypassed queue-time validation.
	insertArchiveAssuranceSnapshot(t, d, task.ID)
	if err := d.CompleteTaskArchive(archive.ID, snapshot, "/tmp/synthetic-assurance-archive.tar.zst", "synthetic", 1, 1); !errors.Is(err, ErrTaskArchiveState) {
		t.Fatalf("compaction ignored newly added assurance: %v", err)
	}
	if live, err := d.GetTask(task.ID); err != nil || live == nil {
		t.Fatalf("rejected compaction hid task: task=%+v err=%v", live, err)
	}
	if finding, err := d.GetFinding(findingID); err != nil || finding == nil || finding.Evidence != "synthetic evidence" {
		t.Fatalf("rejected compaction changed finding: finding=%+v err=%v", finding, err)
	}
	if saved, err := d.GetTaskArchive(archive.ID); err != nil || saved == nil || saved.State != Archiving || saved.ArchivedAt != nil {
		t.Fatalf("rejected compaction published archive: archive=%+v err=%v", saved, err)
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM assurance_snapshots WHERE task_id=$1`, task.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rejected compaction lost assurance: count=%d err=%v", count, err)
	}
}

func TestTaskArchiveAssuranceRoundTrip(t *testing.T) {
	d, task, fid := assuranceDB(t)
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM task_archives WHERE task_id=$1`, task.ID) })
	if err := d.EnsureLLMRecordsTable(); err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureLLMUsageTable(); err != nil {
		t.Fatal(err)
	}
	assuranceInit(t, d, task.ID)
	cid := fmt.Sprintf("finding:%d", fid)
	if _, err := d.UpsertAssuranceConclusion(t.Context(), task.ID, cid, assuranceMapping(1, 1)); err != nil {
		t.Fatal(err)
	}
	w := assuranceWorkspace(t, d, task.ID)
	c := assuranceConclusion(t, w, cid)
	if _, err := d.RecordAssuranceReview(t.Context(), task.ID, cid, AssuranceReviewInput{ExpectedGeneration: w.Current.Generation, ExpectedEvidenceVersion: c.EvidenceVersion, ExpectedEvidenceHash: c.EvidenceHash, State: "operator_verified", Provenance: "operator", Reviewer: "archive fixture", Rationale: "synthetic review", EvidenceRefs: c.EvidenceRefs}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddCoverageCheck(t.Context(), task.ID, AssuranceCoverageInput{AssuranceConclusionInput: assuranceMapping(w.Current.Generation, 1), Surface: "fixture", Identity: "test-user", Workflow: "read", RiskCategory: "authorization", Outcome: "observed_not_vulnerable", Rationale: "synthetic scoped check", EvidenceRefs: c.EvidenceRefs}); err != nil {
		t.Fatal(err)
	}
	w = assuranceWorkspace(t, d, task.ID)
	if _, err := d.AppendAssuranceSnapshot(t.Context(), task.ID, AssuranceSnapshotInput{ExpectedGeneration: w.Current.Generation, Environment: "local-test", Revision: "build-b", Nodes: []retestplan.Node{{ID: "route", DependsOn: []string{"policy"}}, {ID: "policy"}}, Versions: map[string]string{"route": "r1", "policy": "p2"}, AvailablePrerequisites: []string{"test-user"}}); err != nil {
		t.Fatal(err)
	}
	w = assuranceWorkspace(t, d, task.ID)
	plan, err := d.CreateAssurancePlan(t.Context(), task.ID, AssurancePlanInput{ExpectedGeneration: w.Current.Generation, BudgetUnits: 100})
	if err != nil {
		t.Fatal(err)
	}
	run, conv, created, err := d.CreateAssuranceRetest(t.Context(), task.ID, plan.ID, cid, "")
	if err != nil || !created {
		t.Fatalf("dispatch %v %v", created, err)
	}
	t.Cleanup(func() { _ = d.DeleteConversation(conv.ID) })
	if ok, err := d.StartFindingRetest(t.Context(), run.ID); err != nil || !ok {
		t.Fatalf("start %v %v", ok, err)
	}
	if err := d.RecordFindingRetestResult(t.Context(), conv.ID, "reproduced", "synthetic result", "synthetic executed check <response>&value</response>"); err != nil {
		t.Fatal(err)
	}
	if err := d.FinishFindingRetest(run.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	manual, manualConv, _, err := d.CreateFindingRetest(t.Context(), fid, "synthetic history")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteConversation(manualConv.ID) })
	if ok, err := d.StartFindingRetest(t.Context(), manual.ID); err != nil || !ok {
		t.Fatalf("manual start %v %v", ok, err)
	}
	if err := d.RecordFindingRetestResult(t.Context(), manualConv.ID, "inconclusive", "synthetic manual result", "synthetic missing prerequisite"); err != nil {
		t.Fatal(err)
	}
	if err := d.FinishFindingRetest(manual.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	before, err := d.GetTaskAssuranceExport(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetPaused(task.ID, true); err != nil {
		t.Fatal(err)
	}
	archive, err := d.QueueTaskArchive(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE task_archives SET state=$2 WHERE id=$1`, archive.ID, Archiving); err != nil {
		t.Fatal(err)
	}
	snapshot, err := d.SnapshotTaskArchive(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Package encoding must preserve exact immutable JSON despite whitespace changes.
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteTaskArchive(archive.ID, snapshot, "/tmp/fixture-assurance.tar.zst", "synthetic", 1, 1); err != nil {
		t.Fatal(err)
	}
	for _, table := range assuranceArchiveTables {
		raw, count, err := queryArchiveRows(d, assuranceArchiveQuery(table), task.ID)
		if err != nil || count != 0 {
			t.Fatalf("hot %s remains count=%d rows=%s err=%v", table, count, raw, err)
		}
	}
	// Global conversation deletion while a task is cold must not lose the report
	// or resurrect an agent session when the task is restored.
	if err := d.DeleteConversation(conv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE task_archives SET state=$2 WHERE id=$1`, archive.ID, Restoring); err != nil {
		t.Fatal(err)
	}
	var foreign TaskArchiveSnapshot
	if err := json.Unmarshal(encoded, &foreign); err != nil {
		t.Fatal(err)
	}
	foreignRows, err := decodeArchiveRows(foreign.Tables["assurance_snapshots"])
	if err != nil {
		t.Fatal(err)
	}
	foreignRows[0]["task_id"] = task.ID + 1000000
	foreign.Tables["assurance_snapshots"], err = json.Marshal(foreignRows)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.RestoreTaskArchive(archive.ID, &foreign, 0); err == nil {
		t.Fatal("foreign assurance history accepted")
	}
	if live, err := d.GetTask(task.ID); err != nil || live != nil {
		t.Fatalf("rejected restore published partial task: %+v %v", live, err)
	}
	if _, err := d.RestoreTaskArchive(archive.ID, snapshot, 0); err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
	after, err := d.GetTaskAssuranceExport(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, historical := range before.Retests {
		if historical.ID == run.ID {
			historical.ConversationID = nil
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("assurance changed across restore: current=%t history=%t conclusions=%t plans=%t mappings=%t reviews=%t retests=%t status=%s/%s", reflect.DeepEqual(before.Current, after.Current), reflect.DeepEqual(before.History, after.History), reflect.DeepEqual(before.Conclusions, after.Conclusions), reflect.DeepEqual(before.Plans, after.Plans), reflect.DeepEqual(before.MappingHistory, after.MappingHistory), reflect.DeepEqual(before.ReviewHistory, after.ReviewHistory), reflect.DeepEqual(before.Retests, after.Retests), before.TaskStatus, after.TaskStatus)
	}
	runs, err := d.ListFindingRetests(fid)
	if err != nil || len(runs) != 2 || runs[1].ID != run.ID || runs[1].Status != "completed" || runs[1].Verdict != "reproduced" || runs[1].ConversationID != nil || runs[1].AssurancePlanID != plan.ID || runs[1].Evidence != "synthetic executed check <response>&value</response>" {
		t.Fatalf("terminal retest was lost or restarted: %+v %v", runs, err)
	}
	if runs[0].ID != manual.ID || runs[0].ConversationID == nil || *runs[0].ConversationID != manualConv.ID || runs[0].Verdict != "inconclusive" {
		t.Fatalf("existing manual conversation link was lost: %+v", runs[0])
	}
}

func TestTaskArchiveAssuranceLegacyRestore(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			d, task := assuranceArchiveFixture(t)
			archive, err := d.QueueTaskArchive(task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(`UPDATE task_archives SET state=$2 WHERE id=$1`, archive.ID, Archiving); err != nil {
				t.Fatal(err)
			}
			snapshot, err := d.SnapshotTaskArchive(task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := d.CompleteTaskArchive(archive.ID, snapshot, "/tmp/legacy-fixture.tar.zst", "synthetic", 1, 1); err != nil {
				t.Fatal(err)
			}
			snapshot.FormatVersion = version
			for _, table := range assuranceArchiveTables {
				delete(snapshot.Tables, table)
				delete(snapshot.DataCounts, table)
			}
			if _, err := d.Exec(`UPDATE task_archives SET state=$2 WHERE id=$1`, archive.ID, Restoring); err != nil {
				t.Fatal(err)
			}
			if _, err := d.RestoreTaskArchive(archive.ID, snapshot, 0); err != nil {
				t.Fatalf("legacy v%d restore: %v", version, err)
			}
			if err := d.CompleteTaskArchiveRestore(archive.ID); err != nil {
				t.Fatal(err)
			}
			w, err := d.GetTaskAssurance(t.Context(), task.ID)
			if err != nil || w.Current != nil || len(w.Conclusions) != 0 {
				t.Fatalf("legacy restore invented assurance: %+v %v", w, err)
			}
		})
	}
}
