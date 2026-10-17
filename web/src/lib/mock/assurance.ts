// Stateful UI demo only. No model calls or target requests are made here.
import type {
  AssuranceConclusion,
  AssuranceMapping,
  AssurancePlan,
  AssurancePlanItem,
  AssuranceReason,
  AssuranceSnapshot,
  TaskAssurance,
} from "@/lib/assurance";
import type { Finding, FindingRetest } from "@/lib/types";

const workspaces = new Map<string, TaskAssurance>();
const clone = <T>(value: T): T => structuredClone(value);
const now = () => new Date().toISOString();

export function resetMockAssurance() {
  workspaces.clear();
}

export function syncMockAssuranceRetests(retests: FindingRetest[]) {
  for (const workspace of workspaces.values()) {
    for (const row of workspace.retests) {
      const source = retests.find((retest) => retest.id === row.id);
      if (source) Object.assign(row, clone(source));
      if (row.status === "completed" && row.assurance_generation !== workspace.current?.generation)
        row.assurance_stale = true;
    }
  }
}

function seed(taskId: string, findings: Finding[]): TaskAssurance {
  const nodes = [
    { id: "orders-route", depends_on: ["permissions"] },
    { id: "permissions", depends_on: [] },
  ];
  const baseline: AssuranceSnapshot = {
    generation: 1,
    environment: "demo",
    revision: "build-a",
    nodes,
    versions: { "orders-route": "r1", permissions: "p1" },
    available_prerequisites: ["test-user"],
    fingerprint: "demo-1",
    source: "declared",
    created_at: now(),
  };
  const current = {
    ...clone(baseline),
    generation: 2,
    revision: "build-b",
    versions: { "orders-route": "r1", permissions: "p2" },
    fingerprint: "demo-2",
  };
  const conclusions: AssuranceConclusion[] = findings
    .filter((item) => item.task_id === taskId)
    .map((finding, index) => ({
      id: `finding:${finding.finding_id ?? finding.id}`,
      kind: "finding",
      finding_id: finding.finding_id ?? finding.id,
      title: finding.name || finding.vulnclass,
      severity: finding.severity,
      triage_status: finding.status,
      assurance: index === 0 ? "runtime_observed" : "candidate",
      freshness: "unknown",
      evidence_version: 1,
      evidence_hash: `demo-evidence-${finding.id}`,
      evidence_refs:
        index === 0 ? [`demo-snapshot-${finding.id}`] : [`finding:${finding.id}:evidence:demo-evidence-${finding.id}`],
      evidence_ref_kinds:
        index === 0
          ? { [`demo-snapshot-${finding.id}`]: "traffic_snapshot" }
          : { [`finding:${finding.id}:evidence:demo-evidence-${finding.id}`]: "finding_document" },
      baseline_generation: 1,
      dependencies: index < 2 ? ["orders-route"] : [],
      mapping_complete: index < 2,
      prerequisites: index === 1 ? ["test-admin"] : [],
      impact: Math.max(1, 5 - index),
      effort_units: index === 0 ? 3 : 2,
      reviews: [],
      reasons: [],
    }));
  conclusions.push({
    id: "check:1",
    kind: "negative_coverage",
    title: "Demo: admin order access",
    assurance: "operator_reported",
    freshness: "stale",
    evidence_version: 1,
    evidence_hash: "demo-check-1",
    evidence_refs: [],
    baseline_generation: 1,
    dependencies: ["permissions"],
    mapping_complete: true,
    prerequisites: ["test-admin"],
    impact: 4,
    effort_units: 2,
    surface: "/orders",
    identity: "admin",
    workflow: "read another account's order",
    risk_category: "access control",
    outcome: "observed_not_vulnerable",
    rationale: "Synthetic UI demonstration. No requests were sent to a target.",
    reviews: [],
    reasons: [],
  });
  return {
    task_id: taskId,
    task_status: "running",
    current,
    history: [current, baseline],
    conclusions,
    plans: [],
    retests: [],
    summary: {
      total: 0,
      candidate: 0,
      runtime_observed: 0,
      operator_verified: 0,
      negative_checks: 0,
      mapping_complete: 0,
      supported: 0,
      stale: 0,
      unknown: 0,
    },
  };
}

function assess(item: AssuranceConclusion, data: TaskAssurance): Pick<AssurancePlanItem, "freshness" | "reasons"> {
  const reasons: AssuranceReason[] = [];
  const current = data.current;
  const baseline = data.history.find((row) => row.generation === item.baseline_generation);
  if (!current || !baseline) return { freshness: "unknown", reasons: [{ kind: "baseline_missing" }] };
  let unknown = !item.mapping_complete || !item.dependencies.length;
  let changed = false;
  if (unknown) reasons.push({ kind: "incomplete_mapping" });
  const visited = new Set<string>();
  function walk(id: string, path: string[]) {
    if (visited.has(id)) return;
    visited.add(id);
    const node = current?.nodes.find((row) => row.id === id);
    const next = [...path, id];
    if (!node) {
      unknown = true;
      reasons.push({ kind: "missing_dependency", path: next });
      return;
    }
    const old = baseline?.versions[id];
    const value = current?.versions[id];
    if (!old || !value) {
      unknown = true;
      reasons.push({ kind: "unknown_version", path: next });
    } else if (old !== value) {
      changed = true;
      reasons.push({ kind: "dependency_changed", path: next, old, new: value });
    }
    for (const child of node.depends_on) walk(child, next);
  }
  for (const id of item.dependencies) walk(id, [item.id]);
  if (item.kind === "negative_coverage" && item.outcome !== "observed_not_vulnerable") {
    unknown = true;
    reasons.push({ kind: "coverage_not_negative" });
  }
  return { freshness: changed ? "stale" : unknown ? "unknown" : "supported_for_snapshot", reasons };
}

function refresh(data: TaskAssurance) {
  const summary = {
    total: data.conclusions.length,
    candidate: 0,
    runtime_observed: 0,
    operator_verified: 0,
    negative_checks: 0,
    mapping_complete: 0,
    supported: 0,
    stale: 0,
    unknown: 0,
  };
  for (const item of data.conclusions) {
    Object.assign(item, assess(item, data));
    if (item.assurance === "candidate") summary.candidate++;
    if (item.assurance === "runtime_observed") summary.runtime_observed++;
    if (item.assurance === "operator_verified") summary.operator_verified++;
    if (item.kind === "negative_coverage") summary.negative_checks++;
    if (item.mapping_complete) summary.mapping_complete++;
    if (item.freshness === "stale") summary.stale++;
    else if (item.freshness === "unknown") summary.unknown++;
    else summary.supported++;
  }
  data.summary = summary;
  return clone(data);
}

function assertGeneration(data: TaskAssurance, body: Record<string, unknown>) {
  if (body.expected_generation !== (data.current?.generation ?? 0))
    throw new Error("The snapshot changed. Refresh and retry.");
}

function advance(data: TaskAssurance) {
  if (!data.current) throw new Error("Create a snapshot first.");
  const generation = data.current.generation + 1;
  data.current = {
    ...clone(data.current),
    generation,
    fingerprint: `demo-${generation}`,
    source: "runtime",
    created_at: now(),
  };
  data.history.unshift(data.current);
}

function mapping(body: Record<string, unknown>): Omit<AssuranceMapping, "expected_generation"> {
  const result = {
    baseline_generation: Number(body.baseline_generation),
    dependencies: body.dependencies as string[],
    mapping_complete: Boolean(body.mapping_complete),
    prerequisites: body.prerequisites as string[],
    impact: Number(body.impact),
    effort_units: Number(body.effort_units),
  };
  if (
    !Number.isInteger(result.impact) ||
    result.impact < 1 ||
    result.impact > 5 ||
    !Number.isInteger(result.effort_units) ||
    result.effort_units < 1
  )
    throw new Error("Impact must be 1–5 and effort must be a positive integer.");
  return result;
}

export function mockAssuranceRoute(
  method: string,
  segments: string[],
  body: Record<string, unknown>,
  findings: Finding[],
  startRetest: (findingId: string, notes: string) => { retest: FindingRetest; created: boolean },
  taskStatus = "running",
): unknown {
  if (segments[0] !== "tasks" || segments[2] !== "assurance") return undefined;
  const taskId = decodeURIComponent(segments[1]);
  let data = workspaces.get(taskId);
  if (!data) {
    data = seed(taskId, findings);
    workspaces.set(taskId, data);
  }
  const action = segments[3];
  data.task_status = taskStatus;
  if (method === "GET" && action === "export")
    return {
      ...refresh(data),
      schema_version: 1,
      exported_at: now(),
      complete: false,
      coverage_basis: "explicitly_recorded_conclusions_only",
    };
  if (method === "GET" && !action) return refresh(data);
  if (action === "snapshots" && method === "POST") {
    assertGeneration(data, body);
    const nodes = clone(body.nodes as AssuranceSnapshot["nodes"]);
    if (!String(body.environment ?? "").trim() || !String(body.revision ?? "").trim())
      throw new Error("Environment and revision are required.");
    if (nodes.some((node) => node.id.startsWith("artex:")))
      throw new Error("The artex: dependency namespace is reserved.");
    const generation = (data.current?.generation ?? 0) + 1;
    data.current = {
      generation,
      environment: String(body.environment),
      revision: String(body.revision),
      nodes,
      versions: clone(body.versions as Record<string, string>),
      available_prerequisites: clone(body.available_prerequisites as string[]),
      fingerprint: `demo-${generation}`,
      source: "declared",
      created_at: now(),
    };
    data.history.unshift(data.current);
    return clone(data.current);
  }
  if (action === "coverage" && method === "POST") {
    assertGeneration(data, body);
    for (const key of ["surface", "identity", "workflow", "risk_category", "rationale"])
      if (!String(body[key] ?? "").trim()) throw new Error(`${key} is required.`);
    const item: AssuranceConclusion = {
      ...mapping(body),
      id: `check:${data.conclusions.length + 1}`,
      kind: "negative_coverage",
      title: `${body.surface} · ${body.risk_category}`,
      assurance: "operator_reported",
      freshness: "unknown",
      evidence_version: 1,
      evidence_hash: `demo-check-${data.conclusions.length + 1}`,
      evidence_refs: body.evidence_refs as string[],
      surface: String(body.surface),
      identity: String(body.identity),
      workflow: String(body.workflow),
      risk_category: String(body.risk_category),
      outcome: String(body.outcome),
      rationale: String(body.rationale),
      reasons: [],
      reviews: [],
    };
    data.conclusions.push(item);
    advance(data);
    return clone(item);
  }
  if (action === "conclusions") {
    const item = data.conclusions.find((row) => row.id === decodeURIComponent(segments[4]));
    if (!item) throw new Error("Conclusion not found.");
    assertGeneration(data, body);
    if (method === "PUT") {
      Object.assign(item, mapping(body));
      advance(data);
      return clone(item);
    }
    if (method === "POST" && segments[5] === "reviews") {
      if (item.kind !== "finding") throw new Error("Only findings accept evidence reviews.");
      if (
        body.expected_evidence_version !== item.evidence_version ||
        body.expected_evidence_hash !== item.evidence_hash
      )
        throw new Error("Evidence changed. Refresh and review the new evidence.");
      const refs = body.evidence_refs as string[];
      if (!String(body.reviewer ?? "").trim() || !String(body.rationale ?? "").trim())
        throw new Error("Reviewer and rationale are required.");
      if (refs.some((ref) => !item.evidence_refs.includes(ref)) || (body.state === "operator_verified" && !refs.length))
        throw new Error("Select valid bound evidence before verifying.");
      item.assurance = String(body.state);
      const review = {
        id: String(item.reviews.length + 1),
        generation: data.current?.generation ?? 0,
        evidence_version: item.evidence_version,
        evidence_hash: item.evidence_hash,
        state: body.state as "operator_verified" | "rejected",
        provenance: "operator" as const,
        reviewer: String(body.reviewer),
        rationale: String(body.rationale),
        evidence_refs: clone(refs),
        created_at: now(),
      };
      item.reviews.unshift(review);
      return clone(review);
    }
  }
  if (action === "plans") {
    if (method === "POST" && segments.length === 4) {
      assertGeneration(data, body);
      if (!data.current) throw new Error("Create a snapshot first.");
      const budget = Number(body.budget_units);
      if (!Number.isInteger(budget) || budget < 0) throw new Error("Budget must be a nonnegative integer.");
      let remaining = budget;
      const items: AssurancePlanItem[] = [...data.conclusions]
        .sort((a, b) => b.impact - a.impact || a.id.localeCompare(b.id))
        .map((item) => {
          const state = assess(item, data);
          const missing = item.prerequisites.filter((value) => !data.current?.available_prerequisites.includes(value));
          let decision = "not_selected";
          if (state.freshness !== "supported_for_snapshot") {
            if (missing.length || (item.kind === "negative_coverage" && item.outcome === "blocked"))
              decision = "blocked";
            else if (item.effort_units <= remaining) {
              decision = "selected";
              remaining -= item.effort_units;
            } else decision = "deferred_budget";
          }
          return {
            id: item.id,
            kind: item.kind,
            ...state,
            decision,
            missing_prerequisites: missing,
            impact: item.impact,
            effort_units: item.effort_units,
          };
        });
      const plan: AssurancePlan = {
        id: String(data.plans.length + 1),
        task_id: taskId,
        generation: data.current.generation,
        fingerprint: data.current.fingerprint,
        created_at: now(),
        result: {
          schema_version: 1,
          project: taskId,
          environment: data.current.environment,
          target_revision: data.current.revision,
          generation: data.current.generation,
          budget_units: budget,
          allocated_units: budget - remaining,
          revalidation_required: items.some((item) => item.freshness !== "supported_for_snapshot"),
          broader_review_required: items.some((item) =>
            item.reasons.some((reason) => reason.kind !== "dependency_changed"),
          ),
          items,
        },
      };
      data.plans.unshift(plan);
      return clone(plan);
    }
    const plan = data.plans.find((row) => row.id === segments[4]);
    if (!plan) throw new Error("Plan not found.");
    if (method === "GET") return clone(plan);
    if (method === "POST" && segments[5] === "items" && segments[7] === "retest") {
      if (plan.generation !== data.current?.generation || plan.fingerprint !== data.current.fingerprint)
        throw new Error("The plan is stale. Build a new plan.");
      const id = decodeURIComponent(segments[6]);
      const item = plan.result.items.find((row) => row.id === id);
      const conclusion = data.conclusions.find((row) => row.id === id);
      if (!item || item.kind !== "finding" || item.decision !== "selected" || !conclusion?.finding_id)
        throw new Error("Only selected findings can be dispatched.");
      const result = startRetest(conclusion.finding_id, String(body.notes ?? ""));
      Object.assign(result.retest, {
        assurance_task_id: taskId,
        assurance_plan_id: plan.id,
        assurance_conclusion_id: id,
        assurance_generation: plan.generation,
        assurance_stale: false,
      });
      data.retests = [result.retest, ...(data.retests ?? []).filter((row) => row.id !== result.retest.id)];
      return result;
    }
  }
  throw new Error("Unknown assurance demo route.");
}
