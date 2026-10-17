package retestplan

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func fixture(t *testing.T) Input {
	t.Helper()
	raw, err := os.ReadFile("testdata/policy-change.json")
	if err != nil {
		t.Fatal(err)
	}
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func mustPlan(t *testing.T, in Input) Plan {
	t.Helper()
	plan, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func find(t *testing.T, p Plan, id string) Item {
	t.Helper()
	for _, item := range p.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("missing item %s", id)
	return Item{}
}

func TestSharedPolicyInvalidatesPositiveAndNegativeConclusions(t *testing.T) {
	p := mustPlan(t, fixture(t))
	positive := find(t, p, "order-access-finding")
	negative := find(t, p, "admin-route-negative")
	if positive.Freshness != "stale" || negative.Freshness != "stale" {
		t.Fatal("both kinds of conclusion must become stale")
	}
	if positive.Decision != "selected" || negative.Decision != "blocked" {
		t.Fatal("available credentials should select one check and block the other")
	}
	if !slices.Equal(negative.MissingPrerequisites, []string{"test-admin"}) {
		t.Fatalf("missing prerequisite not explained: %+v", negative)
	}
	path := []string{"order-access-finding", "orders-route", "permissions"}
	if len(positive.Reasons) != 1 || !slices.Equal(positive.Reasons[0].Path, path) {
		t.Fatalf("missing transitive explanation: %+v", positive.Reasons)
	}
	if p.AllocatedUnits != 3 || !p.RevalidationRequired {
		t.Fatal("selection must consume budget without implying revalidation completed")
	}
	if p.SchemaVersion != SchemaVersion {
		t.Fatal("output contract version missing")
	}
}

func TestIndependentMappedResultRetainsConditionalSupport(t *testing.T) {
	item := find(t, mustPlan(t, fixture(t)), "pricing-negative")
	if item.Freshness != "supported_for_snapshot" || item.Decision != "not_selected" || len(item.Reasons) != 0 {
		t.Fatalf("unrelated complete mapping should not be invalidated: %+v", item)
	}
}

func TestIncompleteMappingAndUnknownVersionsRemainVisible(t *testing.T) {
	in := fixture(t)
	delete(in.Current.Versions, "pricing")
	p := mustPlan(t, in)
	for _, id := range []string{"historical-unmapped-check", "pricing-negative"} {
		item := find(t, p, id)
		if item.Freshness != "unknown" || item.Decision != "deferred_budget" {
			t.Fatalf("uncertainty should remain visible after budget selection: %+v", item)
		}
	}
	if !p.BroaderReviewRequired || !p.RevalidationRequired {
		t.Fatal("unknown coverage must recommend broader review")
	}
}

func TestEvidenceReplacementInvalidatesAssuranceWithoutCodeChange(t *testing.T) {
	in := fixture(t)
	in.Current.Versions = maps.Clone(in.Baseline.Versions)
	in.Current.Revision = in.Baseline.Revision
	in.Current.Versions["order-evidence"] = "e2"
	p := mustPlan(t, in)
	item := find(t, p, "order-access-finding")
	if item.Freshness != "stale" || item.Reasons[0].Path[len(item.Reasons[0].Path)-1] != "order-evidence" {
		t.Fatalf("evidence-only changes must invalidate assurance: %+v", item)
	}
	if find(t, p, "admin-route-negative").Freshness != "supported_for_snapshot" {
		t.Fatal("the unchanged policy should retain conditional support")
	}
}

func TestCyclesAndRepeatedEdgesDoNotDuplicateReasons(t *testing.T) {
	in := fixture(t)
	in.Nodes[0].DependsOn = []string{"permissions", "permissions"}
	in.Nodes[1].DependsOn = []string{"orders-route"}
	in.Conclusions[0].Dependencies = []string{"orders-route", "orders-route", "order-evidence"}
	p := mustPlan(t, in)
	if len(find(t, p, "order-access-finding").Reasons) != 1 {
		t.Fatal("cycle or duplicate edge duplicated the explanation")
	}
}

func TestMissingGraphNodeIsUnknownEvenWhenVersionExists(t *testing.T) {
	in := fixture(t)
	in.Nodes = slices.Delete(in.Nodes, 2, 3) // remove pricing's graph record
	p := mustPlan(t, in)
	if find(t, p, "pricing-negative").Freshness != "unknown" {
		t.Fatal("a version without a dependency record is not sufficient")
	}
}

func TestBudgetPreservesAllUnresolvedItems(t *testing.T) {
	for budget := 0; budget <= 12; budget++ {
		in := fixture(t)
		in.BudgetUnits = budget
		in.AvailablePrerequisites = []string{"test-user", "test-admin"}
		p := mustPlan(t, in)
		if p.AllocatedUnits > budget || len(p.Items) != len(in.Conclusions) || !p.RevalidationRequired {
			t.Fatalf("budget %d discarded unresolved work or overspent: %+v", budget, p)
		}
		for _, item := range p.Items {
			if item.Freshness != "supported_for_snapshot" && item.Decision != "selected" && item.Decision != "deferred_budget" {
				t.Fatalf("unresolved item disappeared: %+v", item)
			}
		}
	}
}

func TestRejectsMismatchedScopeAndConflictingSnapshots(t *testing.T) {
	cases := map[string]func(*Input){
		"missing-schema":       func(i *Input) { i.SchemaVersion = 0 },
		"future-schema":        func(i *Input) { i.SchemaVersion++ },
		"missing-identity":     func(i *Input) { i.Current.Revision = "" },
		"invalid-kind":         func(i *Input) { i.Conclusions[0].Kind = "unknown" },
		"invalid-impact":       func(i *Input) { i.Conclusions[0].Impact = 6 },
		"project":              func(i *Input) { i.Current.Project = "other-project" },
		"environment":          func(i *Input) { i.Current.Environment = "production" },
		"old-event":            func(i *Input) { i.Current.Generation = 0 },
		"conflict":             func(i *Input) { i.Current.Generation = i.Baseline.Generation },
		"negative-budget":      func(i *Input) { i.BudgetUnits = -1 },
		"duplicate-conclusion": func(i *Input) { i.Conclusions = append(i.Conclusions, i.Conclusions[0]) },
		"duplicate-node":       func(i *Input) { i.Nodes = append(i.Nodes, i.Nodes[0]) },
		"zero-effort":          func(i *Input) { i.Conclusions[0].EffortUnits = 0 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			in := fixture(t)
			change(&in)
			if _, err := Build(in); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}

func TestChangedDependencyWithIncompleteMappingStillRequiresBroaderReview(t *testing.T) {
	in := fixture(t)
	in.Conclusions[0].MappingComplete = false
	p := mustPlan(t, in)
	item := find(t, p, "order-access-finding")
	if item.Freshness != "stale" || !p.BroaderReviewRequired || len(item.Reasons) != 2 {
		t.Fatalf("known change must not hide incomplete mapping: %+v", p)
	}
}

func TestIdenticalSnapshotsNeedNoRevalidationWhenFullyMapped(t *testing.T) {
	in := fixture(t)
	in.Current = in.Baseline
	in.Conclusions = []Conclusion{in.Conclusions[0], in.Conclusions[3]}
	p := mustPlan(t, in)
	if p.RevalidationRequired || p.BroaderReviewRequired || p.AllocatedUnits != 0 {
		t.Fatalf("unchanged complete mappings should preserve conditional support: %+v", p)
	}
}

func TestComplexInputFailsWithoutReturningPartialPlan(t *testing.T) {
	t.Run("depth", func(t *testing.T) {
		in := fixture(t)
		in.Nodes = nil
		for n := 0; n <= maxDependencyDepth; n++ {
			in.Nodes = append(in.Nodes, Node{ID: fmt.Sprint(n), DependsOn: []string{fmt.Sprint(n + 1)}})
		}
		in.Conclusions = []Conclusion{{ID: "deep", Kind: "finding", Dependencies: []string{"0"}, Impact: 1, EffortUnits: 1}}
		p, err := Build(in)
		if err == nil || !strings.Contains(err.Error(), "depth") || p.Items != nil {
			t.Fatalf("deep graph returned a partial plan: %+v, %v", p, err)
		}
	})
	t.Run("total-work", func(t *testing.T) {
		in := fixture(t)
		in.Conclusions = nil
		in.Nodes = nil
		dependencies := make([]string, 400)
		for n := range dependencies {
			dependencies[n] = fmt.Sprint(n)
			in.Nodes = append(in.Nodes, Node{ID: dependencies[n]})
		}
		for n := 0; n < 300; n++ {
			in.Conclusions = append(in.Conclusions, Conclusion{ID: fmt.Sprint(n), Kind: "finding", Dependencies: dependencies, Impact: 1, EffortUnits: 1})
		}
		p, err := Build(in)
		if err == nil || !strings.Contains(err.Error(), "steps") || p.Items != nil {
			t.Fatalf("excessive traversal returned a partial plan: %+v, %v", p, err)
		}
	})
}

func TestResultCannotBecomeCurrentAfterConcurrentChange(t *testing.T) {
	tested := fixture(t).Current
	current := tested
	if !ResultStillCurrent(tested, current) {
		t.Fatal("identical valid snapshots should match")
	}
	current.Generation++
	if ResultStillCurrent(tested, current) {
		t.Fatal("a newer generation must reject publication as current")
	}
	current = tested
	current.Versions = maps.Clone(tested.Versions)
	current.Versions["permissions"] = "p3"
	if ResultStillCurrent(tested, current) {
		t.Fatal("changed versions must reject publication even with a conflicting generation")
	}
	if ResultStillCurrent(Snapshot{}, Snapshot{}) {
		t.Fatal("empty snapshots must not match")
	}
}

func TestPlanningIsRepeatableAndDoesNotMutateInput(t *testing.T) {
	in := fixture(t)
	before, _ := json.Marshal(in)
	want := mustPlan(t, in)
	got := mustPlan(t, in)
	after, _ := json.Marshal(in)
	if !slices.Equal(before, after) || !reflect.DeepEqual(want, got) {
		t.Fatal("repeated planning changed input or result")
	}
	slices.Reverse(in.Nodes)
	slices.Reverse(in.Conclusions)
	if !reflect.DeepEqual(want, mustPlan(t, in)) {
		t.Fatal("input ordering changed plan")
	}
}
