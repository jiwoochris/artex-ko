package db

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/Autumn-27/artex/retestplan"
)

var (
	ErrAssuranceInvalid  = errors.New("invalid assurance input")
	ErrAssuranceNotFound = errors.New("assurance record not found")
	ErrAssuranceConflict = errors.New("assurance changed; refresh and retry")
)

// AssuranceSnapshot is immutable. Runtime evidence/constraint changes create a
// new generation when the workspace is read, without changing a declared revision.
type AssuranceSnapshot struct {
	Generation             int64             `json:"generation"`
	Environment            string            `json:"environment"`
	Revision               string            `json:"revision"`
	Nodes                  []retestplan.Node `json:"nodes"`
	Versions               map[string]string `json:"versions"`
	AvailablePrerequisites []string          `json:"available_prerequisites"`
	Fingerprint            string            `json:"fingerprint"`
	Source                 string            `json:"source"`
	CreatedAt              time.Time         `json:"created_at"`
}

type AssuranceSnapshotInput struct {
	ExpectedGeneration     int64             `json:"expected_generation"`
	Environment            string            `json:"environment"`
	Revision               string            `json:"revision"`
	Nodes                  []retestplan.Node `json:"nodes"`
	Versions               map[string]string `json:"versions"`
	AvailablePrerequisites []string          `json:"available_prerequisites"`
}

type AssuranceConclusionInput struct {
	ExpectedGeneration int64    `json:"expected_generation"`
	BaselineGeneration int64    `json:"baseline_generation"`
	Dependencies       []string `json:"dependencies"`
	MappingComplete    bool     `json:"mapping_complete"`
	Prerequisites      []string `json:"prerequisites"`
	Impact             int      `json:"impact"`
	EffortUnits        int      `json:"effort_units"`
}

type AssuranceCoverageInput struct {
	AssuranceConclusionInput
	Surface      string   `json:"surface"`
	Identity     string   `json:"identity"`
	Workflow     string   `json:"workflow"`
	RiskCategory string   `json:"risk_category"`
	Outcome      string   `json:"outcome"`
	Rationale    string   `json:"rationale"`
	EvidenceRefs []string `json:"evidence_refs"`
}

type AssuranceReviewInput struct {
	ExpectedGeneration      int64    `json:"expected_generation"`
	ExpectedEvidenceVersion int64    `json:"expected_evidence_version"`
	ExpectedEvidenceHash    string   `json:"expected_evidence_hash"`
	State                   string   `json:"state"`
	Provenance              string   `json:"provenance"`
	Reviewer                string   `json:"reviewer"`
	Rationale               string   `json:"rationale"`
	EvidenceRefs            []string `json:"evidence_refs"`
}

type AssuranceReview struct {
	ID              int64     `json:"id,string"`
	Generation      int64     `json:"generation"`
	EvidenceVersion int64     `json:"evidence_version"`
	EvidenceHash    string    `json:"evidence_hash"`
	State           string    `json:"state"`
	Provenance      string    `json:"provenance"`
	Reviewer        string    `json:"reviewer"`
	Rationale       string    `json:"rationale"`
	EvidenceRefs    []string  `json:"evidence_refs"`
	Dependencies    []string  `json:"dependencies"`
	CreatedAt       time.Time `json:"created_at"`
}

type AssuranceConclusion struct {
	ID                   string              `json:"id"`
	Kind                 string              `json:"kind"`
	FindingID            *int64              `json:"finding_id,omitempty,string"`
	Title                string              `json:"title"`
	Severity             string              `json:"severity,omitempty"`
	TriageStatus         string              `json:"triage_status,omitempty"`
	Assurance            string              `json:"assurance"`
	Freshness            string              `json:"freshness"`
	EvidenceVersion      int64               `json:"evidence_version"`
	EvidenceHash         string              `json:"evidence_hash"`
	EvidenceRefs         []string            `json:"evidence_refs"`
	EvidenceRefKinds     map[string]string   `json:"evidence_ref_kinds"`
	EvidenceDependencies []string            `json:"evidence_dependencies,omitempty"`
	BaselineGeneration   int64               `json:"baseline_generation"`
	Dependencies         []string            `json:"dependencies"`
	MappingComplete      bool                `json:"mapping_complete"`
	Prerequisites        []string            `json:"prerequisites"`
	Impact               int                 `json:"impact"`
	EffortUnits          int                 `json:"effort_units"`
	Surface              string              `json:"surface,omitempty"`
	Identity             string              `json:"identity,omitempty"`
	Workflow             string              `json:"workflow,omitempty"`
	RiskCategory         string              `json:"risk_category,omitempty"`
	Outcome              string              `json:"outcome,omitempty"`
	Rationale            string              `json:"rationale,omitempty"`
	Reviews              []AssuranceReview   `json:"reviews"`
	Reasons              []retestplan.Reason `json:"reasons"`
	LastRetestID         int64               `json:"last_retest_id,omitempty,string"`
	LastRetestVerdict    string              `json:"last_retest_verdict,omitempty"`
}

type AssurancePlanInput struct {
	ExpectedGeneration int64 `json:"expected_generation"`
	BudgetUnits        int   `json:"budget_units"`
}

type AssurancePlan struct {
	ID          int64           `json:"id,string"`
	TaskID      int64           `json:"task_id,string"`
	Generation  int64           `json:"generation"`
	Fingerprint string          `json:"fingerprint"`
	Input       json.RawMessage `json:"input,omitempty"`
	Result      retestplan.Plan `json:"result"`
	CreatedAt   time.Time       `json:"created_at"`
}

// AssurancePlanDocument preserves reproducible inputs without repeating the
// complete graph and current snapshot for every conclusion.
type AssurancePlanDocument struct {
	SchemaVersion          int                            `json:"schema_version"`
	Current                retestplan.Snapshot            `json:"current"`
	Nodes                  []retestplan.Node              `json:"nodes"`
	Baselines              map[string]retestplan.Snapshot `json:"baselines"`
	Conclusions            []AssurancePlanConclusion      `json:"conclusions"`
	AvailablePrerequisites []string                       `json:"available_prerequisites"`
	BudgetUnits            int                            `json:"budget_units"`
}

type AssurancePlanConclusion struct {
	Conclusion         retestplan.Conclusion `json:"conclusion"`
	BaselineGeneration int64                 `json:"baseline_generation"`
	Outcome            string                `json:"outcome,omitempty"`
}

type AssuranceSummary struct {
	Total            int `json:"total"`
	Candidate        int `json:"candidate"`
	RuntimeObserved  int `json:"runtime_observed"`
	OperatorVerified int `json:"operator_verified"`
	NegativeChecks   int `json:"negative_checks"`
	MappingComplete  int `json:"mapping_complete"`
	Supported        int `json:"supported"`
	Stale            int `json:"stale"`
	Unknown          int `json:"unknown"`
}

type TaskAssurance struct {
	TaskID           int64                     `json:"task_id,string"`
	TaskStatus       string                    `json:"task_status"`
	Current          *AssuranceSnapshot        `json:"current"`
	History          []AssuranceSnapshot       `json:"history"`
	Conclusions      []AssuranceConclusion     `json:"conclusions"`
	Plans            []AssurancePlan           `json:"plans"`
	Summary          AssuranceSummary          `json:"summary"`
	Retests          []*FindingRetest          `json:"retests"`
	HistoryTruncated bool                      `json:"history_truncated"`
	PlansTruncated   bool                      `json:"plans_truncated"`
	MappingHistory   []AssuranceMappingHistory `json:"mapping_history,omitempty"`
	ReviewHistory    []AssuranceReviewHistory  `json:"review_history,omitempty"`
}

type AssuranceReviewHistory struct {
	ConclusionID string          `json:"conclusion_id"`
	Review       AssuranceReview `json:"review"`
}

type AssuranceMappingHistory struct {
	ID            int64                    `json:"id,string"`
	ConclusionID  string                   `json:"conclusion_id"`
	Mapping       AssuranceConclusionInput `json:"mapping"`
	CreatedAt     time.Time                `json:"created_at"`
	Provenance    string                   `json:"provenance"`
	RetestID      int64                    `json:"retest_id,omitempty,string"`
	RetestVerdict string                   `json:"retest_verdict,omitempty"`
}
