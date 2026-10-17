package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// CreateAssuranceRetest binds a selected finding check to its immutable plan in
// the same transaction that creates the existing retest/conversation. A retry
// returns the same run, including after completion; it never starts a new turn.
func (d *DB) CreateAssuranceRetest(ctx context.Context, taskID, planID int64, conclusionID, notes string) (*FindingRetest, *Conversation, bool, error) {
	plan, err := d.GetAssurancePlan(ctx, taskID, planID)
	if err != nil {
		return nil, nil, false, err
	}
	var selected bool
	for _, item := range plan.Result.Items {
		if item.ID == conclusionID {
			selected = item.Kind == "finding" && item.Decision == "selected"
			break
		}
	}
	findingID, parseErr := strconv.ParseInt(strings.TrimPrefix(conclusionID, "finding:"), 10, 64)
	if !selected || !strings.HasPrefix(conclusionID, "finding:") || parseErr != nil || findingID <= 0 {
		return nil, nil, false, fmt.Errorf("%w: only selected finding checks can start an automated retest", ErrAssuranceInvalid)
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, false, err
	}
	defer tx.Rollback()
	snapshot, err := AssuranceSnapshotTx(ctx, tx, taskID, plan.Generation)
	if err != nil {
		return nil, nil, false, err
	}
	if snapshot == nil || snapshot.Fingerprint != plan.Fingerprint {
		return nil, nil, false, ErrAssuranceConflict
	}
	var owner sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT task_id FROM findings WHERE id=$1 FOR UPDATE`, findingID).Scan(&owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, false, ErrAssuranceNotFound
		}
		return nil, nil, false, err
	}
	if !owner.Valid || owner.Int64 != taskID {
		return nil, nil, false, ErrAssuranceNotFound
	}
	previous, err := scanRetest(tx.QueryRowContext(ctx, `SELECT `+retestCols+` FROM finding_retests WHERE assurance_task_id=$1 AND assurance_plan_id=$2 AND assurance_conclusion_id=$3`, taskID, planID, conclusionID))
	if err != nil {
		return nil, nil, false, err
	}
	if previous != nil {
		return previous, nil, false, nil
	}
	r, c, created, err := createFindingRetestTx(ctx, tx, findingID, notes)
	if err != nil {
		return nil, nil, false, err
	}
	if !created {
		// A manual/other-plan retest has a different tested snapshot. Do not
		// relabel it as this plan's work just because creation was deduplicated.
		return nil, nil, false, fmt.Errorf("%w: this finding already has an active retest", ErrAssuranceConflict)
	}
	_, err = tx.ExecContext(ctx, `UPDATE finding_retests SET assurance_task_id=$2, assurance_plan_id=$3,
	assurance_conclusion_id=$4, assurance_generation=$5, assurance_fingerprint=$6,
	snapshot=snapshot || jsonb_build_object('assurance_target',$7::jsonb)
	WHERE id=$1`, r.ID, taskID, planID, conclusionID, plan.Generation, plan.Fingerprint, mustJSON(snapshot))
	if err != nil {
		return nil, nil, false, err
	}
	r.AssuranceTaskID, r.AssurancePlanID, r.AssuranceConclusionID = taskID, planID, conclusionID
	r.AssuranceGeneration, r.AssuranceFingerprint = plan.Generation, plan.Fingerprint
	if err := tx.Commit(); err != nil {
		return nil, nil, false, err
	}
	return r, c, true, nil
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value) // AssuranceSnapshot contains only JSON-safe fields.
	return string(data)
}

func (d *DB) ListTaskAssuranceRetests(ctx context.Context, taskID int64) ([]*FindingRetest, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+retestCols+` FROM finding_retests WHERE assurance_task_id=$1 ORDER BY id DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*FindingRetest{}
	for rows.Next() {
		row, err := scanRetest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
