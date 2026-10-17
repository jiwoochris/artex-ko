package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/retestplan"
)

const (
	assuranceMaxRecords      = 2000
	assuranceMaxNodes        = 1000
	assuranceMaxEdges        = 8000
	assuranceConstraintsNode = "artex:task_constraints"
	assuranceGraphNode       = "artex:dependency_map"
	assuranceMappingsNode    = "artex:workspace_mappings"
)

type assuranceCoverageRecord struct {
	AssuranceCoverageInput
	EvidenceDependencies []string `json:"evidence_dependencies"`
}

type assuranceMappingRecord struct {
	AssuranceConclusionInput
	OperatorMapping *AssuranceConclusionInput `json:"operator_mapping,omitempty"`
	Provenance      string                    `json:"provenance"`
	RetestID        int64                     `json:"last_retest_id,omitempty,string"`
	RetestVerdict   string                    `json:"last_retest_verdict,omitempty"`
}

func assuranceInvalid(s string) error { return fmt.Errorf("%w: %s", ErrAssuranceInvalid, s) }
func assuranceHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func assuranceJSON(v any) []byte           { b, _ := json.Marshal(v); return b }
func assuranceFindingNode(id int64) string { return fmt.Sprintf("artex:finding:%d:evidence", id) }
func assuranceStrings(v []string) []string {
	out := slices.Clone(v)
	if out == nil {
		out = []string{}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
func assuranceStringList(v []string, max int) error {
	if len(v) > max {
		return assuranceInvalid("too many list entries")
	}
	for _, s := range v {
		if strings.TrimSpace(s) == "" || len(s) > 512 {
			return assuranceInvalid("list entries must contain 1..512 bytes")
		}
	}
	return nil
}
func assuranceValidateMapping(in AssuranceConclusionInput) error {
	if in.ExpectedGeneration < 1 || in.BaselineGeneration < 1 || in.BaselineGeneration > in.ExpectedGeneration {
		return assuranceInvalid("a valid baseline and expected generation are required")
	}
	if in.Impact < 1 || in.Impact > 5 || in.EffortUnits < 1 || in.EffortUnits > 1000000 {
		return assuranceInvalid("impact must be 1..5 and effort_units 1..1000000")
	}
	if err := assuranceStringList(in.Dependencies, 100); err != nil {
		return err
	}
	if err := assuranceStringList(in.Prerequisites, 100); err != nil {
		return err
	}
	for _, dep := range in.Dependencies {
		if strings.HasPrefix(dep, "artex:") {
			return assuranceInvalid("artex: dependencies are maintained by the server")
		}
	}
	if in.MappingComplete && len(in.Dependencies) == 0 {
		return assuranceInvalid("complete mappings require at least one declared dependency")
	}
	return nil
}

func assuranceLockTask(ctx context.Context, tx *sql.Tx, taskID int64) error {
	if taskID <= 0 {
		return ErrAssuranceNotFound
	}
	if err := LockTaskEvidenceTx(tx, taskID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAssuranceNotFound
		}
		return err
	}
	var archived sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT archived_at FROM tasks WHERE id=$1`, taskID).Scan(&archived); err != nil {
		return err
	}
	if archived.Valid {
		return ErrTaskArchiveState
	}
	return nil
}

func assuranceLatest(ctx context.Context, tx *sql.Tx, taskID int64) (*AssuranceSnapshot, error) {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT snapshot FROM assurance_snapshots WHERE task_id=$1 ORDER BY generation DESC LIMIT 1`, taskID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s AssuranceSnapshot
	if err = json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
func assuranceSnapshotAt(ctx context.Context, tx *sql.Tx, taskID, generation int64) (*AssuranceSnapshot, error) {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT snapshot FROM assurance_snapshots WHERE task_id=$1 AND generation=$2`, taskID, generation).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAssuranceNotFound
		}
		return nil, err
	}
	var s AssuranceSnapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Runtime versions contain hashes, never captured request bodies or credentials.
// Finding row share locks keep textual evidence edits from racing publication.
func assuranceRuntimeVersions(ctx context.Context, tx *sql.Tx, taskID int64) (map[string]string, error) {
	out := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT id, jsonb_build_object('class',vulnclass,'summary',summary,'evidence',evidence,'assets',asset_ids,'evidence_version',evidence_version) FROM findings WHERE task_id=$1 ORDER BY id FOR SHARE`, taskID)
	if err != nil {
		return nil, err
	}
	count := 0
	for rows.Next() {
		var id int64
		var raw json.RawMessage
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		count++
		if count > assuranceMaxRecords {
			rows.Close()
			return nil, assuranceInvalid("workspace exceeds 2000 findings")
		}
		out[assuranceFindingNode(id)] = assuranceHash(raw)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var constraints json.RawMessage
	err = tx.QueryRowContext(ctx, `SELECT jsonb_build_object('goal',t.goal,'description',t.description,'constraints',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',c.id,'kind',c.kind,'text',c.text) ORDER BY c.id) FROM task_constraints c WHERE c.exploration_id=t.exploration_id),'[]'::jsonb)) FROM tasks t WHERE t.id=$1`, taskID).Scan(&constraints)
	if err != nil {
		return nil, err
	}
	out[assuranceConstraintsNode] = assuranceHash(constraints)
	var mappings json.RawMessage
	err = tx.QueryRowContext(ctx, `SELECT jsonb_build_object('mappings',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',conclusion_id,'mapping',COALESCE(mapping->'operator_mapping',mapping)) ORDER BY conclusion_id) FROM assurance_conclusions WHERE task_id=$1 AND COALESCE((mapping->'operator_mapping'->>'expected_generation')::bigint,(mapping->>'expected_generation')::bigint,0)>0),'[]'::jsonb),'checks',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',id,'check',check_data) ORDER BY id) FROM assurance_coverage_checks WHERE task_id=$1),'[]'::jsonb))`, taskID).Scan(&mappings)
	if err != nil {
		return nil, err
	}
	out[assuranceMappingsNode] = assuranceHash(mappings)
	return out, nil
}
func assuranceOverlay(s AssuranceSnapshot, runtime map[string]string) AssuranceSnapshot {
	s.Nodes = slices.Clone(s.Nodes)
	s.Versions = maps.Clone(s.Versions)
	if s.Versions == nil {
		s.Versions = map[string]string{}
	}
	nodes := []retestplan.Node{}
	for _, n := range s.Nodes {
		if !strings.HasPrefix(n.ID, "artex:") {
			n.DependsOn = assuranceStrings(n.DependsOn)
			nodes = append(nodes, n)
		}
	}
	slices.SortFunc(nodes, func(a, b retestplan.Node) int { return strings.Compare(a.ID, b.ID) })
	for k := range s.Versions {
		if strings.HasPrefix(k, "artex:") {
			delete(s.Versions, k)
		}
	}
	s.Versions[assuranceGraphNode] = assuranceHash(nodes)
	for k, v := range runtime {
		s.Versions[k] = v
	}
	for k := range s.Versions {
		if strings.HasPrefix(k, "artex:") {
			nodes = append(nodes, retestplan.Node{ID: k, DependsOn: []string{}})
		}
	}
	slices.SortFunc(nodes, func(a, b retestplan.Node) int { return strings.Compare(a.ID, b.ID) })
	s.Nodes = nodes
	s.AvailablePrerequisites = assuranceStrings(s.AvailablePrerequisites)
	s.Fingerprint = assuranceHash(struct {
		Environment, Revision string
		Nodes                 []retestplan.Node
		Versions              map[string]string
		Prerequisites         []string
	}{s.Environment, s.Revision, s.Nodes, s.Versions, s.AvailablePrerequisites})
	return s
}
func assuranceSaveSnapshot(ctx context.Context, tx *sql.Tx, taskID int64, s *AssuranceSnapshot) error {
	s.CreatedAt = time.Now().UTC()
	_, err := tx.ExecContext(ctx, `INSERT INTO assurance_snapshots(task_id,generation,fingerprint,snapshot,created_at) VALUES($1,$2,$3,$4,$5)`, taskID, s.Generation, s.Fingerprint, assuranceJSON(s), s.CreatedAt)
	return err
}
func assuranceSync(ctx context.Context, tx *sql.Tx, taskID int64) (*AssuranceSnapshot, error) {
	s, err := assuranceLatest(ctx, tx, taskID)
	if err != nil || s == nil {
		return s, err
	}
	runtime, err := assuranceRuntimeVersions(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	current := assuranceOverlay(*s, runtime)
	if current.Fingerprint == s.Fingerprint {
		return s, nil
	}
	current.Generation++
	current.Source = "runtime"
	if err = assuranceSaveSnapshot(ctx, tx, taskID, &current); err != nil {
		return nil, err
	}
	return &current, nil
}

// AssuranceSnapshotTx locks the task, captures runtime evidence changes and
// compares the generation in the same transaction as the caller's publication.
// The caller must acquire task before finding locks and commit the transaction.
func AssuranceSnapshotTx(ctx context.Context, tx *sql.Tx, taskID, expectedGeneration int64) (*AssuranceSnapshot, error) {
	if err := assuranceLockTask(ctx, tx, taskID); err != nil {
		return nil, err
	}
	s, err := assuranceSync(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrAssuranceNotFound
	}
	if s.Generation != expectedGeneration {
		return nil, ErrAssuranceConflict
	}
	return s, nil
}

func (d *DB) AppendAssuranceSnapshot(ctx context.Context, taskID int64, in AssuranceSnapshotInput) (*AssuranceSnapshot, error) {
	if in.ExpectedGeneration < 0 || len(strings.TrimSpace(in.Environment)) == 0 || len(in.Environment) > 128 || len(strings.TrimSpace(in.Revision)) == 0 || len(in.Revision) > 512 {
		return nil, assuranceInvalid("environment and revision are required")
	}
	if len(in.Nodes) > assuranceMaxNodes || len(in.Versions) > assuranceMaxNodes {
		return nil, assuranceInvalid("at most 1000 declared dependencies are allowed")
	}
	if err := assuranceStringList(in.AvailablePrerequisites, 100); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	edges := 0
	for _, n := range in.Nodes {
		if strings.TrimSpace(n.ID) == "" || len(n.ID) > 512 || seen[n.ID] || strings.HasPrefix(n.ID, "artex:") {
			return nil, assuranceInvalid("dependency IDs must be unique and cannot use artex:")
		}
		seen[n.ID] = true
		edges += len(n.DependsOn)
		if err := assuranceStringList(n.DependsOn, 100); err != nil {
			return nil, err
		}
		for _, dep := range n.DependsOn {
			if strings.HasPrefix(dep, "artex:") {
				return nil, assuranceInvalid("artex: dependencies are server-owned")
			}
		}
	}
	if edges > assuranceMaxEdges {
		return nil, assuranceInvalid("dependency graph exceeds 8000 edges")
	}
	for k, v := range in.Versions {
		if !seen[k] || len(v) > 512 {
			return nil, assuranceInvalid("versions must refer to declared dependencies and contain at most 512 bytes")
		}
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = assuranceLockTask(ctx, tx, taskID); err != nil {
		return nil, err
	}
	previous, err := assuranceSync(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	var generation int64
	if previous != nil {
		generation = previous.Generation
		if previous.Environment != in.Environment {
			return nil, assuranceInvalid("environment cannot change within one task")
		}
	}
	if generation != in.ExpectedGeneration {
		return nil, ErrAssuranceConflict
	}
	runtime, err := assuranceRuntimeVersions(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	s := assuranceOverlay(AssuranceSnapshot{Generation: generation + 1, Environment: in.Environment, Revision: in.Revision, Nodes: in.Nodes, Versions: in.Versions, AvailablePrerequisites: in.AvailablePrerequisites, Source: "declared"}, runtime)
	if err = assuranceSaveSnapshot(ctx, tx, taskID, &s); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &s, nil
}

func assuranceLoadConclusions(ctx context.Context, tx *sql.Tx, taskID int64, current *AssuranceSnapshot) ([]AssuranceConclusion, error) {
	out := []AssuranceConclusion{}
	rows, err := tx.QueryContext(ctx, `SELECT f.id,COALESCE(NULLIF(f.name,''),NULLIF(f.vulnclass,''),'Finding'),f.severity,f.status,f.evidence_version,COALESCE((SELECT jsonb_agg(b.snapshot_id ORDER BY b.position,b.id) FROM finding_traffic_bindings b WHERE b.finding_id=f.id),'[]'::jsonb),length(trim(f.evidence))>0 FROM findings f WHERE f.task_id=$1 ORDER BY f.id LIMIT 2001`, taskID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var refs []byte
		var hasDocument bool
		c := AssuranceConclusion{Kind: "finding", Assurance: "candidate", Freshness: "unknown", Dependencies: []string{}, Prerequisites: []string{}, Reviews: []AssuranceReview{}, Reasons: []retestplan.Reason{}, EffortUnits: 1, Impact: 3}
		if err = rows.Scan(&id, &c.Title, &c.Severity, &c.TriageStatus, &c.EvidenceVersion, &refs, &hasDocument); err != nil {
			rows.Close()
			return nil, err
		}
		c.ID = fmt.Sprintf("finding:%d", id)
		c.FindingID = &id
		_ = json.Unmarshal(refs, &c.EvidenceRefs)
		if len(c.EvidenceRefs) > 0 {
			c.Assurance = "runtime_observed"
		}
		if current != nil {
			c.EvidenceHash = current.Versions[assuranceFindingNode(id)]
			if hasDocument {
				c.EvidenceRefs = append(c.EvidenceRefs, fmt.Sprintf("finding:%d:evidence:%s", id, c.EvidenceHash))
			}
		}
		switch c.Severity {
		case "critical":
			c.Impact = 5
		case "high":
			c.Impact = 4
		case "low":
			c.Impact = 2
		}
		out = append(out, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,check_data FROM assurance_coverage_checks WHERE task_id=$1 ORDER BY id LIMIT 2001`, taskID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var raw []byte
		var in assuranceCoverageRecord
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(raw, &in); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, AssuranceConclusion{ID: fmt.Sprintf("check:%d", id), Kind: "negative_coverage", Title: in.Surface + " · " + in.RiskCategory, Assurance: "operator_reported", Freshness: "unknown", Surface: in.Surface, Identity: in.Identity, Workflow: in.Workflow, RiskCategory: in.RiskCategory, Outcome: in.Outcome, Rationale: in.Rationale, EvidenceRefs: in.EvidenceRefs, EvidenceDependencies: in.EvidenceDependencies, Reviews: []AssuranceReview{}, Reasons: []retestplan.Reason{}})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(out) > assuranceMaxRecords {
		return nil, assuranceInvalid("workspace exceeds 2000 conclusions")
	}
	mapping := map[string]assuranceMappingRecord{}
	rows, err = tx.QueryContext(ctx, `SELECT conclusion_id,mapping FROM assurance_conclusions WHERE task_id=$1`, taskID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var raw []byte
		var m assuranceMappingRecord
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(raw, &m); err != nil {
			rows.Close()
			return nil, err
		}
		mapping[id] = m
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	reviews := map[string][]AssuranceReview{}
	rows, err = tx.QueryContext(ctx, `SELECT conclusion_id,review FROM assurance_reviews WHERE task_id=$1 ORDER BY id DESC`, taskID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var raw []byte
		var review AssuranceReview
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(raw, &review); err != nil {
			rows.Close()
			return nil, err
		}
		reviews[id] = append(reviews[id], review)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	reasonBytes := 0
	for i := range out {
		c := &out[i]
		if m, ok := mapping[c.ID]; ok {
			c.BaselineGeneration = m.BaselineGeneration
			c.Dependencies = assuranceStrings(m.Dependencies)
			c.MappingComplete = m.MappingComplete
			c.Prerequisites = assuranceStrings(m.Prerequisites)
			c.Impact = m.Impact
			c.EffortUnits = m.EffortUnits
			c.LastRetestID = m.RetestID
			c.LastRetestVerdict = m.RetestVerdict
		}
		if c.EvidenceRefs == nil {
			c.EvidenceRefs = []string{}
		}
		c.EvidenceRefKinds = map[string]string{}
		for _, ref := range c.EvidenceRefs {
			kind := "traffic_snapshot"
			if strings.HasPrefix(ref, "finding:") {
				kind = "finding_document"
			}
			c.EvidenceRefKinds[ref] = kind
		}
		if rev, ok := reviews[c.ID]; ok {
			c.Reviews = rev
		}
		if current != nil {
			item, _, e := assuranceEvaluate(ctx, tx, taskID, *c, *current)
			if e != nil {
				return nil, e
			}
			c.Freshness = item.Freshness
			c.Reasons = item.Reasons
			reasonBytes += len(assuranceJSON(item.Reasons))
			if reasonBytes > 4<<20 {
				return nil, assuranceInvalid("dependency explanations exceed 4 MiB; narrow the workspace")
			}
		}
		if c.LastRetestID > 0 && current != nil && c.Freshness != "stale" {
			c.Assurance = "retest_reported"
		}
		if len(c.Reviews) > 0 && current != nil {
			r := c.Reviews[0]
			baseline, e := assuranceSnapshotAt(ctx, tx, taskID, r.Generation)
			if e != nil {
				return nil, e
			}
			if r.EvidenceHash == c.EvidenceHash && assuranceDependenciesMatch(r.Dependencies, *baseline, *current) {
				c.Assurance = r.State
			}
		}
	}
	return out, nil
}

func assuranceEvaluate(ctx context.Context, tx *sql.Tx, taskID int64, c AssuranceConclusion, current AssuranceSnapshot) (retestplan.Item, retestplan.Input, error) {
	baseline := &current
	var err error
	if c.BaselineGeneration > 0 {
		baseline, err = assuranceSnapshotAt(ctx, tx, taskID, c.BaselineGeneration)
		if err != nil {
			return retestplan.Item{}, retestplan.Input{}, err
		}
	}
	deps := append(slices.Clone(c.Dependencies), assuranceConstraintsNode, assuranceGraphNode)
	deps = append(deps, c.EvidenceDependencies...)
	if c.FindingID != nil {
		deps = append(deps, assuranceFindingNode(*c.FindingID))
	}
	complete := c.MappingComplete && c.BaselineGeneration > 0
	if c.Kind == "negative_coverage" && c.Outcome != "observed_not_vulnerable" {
		complete = false
	}
	input := retestplan.Input{SchemaVersion: retestplan.SchemaVersion, Baseline: retestplan.Snapshot{Project: fmt.Sprintf("task:%d", taskID), Environment: baseline.Environment, Revision: baseline.Revision, Generation: baseline.Generation, Versions: baseline.Versions}, Current: retestplan.Snapshot{Project: fmt.Sprintf("task:%d", taskID), Environment: current.Environment, Revision: current.Revision, Generation: current.Generation, Versions: current.Versions}, Nodes: current.Nodes, AvailablePrerequisites: current.AvailablePrerequisites, BudgetUnits: 0, Conclusions: []retestplan.Conclusion{{ID: c.ID, Kind: c.Kind, Dependencies: deps, MappingComplete: complete, Prerequisites: c.Prerequisites, Impact: c.Impact, EffortUnits: c.EffortUnits}}}
	p, err := retestplan.Build(input)
	if err != nil {
		return retestplan.Item{}, input, err
	}
	item := p.Items[0]
	if c.Kind == "negative_coverage" && c.Outcome != "observed_not_vulnerable" {
		item.Reasons = append(item.Reasons, retestplan.Reason{Kind: "check_" + c.Outcome})
		if c.Outcome == "blocked" {
			item.Decision = "blocked"
		}
	}
	return item, input, nil
}

func (d *DB) GetTaskAssurance(ctx context.Context, taskID int64) (*TaskAssurance, error) {
	return d.getTaskAssurance(ctx, taskID, false)
}

// GetTaskAssuranceExport includes every stored snapshot, plan input and mapping
// revision under the same publication lock. The interactive view is bounded.
func (d *DB) GetTaskAssuranceExport(ctx context.Context, taskID int64) (*TaskAssurance, error) {
	return d.getTaskAssurance(ctx, taskID, true)
}

func (d *DB) getTaskAssurance(ctx context.Context, taskID int64, full bool) (*TaskAssurance, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = assuranceLockTask(ctx, tx, taskID); err != nil {
		return nil, err
	}
	current, err := assuranceSync(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	out := &TaskAssurance{TaskID: taskID, Current: current, History: []AssuranceSnapshot{}, Plans: []AssurancePlan{}, Retests: []*FindingRetest{}}
	if err = tx.QueryRowContext(ctx, `SELECT status FROM tasks WHERE id=$1`, taskID).Scan(&out.TaskStatus); err != nil {
		return nil, err
	}
	out.Conclusions, err = assuranceLoadConclusions(ctx, tx, taskID, current)
	if err != nil {
		return nil, err
	}
	historyLimit, planLimit := " LIMIT 101", " LIMIT 51"
	if full {
		historyLimit, planLimit = "", ""
	}
	rows, err := tx.QueryContext(ctx, `SELECT snapshot FROM assurance_snapshots WHERE task_id=$1 ORDER BY generation DESC`+historyLimit, taskID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var raw []byte
		var s AssuranceSnapshot
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(raw, &s); err != nil {
			rows.Close()
			return nil, err
		}
		if !full && len(out.History) == 100 {
			out.HistoryTruncated = true
		} else {
			out.History = append(out.History, s)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,task_id,generation,fingerprint,input,result,created_at FROM assurance_plans WHERE task_id=$1 ORDER BY id DESC`+planLimit, taskID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p AssurancePlan
		var raw []byte
		if err = rows.Scan(&p.ID, &p.TaskID, &p.Generation, &p.Fingerprint, &p.Input, &raw, &p.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(raw, &p.Result); err != nil {
			rows.Close()
			return nil, err
		}
		if !full {
			p.Input = nil
		}
		if !full && len(out.Plans) == 50 {
			out.PlansTruncated = true
		} else {
			out.Plans = append(out.Plans, p)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if full {
		rows, err = tx.QueryContext(ctx, `SELECT id,conclusion_id,mapping,created_at FROM assurance_conclusion_history WHERE task_id=$1 ORDER BY id`, taskID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item AssuranceMappingHistory
			var raw []byte
			if err = rows.Scan(&item.ID, &item.ConclusionID, &raw, &item.CreatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			var record assuranceMappingRecord
			if err = json.Unmarshal(raw, &record); err != nil {
				rows.Close()
				return nil, err
			}
			item.Mapping = record.AssuranceConclusionInput
			item.Provenance = record.Provenance
			item.RetestID = record.RetestID
			item.RetestVerdict = record.RetestVerdict
			out.MappingHistory = append(out.MappingHistory, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		rows, err = tx.QueryContext(ctx, `SELECT conclusion_id,review FROM assurance_reviews WHERE task_id=$1 ORDER BY id`, taskID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item AssuranceReviewHistory
			var raw []byte
			if err = rows.Scan(&item.ConclusionID, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			if err = json.Unmarshal(raw, &item.Review); err != nil {
				rows.Close()
				return nil, err
			}
			out.ReviewHistory = append(out.ReviewHistory, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT `+retestCols+` FROM finding_retests WHERE assurance_task_id=$1 ORDER BY id DESC`, taskID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		retest, err := scanRetest(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out.Retests = append(out.Retests, retest)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, c := range out.Conclusions {
		out.Summary.Total++
		switch c.Assurance {
		case "candidate":
			out.Summary.Candidate++
		case "runtime_observed":
			out.Summary.RuntimeObserved++
		case "operator_verified":
			out.Summary.OperatorVerified++
		}
		if c.Kind == "negative_coverage" {
			out.Summary.NegativeChecks++
		}
		if c.MappingComplete {
			out.Summary.MappingComplete++
		}
		switch c.Freshness {
		case "supported_for_snapshot":
			out.Summary.Supported++
		case "stale":
			out.Summary.Stale++
		default:
			out.Summary.Unknown++
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func assuranceFindConclusion(conclusions []AssuranceConclusion, id string) (*AssuranceConclusion, error) {
	for i := range conclusions {
		if conclusions[i].ID == id {
			return &conclusions[i], nil
		}
	}
	return nil, ErrAssuranceNotFound
}
func assuranceSaveMapping(ctx context.Context, tx *sql.Tx, taskID int64, id string, in AssuranceConclusionInput) error {
	in.Dependencies = assuranceStrings(in.Dependencies)
	in.Prerequisites = assuranceStrings(in.Prerequisites)
	return assuranceSaveMappingRecord(ctx, tx, taskID, id, assuranceMappingRecord{AssuranceConclusionInput: in, OperatorMapping: &in, Provenance: "operator"})
}
func assuranceSaveMappingRecord(ctx context.Context, tx *sql.Tx, taskID int64, id string, in assuranceMappingRecord) error {
	in.Dependencies = assuranceStrings(in.Dependencies)
	in.Prerequisites = assuranceStrings(in.Prerequisites)
	raw := assuranceJSON(in)
	if _, err := tx.ExecContext(ctx, `INSERT INTO assurance_conclusions(task_id,conclusion_id,mapping) VALUES($1,$2,$3) ON CONFLICT(task_id,conclusion_id) DO UPDATE SET mapping=EXCLUDED.mapping,updated_at=now()`, taskID, id, raw); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO assurance_conclusion_history(task_id,conclusion_id,mapping) VALUES($1,$2,$3)`, taskID, id, raw)
	return err
}
func (d *DB) UpsertAssuranceConclusion(ctx context.Context, taskID int64, id string, in AssuranceConclusionInput) (*AssuranceConclusion, error) {
	if err := assuranceValidateMapping(in); err != nil {
		return nil, err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	current, err := AssuranceSnapshotTx(ctx, tx, taskID, in.ExpectedGeneration)
	if err != nil {
		return nil, err
	}
	if _, err = assuranceSnapshotAt(ctx, tx, taskID, in.BaselineGeneration); err != nil {
		return nil, err
	}
	all, err := assuranceLoadConclusions(ctx, tx, taskID, current)
	if err != nil {
		return nil, err
	}
	if _, err = assuranceFindConclusion(all, id); err != nil {
		return nil, err
	}
	if err = assuranceSaveMapping(ctx, tx, taskID, id, in); err != nil {
		return nil, err
	}
	current, err = assuranceSync(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	all, err = assuranceLoadConclusions(ctx, tx, taskID, current)
	if err != nil {
		return nil, err
	}
	c, err := assuranceFindConclusion(all, id)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return c, nil
}
func assuranceValidateRefs(ctx context.Context, tx *sql.Tx, taskID int64, findingID *int64, refs []string) error {
	if err := assuranceStringList(refs, 100); err != nil {
		return err
	}
	for _, ref := range refs {
		if strings.HasPrefix(ref, "finding:") {
			id, hash, ok := assuranceDocumentRef(ref)
			if !ok || (findingID != nil && *findingID != id) {
				return assuranceInvalid("invalid finding document reference")
			}
			var document json.RawMessage
			err := tx.QueryRowContext(ctx, `SELECT jsonb_build_object('class',vulnclass,'summary',summary,'evidence',evidence,'assets',asset_ids,'evidence_version',evidence_version) FROM findings WHERE id=$1 AND task_id=$2 AND length(trim(evidence))>0`, id, taskID).Scan(&document)
			if errors.Is(err, sql.ErrNoRows) {
				return assuranceInvalid("finding document belongs to another task or is unavailable")
			}
			if err != nil {
				return err
			}
			if assuranceHash(document) != hash {
				return ErrAssuranceConflict
			}
			continue
		}
		var ok bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM finding_traffic_bindings b JOIN findings f ON f.id=b.finding_id WHERE f.task_id=$1 AND ($2::bigint IS NULL OR f.id=$2) AND b.snapshot_id=$3)`, taskID, findingID, ref).Scan(&ok)
		if err != nil {
			return err
		}
		if !ok {
			return assuranceInvalid("evidence reference is not bound to the selected finding/task")
		}
	}
	return nil
}
func (d *DB) AddCoverageCheck(ctx context.Context, taskID int64, in AssuranceCoverageInput) (*AssuranceConclusion, error) {
	if err := assuranceValidateMapping(in.AssuranceConclusionInput); err != nil {
		return nil, err
	}
	for _, s := range []string{in.Surface, in.Identity, in.Workflow, in.RiskCategory} {
		if strings.TrimSpace(s) == "" || len(s) > 512 {
			return nil, assuranceInvalid("surface, identity, workflow and risk_category are required (512 bytes each)")
		}
	}
	if strings.TrimSpace(in.Rationale) == "" || len(in.Rationale) > 16000 {
		return nil, assuranceInvalid("rationale is required (at most 16KB)")
	}
	if in.Outcome != "observed_not_vulnerable" && in.Outcome != "inconclusive" && in.Outcome != "blocked" {
		return nil, assuranceInvalid("unsupported coverage outcome")
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	current, err := AssuranceSnapshotTx(ctx, tx, taskID, in.ExpectedGeneration)
	if err != nil {
		return nil, err
	}
	if _, err = assuranceSnapshotAt(ctx, tx, taskID, in.BaselineGeneration); err != nil {
		return nil, err
	}
	if err = assuranceValidateRefs(ctx, tx, taskID, nil, in.EvidenceRefs); err != nil {
		return nil, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM findings WHERE task_id=$1)+(SELECT count(*) FROM assurance_coverage_checks WHERE task_id=$1)`, taskID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= assuranceMaxRecords {
		return nil, assuranceInvalid("workspace exceeds 2000 conclusions")
	}
	in.EvidenceRefs = assuranceStrings(in.EvidenceRefs)
	record := assuranceCoverageRecord{AssuranceCoverageInput: in, EvidenceDependencies: []string{}}
	for _, ref := range in.EvidenceRefs {
		if id, _, ok := assuranceDocumentRef(ref); ok {
			record.EvidenceDependencies = append(record.EvidenceDependencies, assuranceFindingNode(id))
			continue
		}
		rows, err := tx.QueryContext(ctx, `SELECT f.id FROM findings f JOIN finding_traffic_bindings b ON b.finding_id=f.id WHERE f.task_id=$1 AND b.snapshot_id=$2 ORDER BY f.id`, taskID, ref)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var findingID int64
			if err = rows.Scan(&findingID); err != nil {
				rows.Close()
				return nil, err
			}
			record.EvidenceDependencies = append(record.EvidenceDependencies, assuranceFindingNode(findingID))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	record.EvidenceDependencies = assuranceStrings(record.EvidenceDependencies)
	var id int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO assurance_coverage_checks(task_id,check_data) VALUES($1,$2) RETURNING id`, taskID, assuranceJSON(record)).Scan(&id); err != nil {
		return nil, err
	}
	cid := fmt.Sprintf("check:%d", id)
	if err = assuranceSaveMapping(ctx, tx, taskID, cid, in.AssuranceConclusionInput); err != nil {
		return nil, err
	}
	current, err = assuranceSync(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	all, err := assuranceLoadConclusions(ctx, tx, taskID, current)
	if err != nil {
		return nil, err
	}
	c, err := assuranceFindConclusion(all, cid)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return c, nil
}

func assuranceDocumentRef(ref string) (int64, string, bool) {
	parts := strings.Split(ref, ":")
	if len(parts) != 4 || parts[0] != "finding" || parts[2] != "evidence" || len(parts[3]) != 64 {
		return 0, "", false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	return id, parts[3], err == nil && id > 0
}
func (d *DB) RecordAssuranceReview(ctx context.Context, taskID int64, id string, in AssuranceReviewInput) (*AssuranceReview, error) {
	if in.Provenance != "operator" || (in.State != "operator_verified" && in.State != "rejected") {
		return nil, assuranceInvalid("reviews require operator provenance and state operator_verified or rejected")
	}
	if strings.TrimSpace(in.Reviewer) == "" || len(in.Reviewer) > 256 || strings.TrimSpace(in.Rationale) == "" || len(in.Rationale) > 16000 {
		return nil, assuranceInvalid("reviewer and rationale are required")
	}
	if in.State == "operator_verified" && len(in.EvidenceRefs) == 0 {
		return nil, assuranceInvalid("operator verification requires bound evidence references")
	}
	if in.ExpectedEvidenceVersion < 0 || len(in.ExpectedEvidenceHash) != 64 {
		return nil, assuranceInvalid("expected evidence version and hash are required")
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	current, err := AssuranceSnapshotTx(ctx, tx, taskID, in.ExpectedGeneration)
	if err != nil {
		return nil, err
	}
	all, err := assuranceLoadConclusions(ctx, tx, taskID, current)
	if err != nil {
		return nil, err
	}
	c, err := assuranceFindConclusion(all, id)
	if err != nil {
		return nil, err
	}
	if c.FindingID == nil {
		return nil, assuranceInvalid("reviews apply to findings; coverage preserves its declared outcome")
	}
	if c.EvidenceVersion != in.ExpectedEvidenceVersion || c.EvidenceHash != in.ExpectedEvidenceHash {
		return nil, ErrAssuranceConflict
	}
	if err = assuranceValidateRefs(ctx, tx, taskID, c.FindingID, in.EvidenceRefs); err != nil {
		return nil, err
	}
	dependencies := append(slices.Clone(c.Dependencies), assuranceConstraintsNode, assuranceGraphNode, assuranceFindingNode(*c.FindingID))
	review := AssuranceReview{Generation: current.Generation, EvidenceVersion: c.EvidenceVersion, EvidenceHash: c.EvidenceHash, State: in.State, Provenance: in.Provenance, Reviewer: in.Reviewer, Rationale: in.Rationale, EvidenceRefs: assuranceStrings(in.EvidenceRefs), Dependencies: assuranceStrings(dependencies), CreatedAt: time.Now().UTC()}
	if err = tx.QueryRowContext(ctx, `INSERT INTO assurance_reviews(task_id,conclusion_id,generation,review,created_at) VALUES($1,$2,$3,'{}',$4) RETURNING id`, taskID, id, current.Generation, review.CreatedAt).Scan(&review.ID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE assurance_reviews SET review=$2 WHERE id=$1`, review.ID, assuranceJSON(review)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &review, nil
}

func (d *DB) CreateAssurancePlan(ctx context.Context, taskID int64, in AssurancePlanInput) (*AssurancePlan, error) {
	if in.BudgetUnits < 0 || in.BudgetUnits > 1000000 {
		return nil, assuranceInvalid("budget_units must be 0..1000000")
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	current, err := AssuranceSnapshotTx(ctx, tx, taskID, in.ExpectedGeneration)
	if err != nil {
		return nil, err
	}
	all, err := assuranceLoadConclusions(ctx, tx, taskID, current)
	if err != nil {
		return nil, err
	}
	result := retestplan.Plan{SchemaVersion: retestplan.SchemaVersion, Project: fmt.Sprintf("task:%d", taskID), Environment: current.Environment, TargetRevision: current.Revision, Generation: current.Generation, BudgetUnits: in.BudgetUnits, Items: []retestplan.Item{}}
	document := AssurancePlanDocument{SchemaVersion: 1, Current: retestplan.Snapshot{Project: result.Project, Environment: current.Environment, Revision: current.Revision, Generation: current.Generation, Versions: current.Versions}, Nodes: current.Nodes, Baselines: map[string]retestplan.Snapshot{}, Conclusions: []AssurancePlanConclusion{}, AvailablePrerequisites: current.AvailablePrerequisites, BudgetUnits: in.BudgetUnits}
	inputBytes := len(assuranceJSON(document))
	for _, c := range all {
		item, input, e := assuranceEvaluate(ctx, tx, taskID, c, *current)
		if e != nil {
			return nil, e
		}
		if item.Decision == "deferred_budget" {
			item.Decision = "pending_selection"
		}
		if item.Freshness != "supported_for_snapshot" {
			result.RevalidationRequired = true
		}
		for _, r := range item.Reasons {
			if r.Kind != "dependency_changed" {
				result.BroaderReviewRequired = true
			}
		}
		result.Items = append(result.Items, item)
		key := strconv.FormatInt(input.Baseline.Generation, 10)
		if _, exists := document.Baselines[key]; !exists {
			inputBytes += len(assuranceJSON(input.Baseline))
		}
		entry := AssurancePlanConclusion{Conclusion: input.Conclusions[0], BaselineGeneration: input.Baseline.Generation, Outcome: c.Outcome}
		inputBytes += len(assuranceJSON(entry))
		if inputBytes > 4<<20 {
			return nil, assuranceInvalid("saved plan inputs exceed 4 MiB; narrow the workspace or baseline history")
		}
		document.Baselines[key] = input.Baseline
		document.Conclusions = append(document.Conclusions, entry)
	}
	sort.Slice(result.Items, func(i, j int) bool {
		if result.Items[i].Impact != result.Items[j].Impact {
			return result.Items[i].Impact > result.Items[j].Impact
		}
		return result.Items[i].ID < result.Items[j].ID
	})
	for i := range result.Items {
		item := &result.Items[i]
		if item.Decision != "pending_selection" {
			continue
		}
		if item.EffortUnits > in.BudgetUnits-result.AllocatedUnits {
			item.Decision = "deferred_budget"
		} else {
			item.Decision = "selected"
			result.AllocatedUnits += item.EffortUnits
		}
	}
	rawInput := assuranceJSON(document)
	if len(rawInput) > 4<<20 {
		return nil, assuranceInvalid("saved plan inputs exceed 4 MiB; narrow the workspace or baseline history")
	}
	plan := &AssurancePlan{TaskID: taskID, Generation: current.Generation, Fingerprint: current.Fingerprint, Input: rawInput, Result: result, CreatedAt: time.Now().UTC()}
	if err = tx.QueryRowContext(ctx, `INSERT INTO assurance_plans(task_id,generation,fingerprint,input,result,created_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, taskID, plan.Generation, plan.Fingerprint, []byte(plan.Input), assuranceJSON(plan.Result), plan.CreatedAt).Scan(&plan.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return plan, nil
}

func AssurancePlanTx(ctx context.Context, tx *sql.Tx, taskID, planID int64) (*AssurancePlan, error) {
	p := &AssurancePlan{}
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT id,task_id,generation,fingerprint,input,result,created_at FROM assurance_plans WHERE task_id=$1 AND id=$2`, taskID, planID).Scan(&p.ID, &p.TaskID, &p.Generation, &p.Fingerprint, &p.Input, &raw, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAssuranceNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &p.Result); err != nil {
		return nil, err
	}
	return p, nil
}
func (d *DB) GetAssurancePlan(ctx context.Context, taskID, planID int64) (*AssurancePlan, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	p, err := AssurancePlanTx(ctx, tx, taskID, planID)
	if err != nil {
		return nil, err
	}
	return p, tx.Commit()
}

// AssuranceFindingID parses the stable conclusion identity used by saved plans.
func AssuranceFindingID(id string) (int64, error) {
	if !strings.HasPrefix(id, "finding:") {
		return 0, assuranceInvalid("only findings can use the existing retest runner")
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(id, "finding:"), 10, 64)
	if err != nil || n <= 0 {
		return 0, assuranceInvalid("invalid finding identity")
	}
	return n, nil
}

// assuranceDependenciesMatch evaluates the dependencies captured by the reviewer,
// rather than allowing a later mapping edit to erase the scope of that review.
func assuranceDependenciesMatch(deps []string, baseline, current AssuranceSnapshot) bool {
	if len(deps) == 0 {
		return false
	}
	nodes := map[string]retestplan.Node{}
	for _, n := range current.Nodes {
		nodes[n.ID] = n
	}
	seen := map[string]bool{}
	stack := slices.Clone(deps)
	steps := 0
	for len(stack) > 0 {
		steps++
		if steps > 100000 {
			return false
		}
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		n, ok := nodes[id]
		if !ok || baseline.Versions[id] == "" || baseline.Versions[id] != current.Versions[id] {
			return false
		}
		stack = append(stack, n.DependsOn...)
	}
	return true
}

// AcceptAssuranceRetestTx records a completed runtime report, never an independent
// verification. The caller finishes the actual bound retest first in this same
// task→finding transaction. A later mapping edit or target change remains visible.
func AcceptAssuranceRetestTx(ctx context.Context, tx *sql.Tx, taskID int64, cid string, generation, retestID int64, verdict string) error {
	if verdict != "reproduced" && verdict != "fixed" {
		return assuranceInvalid("only reproduced or fixed completed retests can advance a baseline")
	}
	current, err := AssuranceSnapshotTx(ctx, tx, taskID, generation)
	if err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM finding_retests WHERE id=$1 AND assurance_task_id=$2 AND assurance_conclusion_id=$3 AND assurance_generation=$4 AND status='completed' AND verdict=$5 AND NOT assurance_stale)`, retestID, taskID, cid, generation, verdict).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrAssuranceConflict
	}
	all, err := assuranceLoadConclusions(ctx, tx, taskID, current)
	if err != nil {
		return err
	}
	c, err := assuranceFindConclusion(all, cid)
	if err != nil {
		return err
	}
	if c.FindingID == nil {
		return assuranceInvalid("runtime finding retest cannot update negative coverage")
	}
	in := AssuranceConclusionInput{ExpectedGeneration: generation, BaselineGeneration: generation, Dependencies: c.Dependencies, MappingComplete: c.MappingComplete, Prerequisites: c.Prerequisites, Impact: c.Impact, EffortUnits: c.EffortUnits}
	var raw []byte
	var previous assuranceMappingRecord
	err = tx.QueryRowContext(ctx, `SELECT mapping FROM assurance_conclusions WHERE task_id=$1 AND conclusion_id=$2`, taskID, cid).Scan(&raw)
	if err == nil {
		if err = json.Unmarshal(raw, &previous); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if previous.OperatorMapping == nil {
		previous.OperatorMapping = &previous.AssuranceConclusionInput
	}
	return assuranceSaveMappingRecord(ctx, tx, taskID, cid, assuranceMappingRecord{AssuranceConclusionInput: in, OperatorMapping: previous.OperatorMapping, Provenance: "retest_reported", RetestID: retestID, RetestVerdict: verdict})
}
