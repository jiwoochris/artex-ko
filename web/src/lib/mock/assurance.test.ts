import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AssurancePlan, TaskAssurance, TaskAssuranceExport } from "@/lib/assurance";
import type { Finding, FindingRetest } from "@/lib/types";

import { mockAssuranceRoute, resetMockAssurance } from "./assurance";

const finding: Finding = {
  id: "10",
  task_id: "1",
  vulnclass: "access control",
  severity: "high",
  status: "confirmed",
  summary: "Fixture",
  evidence: "Fixture",
  ts: "2026-10-17T00:00:00Z",
};
const start = vi.fn(() => ({ created: true, retest: { id: 1, conversation_id: 12 } as FindingRetest }));
const route = (method: string, suffix = "", body: Record<string, unknown> = {}) =>
  mockAssuranceRoute(method, ["tasks", "1", "assurance", ...suffix.split("/").filter(Boolean)], body, [finding], start);

describe("assurance demo workflow", () => {
  beforeEach(() => {
    resetMockAssurance();
    start.mockClear();
  });

  it("retains blocked and deferred checks without starting any execution", () => {
    const data = route("GET") as TaskAssurance;
    const plan = route("POST", "plans", {
      expected_generation: data.current?.generation,
      budget_units: 0,
    }) as AssurancePlan;
    expect(plan.result.items.find((item) => item.kind === "finding")?.decision).toBe("deferred_budget");
    expect(plan.result.items.find((item) => item.kind === "negative_coverage")?.decision).toBe("blocked");
    expect(start).not.toHaveBeenCalled();
    expect(() => route("POST", `plans/${plan.id}/items/finding%3A10/retest`)).toThrow("Only selected findings");
  });

  it("rejects a saved plan after a snapshot changes", () => {
    const data = route("GET") as TaskAssurance;
    const plan = route("POST", "plans", { expected_generation: 2, budget_units: 3 }) as AssurancePlan;
    route("POST", "snapshots", { ...data.current, expected_generation: 2, revision: "build-c" });
    expect(() => route("POST", `plans/${plan.id}/items/finding%3A10/retest`)).toThrow("stale");
    expect(start).not.toHaveBeenCalled();
  });

  it("rejects evidence verification without bound evidence or with a stale evidence version", () => {
    const data = route("GET") as TaskAssurance;
    const conclusion = data.conclusions[0];
    const review = {
      expected_generation: 2,
      expected_evidence_version: conclusion.evidence_version,
      expected_evidence_hash: conclusion.evidence_hash,
      reviewer: "Fixture operator",
      rationale: "Checked fixture",
      state: "operator_verified",
      provenance: "operator",
      evidence_refs: [],
    };
    expect(() => route("POST", "conclusions/finding%3A10/reviews", review)).toThrow("bound evidence");
    expect(() =>
      route("POST", "conclusions/finding%3A10/reviews", {
        ...review,
        evidence_refs: conclusion.evidence_refs,
        expected_evidence_version: 0,
      }),
    ).toThrow("Evidence changed");
    route("POST", "conclusions/finding%3A10/reviews", { ...review, evidence_refs: conclusion.evidence_refs });
    const updated = route("GET") as TaskAssurance;
    expect(updated.conclusions[0].assurance).toBe("operator_verified");
    expect(updated.current?.generation).toBe(2);
  });

  it("keeps task workspaces independent and returns detached snapshots", () => {
    const first = route("GET") as TaskAssurance;
    first.conclusions[0].title = "local edit";
    const second = mockAssuranceRoute("GET", ["tasks", "2", "assurance"], {}, [finding], start) as TaskAssurance;
    expect(second.conclusions.some((item) => item.kind === "finding")).toBe(false);
    expect((route("GET") as TaskAssurance).conclusions[0].title).not.toBe("local edit");
  });

  it("exports the versioned incomplete-coverage envelope and retained plans", () => {
    route("POST", "plans", { expected_generation: 2, budget_units: 3 });
    const exported = route("GET", "export") as TaskAssuranceExport;
    expect(exported).toMatchObject({
      schema_version: 1,
      complete: false,
      coverage_basis: "explicitly_recorded_conclusions_only",
      task_status: "running",
    });
    expect(Number.isNaN(Date.parse(exported.exported_at))).toBe(false);
    expect(exported.plans).toHaveLength(1);
    expect(exported.history).toHaveLength(2);
    expect(exported.conclusions.some((item) => item.kind === "negative_coverage")).toBe(true);
  });
});
