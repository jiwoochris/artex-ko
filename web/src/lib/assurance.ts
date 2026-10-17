import { http } from "@/lib/api";
import type { FindingRetest } from "@/lib/types";

export interface AssuranceNode {
  id: string;
  depends_on: string[];
}

export interface AssuranceSnapshot {
  generation: number;
  environment: string;
  revision: string;
  nodes: AssuranceNode[];
  versions: Record<string, string>;
  available_prerequisites: string[];
  fingerprint: string;
  source: string;
  created_at: string;
}

export interface AssuranceMapping {
  expected_generation: number;
  baseline_generation: number;
  dependencies: string[];
  mapping_complete: boolean;
  prerequisites: string[];
  impact: number;
  effort_units: number;
}

export interface AssuranceReason {
  kind: string;
  path?: string[];
  old?: string;
  new?: string;
}

export interface AssuranceReviewInput {
  expected_generation: number;
  expected_evidence_version: number;
  expected_evidence_hash: string;
  state: "operator_verified" | "rejected";
  provenance: "operator";
  reviewer: string;
  rationale: string;
  evidence_refs: string[];
}

export interface AssuranceReview
  extends Omit<AssuranceReviewInput, "expected_generation" | "expected_evidence_version" | "expected_evidence_hash"> {
  id: string;
  generation: number;
  evidence_version: number;
  evidence_hash: string;
  created_at: string;
}

export interface AssuranceConclusion extends Omit<AssuranceMapping, "expected_generation"> {
  id: string;
  kind: "finding" | "negative_coverage";
  finding_id?: string;
  title: string;
  severity?: string;
  triage_status?: string;
  assurance: string;
  freshness: string;
  evidence_version: number;
  evidence_hash: string;
  evidence_refs: string[];
  evidence_ref_kinds?: Record<string, "finding_document" | "traffic_snapshot">;
  last_retest_id?: string;
  last_retest_verdict?: string;
  surface?: string;
  identity?: string;
  workflow?: string;
  risk_category?: string;
  outcome?: string;
  rationale?: string;
  reviews: AssuranceReview[];
  reasons: AssuranceReason[];
}

export interface AssuranceCoverageInput extends AssuranceMapping {
  surface: string;
  identity: string;
  workflow: string;
  risk_category: string;
  outcome: "observed_not_vulnerable" | "inconclusive" | "blocked";
  rationale: string;
  evidence_refs: string[];
}

export interface AssurancePlanItem {
  id: string;
  kind: string;
  freshness: string;
  decision: string;
  reasons: AssuranceReason[];
  missing_prerequisites?: string[];
  impact: number;
  effort_units: number;
}

export interface AssurancePlan {
  id: string;
  task_id: string;
  generation: number;
  fingerprint: string;
  created_at: string;
  result: {
    schema_version: number;
    project: string;
    environment: string;
    target_revision: string;
    generation: number;
    budget_units: number;
    allocated_units: number;
    revalidation_required: boolean;
    broader_review_required: boolean;
    items: AssurancePlanItem[];
  };
}

export interface TaskAssurance {
  task_id: string;
  task_status: string;
  current: AssuranceSnapshot | null;
  history: AssuranceSnapshot[];
  conclusions: AssuranceConclusion[];
  plans: AssurancePlan[];
  retests: FindingRetest[];
  history_truncated?: boolean;
  plans_truncated?: boolean;
  summary: {
    total: number;
    candidate: number;
    runtime_observed: number;
    operator_verified: number;
    negative_checks: number;
    mapping_complete: number;
    supported: number;
    stale: number;
    unknown: number;
  };
}

export interface TaskAssuranceExport extends TaskAssurance {
  schema_version: 1;
  exported_at: string;
  complete: false;
  coverage_basis: "explicitly_recorded_conclusions_only";
}

export interface AssuranceSnapshotInput {
  expected_generation: number;
  environment: string;
  revision: string;
  nodes: AssuranceNode[];
  versions: Record<string, string>;
  available_prerequisites: string[];
}

const base = (taskId: string) => `/tasks/${encodeURIComponent(taskId)}/assurance`;
const write = <T>(path: string, body: unknown, method = "POST") =>
  http<T>(path, { method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });

export const assuranceApi = {
  get: (taskId: string) => http<TaskAssurance>(base(taskId)),
  snapshot: (taskId: string, input: AssuranceSnapshotInput) => write<unknown>(`${base(taskId)}/snapshots`, input),
  mapping: (taskId: string, id: string, input: AssuranceMapping) =>
    write<unknown>(`${base(taskId)}/conclusions/${encodeURIComponent(id)}`, input, "PUT"),
  coverage: (taskId: string, input: AssuranceCoverageInput) => write<unknown>(`${base(taskId)}/coverage`, input),
  review: (taskId: string, id: string, input: AssuranceReviewInput) =>
    write<unknown>(`${base(taskId)}/conclusions/${encodeURIComponent(id)}/reviews`, input),
  plan: (taskId: string, expected_generation: number, budget_units: number) =>
    write<AssurancePlan>(`${base(taskId)}/plans`, { expected_generation, budget_units }),
  dispatch: (taskId: string, planId: string, conclusionId: string, notes: string) =>
    write<{ retest: FindingRetest; created: boolean }>(
      `${base(taskId)}/plans/${encodeURIComponent(planId)}/items/${encodeURIComponent(conclusionId)}/retest`,
      { notes },
    ),
  export: (taskId: string) => http<TaskAssuranceExport>(`${base(taskId)}/export`),
};

export function splitAssuranceList(value: string): string[] {
  return [
    ...new Set(
      value
        .split(/[,\n]/)
        .map((item) => item.trim())
        .filter(Boolean),
    ),
  ];
}

export function canDispatchAssuranceItem(
  plan: AssurancePlan,
  current: AssuranceSnapshot | null,
  item: AssurancePlanItem,
): boolean {
  return (
    current !== null &&
    plan.generation === current.generation &&
    plan.fingerprint === current.fingerprint &&
    item.kind === "finding" &&
    item.decision === "selected"
  );
}
