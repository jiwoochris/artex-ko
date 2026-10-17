"use client";

import * as React from "react";

import Link from "next/link";

import { DownloadIcon, PlusIcon, RefreshCwIcon, RotateCcwIcon, Trash2Icon } from "lucide-react";
import { useTranslations } from "next-intl";

import { StatusBadge } from "@/components/status-badge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Skeleton } from "@/components/ui/skeleton";
import { Textarea } from "@/components/ui/textarea";
import {
  type AssuranceConclusion,
  type AssuranceCoverageInput,
  type AssuranceMapping,
  type AssurancePlan,
  type AssurancePlanItem,
  type AssuranceReason,
  type AssuranceReviewInput,
  assuranceApi,
  canDispatchAssuranceItem,
  splitAssuranceList,
  type TaskAssurance,
} from "@/lib/assurance";
import type { FindingRetest } from "@/lib/types";

type Save = (operation: () => Promise<unknown>) => Promise<void>;

function useSubmit() {
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");
  const lock = React.useRef(false);
  async function run(operation: () => Promise<unknown>) {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    setError("");
    try {
      await operation();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  return { busy, error, run };
}

function FormError({ message }: { message: string }) {
  return message ? (
    <Alert variant="destructive">
      <AlertDescription>{message}</AlertDescription>
    </Alert>
  ) : null;
}

function TextField({
  label,
  name,
  multiline = false,
  ...props
}: {
  label: string;
  name: string;
  multiline?: boolean;
  defaultValue?: string | number;
  placeholder?: string;
  required?: boolean;
  readOnly?: boolean;
  type?: string;
  min?: number;
  max?: number;
}) {
  const id = React.useId();
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <Label htmlFor={id}>{label}</Label>
      {multiline ? (
        <Textarea
          id={id}
          name={name}
          defaultValue={props.defaultValue}
          placeholder={props.placeholder}
          required={props.required}
          maxLength={4000}
          rows={3}
        />
      ) : (
        <Input id={id} name={name} {...props} maxLength={1000} className="h-10" />
      )}
    </div>
  );
}

function StateBadge({ value }: { value: string }) {
  const t = useTranslations("assurance");
  const key = `state.${value}`;
  return <Badge variant={value === "stale" ? "destructive" : "secondary"}>{t.has(key) ? t(key) : value}</Badge>;
}

function Reasons({ reasons }: { reasons: AssuranceReason[] }) {
  const t = useTranslations("assurance");
  return (
    <ul className="space-y-1 text-muted-foreground text-xs">
      {(reasons ?? []).map((reason, i) => (
        <li key={`${reason.kind}-${(reason.path ?? []).join("/")}-${i}`}>
          {t.has(`reason.${reason.kind}`) ? t(`reason.${reason.kind}`) : reason.kind}
          {reason.path?.length ? <span className="block break-words font-mono">{reason.path.join(" → ")}</span> : null}
          {reason.old || reason.new ? (
            <span className="block break-words">
              {reason.old || "?"} → {reason.new || "?"}
            </span>
          ) : null}
        </li>
      ))}
    </ul>
  );
}

export function AssuranceTab({ taskId }: { taskId: string }) {
  const t = useTranslations("assurance");
  const [data, setData] = React.useState<TaskAssurance | null>(null);
  const [error, setError] = React.useState("");
  const [selectedId, setSelectedId] = React.useState("");
  const [coverageOpen, setCoverageOpen] = React.useState(false);
  const seq = React.useRef(0);
  const exporting = useSubmit();
  const load = React.useCallback(async () => {
    const request = ++seq.current;
    try {
      const next = await assuranceApi.get(taskId);
      if (request !== seq.current) return;
      setData(next);
      setSelectedId((previous) =>
        next.conclusions.some((item) => item.id === previous) ? previous : (next.conclusions[0]?.id ?? ""),
      );
      setError("");
    } catch (e) {
      if (request === seq.current) setError((e as Error).message);
      throw e;
    }
  }, [taskId]);
  React.useEffect(() => {
    void load().catch(() => undefined);
    return () => {
      seq.current++;
    };
  }, [load]);
  const save: Save = async (operation) => {
    await operation();
    await load();
  };
  const selected = data?.conclusions.find((item) => item.id === selectedId);

  async function download() {
    const result = await assuranceApi.export(taskId);
    const url = URL.createObjectURL(new Blob([JSON.stringify(result, null, 2)], { type: "application/json" }));
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `artex-assurance-${taskId}.json`;
    anchor.click();
    URL.revokeObjectURL(url);
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="max-w-3xl space-y-1">
          <h2 className="font-semibold text-lg">{t("title")}</h2>
          <p className="text-muted-foreground text-sm">{t("description")}</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button className="h-10" variant="outline" onClick={() => void load().catch(() => undefined)}>
            <RefreshCwIcon aria-hidden="true" />
            {t("refresh")}
          </Button>
          <Button
            className="h-10"
            variant="outline"
            disabled={!data || exporting.busy}
            onClick={() => void exporting.run(download)}
          >
            <DownloadIcon aria-hidden="true" />
            {t("export")}
          </Button>
        </div>
      </div>
      <FormError message={error || exporting.error} />
      {!data && !error ? (
        <>
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-72 w-full" />
        </>
      ) : null}
      {data ? (
        <>
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            {(
              [
                ["operator_verified", data.summary.operator_verified],
                ["negative_checks", data.summary.negative_checks],
                ["stale", data.summary.stale],
                ["unknown", data.summary.unknown],
              ] as const
            ).map(([key, count]) => (
              <div key={key} className="rounded-lg border p-3">
                <div className="font-semibold text-2xl tabular-nums">{count}</div>
                <div className="text-muted-foreground text-xs">{t(`summary.${key}`)}</div>
              </div>
            ))}
          </div>
          <SnapshotEditor key={data.current?.generation ?? 0} data={data} taskId={taskId} save={save} />
          {data.history_truncated || data.plans_truncated ? (
            <p className="text-muted-foreground text-xs">{t("historyLimited")}</p>
          ) : null}
          <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(22rem,0.8fr)]">
            <Card className="min-w-0">
              <CardHeader className="flex flex-row flex-wrap justify-between gap-3">
                <div>
                  <CardTitle>{t("conclusions")}</CardTitle>
                  <CardDescription className="mt-2">{t("conclusionsDesc")}</CardDescription>
                </div>
                <Button
                  className="h-10"
                  variant="outline"
                  disabled={!data.current}
                  onClick={() => setCoverageOpen(true)}
                >
                  <PlusIcon aria-hidden="true" />
                  {t("addCoverage")}
                </Button>
              </CardHeader>
              <CardContent className="space-y-4">
                {!data.conclusions.length ? (
                  <p className="rounded-lg border border-dashed p-6 text-muted-foreground text-sm">{t("empty")}</p>
                ) : null}
                <div className="grid max-h-80 gap-2 overflow-y-auto">
                  {data.conclusions.map((item) => (
                    <button
                      key={item.id}
                      type="button"
                      aria-pressed={item.id === selectedId}
                      onClick={() => setSelectedId(item.id)}
                      className={`flex min-h-10 flex-col items-start gap-2 rounded-lg border p-3 text-left focus-visible:outline-2 focus-visible:outline-ring ${item.id === selectedId ? "border-primary bg-muted" : "hover:bg-muted/50"}`}
                    >
                      <span className="break-words font-medium text-sm">{item.title}</span>
                      <span className="flex flex-wrap gap-1">
                        <StateBadge value={item.assurance} />
                        <StateBadge value={item.freshness} />
                        {item.kind === "negative_coverage" ? (
                          <StateBadge value={item.outcome ?? "inconclusive"} />
                        ) : null}
                      </span>
                    </button>
                  ))}
                </div>
                {selected ? (
                  <ConclusionEditor
                    key={`${selected.id}-${data.current?.generation}-${selected.reviews?.length}`}
                    taskId={taskId}
                    item={selected}
                    data={data}
                    save={save}
                  />
                ) : null}
              </CardContent>
            </Card>
            <PlanPanel taskId={taskId} data={data} save={save} />
          </div>
          {coverageOpen ? (
            <CoverageDialog taskId={taskId} data={data} save={save} onClose={() => setCoverageOpen(false)} />
          ) : null}
        </>
      ) : null}
    </div>
  );
}

function SnapshotEditor({ data, taskId, save }: { data: TaskAssurance; taskId: string; save: Save }) {
  const t = useTranslations("assurance");
  const current = data.current;
  const submit = useSubmit();
  const [rows, setRows] = React.useState(() =>
    (current?.nodes ?? [])
      .filter((node) => !node.id.startsWith("artex:"))
      .map((node) => ({
        key: node.id,
        id: node.id,
        version: current?.versions[node.id] ?? "",
        depends: (node.depends_on ?? []).join(", "),
      })),
  );
  async function create(form: HTMLFormElement) {
    const fields = new FormData(form);
    const nodes = rows.map((row) => ({ id: row.id.trim(), depends_on: splitAssuranceList(row.depends) }));
    if (
      nodes.some((node) => !node.id) ||
      new Set(nodes.map((node) => node.id)).size !== nodes.length ||
      rows.some((row) => !row.version.trim())
    )
      throw new Error(t("dependencyValidation"));
    await save(() =>
      assuranceApi.snapshot(taskId, {
        expected_generation: current?.generation ?? 0,
        environment: String(fields.get("environment")).trim(),
        revision: String(fields.get("revision")).trim(),
        nodes,
        versions: Object.fromEntries(rows.map((row) => [row.id.trim(), row.version.trim()])),
        available_prerequisites: splitAssuranceList(String(fields.get("available_prerequisites"))),
      }),
    );
  }
  function edit(key: string, field: "id" | "version" | "depends", value: string) {
    setRows((previous) => previous.map((row) => (row.key === key ? { ...row, [field]: value } : row)));
  }
  return (
    <Card>
      <CardContent className="pt-5">
        <details open={!current}>
          <summary className="cursor-pointer rounded font-medium focus-visible:outline-2 focus-visible:outline-ring">
            {t("snapshot")}
            {current
              ? ` · ${current.environment} / ${current.revision} · ${t("generation", { value: current.generation })}`
              : ""}
          </summary>
          <p className="my-3 text-muted-foreground text-sm">{t("snapshotDesc")}</p>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const form = e.currentTarget;
              void submit.run(() => create(form));
            }}
          >
            <fieldset disabled={submit.busy} className="space-y-4">
              <div className="grid gap-3 md:grid-cols-3">
                <TextField
                  name="environment"
                  label={t("environment")}
                  defaultValue={current?.environment ?? ""}
                  readOnly={Boolean(current)}
                  required
                />
                <TextField name="revision" label={t("revision")} defaultValue={current?.revision ?? ""} required />
                <TextField
                  name="available_prerequisites"
                  label={t("availablePrerequisites")}
                  defaultValue={(current?.available_prerequisites ?? []).join(", ")}
                />
              </div>
              <div className="space-y-3">
                {rows.map((row) => (
                  <div
                    key={row.key}
                    className="grid items-end gap-2 rounded-lg border p-3 sm:grid-cols-[1fr_1fr_1fr_auto]"
                  >
                    {(
                      [
                        ["id", "dependencyId"],
                        ["version", "version"],
                        ["depends", "dependsOn"],
                      ] as const
                    ).map(([field, label]) => (
                      <div key={field} className="space-y-2">
                        <Label htmlFor={`${row.key}-${field}`}>{t(label)}</Label>
                        <Input
                          id={`${row.key}-${field}`}
                          className="h-10"
                          required={field !== "depends"}
                          value={row[field]}
                          onChange={(e) => edit(row.key, field, e.target.value)}
                        />
                      </div>
                    ))}
                    <Button
                      type="button"
                      variant="ghost"
                      className="size-10"
                      aria-label={t("removeDependency", { id: row.id })}
                      onClick={() => setRows((previous) => previous.filter((item) => item.key !== row.key))}
                    >
                      <Trash2Icon aria-hidden="true" />
                    </Button>
                  </div>
                ))}
                <Button
                  type="button"
                  className="h-10"
                  variant="outline"
                  onClick={() =>
                    setRows((previous) => [...previous, { key: crypto.randomUUID(), id: "", version: "", depends: "" }])
                  }
                >
                  <PlusIcon aria-hidden="true" />
                  {t("addDependency")}
                </Button>
              </div>
              <FormError message={submit.error} />
              <Button type="submit" className="h-10">
                {submit.busy ? t("saving") : t("saveSnapshot")}
              </Button>
            </fieldset>
          </form>
        </details>
      </CardContent>
    </Card>
  );
}

function MappingFields({ data, item }: { data: TaskAssurance; item?: AssuranceConclusion }) {
  const t = useTranslations("assurance");
  const id = React.useId();
  return (
    <>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-2">
          <Label htmlFor={`${id}-baseline`}>{t("baseline")}</Label>
          <NativeSelect
            id={`${id}-baseline`}
            name="baseline_generation"
            defaultValue={String(item?.baseline_generation || data.current?.generation || 0)}
            className="w-full"
          >
            {item?.baseline_generation &&
            !data.history.some((snapshot) => snapshot.generation === item.baseline_generation) ? (
              <NativeSelectOption value={item.baseline_generation}>
                {t("generation", { value: item.baseline_generation })}
              </NativeSelectOption>
            ) : null}
            {data.history.map((snapshot) => (
              <NativeSelectOption key={snapshot.generation} value={snapshot.generation}>
                {snapshot.revision} · {t("generation", { value: snapshot.generation })}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <TextField label={t("dependencies")} name="dependencies" defaultValue={(item?.dependencies ?? []).join(", ")} />
        <TextField
          label={t("prerequisites")}
          name="prerequisites"
          defaultValue={(item?.prerequisites ?? []).join(", ")}
        />
        <div className="grid grid-cols-2 gap-3">
          <TextField
            label={t("impact")}
            name="impact"
            type="number"
            min={1}
            max={5}
            defaultValue={item?.impact ?? 3}
            required
          />
          <TextField
            label={t("effort")}
            name="effort_units"
            type="number"
            min={1}
            max={1000000}
            defaultValue={item?.effort_units ?? 1}
            required
          />
        </div>
      </div>
      <p className="text-muted-foreground text-xs">
        {t("mappingHint", {
          nodes:
            (data.current?.nodes ?? [])
              .filter((node) => !node.id.startsWith("artex:"))
              .map((node) => node.id)
              .join(", ") || "—",
        })}
      </p>
      <div className="flex items-start gap-2">
        <Checkbox id={`${id}-complete`} name="mapping_complete" defaultChecked={item?.mapping_complete ?? false} />
        <Label htmlFor={`${id}-complete`} className="text-sm leading-5">
          {t("mappingComplete")}
        </Label>
      </div>
    </>
  );
}

function mappingInput(form: FormData, generation: number): AssuranceMapping {
  return {
    expected_generation: generation,
    baseline_generation: Number(form.get("baseline_generation")),
    dependencies: splitAssuranceList(String(form.get("dependencies") ?? "")),
    mapping_complete: form.get("mapping_complete") === "on",
    prerequisites: splitAssuranceList(String(form.get("prerequisites") ?? "")),
    impact: Number(form.get("impact")),
    effort_units: Number(form.get("effort_units")),
  };
}

function ConclusionEditor({
  data,
  taskId,
  item,
  save,
}: {
  data: TaskAssurance;
  taskId: string;
  item: AssuranceConclusion;
  save: Save;
}) {
  const t = useTranslations("assurance");
  const submit = useSubmit();
  return (
    <section className="space-y-4 border-t pt-4">
      <div className="space-y-2">
        <h3 className="break-words font-semibold">{item.title}</h3>
        {item.triage_status ? (
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <span>{t("triage")}</span>
            <StatusBadge domain="finding" value={item.triage_status} />
            {item.severity ? <StatusBadge domain="severity" value={item.severity} /> : null}
          </div>
        ) : null}
        <p className="break-words font-mono text-muted-foreground text-xs">
          {item.id} · {t("evidenceVersion", { value: item.evidence_version })}
        </p>
        {item.finding_id ? (
          <Link
            className="inline-block rounded text-sm underline underline-offset-4 focus-visible:outline-2 focus-visible:outline-ring"
            href={`/function/findings/detail?id=${encodeURIComponent(item.finding_id)}`}
          >
            {t("viewFinding")}
          </Link>
        ) : (
          <p className="break-words text-sm">
            {[item.surface, item.identity, item.workflow, item.risk_category].filter(Boolean).join(" · ")}
          </p>
        )}
        {item.rationale ? <p className="whitespace-pre-wrap text-muted-foreground text-sm">{item.rationale}</p> : null}
        <Reasons reasons={item.reasons} />
      </div>
      {data.current ? (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fields = new FormData(e.currentTarget);
            void submit.run(() =>
              save(() => assuranceApi.mapping(taskId, item.id, mappingInput(fields, data.current?.generation ?? 0))),
            );
          }}
        >
          <fieldset disabled={submit.busy} className="space-y-3">
            <MappingFields data={data} item={item} />
            <FormError message={submit.error} />
            <Button className="h-10" type="submit" variant="outline">
              {submit.busy ? t("saving") : t("saveMapping")}
            </Button>
          </fieldset>
        </form>
      ) : (
        <p className="text-muted-foreground text-sm">{t("snapshotRequired")}</p>
      )}
      {item.kind === "finding" ? <ReviewEditor data={data} taskId={taskId} item={item} save={save} /> : null}
    </section>
  );
}

function ReviewEditor({
  data,
  taskId,
  item,
  save,
}: {
  data: TaskAssurance;
  taskId: string;
  item: AssuranceConclusion;
  save: Save;
}) {
  const t = useTranslations("assurance");
  const submit = useSubmit();
  const id = React.useId();
  const [state, setState] = React.useState<AssuranceReviewInput["state"]>("operator_verified");
  const [refs, setRefs] = React.useState<string[]>([]);
  async function review(form: FormData) {
    if (state === "operator_verified" && refs.length === 0) throw new Error(t("evidenceRequired"));
    await save(() =>
      assuranceApi.review(taskId, item.id, {
        expected_generation: data.current?.generation ?? 0,
        expected_evidence_version: item.evidence_version,
        expected_evidence_hash: item.evidence_hash,
        state,
        provenance: "operator",
        reviewer: String(form.get("reviewer")).trim(),
        rationale: String(form.get("rationale")).trim(),
        evidence_refs: refs,
      }),
    );
  }
  return (
    <details className="rounded-lg border p-3">
      <summary className="cursor-pointer rounded font-medium text-sm focus-visible:outline-2 focus-visible:outline-ring">
        {t("review")} · {item.reviews?.length ?? 0}
      </summary>
      <p className="my-3 text-muted-foreground text-sm">{t("reviewDesc")}</p>
      {(item.reviews ?? []).map((row) => (
        <div key={row.id} className="mb-3 rounded-lg bg-muted p-3 text-sm">
          <StateBadge value={row.state} />
          <p className="mt-2 break-words">
            {row.reviewer} · {t("evidenceVersion", { value: row.evidence_version })} ·{" "}
            {t("generation", { value: row.generation })}
          </p>
          <p className="mt-1 whitespace-pre-wrap break-words">{row.rationale}</p>
          <p className="mt-1 break-words text-muted-foreground text-xs">{row.evidence_refs.join(", ")}</p>
        </div>
      ))}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const fields = new FormData(e.currentTarget);
          void submit.run(() => review(fields));
        }}
      >
        <fieldset disabled={submit.busy || !data.current} className="space-y-3">
          <div className="space-y-2">
            <Label htmlFor={`${id}-state`}>{t("reviewDecision")}</Label>
            <NativeSelect
              id={`${id}-state`}
              value={state}
              onChange={(e) => setState(e.target.value as AssuranceReviewInput["state"])}
            >
              <NativeSelectOption value="operator_verified">{t("state.operator_verified")}</NativeSelectOption>
              <NativeSelectOption value="rejected">{t("state.rejected")}</NativeSelectOption>
            </NativeSelect>
          </div>
          <TextField name="reviewer" label={t("reviewer")} required />
          <TextField name="rationale" label={t("rationale")} multiline required />
          <p className="font-medium text-sm">{t("evidenceRefs")}</p>
          {!(item.evidence_refs ?? []).length ? (
            <p className="text-muted-foreground text-xs">{t("noEvidence")}</p>
          ) : null}
          {(item.evidence_refs ?? []).map((ref, index) => (
            <div key={ref} className="flex items-start gap-2">
              <Checkbox
                id={`${id}-ref-${index}`}
                checked={refs.includes(ref)}
                onCheckedChange={(checked) =>
                  setRefs((previous) =>
                    checked === true ? [...previous, ref] : previous.filter((value) => value !== ref),
                  )
                }
              />
              <Label className="break-all font-mono text-xs" htmlFor={`${id}-ref-${index}`}>
                {item.evidence_ref_kinds?.[ref] ? `${t(`evidenceKind.${item.evidence_ref_kinds[ref]}`)} · ` : ""}
                {ref}
              </Label>
            </div>
          ))}
          <FormError message={submit.error} />
          <Button
            type="submit"
            className="h-10"
            variant="outline"
            disabled={state === "operator_verified" && !refs.length}
          >
            {submit.busy ? t("saving") : t("saveReview")}
          </Button>
        </fieldset>
      </form>
    </details>
  );
}

function CoverageDialog({
  data,
  taskId,
  save,
  onClose,
}: {
  data: TaskAssurance;
  taskId: string;
  save: Save;
  onClose: () => void;
}) {
  const t = useTranslations("assurance");
  const submit = useSubmit();
  const id = React.useId();
  async function create(fields: FormData) {
    const field = (name: string) => String(fields.get(name) ?? "").trim();
    await save(() =>
      assuranceApi.coverage(taskId, {
        ...mappingInput(fields, data.current?.generation ?? 0),
        surface: field("surface"),
        identity: field("identity"),
        workflow: field("workflow"),
        risk_category: field("risk_category"),
        outcome: field("outcome") as AssuranceCoverageInput["outcome"],
        rationale: field("rationale"),
        evidence_refs: splitAssuranceList(field("evidence_refs")),
      }),
    );
    onClose();
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !submit.busy) onClose();
      }}
    >
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("addCoverage")}</DialogTitle>
          <DialogDescription>{t("coverageDesc")}</DialogDescription>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fields = new FormData(e.currentTarget);
            void submit.run(() => create(fields));
          }}
        >
          <fieldset disabled={submit.busy} className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-2">
              {(["surface", "identity", "workflow", "risk_category"] as const).map((field) => (
                <TextField key={field} name={field} label={t(field)} required />
              ))}
            </div>
            <div className="space-y-2">
              <Label htmlFor={`${id}-outcome`}>{t("outcome")}</Label>
              <NativeSelect id={`${id}-outcome`} name="outcome">
                <NativeSelectOption value="observed_not_vulnerable">
                  {t("state.observed_not_vulnerable")}
                </NativeSelectOption>
                <NativeSelectOption value="inconclusive">{t("state.inconclusive")}</NativeSelectOption>
                <NativeSelectOption value="blocked">{t("state.blocked")}</NativeSelectOption>
              </NativeSelect>
            </div>
            <TextField name="rationale" label={t("rationale")} multiline required />
            <TextField name="evidence_refs" label={t("coverageEvidenceRefs")} />
            <MappingFields data={data} />
            <FormError message={submit.error} />
            <DialogFooter>
              <Button type="button" className="h-10" variant="outline" onClick={onClose}>
                {t("cancel")}
              </Button>
              <Button className="h-10" type="submit">
                {submit.busy ? t("saving") : t("saveCoverage")}
              </Button>
            </DialogFooter>
          </fieldset>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function PlanPanel({ data, taskId, save }: { data: TaskAssurance; taskId: string; save: Save }) {
  const t = useTranslations("assurance");
  const retestT = useTranslations("retest.panel");
  const submit = useSubmit();
  const [selectedPlanId, setSelectedPlanId] = React.useState("");
  const [dispatch, setDispatch] = React.useState<{ plan: AssurancePlan; item: AssurancePlanItem } | null>(null);
  const plan = data.plans.find((row) => row.id === selectedPlanId) ?? data.plans[0];
  const planStale =
    plan &&
    (!data.current || plan.generation !== data.current.generation || plan.fingerprint !== data.current.fingerprint);
  const selectId = React.useId();
  async function create(budget: number) {
    await save(async () => {
      const result = await assuranceApi.plan(taskId, data.current?.generation ?? 0, budget);
      setSelectedPlanId(result.id);
    });
  }
  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle>{t("plan")}</CardTitle>
        <CardDescription>{t("planDesc")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fields = new FormData(e.currentTarget);
            void submit.run(() => create(Number(fields.get("budget"))));
          }}
        >
          <fieldset className="flex flex-wrap items-end gap-3" disabled={submit.busy || !data.current}>
            <TextField
              name="budget"
              label={t("budget")}
              type="number"
              min={0}
              max={1000000}
              defaultValue={10}
              required
            />
            <Button type="submit" className="h-10">
              {submit.busy ? t("saving") : t("buildPlan")}
            </Button>
          </fieldset>
        </form>
        <FormError message={submit.error} />
        {!data.current ? <p className="text-muted-foreground text-sm">{t("snapshotRequired")}</p> : null}
        {data.plans.length > 1 ? (
          <div className="space-y-2">
            <Label htmlFor={selectId}>{t("planHistory")}</Label>
            <NativeSelect
              id={selectId}
              value={plan?.id ?? ""}
              onChange={(e) => setSelectedPlanId(e.target.value)}
              className="w-full"
            >
              {data.plans.map((row) => (
                <NativeSelectOption key={row.id} value={row.id}>
                  #{row.id} · {row.result.target_revision} · {t("generation", { value: row.generation })}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
        ) : null}
        {!plan ? (
          <p className="rounded-lg border border-dashed p-5 text-muted-foreground text-sm">{t("noPlan")}</p>
        ) : (
          <>
            <div className="space-y-2">
              <p className="text-sm">
                {plan.result.target_revision} ·{" "}
                {t("allocation", { used: plan.result.allocated_units, budget: plan.result.budget_units })}
              </p>
              <p className="text-muted-foreground text-xs">
                {t("generation", { value: plan.generation })} · #{plan.id}
              </p>
            </div>
            {planStale ? (
              <Alert>
                <AlertDescription>{t("planStale")}</AlertDescription>
              </Alert>
            ) : null}
            {plan.result.broader_review_required ? (
              <Alert>
                <AlertDescription>{t("broaderReview")}</AlertDescription>
              </Alert>
            ) : null}
            <div className="space-y-3">
              {plan.result.items.map((item) => {
                const conclusion = data.conclusions.find((row) => row.id === item.id);
                return (
                  <div key={item.id} className="space-y-2 rounded-lg border p-3">
                    <p className="break-words font-medium text-sm">{conclusion?.title ?? item.id}</p>
                    <div className="flex flex-wrap gap-1">
                      <StateBadge value={item.decision} />
                      <StateBadge value={item.freshness} />
                    </div>
                    <p className="text-muted-foreground text-xs">
                      {t("itemCost", { impact: item.impact, effort: item.effort_units })}
                    </p>
                    <Reasons reasons={item.reasons} />
                    {item.missing_prerequisites?.length ? (
                      <p className="break-words text-sm">
                        {t("missingPrerequisites", { names: item.missing_prerequisites.join(", ") })}
                      </p>
                    ) : null}
                    {item.decision === "selected" && item.kind === "finding" ? (
                      <Button
                        className="h-10"
                        variant="outline"
                        disabled={!canDispatchAssuranceItem(plan, data.current, item)}
                        onClick={() => setDispatch({ plan, item })}
                      >
                        <RotateCcwIcon aria-hidden="true" />
                        {t("startRetest")}
                      </Button>
                    ) : null}
                    {item.decision === "selected" && item.kind !== "finding" ? (
                      <p className="text-muted-foreground text-xs">{t("manualCoverageRetest")}</p>
                    ) : null}
                  </div>
                );
              })}
            </div>
          </>
        )}
        {(data.retests ?? []).length ? (
          <section className="space-y-3 border-t pt-4">
            <h3 className="font-medium text-sm">{t("retestHistory")}</h3>
            {data.retests.map((retest) => (
              <div key={retest.id} className="space-y-2 rounded-lg border p-3 text-sm">
                <div className="flex flex-wrap gap-2">
                  <Badge variant="secondary">
                    {retest.status === "completed" && retest.verdict
                      ? retestT(`verdict.${retest.verdict}`)
                      : retestT(`status.${retest.status}`)}
                  </Badge>
                  <span className="text-muted-foreground">
                    {retest.assurance_conclusion_id} · #{retest.assurance_plan_id}
                  </span>
                </div>
                {retest.assurance_stale ? <p className="text-destructive">{t("staleRetest")}</p> : null}
                {retest.summary ? <p className="whitespace-pre-wrap break-words">{retest.summary}</p> : null}
                {retest.error ? (
                  <p className="whitespace-pre-wrap break-words text-destructive">{retest.error}</p>
                ) : null}
                {retest.conversation_id !== null ? (
                  <Button asChild variant="outline" className="h-10">
                    <Link href={`/chat?c=${retest.conversation_id}`}>{t("viewSession")}</Link>
                  </Button>
                ) : null}
              </div>
            ))}
          </section>
        ) : null}
        {dispatch ? (
          <DispatchDialog
            taskId={taskId}
            plan={dispatch.plan}
            item={dispatch.item}
            save={save}
            onClose={() => setDispatch(null)}
          />
        ) : null}
      </CardContent>
    </Card>
  );
}

function DispatchDialog({
  taskId,
  plan,
  item,
  save,
  onClose,
}: {
  taskId: string;
  plan: AssurancePlan;
  item: AssurancePlanItem;
  save: Save;
  onClose: () => void;
}) {
  const t = useTranslations("assurance");
  const retestT = useTranslations("retest.panel");
  const submit = useSubmit();
  const [result, setResult] = React.useState<{ retest: FindingRetest; created: boolean } | null>(null);
  async function start(notes: string) {
    await save(async () => {
      setResult(await assuranceApi.dispatch(taskId, plan.id, item.id, notes));
    });
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !submit.busy) onClose();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("startRetest")}</DialogTitle>
          <DialogDescription>{t("dispatchDesc")}</DialogDescription>
        </DialogHeader>
        {result === null ? (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const fields = new FormData(e.currentTarget);
              void submit.run(() => start(String(fields.get("notes") ?? "").trim()));
            }}
          >
            <fieldset disabled={submit.busy} className="space-y-4">
              <p className="break-words font-mono text-xs">
                {item.id} · {plan.result.environment} / {plan.result.target_revision}
              </p>
              <TextField name="notes" label={t("notes")} multiline />
              <FormError message={submit.error} />
              <DialogFooter>
                <Button className="h-10" type="button" variant="outline" onClick={onClose}>
                  {t("cancel")}
                </Button>
                <Button className="h-10" type="submit">
                  {submit.busy ? t("saving") : t("confirmRetest")}
                </Button>
              </DialogFooter>
            </fieldset>
          </form>
        ) : (
          <div className="space-y-4">
            <p role="status">{result.created ? t("retestStarted") : t("retestExisting")}</p>
            <Badge variant="secondary">
              {result.retest.status === "completed" && result.retest.verdict
                ? retestT(`verdict.${result.retest.verdict}`)
                : retestT(`status.${result.retest.status}`)}
            </Badge>
            {result.retest.summary ? (
              <p className="whitespace-pre-wrap break-words text-sm">{result.retest.summary}</p>
            ) : null}
            {result.retest.conversation_id !== null ? (
              <Button asChild className="h-10">
                <Link href={`/chat?c=${result.retest.conversation_id}`}>{t("viewSession")}</Link>
              </Button>
            ) : (
              <p className="text-muted-foreground text-sm">{t("sessionUnavailable")}</p>
            )}
            <FormError message={submit.error} />
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
