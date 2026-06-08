// AssignmentStudio — staff-admin view for proposing, reviewing, and approving
// algorithm-generated delegate→position assignments.

import { createMemo, createResource, createSignal, For, Show } from "solid-js";
import { useNavigate, useParams, useSearchParams } from "@solidjs/router";
import { ConnectError, Code } from "@connectrpc/connect";

import {
  assignmentClient,
  assignmentRunClient,
  committeeClient,
  delegateClient,
  delegationClient,
  positionClient,
} from "@/lib/api";
import { userSignal } from "@/lib/session";
import {
  AssignmentRunStatus,
  AssignmentStatus,
  type Assignment,
  type AssignmentRun,
} from "@/gen/numun/v1/assignments_pb";
import type { Committee, Position } from "@/gen/numun/v1/committees_pb";
import type { Delegate } from "@/gen/numun/v1/delegates_pb";
import type { Delegation } from "@/gen/numun/v1/delegations_pb";
import { User_Role } from "@/gen/numun/v1/users_pb";

import AssignmentStudioRunControls from "./AssignmentStudioRunControls";

type StatusFilter = "all" | "proposed" | "approved";

export default function AssignmentStudio() {
  const params = useParams<{ conferenceId: string }>();
  const [user] = userSignal;
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();

  const conferenceId = () => params.conferenceId;

  // Reference data: committees, positions, delegates, delegations.
  const [committees, { refetch: refetchCommittees }] = createResource(
    conferenceId,
    async (id) => {
      const resp = await committeeClient.listCommittees({ conferenceId: id });
      return resp.items;
    },
  );

  const [positions, { refetch: refetchPositions }] = createResource(
    () =>
      committees()
        ? committees()!
            .map((c) => c.id)
            .join(",")
        : "",
    async () => {
      const cs = committees();
      if (!cs) return [] as Position[];
      const all: Position[] = [];
      for (const c of cs) {
        const resp = await positionClient.listPositions({
          committeeId: c.id,
        });
        all.push(...resp.items);
      }
      return all;
    },
  );

  const [delegations] = createResource(conferenceId, async (id) => {
    const resp = await delegationClient.listAllDelegations({
      conferenceId: id,
    });
    return resp.items;
  });

  const [delegates] = createResource(
    () =>
      delegations()
        ? delegations()!
            .map((d) => d.id)
            .join(",")
        : "",
    async () => {
      const ds = delegations();
      if (!ds) return [] as Delegate[];
      const all: Delegate[] = [];
      for (const d of ds) {
        const resp = await delegateClient.listAllDelegates({
          delegationId: d.id,
        });
        all.push(...resp.items);
      }
      return all;
    },
  );

  // Current run — fetched once on mount; user can refresh manually.
  const [currentRun, { refetch: refetchCurrentRun }] = createResource(
    conferenceId,
    async (id) => {
      const resp = await assignmentRunClient.getCurrentRun({
        conferenceId: id,
      });
      return resp.run ?? null;
    },
  );

  const runInFlight = createMemo(
    () => currentRun()?.status === AssignmentRunStatus.RUNNING,
  );

  // Filters live in URL search params.
  const firstParam = (v: string | string[] | undefined): string =>
    Array.isArray(v) ? (v[0] ?? "") : (v ?? "");
  const committeeFilter = () => firstParam(searchParams.committee);
  const delegationFilter = () => firstParam(searchParams.delegation);
  const statusFilter = (): StatusFilter => {
    const v = firstParam(searchParams.status);
    if (v === "proposed" || v === "approved") return v;
    return "all";
  };

  const filterKey = () =>
    `${conferenceId()}|${committeeFilter()}|${delegationFilter()}|${statusFilter()}`;

  const [assignments, { refetch: refetchAssignments }] = createResource(
    filterKey,
    async () => {
      const id = conferenceId();
      if (!id) return [] as Assignment[];
      const resp = await assignmentClient.listAssignments({
        conferenceId: id,
        committeeId: committeeFilter(),
        delegationId: delegationFilter(),
        status: filterToStatus(statusFilter()),
      });
      return resp.items;
    },
  );

  const [runs, { refetch: refetchRuns }] = createResource(
    conferenceId,
    async (id) => {
      const resp = await assignmentRunClient.listAssignmentRuns({
        conferenceId: id,
      });
      return resp.items;
    },
  );

  const [error, setError] = createSignal<string | null>(null);
  const [busyAction, setBusyAction] = createSignal<string | null>(null);
  const [approveAllOpen, setApproveAllOpen] = createSignal(false);
  const [editTarget, setEditTarget] = createSignal<Assignment | null>(null);
  const [historyOpen, setHistoryOpen] = createSignal(false);

  function updateFilter(patch: Record<string, string | undefined>) {
    setSearchParams(patch, { replace: true });
  }

  function delegateById(id: string): Delegate | undefined {
    return delegates()?.find((d) => d.id === id);
  }
  function delegationById(id: string): Delegation | undefined {
    return delegations()?.find((d) => d.id === id);
  }
  function positionById(id: string): Position | undefined {
    return positions()?.find((p) => p.id === id);
  }
  function committeeById(id: string): Committee | undefined {
    return committees()?.find((c) => c.id === id);
  }

  async function approve(a: Assignment) {
    setBusyAction(a.id);
    setError(null);
    try {
      await assignmentClient.approve({
        assignmentId: a.id,
        expectedVersion: a.version,
      });
      await refetchAssignments();
    } catch (err) {
      setError(handleMutationError(err));
    } finally {
      setBusyAction(null);
    }
  }

  async function unapprove(a: Assignment) {
    setBusyAction(a.id);
    setError(null);
    try {
      await assignmentClient.unapprove({
        assignmentId: a.id,
        expectedVersion: a.version,
      });
      await refetchAssignments();
    } catch (err) {
      setError(handleMutationError(err));
    } finally {
      setBusyAction(null);
    }
  }

  async function doApproveAll() {
    setBusyAction("approve-all");
    setError(null);
    try {
      await assignmentClient.approveAll({
        conferenceId: conferenceId(),
        runId: "",
      });
      setApproveAllOpen(false);
      await refetchAssignments();
    } catch (err) {
      setError(handleMutationError(err));
    } finally {
      setBusyAction(null);
    }
  }

  return (
    <main class="mx-auto max-w-7xl px-6 py-6">
      <header class="flex items-center justify-between">
        <div>
          <h1 class="text-2xl font-bold text-nu-purple">Assignment Studio</h1>
          <p class="mt-1 text-sm text-nu-purple-700">
            Conference {conferenceId()}
          </p>
        </div>
        <div class="flex items-center gap-3 text-sm">
          <RunStatusBadge
            run={currentRun()}
            onRefresh={() => {
              refetchCurrentRun();
              refetchRuns();
            }}
          />
          <button
            type="button"
            onClick={() => navigate("/dashboard")}
            class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
          >
            Back
          </button>
        </div>
      </header>

      <Show when={user()?.role !== User_Role.STAFF_ADMIN}>
        <p class="mt-6 rounded bg-amber-50 p-3 text-sm text-amber-900">
          Admin only.
        </p>
      </Show>

      <Show when={user()?.role === User_Role.STAFF_ADMIN}>
        <Show when={error()}>
          {(e) => (
            <p class="mt-4 rounded bg-red-50 p-3 text-sm text-red-900">{e()}</p>
          )}
        </Show>

        <div class="mt-6 grid gap-4 md:grid-cols-[1fr_18rem]">
          <section>
            <Filters
              committees={committees() ?? []}
              delegations={delegations() ?? []}
              committeeId={committeeFilter()}
              delegationId={delegationFilter()}
              status={statusFilter()}
              onChange={updateFilter}
            />

            <div class="mt-3 flex items-center justify-between">
              <p class="text-xs text-nu-purple-700">
                {(assignments() ?? []).length} assignments
              </p>
              <div class="flex gap-2">
                <button
                  type="button"
                  onClick={() => {
                    refetchAssignments();
                    refetchCommittees();
                    refetchPositions();
                  }}
                  class="rounded border border-nu-purple-300 px-3 py-1 text-xs text-nu-purple-700"
                >
                  Reload
                </button>
                <button
                  type="button"
                  disabled={
                    busyAction() === "approve-all" ||
                    (assignments() ?? []).every(
                      (a) => a.status === AssignmentStatus.APPROVED,
                    )
                  }
                  onClick={() => setApproveAllOpen(true)}
                  class="rounded bg-nu-purple px-3 py-1 text-xs text-white disabled:opacity-50"
                >
                  Approve all
                </button>
              </div>
            </div>

            <section class="mt-3 overflow-x-auto rounded border border-nu-purple-200 bg-white">
              <table class="w-full table-fixed text-sm">
                <thead class="bg-nu-purple-50 text-left text-xs uppercase tracking-wide text-nu-purple-700">
                  <tr>
                    <th class="px-2 py-2">Delegate</th>
                    <th class="px-2 py-2">Committee → Position</th>
                    <th class="w-24 px-2 py-2">Status</th>
                    <th class="w-16 px-2 py-2 text-right">Score</th>
                    <th class="w-48 px-2 py-2">Reason</th>
                    <th class="w-40 px-2 py-2 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  <Show
                    when={!assignments.loading}
                    fallback={
                      <tr>
                        <td
                          colspan={6}
                          class="px-2 py-4 text-center text-nu-purple-700"
                        >
                          Loading…
                        </td>
                      </tr>
                    }
                  >
                    <Show
                      when={(assignments() ?? []).length > 0}
                      fallback={
                        <tr>
                          <td
                            colspan={6}
                            class="px-2 py-4 text-center text-nu-purple-700"
                          >
                            No assignments yet. Use Propose to generate.
                          </td>
                        </tr>
                      }
                    >
                      <For each={assignments() ?? []}>
                        {(a) => {
                          const d = delegateById(a.delegateId);
                          const del = delegationById(a.delegationId);
                          const p = positionById(a.positionId);
                          const c = committeeById(a.committeeId);
                          return (
                            <tr class="border-t border-nu-purple-100 align-top">
                              <td class="px-2 py-2">
                                <div class="font-semibold">
                                  {d
                                    ? `${d.firstName} ${d.lastName}`
                                    : a.delegateId}
                                </div>
                                <div class="text-xs text-nu-purple-700">
                                  {del?.school ?? a.delegationId}
                                </div>
                              </td>
                              <td class="px-2 py-2">
                                <div class="font-semibold">
                                  {c?.name ?? a.committeeId}
                                </div>
                                <div class="text-xs text-nu-purple-700">
                                  {p?.name ?? a.positionId}
                                </div>
                              </td>
                              <td class="px-2 py-2">
                                <StatusChip status={a.status} />
                              </td>
                              <td class="px-2 py-2 text-right tabular-nums">
                                {a.score.toFixed(2)}
                              </td>
                              <td
                                class="truncate px-2 py-2 text-xs text-nu-purple-700"
                                title={a.reason}
                              >
                                {a.reason || "—"}
                              </td>
                              <td class="px-2 py-2 text-right">
                                <Show
                                  when={a.status === AssignmentStatus.PROPOSED}
                                >
                                  <button
                                    type="button"
                                    disabled={busyAction() === a.id}
                                    onClick={() => approve(a)}
                                    class="rounded bg-nu-purple px-2 py-0.5 text-xs text-white disabled:opacity-50"
                                  >
                                    Approve
                                  </button>
                                </Show>
                                <Show
                                  when={a.status === AssignmentStatus.APPROVED}
                                >
                                  <button
                                    type="button"
                                    disabled={busyAction() === a.id}
                                    onClick={() => unapprove(a)}
                                    class="rounded border border-nu-purple-300 px-2 py-0.5 text-xs text-nu-purple-700 disabled:opacity-50"
                                  >
                                    Unapprove
                                  </button>
                                </Show>{" "}
                                <button
                                  type="button"
                                  onClick={() => setEditTarget(a)}
                                  class="rounded border border-nu-purple-300 px-2 py-0.5 text-xs text-nu-purple-700"
                                >
                                  Edit
                                </button>
                              </td>
                            </tr>
                          );
                        }}
                      </For>
                    </Show>
                  </Show>
                </tbody>
              </table>
            </section>

            <section class="mt-6 rounded border border-nu-purple-200 bg-white">
              <button
                type="button"
                onClick={() => setHistoryOpen(!historyOpen())}
                class="flex w-full items-center justify-between px-4 py-2 text-left text-sm font-semibold text-nu-purple"
              >
                <span>Run history</span>
                <span class="text-nu-purple-700">
                  {historyOpen() ? "▾" : "▸"}
                </span>
              </button>
              <Show when={historyOpen()}>
                <RunHistoryTable runs={runs() ?? []} />
              </Show>
            </section>
          </section>

          <AssignmentStudioRunControls
            conferenceId={conferenceId()}
            currentRun={currentRun()}
            disabled={runInFlight()}
            onComplete={async (_resp, _mode) => {
              await refetchAssignments();
              await refetchCurrentRun();
              await refetchRuns();
            }}
          />
        </div>
      </Show>

      <Show when={approveAllOpen()}>
        <ModalShell
          title="Approve all proposed assignments?"
          onClose={() => setApproveAllOpen(false)}
        >
          <p class="text-sm text-nu-purple-700">
            This will flip every proposed assignment in this conference to
            approved. Approved assignments are pinned across re-runs.
          </p>
          <div class="mt-4 flex justify-end gap-2">
            <button
              type="button"
              onClick={() => setApproveAllOpen(false)}
              class="rounded border border-nu-purple-300 px-3 py-1 text-sm text-nu-purple-700"
            >
              Cancel
            </button>
            <button
              type="button"
              disabled={busyAction() === "approve-all"}
              onClick={doApproveAll}
              class="rounded bg-nu-purple px-3 py-1 text-sm text-white disabled:opacity-50"
            >
              {busyAction() === "approve-all" ? "Approving…" : "Approve all"}
            </button>
          </div>
        </ModalShell>
      </Show>

      <Show when={editTarget()}>
        {(target) => (
          <EditAssignmentModal
            assignment={target()}
            positions={positions() ?? []}
            committees={committees() ?? []}
            onClose={() => setEditTarget(null)}
            onSubmit={async (newPositionId) => {
              setError(null);
              try {
                await assignmentClient.updateAssignment({
                  assignmentId: target().id,
                  positionId: newPositionId,
                  expectedVersion: target().version,
                });
                setEditTarget(null);
                await refetchAssignments();
              } catch (err) {
                setError(handleMutationError(err));
              }
            }}
          />
        )}
      </Show>
    </main>
  );
}

function Filters(props: {
  committees: Committee[];
  delegations: Delegation[];
  committeeId: string;
  delegationId: string;
  status: StatusFilter;
  onChange: (patch: Record<string, string | undefined>) => void;
}) {
  return (
    <section class="grid gap-2 rounded border border-nu-purple-200 bg-white p-3 text-sm md:grid-cols-3">
      <label class="block">
        <span class="text-xs font-semibold uppercase tracking-wide text-nu-purple-700">
          Committee
        </span>
        <select
          value={props.committeeId}
          onChange={(e) =>
            props.onChange({ committee: e.currentTarget.value || undefined })
          }
          class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
        >
          <option value="">All</option>
          <For each={props.committees}>
            {(c) => <option value={c.id}>{c.name}</option>}
          </For>
        </select>
      </label>
      <label class="block">
        <span class="text-xs font-semibold uppercase tracking-wide text-nu-purple-700">
          Delegation
        </span>
        <select
          value={props.delegationId}
          onChange={(e) =>
            props.onChange({ delegation: e.currentTarget.value || undefined })
          }
          class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
        >
          <option value="">All</option>
          <For each={props.delegations}>
            {(d) => <option value={d.id}>{d.school}</option>}
          </For>
        </select>
      </label>
      <label class="block">
        <span class="text-xs font-semibold uppercase tracking-wide text-nu-purple-700">
          Status
        </span>
        <select
          value={props.status}
          onChange={(e) =>
            props.onChange({
              status:
                e.currentTarget.value === "all"
                  ? undefined
                  : e.currentTarget.value,
            })
          }
          class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
        >
          <option value="all">All</option>
          <option value="proposed">Proposed</option>
          <option value="approved">Approved</option>
        </select>
      </label>
    </section>
  );
}

function RunStatusBadge(props: {
  run: AssignmentRun | null | undefined;
  onRefresh: () => void;
}) {
  const r = props.run;
  if (!r) {
    return (
      <span class="rounded bg-nu-purple-50 px-2 py-0.5 text-xs text-nu-purple-700">
        No active run
      </span>
    );
  }
  if (r.status === AssignmentRunStatus.RUNNING) {
    return (
      <span class="flex items-center gap-2 rounded bg-amber-50 px-2 py-0.5 text-xs text-amber-900">
        <Spinner /> Run in progress
        <button type="button" onClick={props.onRefresh} class="underline">
          Refresh
        </button>
      </span>
    );
  }
  if (r.status === AssignmentRunStatus.DONE) {
    return (
      <span class="rounded bg-nu-purple-50 px-2 py-0.5 text-xs text-nu-purple-700">
        Last run: done · obj {r.objective.toFixed(2)}
      </span>
    );
  }
  if (r.status === AssignmentRunStatus.FAILED) {
    return (
      <span
        class="rounded bg-amber-50 px-2 py-0.5 text-xs text-amber-900"
        title={r.diagnostics}
      >
        Last run: failed
      </span>
    );
  }
  return (
    <span class="rounded bg-nu-purple-50 px-2 py-0.5 text-xs text-nu-purple-700">
      —
    </span>
  );
}

function Spinner() {
  return (
    <span
      class="inline-block h-3 w-3 animate-spin rounded-full border-2 border-amber-900 border-t-transparent"
      aria-hidden="true"
    />
  );
}

function StatusChip(props: { status: AssignmentStatus }) {
  if (props.status === AssignmentStatus.APPROVED) {
    return (
      <span class="inline-block rounded bg-nu-green/20 px-2 py-0.5 text-xs font-semibold text-nu-green-dark">
        Approved
      </span>
    );
  }
  if (props.status === AssignmentStatus.PROPOSED) {
    return (
      <span class="inline-block rounded bg-nu-purple-50 px-2 py-0.5 text-xs font-semibold text-nu-purple-700">
        Proposed
      </span>
    );
  }
  return <span class="text-xs text-nu-purple-700">—</span>;
}

function RunHistoryTable(props: { runs: AssignmentRun[] }) {
  return (
    <div class="overflow-x-auto">
      <table class="w-full text-xs">
        <thead class="bg-nu-purple-50 text-left uppercase tracking-wide text-nu-purple-700">
          <tr>
            <th class="px-2 py-1">Ordinal</th>
            <th class="px-2 py-1">Status</th>
            <th class="px-2 py-1">Seed</th>
            <th class="px-2 py-1">Canonical</th>
            <th class="px-2 py-1">Triggered at</th>
            <th class="px-2 py-1">Objective</th>
            <th class="px-2 py-1">Assignments</th>
          </tr>
        </thead>
        <tbody>
          <Show
            when={props.runs.length > 0}
            fallback={
              <tr>
                <td
                  colspan={7}
                  class="px-2 py-3 text-center text-nu-purple-700"
                >
                  No runs yet.
                </td>
              </tr>
            }
          >
            <For each={props.runs}>
              {(r) => (
                <tr class="border-t border-nu-purple-100">
                  <td class="px-2 py-1 tabular-nums">{r.runOrdinal}</td>
                  <td class="px-2 py-1">{runStatusLabel(r.status)}</td>
                  <td class="px-2 py-1 font-mono">{r.seed.toString()}</td>
                  <td class="px-2 py-1">{r.isCanonical ? "Yes" : "No"}</td>
                  <td class="px-2 py-1">{formatTs(r.triggeredAt)}</td>
                  <td class="px-2 py-1 tabular-nums">
                    {r.status === AssignmentRunStatus.DONE
                      ? r.objective.toFixed(2)
                      : "—"}
                  </td>
                  <td class="px-2 py-1 tabular-nums">{r.assignmentCount}</td>
                </tr>
              )}
            </For>
          </Show>
        </tbody>
      </table>
    </div>
  );
}

function EditAssignmentModal(props: {
  assignment: Assignment;
  positions: Position[];
  committees: Committee[];
  onClose: () => void;
  onSubmit: (positionId: string) => Promise<void>;
}) {
  const [positionId, setPositionId] = createSignal(props.assignment.positionId);
  const [busy, setBusy] = createSignal(false);

  function committeeName(p: Position): string {
    return (
      props.committees.find((c) => c.id === p.committeeId)?.name ??
      p.committeeId
    );
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await props.onSubmit(positionId());
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell title="Edit assignment" onClose={props.onClose}>
      <form onSubmit={submit} class="space-y-3 text-sm">
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wide text-nu-purple-700">
            Position
          </span>
          <select
            value={positionId()}
            onChange={(e) => setPositionId(e.currentTarget.value)}
            class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
          >
            <For each={props.positions}>
              {(p) => (
                <option value={p.id}>
                  {committeeName(p)} — {p.name}
                </option>
              )}
            </For>
          </select>
        </label>
        <p class="text-xs text-nu-purple-700">
          The handler will validate hard constraints (capacity, dual-delegation
          pairing, reserved-tier rules) before committing.
        </p>
        <div class="flex justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={props.onClose}
            class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={busy()}
            class="rounded bg-nu-purple px-3 py-1 text-white disabled:opacity-50"
          >
            {busy() ? "Saving…" : "Save"}
          </button>
        </div>
      </form>
    </ModalShell>
  );
}

function ModalShell(props: {
  title: string;
  onClose: () => void;
  children: import("solid-js").JSX.Element;
}) {
  return (
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4"
      onClick={props.onClose}
    >
      <div
        class="w-full max-w-md rounded border border-nu-purple-200 bg-white p-4 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <div class="mb-3 flex items-center justify-between">
          <h2 class="text-base font-semibold text-nu-purple">{props.title}</h2>
          <button
            type="button"
            onClick={props.onClose}
            aria-label="Close"
            class="text-nu-purple-700"
          >
            ×
          </button>
        </div>
        {props.children}
      </div>
    </div>
  );
}

function filterToStatus(s: StatusFilter): AssignmentStatus {
  switch (s) {
    case "proposed":
      return AssignmentStatus.PROPOSED;
    case "approved":
      return AssignmentStatus.APPROVED;
    default:
      return AssignmentStatus.UNSPECIFIED;
  }
}

function runStatusLabel(s: AssignmentRunStatus): string {
  switch (s) {
    case AssignmentRunStatus.RUNNING:
      return "Running";
    case AssignmentRunStatus.DONE:
      return "Done";
    case AssignmentRunStatus.FAILED:
      return "Failed";
    default:
      return "—";
  }
}

function formatTs(ts: { seconds: bigint; nanos: number } | undefined): string {
  if (!ts) return "—";
  const ms = Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1_000_000);
  return new Date(ms).toLocaleString();
}

function connectMessage(err: unknown): string | null {
  if (err instanceof ConnectError) return err.message;
  if (err instanceof Error) return err.message;
  return null;
}

function handleMutationError(err: unknown): string {
  if (err instanceof ConnectError && err.code === Code.Aborted) {
    return "Another change happened first. Reload to fetch the latest state.";
  }
  if (err instanceof ConnectError && err.code === Code.FailedPrecondition) {
    return err.message;
  }
  return connectMessage(err) ?? "Operation failed.";
}
