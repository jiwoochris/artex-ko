# Change-aware retest planning

When a component, permission policy or evidence bundle changes, previous security
conclusions may need to be checked again. The offline planner follows explicitly
supplied dependency relationships and explains which findings and negative
coverage results are stale or uncertain. It then selects work within an effort
budget, keeping blocked and deferred checks visible.

This is a planning feature. It does not discover dependencies, read ARTEX's
database, call a model, launch retests or change finding/remediation status.

## Run a plan

From a source checkout:

```bash
go run ./cmd/artex -retest-plan retestplan/testdata/policy-change.json
```

Or build the backend binary (no frontend build needed for this mode):

```bash
go build -o artex ./cmd/artex
./artex -retest-plan retestplan/testdata/policy-change.json > plan.json
./artex -retest-plan - < retestplan/testdata/policy-change.json
```

Invoke the binary directly, without `start.sh`, which supervises a long-running
server. The flag exits before configuration loading, database access, log capture,
self-update bootstrap and backend/proxy startup. No model or API key is required.
Input is one JSON document of at most 4 MiB. Unknown fields and trailing documents
are rejected. Successful output is one JSON plan on stdout; errors go to stderr.

| Exit code | Meaning |
| --- | --- |
| `0` | A plan was generated, including any stale, unknown, blocked or deferred items. This is not a security pass. |
| `1` | Writing the plan failed. |
| `2` | The command, input file, JSON or planning data is invalid. No plan is emitted. |

The [synthetic input](../retestplan/testdata/policy-change.json) changes a shared
permission policy from `p1` to `p2`. Its four conclusions demonstrate the outcomes:

| Conclusion | Freshness | Decision | Explanation |
| --- | --- | --- | --- |
| `order-access-finding` | `stale` | `selected` | `orders-route` depends on the changed `permissions` policy; 3 units fit the budget. |
| `admin-route-negative` | `stale` | `blocked` | The policy changed, but prerequisite `test-admin` is unavailable. |
| `historical-unmapped-check` | `unknown` | `deferred_budget` | The dependency map is incomplete and the remaining budget cannot cover the check. |
| `pricing-negative` | `supported_for_snapshot` | `not_selected` | Its declared complete mapping contains only unchanged dependencies. |

## Input contract (schema version 1)

`schema_version` must be `1`. The remaining top-level fields are:

| Field | Contract |
| --- | --- |
| `baseline`, `current` | Snapshots with nonempty `project`, `environment` and `revision`, a nonnegative integer `generation`, and a `versions` map from dependency ID to version string. Both snapshots must have the same project/environment. |
| `nodes` | Dependency records with a unique, nonempty `id` and optional `depends_on` IDs. An empty dependency list declares a leaf. Cycles and repeated edges are allowed. |
| `conclusions` | Previously established findings or negative coverage results, described below. IDs must be unique and nonempty. |
| `available_prerequisites` | Available role, fixture or test-context identifiers. Do not include credential values. Availability is a caller assertion. |
| `budget_units` | Nonnegative integer effort budget; the default is zero. Units are caller-defined estimates, not money or model tokens. |

Each conclusion supplies:

| Field | Contract |
| --- | --- |
| `id` | Stable identifier for this historical conclusion. |
| `kind` | `finding` or `negative_coverage`. Negative coverage means no issue was observed under the original test conditions; it is not universal absence of vulnerabilities. |
| `dependencies` | Direct dependency IDs; the planner also follows their transitive dependencies. |
| `mapping_complete` | Explicit assertion that the supplied dependency graph is complete for this conclusion and applies to **both** snapshots. Omitted/false or empty dependencies mean unknown coverage. |
| `impact` | Integer priority from 1 (lowest) to 5 (highest). This is a scheduling input, not a new vulnerability severity verdict. |
| `effort_units` | Positive integer estimated cost of checking this conclusion. |
| `prerequisites` | Identifiers required to perform the check. Missing identifiers block selection. |

Snapshots use caller-maintained generations. Current generation cannot precede
baseline; equal generations require identical revision and version maps. Advance
the generation when any snapshot data changes, including evidence-only changes
with the same code revision. IDs and version strings are compared exactly.

Versions are opaque, non-secret identifiers (for example, content hashes or policy
revision IDs). A version must cover the component's relevant behavior **and its
dependency relationships**. When relationships change, use a graph that covers
both snapshots (for example, their union), advance affected component versions,
and mark the mapping incomplete if coverage cannot be established. A code revision
change alone does not invalidate a conclusion whose complete dependency map still
matches. An incomplete or inaccurate map can therefore miss changes: the planner
cannot establish the truth of `mapping_complete` or the original conclusion.

## Output and selection

The output includes `schema_version`, project/environment, target revision and
generation, budget/allocation totals and an item for every input conclusion.

- `freshness` is `stale` if any reachable known version changed, `unknown` if
  mapping information is missing, or `supported_for_snapshot` otherwise. Support
  is conditional on the supplied history and mapping; it is not a new verification.
- `reasons` retain an explanation path starting at the conclusion. Reason kinds
  are `dependency_changed` (with `old` and `new` versions), `incomplete_mapping`,
  `missing_dependency` and `unknown_version` (missing or empty version in either
  snapshot). Each dependency contributes at most one reason per conclusion; one
  deterministic path is returned, not every possible path.
- If both a known change and missing mapping information exist, freshness is
  `stale` and the uncertainty reasons remain visible.
- `revalidation_required` is true whenever any conclusion is stale or unknown,
  even if all needed work is selected. `broader_review_required` is true whenever
  any mapping/version information is unknown, including for stale conclusions.
- `decision` is `selected`, `blocked`, `deferred_budget` or `not_selected`.
  Unchanged, completely mapped conclusions are not selected. Missing prerequisites
  block affected checks before any budget is allocated.

Selection sorts all items by descending impact, then ascending ID. Each eligible
item is selected if its effort fits the remaining budget; oversized items are
deferred and smaller later items can still be selected. This deterministic greedy
rule does not claim an optimal schedule, cost savings or improved detection recall.
Blocked/deferred items retain their freshness and reasons. An empty conclusion
list yields an empty plan, with no statement about unrepresented coverage.

Planning stops with an error if a new dependency path exceeds 128 nodes or total
dependency traversal exceeds 100,000 steps across all conclusions. These bounds
limit computation and explanation size. No partial plan is returned on error.

## Integration boundary

The pure Go API is in [`retestplan`](../retestplan/planner.go); the command adapter
is in [`cmd/artex/retest_plan.go`](../cmd/artex/retest_plan.go). There are no new
external dependencies or database migrations.

`ResultStillCurrent(tested, current)` compares project/environment, revision,
generation and all supplied versions. A future executor must compare and publish
within one storage transaction; calling this helper before a separate write leaves
a race. The helper does not verify test outcomes or provide persistent event
ordering. A result for an older snapshot must remain historical.

Database export, dependency discovery, expiry, grouping checks, execution and a UI
are follow-up work. A future executor must validate current scope and prerequisites
before performing any action. A generated plan alone authorizes no execution.

## Verification

```bash
go test ./retestplan ./cmd/artex -count=1
```

Tests cover shared-policy and evidence changes, negative coverage, missing maps,
cycles, deterministic ordering, budgets, prerequisites, snapshot conflicts,
concurrent-change detection, resource limits, strict input parsing and the actual
CLI startup path. Command tests use synthetic data with invalid server/config
settings to verify that planning succeeds without backend startup.

This is the first code contribution toward [issue #47](https://github.com/jiwoochris/artex-global/issues/47).
