package db

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
)

// v4 keeps immutable assurance snapshots, operator history, plan inputs and
// terminal retest reports. Task/finding/plan IDs survive archive restore; asset
// remapping is detected by the ordinary runtime fingerprint on the next read.
var assuranceArchiveTables = []string{
	"assurance_snapshots", "assurance_conclusions", "assurance_coverage_checks",
	"assurance_reviews", "assurance_plans", "assurance_conclusion_history", "finding_retests",
}

func assuranceArchiveQuery(table string) string {
	switch table {
	case "assurance_snapshots":
		return `SELECT * FROM assurance_snapshots WHERE task_id=$1 ORDER BY generation`
	case "assurance_conclusions":
		return `SELECT * FROM assurance_conclusions WHERE task_id=$1 ORDER BY conclusion_id`
	case "finding_retests":
		return `SELECT r.* FROM finding_retests r JOIN findings f ON f.id=r.finding_id WHERE f.task_id=$1 ORDER BY r.id`
	default:
		// Only the private constant list above reaches this branch.
		return `SELECT * FROM ` + table + ` WHERE task_id=$1 ORDER BY id`
	}
}

func snapshotAssuranceArchiveTx(tx *sql.Tx, taskID int64, tables map[string]json.RawMessage, counts map[string]int64) error {
	for _, table := range assuranceArchiveTables {
		raw, count, err := queryArchiveRows(tx, assuranceArchiveQuery(table), taskID)
		if err != nil {
			return fmt.Errorf("snapshot %s: %w", table, err)
		}
		tables[table], counts[table] = raw, count
	}
	return nil
}

// Check again immediately before compaction while holding the task lock. This
// also catches a conversation deletion that changed a terminal retest link after
// capture, and refuses to delete history that is absent from the written package.
func checkAssuranceArchiveSnapshotTx(tx *sql.Tx, snapshot *TaskArchiveSnapshot) error {
	for _, table := range assuranceArchiveTables {
		live, _, err := queryArchiveRows(tx, assuranceArchiveQuery(table), snapshot.TaskID)
		if err != nil {
			return err
		}
		captured := snapshot.Tables[table]
		if len(captured) == 0 {
			captured = json.RawMessage("[]")
		}
		var left, right any
		liveDecoder := json.NewDecoder(bytes.NewReader(live))
		liveDecoder.UseNumber()
		if err := liveDecoder.Decode(&left); err != nil {
			return err
		}
		capturedDecoder := json.NewDecoder(bytes.NewReader(captured))
		capturedDecoder.UseNumber()
		if err := capturedDecoder.Decode(&right); err != nil {
			return err
		}
		// Package JSON may escape HTML or reorder object keys. Compare values,
		// preserving integer precision and array order, rather than encoded bytes.
		if !reflect.DeepEqual(left, right) {
			return fmt.Errorf("%w: %s changed after the archive snapshot; retry archival", ErrTaskArchiveState, table)
		}
	}
	return nil
}

func compactAssuranceArchiveTx(tx *sql.Tx, taskID int64) error {
	// Finding retests are removed by the existing finding FK cascade. Reviews and
	// plans must be removed before the snapshots they reference.
	for _, table := range []string{"assurance_reviews", "assurance_plans", "assurance_conclusion_history", "assurance_conclusions", "assurance_coverage_checks", "assurance_snapshots"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE task_id=$1`, taskID); err != nil {
			return err
		}
	}
	return nil
}

func restoreAssuranceArchiveTx(tx *sql.Tx, snapshot *TaskArchiveSnapshot) error {
	for _, table := range assuranceArchiveTables {
		raw := snapshot.Tables[table]
		rows, err := decodeArchiveRows(raw)
		if err != nil {
			return fmt.Errorf("restore %s: %w", table, err)
		}
		if len(rows) == 0 {
			continue
		}
		if snapshot.FormatVersion < 4 {
			return ErrTaskArchiveFormatMismatch
		}
		for _, row := range rows {
			if table != "finding_retests" {
				taskID, ok := jsonInt64(row["task_id"])
				if !ok || taskID != snapshot.TaskID {
					return fmt.Errorf("archive %s row belongs to another task", table)
				}
				continue
			}
			findingID, ok := jsonInt64(row["finding_id"])
			if !ok {
				return fmt.Errorf("invalid archived retest finding")
			}
			var owned bool
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM findings WHERE id=$1 AND task_id=$2)`, findingID, snapshot.TaskID).Scan(&owned); err != nil {
				return err
			}
			if !owned {
				return fmt.Errorf("archived retest finding belongs to another task")
			}
			status, _ := row["status"].(string)
			if status != "completed" && status != "failed" && status != "stopped" {
				return fmt.Errorf("archived retest must be terminal")
			}
			boundTask, ok := jsonInt64(row["assurance_task_id"])
			if !ok || (boundTask != 0 && boundTask != snapshot.TaskID) {
				return fmt.Errorf("archived retest assurance belongs to another task")
			}
			planID, _ := jsonInt64(row["assurance_plan_id"])
			generation, _ := jsonInt64(row["assurance_generation"])
			cid, _ := row["assurance_conclusion_id"].(string)
			fingerprint, _ := row["assurance_fingerprint"].(string)
			if boundTask > 0 {
				if cid != fmt.Sprintf("finding:%d", findingID) {
					return fmt.Errorf("archived retest conclusion does not match finding")
				}
				if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM assurance_plans WHERE id=$1 AND task_id=$2 AND generation=$3 AND fingerprint=$4)`, planID, boundTask, generation, fingerprint).Scan(&owned); err != nil {
					return err
				}
				if !owned {
					return fmt.Errorf("archived retest does not match its saved plan")
				}
			} else if planID != 0 || generation != 0 || cid != "" || fingerprint != "" {
				return fmt.Errorf("archived manual retest has an inconsistent plan binding")
			}
			// Conversations are global and are not copied into a task package. Keep
			// a valid historical link, but never recreate a deleted conversation or
			// restart its already terminal retest.
			if conversationID, ok := jsonInt64(row["conversation_id"]); ok {
				var present bool
				if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM conversations WHERE id=$1)`, conversationID).Scan(&present); err != nil {
					return err
				}
				if !present {
					row["conversation_id"] = nil
				}
			}
		}
		encoded, err := json.Marshal(rows)
		if err != nil {
			return err
		}
		// Table is selected only from the fixed internal allowlist above.
		if _, err := tx.Exec(`INSERT INTO `+table+` SELECT * FROM json_populate_recordset(NULL::`+table+`,$1::json)`, string(encoded)); err != nil {
			return fmt.Errorf("restore %s: %w", table, err)
		}
	}
	return nil
}
