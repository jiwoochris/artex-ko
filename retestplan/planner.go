// Package retestplan plans checks from explicitly supplied dependency snapshots.
// It does not discover dependencies, execute tests, or establish authorization.
package retestplan

import (
	"fmt"
	"maps"
	"slices"
	"sort"
)

// SchemaVersion is the input and output contract version.
const SchemaVersion = 1

// Bound work across all conclusions, including references to visited nodes.
// Overly complex inputs fail as a whole; no partial assurance plan is returned.
const (
	maxTraversalSteps  = 100_000
	maxDependencyDepth = 128
)

// Snapshot identifies one observation of a project in a specific environment.
type Snapshot struct {
	Project     string            `json:"project"`
	Environment string            `json:"environment"`
	Revision    string            `json:"revision"`
	Generation  int64             `json:"generation"`
	Versions    map[string]string `json:"versions"`
}

type Node struct {
	ID        string   `json:"id"`
	DependsOn []string `json:"depends_on"`
}

type Conclusion struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	Dependencies    []string `json:"dependencies"`
	MappingComplete bool     `json:"mapping_complete"`
	Impact          int      `json:"impact"`
	EffortUnits     int      `json:"effort_units"`
	Prerequisites   []string `json:"prerequisites"`
}

type Input struct {
	SchemaVersion          int          `json:"schema_version"`
	Baseline               Snapshot     `json:"baseline"`
	Current                Snapshot     `json:"current"`
	Nodes                  []Node       `json:"nodes"`
	Conclusions            []Conclusion `json:"conclusions"`
	AvailablePrerequisites []string     `json:"available_prerequisites"`
	BudgetUnits            int          `json:"budget_units"`
}

type Reason struct {
	Kind string   `json:"kind"`
	Path []string `json:"path,omitempty"`
	Old  string   `json:"old,omitempty"`
	New  string   `json:"new,omitempty"`
}

type Item struct {
	ID                   string   `json:"id"`
	Kind                 string   `json:"kind"`
	Freshness            string   `json:"freshness"`
	Decision             string   `json:"decision"`
	Reasons              []Reason `json:"reasons"`
	MissingPrerequisites []string `json:"missing_prerequisites,omitempty"`
	Impact               int      `json:"impact"`
	EffortUnits          int      `json:"effort_units"`
}

type Plan struct {
	SchemaVersion         int    `json:"schema_version"`
	Project               string `json:"project"`
	Environment           string `json:"environment"`
	TargetRevision        string `json:"target_revision"`
	Generation            int64  `json:"generation"`
	BudgetUnits           int    `json:"budget_units"`
	AllocatedUnits        int    `json:"allocated_units"`
	RevalidationRequired  bool   `json:"revalidation_required"`
	BroaderReviewRequired bool   `json:"broader_review_required"`
	Items                 []Item `json:"items"`
}

func validSnapshot(s Snapshot) bool {
	return s.Project != "" && s.Environment != "" && s.Revision != "" && s.Generation >= 0
}

// ResultStillCurrent is a conservative publish-time guard. A storage adapter
// must compare-and-publish transactionally; calling this before an independent
// database write would still leave a race. It is not evidence of a valid test.
func ResultStillCurrent(tested, current Snapshot) bool {
	return validSnapshot(tested) && validSnapshot(current) &&
		tested.Project == current.Project && tested.Environment == current.Environment &&
		tested.Revision == current.Revision && tested.Generation == current.Generation &&
		maps.Equal(tested.Versions, current.Versions)
}

// Build produces a deterministic, read-only plan. MappingComplete means the
// caller has established that the supplied dependency mapping applies to both
// snapshots; the planner cannot infer or verify that claim automatically.
func Build(in Input) (Plan, error) {
	if in.SchemaVersion != SchemaVersion {
		return Plan{}, fmt.Errorf("schema_version must be %d", SchemaVersion)
	}
	if !validSnapshot(in.Baseline) || !validSnapshot(in.Current) {
		return Plan{}, fmt.Errorf("snapshot identity, revision and generation are required")
	}
	if in.Baseline.Project != in.Current.Project || in.Baseline.Environment != in.Current.Environment {
		return Plan{}, fmt.Errorf("snapshots must belong to the same project and environment")
	}
	if in.Current.Generation < in.Baseline.Generation {
		return Plan{}, fmt.Errorf("current generation precedes baseline")
	}
	if in.Current.Generation == in.Baseline.Generation && !ResultStillCurrent(in.Baseline, in.Current) {
		return Plan{}, fmt.Errorf("different snapshots cannot share a generation")
	}
	if in.BudgetUnits < 0 {
		return Plan{}, fmt.Errorf("budget must be nonnegative")
	}
	nodes := make(map[string]Node)
	for _, n := range in.Nodes {
		if n.ID == "" {
			return Plan{}, fmt.Errorf("dependency identity is required")
		}
		if _, exists := nodes[n.ID]; exists {
			return Plan{}, fmt.Errorf("duplicate dependency %q", n.ID)
		}
		n.DependsOn = sortedUnique(n.DependsOn)
		nodes[n.ID] = n
	}
	available := make(map[string]bool)
	for _, p := range in.AvailablePrerequisites {
		available[p] = true
	}
	plan := Plan{SchemaVersion: SchemaVersion, Project: in.Current.Project, Environment: in.Current.Environment,
		TargetRevision: in.Current.Revision, Generation: in.Current.Generation,
		BudgetUnits: in.BudgetUnits, Items: []Item{}}
	seenIDs := make(map[string]bool)
	steps := 0
	for _, c := range in.Conclusions {
		if c.ID == "" || seenIDs[c.ID] {
			return Plan{}, fmt.Errorf("conclusion identities must be nonempty and unique")
		}
		seenIDs[c.ID] = true
		if c.Kind != "finding" && c.Kind != "negative_coverage" {
			return Plan{}, fmt.Errorf("unsupported conclusion kind %q", c.Kind)
		}
		if c.Impact < 1 || c.Impact > 5 || c.EffortUnits <= 0 {
			return Plan{}, fmt.Errorf("conclusion %q needs impact 1..5 and positive effort", c.ID)
		}
		item := Item{ID: c.ID, Kind: c.Kind, Freshness: "supported_for_snapshot", Decision: "not_selected",
			Impact: c.Impact, EffortUnits: c.EffortUnits, Reasons: []Reason{}}
		unknown, changed := !c.MappingComplete || len(c.Dependencies) == 0, false
		if unknown {
			item.Reasons = append(item.Reasons, Reason{Kind: "incomplete_mapping"})
		}
		visited := make(map[string]bool)
		var visit func(string, []string) error
		visit = func(id string, parent []string) error {
			steps++
			if steps > maxTraversalSteps {
				return fmt.Errorf("dependency traversal exceeds %d steps", maxTraversalSteps)
			}
			if visited[id] {
				return nil
			}
			if len(parent) > maxDependencyDepth {
				return fmt.Errorf("dependency path exceeds depth %d", maxDependencyDepth)
			}
			visited[id] = true
			path := append(slices.Clone(parent), id)
			n, exists := nodes[id]
			if !exists {
				unknown = true
				item.Reasons = append(item.Reasons, Reason{Kind: "missing_dependency", Path: path})
				return nil
			}
			old, oldOK := in.Baseline.Versions[id]
			now, nowOK := in.Current.Versions[id]
			if !oldOK || !nowOK || old == "" || now == "" {
				unknown = true
				item.Reasons = append(item.Reasons, Reason{Kind: "unknown_version", Path: path})
			} else if old != now {
				changed = true
				item.Reasons = append(item.Reasons, Reason{Kind: "dependency_changed", Path: path, Old: old, New: now})
			}
			for _, dep := range n.DependsOn {
				if err := visit(dep, path); err != nil {
					return err
				}
			}
			return nil
		}
		for _, dep := range sortedUnique(c.Dependencies) {
			if err := visit(dep, []string{c.ID}); err != nil {
				return Plan{}, err
			}
		}
		if unknown {
			item.Freshness = "unknown"
			plan.BroaderReviewRequired = true
		}
		if changed {
			item.Freshness = "stale"
		}
		if unknown || changed {
			plan.RevalidationRequired = true
			item.Decision = "pending_selection"
			for _, prerequisite := range sortedUnique(c.Prerequisites) {
				if !available[prerequisite] {
					item.MissingPrerequisites = append(item.MissingPrerequisites, prerequisite)
				}
			}
			if len(item.MissingPrerequisites) > 0 {
				item.Decision = "blocked"
			}
		}
		plan.Items = append(plan.Items, item)
	}
	// Priority is deliberately simple and inspectable, not claimed to be optimal.
	sort.Slice(plan.Items, func(i, j int) bool {
		if plan.Items[i].Impact != plan.Items[j].Impact {
			return plan.Items[i].Impact > plan.Items[j].Impact
		}
		return plan.Items[i].ID < plan.Items[j].ID
	})
	for i := range plan.Items {
		item := &plan.Items[i]
		if item.Decision != "pending_selection" {
			continue
		}
		if item.EffortUnits > in.BudgetUnits-plan.AllocatedUnits {
			item.Decision = "deferred_budget"
			continue
		}
		item.Decision = "selected"
		plan.AllocatedUnits += item.EffortUnits
	}
	return plan, nil
}

func sortedUnique(values []string) []string {
	result := slices.Clone(values)
	sort.Strings(result)
	return slices.Compact(result)
}
