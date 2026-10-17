import "@testing-library/jest-dom/vitest";
import { cleanup, configure, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { type AssurancePlan, assuranceApi, type TaskAssurance } from "@/lib/assurance";
import { mockAssuranceRoute, resetMockAssurance } from "@/lib/mock/assurance";
import type { Finding, FindingRetest } from "@/lib/types";

import messages from "../../../../../../../messages/en.json";
import { AssuranceTab } from "./assurance-tab";

const fixture: Finding = {
  id: "10",
  task_id: "1",
  vulnclass: "Object access",
  severity: "high",
  status: "confirmed",
  summary: "Fixture finding",
  evidence: "Fixture evidence",
  ts: "2026-10-17T00:00:00Z",
};
const dispatchResult = {
  created: true,
  retest: {
    id: 1,
    conversation_id: 12,
    status: "running",
    verdict: "",
    notes: "",
    summary: "",
    evidence: "",
    error: "",
    created_at: "2026-10-17T00:00:00Z",
    started_at: null,
    finished_at: null,
    finding_id: 10,
  } satisfies FindingRetest,
};
const route = (method: string, suffix = "", body: Record<string, unknown> = {}) =>
  mockAssuranceRoute(method, ["tasks", "1", "assurance", ...suffix.split("/").filter(Boolean)], body, [fixture], () =>
    structuredClone(dispatchResult),
  );
const view = (taskId = "1") => (
  <NextIntlClientProvider locale="en" messages={messages}>
    <AssuranceTab key={taskId} taskId={taskId} />
  </NextIntlClientProvider>
);

describe("task assurance workflow", { timeout: 20000 }, () => {
  beforeEach(() => {
    configure({ asyncUtilTimeout: 10000 });
    vi.stubGlobal(
      "ResizeObserver",
      class {
        observe() {
          return undefined;
        }
        unobserve() {
          return undefined;
        }
        disconnect() {
          return undefined;
        }
      },
    );
    resetMockAssurance();
    vi.spyOn(assuranceApi, "get").mockImplementation(async () => route("GET") as TaskAssurance);
    vi.spyOn(assuranceApi, "plan").mockImplementation(
      async (_task, expected_generation, budget_units) =>
        route("POST", "plans", { expected_generation, budget_units }) as AssurancePlan,
    );
    vi.spyOn(assuranceApi, "dispatch").mockImplementation(
      async (_task, planId, id, notes) =>
        route("POST", `plans/${planId}/items/${encodeURIComponent(id)}/retest`, { notes }) as typeof dispatchResult,
    );
    vi.spyOn(assuranceApi, "review").mockImplementation(async (_task, id, input) =>
      route("POST", `conclusions/${encodeURIComponent(id)}/reviews`, { ...input }),
    );
    vi.spyOn(assuranceApi, "snapshot").mockImplementation(async (_task, input) =>
      route("POST", "snapshots", { ...input }),
    );
    vi.spyOn(assuranceApi, "coverage").mockImplementation(async (_task, input) =>
      route("POST", "coverage", { ...input }),
    );
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("recovers from a load failure and shows provenance separately from freshness", async () => {
    vi.mocked(assuranceApi.get).mockRejectedValueOnce(new Error("Temporary read failure"));
    render(view());
    expect(await screen.findByRole("alert")).toHaveTextContent("Temporary read failure");
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    expect(await screen.findByText("Runtime evidence observed")).toBeInTheDocument();
    expect(screen.getAllByText("Needs revalidation").length).toBeGreaterThan(0);
    expect(assuranceApi.dispatch).not.toHaveBeenCalled();
  });

  it("requires explicit confirmation before dispatch and preserves blocked work", async () => {
    render(view());
    fireEvent.click(await screen.findByRole("button", { name: "Build retest plan" }));
    fireEvent.click(await screen.findByRole("button", { name: "Retest finding" }));
    expect(assuranceApi.dispatch).not.toHaveBeenCalled();
    expect(screen.getByText("Missing prerequisites: test-admin")).toBeInTheDocument();
    const dialog = screen.getByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Start selected retest" }));
    expect(await within(dialog).findByRole("link", { name: "View retest session" })).toHaveAttribute(
      "href",
      "/chat?c=12",
    );
    expect(assuranceApi.dispatch).toHaveBeenCalledTimes(1);
  });

  it("shows a retained terminal result when an idempotent dispatch has no remaining session", async () => {
    vi.mocked(assuranceApi.dispatch).mockResolvedValueOnce({
      created: false,
      retest: {
        ...dispatchResult.retest,
        conversation_id: null,
        status: "completed",
        verdict: "inconclusive",
        summary: "Retained fixture result",
      },
    });
    render(view());
    fireEvent.click(await screen.findByRole("button", { name: "Build retest plan" }));
    fireEvent.click(await screen.findByRole("button", { name: "Retest finding" }));
    const dialog = screen.getByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Start selected retest" }));
    expect(
      await within(dialog).findByText("This plan item already has a retest record. No new run was started."),
    ).toBeInTheDocument();
    expect(within(dialog).getByText("Retained fixture result")).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "Start selected retest" })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("link", { name: "View retest session" })).not.toBeInTheDocument();
    expect(assuranceApi.dispatch).toHaveBeenCalledTimes(1);
  });

  it("disables verification until bound evidence is selected and submits exact evidence tokens", async () => {
    render(view());
    fireEvent.click(await screen.findByText("Operator evidence review · 0"));
    const button = screen.getByRole("button", { name: "Record review" });
    expect(button).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Reviewer attribution"), { target: { value: "Test operator" } });
    fireEvent.change(screen.getByLabelText("Rationale and limitations"), {
      target: { value: "Reviewed the bounded fixture evidence." },
    });
    fireEvent.click(screen.getByLabelText(/demo-snapshot-10/));
    expect(button).toBeEnabled();
    fireEvent.click(button);
    await waitFor(() =>
      expect(assuranceApi.review).toHaveBeenCalledWith(
        "1",
        "finding:10",
        expect.objectContaining({
          expected_generation: 2,
          expected_evidence_version: 1,
          expected_evidence_hash: "demo-evidence-10",
          provenance: "operator",
          evidence_refs: ["demo-snapshot-10"],
        }),
      ),
    );
  });

  it("omits generated ARTEX dependencies when saving a declared snapshot", async () => {
    const data = route("GET") as TaskAssurance;
    if (!data.current) throw new Error("Missing fixture snapshot");
    data.current.nodes.push({ id: "artex:task_constraints", depends_on: [] });
    data.current.versions["artex:task_constraints"] = "server-owned";
    vi.mocked(assuranceApi.get).mockResolvedValueOnce(data);
    render(view());
    fireEvent.click(await screen.findByText(/Environment snapshot ·/));
    fireEvent.change(screen.getByLabelText("Deployed revision"), { target: { value: "build-c" } });
    fireEvent.click(screen.getByRole("button", { name: "Save new snapshot" }));
    await waitFor(() => expect(assuranceApi.snapshot).toHaveBeenCalled());
    const input = vi.mocked(assuranceApi.snapshot).mock.calls[0][1];
    expect(input.nodes.map((node) => node.id)).toEqual(["orders-route", "permissions"]);
    expect(input.versions).not.toHaveProperty("artex:task_constraints");
  });

  it("disables saved-plan dispatch after a new snapshot and records contextual coverage", async () => {
    route("POST", "plans", { expected_generation: 2, budget_units: 3 });
    const data = route("GET") as TaskAssurance;
    route("POST", "snapshots", { ...data.current, expected_generation: 2, revision: "build-c" });
    render(view());
    expect(await screen.findByText(/This plan predates/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retest finding" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Record coverage check" }));
    const dialog = screen.getByRole("dialog");
    for (const [label, value] of [
      ["Surface or endpoint", "/pricing"],
      ["Identity or role tested", "anonymous"],
      ["Workflow tested", "view pricing"],
      ["Risk category", "access control"],
      ["Rationale and limitations", "Checked only the public fixture endpoint."],
    ]) {
      fireEvent.change(within(dialog).getByLabelText(label), { target: { value } });
    }
    fireEvent.click(within(dialog).getByRole("button", { name: "Record check" }));
    await waitFor(() =>
      expect(assuranceApi.coverage).toHaveBeenCalledWith(
        "1",
        expect.objectContaining({
          surface: "/pricing",
          identity: "anonymous",
          workflow: "view pricing",
          risk_category: "access control",
          outcome: "observed_not_vulnerable",
          mapping_complete: false,
        }),
      ),
    );
    expect(assuranceApi.dispatch).not.toHaveBeenCalled();
  });

  it("keeps planning and reviews disabled until the first environment snapshot", async () => {
    const data = route("GET") as TaskAssurance;
    data.current = null;
    data.history = [];
    vi.mocked(assuranceApi.get).mockResolvedValueOnce(data);
    render(view());
    expect(await screen.findByRole("button", { name: "Save new snapshot" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Build retest plan" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Record coverage check" })).toBeDisabled();
    expect(screen.getByLabelText("Environment")).toHaveValue("");
    expect(assuranceApi.dispatch).not.toHaveBeenCalled();
  });

  it("drops forms and ignores an old load when switching tasks", async () => {
    let resolveOld: (value: TaskAssurance) => void = () => undefined;
    const oldLoad = new Promise<TaskAssurance>((resolve) => {
      resolveOld = resolve;
    });
    vi.mocked(assuranceApi.get).mockReturnValueOnce(oldLoad);
    const next = route("GET") as TaskAssurance;
    next.task_id = "2";
    next.conclusions = [];
    vi.mocked(assuranceApi.get).mockResolvedValueOnce(next);
    const page = render(view());
    page.rerender(view("2"));
    expect(await screen.findByText(/No findings or coverage checks yet/)).toBeInTheDocument();
    resolveOld(route("GET") as TaskAssurance);
    await waitFor(() => expect(screen.queryByText("Object access")).not.toBeInTheDocument());
    expect(assuranceApi.dispatch).not.toHaveBeenCalled();
  });
});
