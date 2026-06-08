// Committees admin — staff-admin CRUD for committees + nested positions.

import {
  createResource,
  createSignal,
  For,
  Show,
  type Resource,
} from "solid-js";
import { useNavigate, useParams } from "@solidjs/router";
import { ConnectError, Code } from "@connectrpc/connect";

import { committeeClient, positionClient } from "@/lib/api";
import { userSignal } from "@/lib/session";
import {
  CommitteeSize,
  CommitteeType,
  PrestigeTier,
  type Committee,
  type Position,
} from "@/gen/numun/v1/committees_pb";
import { User_Role } from "@/gen/numun/v1/users_pb";

type CommitteeFormState = {
  name: string;
  type: CommitteeType;
  size: CommitteeSize;
  backgroundGuideRef: string;
};

type PositionFormState = {
  name: string;
  maxDelegates: number;
  dualDelegation: boolean;
  prestigeTier: PrestigeTier;
};

export default function Committees() {
  const params = useParams<{ conferenceId: string }>();
  const [user] = userSignal;
  const navigate = useNavigate();

  const [committees, { refetch }] = createResource(
    () => params.conferenceId,
    async (id) => {
      const resp = await committeeClient.listCommittees({ conferenceId: id });
      return resp.items;
    },
  );

  const [expandedId, setExpandedId] = createSignal<string | null>(null);
  const [error, setError] = createSignal<string | null>(null);
  const [createOpen, setCreateOpen] = createSignal(false);
  const [editTarget, setEditTarget] = createSignal<Committee | null>(null);
  const [deleteTarget, setDeleteTarget] = createSignal<Committee | null>(null);

  return (
    <main class="mx-auto max-w-6xl px-6 py-8">
      <header class="flex items-center justify-between">
        <div>
          <h1 class="text-2xl font-bold text-nu-purple">Committees</h1>
          <p class="mt-1 text-sm text-nu-purple-700">
            Conference {params.conferenceId}
          </p>
        </div>
        <div class="flex gap-2 text-sm">
          <button
            type="button"
            onClick={() => navigate("/dashboard")}
            class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
          >
            Back
          </button>
          <button
            type="button"
            onClick={() => refetch()}
            class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
          >
            Reload
          </button>
          <button
            type="button"
            onClick={() => setCreateOpen(true)}
            class="rounded bg-nu-purple px-3 py-1 text-white"
          >
            Create committee
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

        <Show
          when={!committees.loading}
          fallback={
            <p class="mt-6 text-sm text-nu-purple-700">Loading committees…</p>
          }
        >
          <Show
            when={(committees() ?? []).length > 0}
            fallback={
              <p class="mt-6 text-sm text-nu-purple-700">
                No committees yet. Create the first one to begin.
              </p>
            }
          >
            <section class="mt-6 overflow-x-auto rounded border border-nu-purple-200 bg-white">
              <table class="w-full table-fixed text-sm">
                <thead class="bg-nu-purple-50 text-left text-xs uppercase tracking-wide text-nu-purple-700">
                  <tr>
                    <th class="w-8 px-2 py-2"></th>
                    <th class="px-2 py-2">Name</th>
                    <th class="w-32 px-2 py-2">Type</th>
                    <th class="w-32 px-2 py-2">Size</th>
                    <th class="w-32 px-2 py-2">Positions</th>
                    <th class="w-56 px-2 py-2">Background guide</th>
                    <th class="w-40 px-2 py-2 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  <For each={committees() ?? []}>
                    {(c) => (
                      <CommitteeRow
                        committee={c}
                        expanded={expandedId() === c.id}
                        onToggle={() =>
                          setExpandedId(expandedId() === c.id ? null : c.id)
                        }
                        onEdit={() => setEditTarget(c)}
                        onDelete={() => setDeleteTarget(c)}
                      />
                    )}
                  </For>
                </tbody>
              </table>
            </section>
          </Show>
        </Show>
      </Show>

      <Show when={createOpen()}>
        <CommitteeModal
          title="Create committee"
          initial={{
            name: "",
            type: CommitteeType.NON_CRISIS,
            size: CommitteeSize.MEDIUM,
            backgroundGuideRef: "",
          }}
          onClose={() => setCreateOpen(false)}
          onSubmit={async (state) => {
            setError(null);
            try {
              await committeeClient.createCommittee({
                conferenceId: params.conferenceId,
                ...state,
              });
              setCreateOpen(false);
              await refetch();
            } catch (err) {
              setError(connectMessage(err) ?? "Failed to create committee.");
            }
          }}
        />
      </Show>

      <Show when={editTarget()}>
        {(target) => (
          <CommitteeModal
            title="Edit committee"
            initial={{
              name: target().name,
              type: target().type,
              size: target().size,
              backgroundGuideRef: target().backgroundGuideRef,
            }}
            onClose={() => setEditTarget(null)}
            onSubmit={async (state) => {
              setError(null);
              try {
                await committeeClient.updateCommittee({
                  committeeId: target().id,
                  expectedVersion: target().version,
                  ...state,
                });
                setEditTarget(null);
                await refetch();
              } catch (err) {
                setError(handleMutationError(err));
              }
            }}
          />
        )}
      </Show>

      <Show when={deleteTarget()}>
        {(target) => (
          <ConfirmModal
            title="Delete committee?"
            message={`This will delete "${target().name}". This cannot be undone.`}
            onCancel={() => setDeleteTarget(null)}
            onConfirm={async () => {
              setError(null);
              try {
                await committeeClient.deleteCommittee({
                  committeeId: target().id,
                  expectedVersion: target().version,
                });
                setDeleteTarget(null);
                await refetch();
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

function CommitteeRow(props: {
  committee: Committee;
  expanded: boolean;
  onToggle: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const [positions, { refetch: refetchPositions }] = createResource(
    () => (props.expanded ? props.committee.id : null),
    async (id) => {
      if (!id) return [] as Position[];
      const resp = await positionClient.listPositions({ committeeId: id });
      return resp.items;
    },
  );

  return (
    <>
      <tr class="border-t border-nu-purple-100">
        <td class="px-2 py-2 align-top">
          <button
            type="button"
            onClick={props.onToggle}
            class="text-nu-purple-700"
            aria-label={props.expanded ? "Collapse" : "Expand"}
          >
            {props.expanded ? "▾" : "▸"}
          </button>
        </td>
        <td class="px-2 py-2 align-top font-semibold text-nu-purple">
          {props.committee.name}
        </td>
        <td class="px-2 py-2 align-top">
          {committeeTypeLabel(props.committee.type)}
        </td>
        <td class="px-2 py-2 align-top">
          {committeeSizeLabel(props.committee.size)}
        </td>
        <td class="px-2 py-2 align-top text-nu-purple-700">
          <PositionCount expanded={props.expanded} positions={positions} />
        </td>
        <td class="px-2 py-2 align-top text-xs text-nu-purple-700">
          <Show
            when={props.committee.backgroundGuideRef}
            fallback={<span class="text-nu-purple-300">—</span>}
          >
            {props.committee.backgroundGuideRef}
          </Show>
        </td>
        <td class="px-2 py-2 align-top text-right">
          <button
            type="button"
            onClick={props.onEdit}
            class="rounded border border-nu-purple-300 px-2 py-0.5 text-xs text-nu-purple-700"
          >
            Edit
          </button>{" "}
          <button
            type="button"
            onClick={props.onDelete}
            class="rounded border border-red-300 px-2 py-0.5 text-xs text-red-700"
          >
            Delete
          </button>
        </td>
      </tr>
      <Show when={props.expanded}>
        <tr class="border-t border-nu-purple-100 bg-nu-purple-50">
          <td colspan={7} class="px-6 py-3">
            <PositionsPanel
              committeeId={props.committee.id}
              positions={positions}
              refetch={refetchPositions}
            />
          </td>
        </tr>
      </Show>
    </>
  );
}

function PositionCount(props: {
  expanded: boolean;
  positions: Resource<Position[]>;
}) {
  return (
    <Show
      when={props.expanded}
      fallback={<span class="text-nu-purple-300">click to expand</span>}
    >
      <Show
        when={!props.positions.loading}
        fallback={<span class="text-nu-purple-300">…</span>}
      >
        {(props.positions() ?? []).length}
      </Show>
    </Show>
  );
}

function PositionsPanel(props: {
  committeeId: string;
  positions: Resource<Position[]>;
  refetch: () => void;
}) {
  const [addOpen, setAddOpen] = createSignal(false);
  const [editTarget, setEditTarget] = createSignal<Position | null>(null);
  const [deleteTarget, setDeleteTarget] = createSignal<Position | null>(null);
  const [error, setError] = createSignal<string | null>(null);

  return (
    <div>
      <div class="flex items-center justify-between">
        <h3 class="text-sm font-semibold text-nu-purple">Positions</h3>
        <button
          type="button"
          onClick={() => setAddOpen(true)}
          class="rounded bg-nu-purple px-2 py-0.5 text-xs text-white"
        >
          Add position
        </button>
      </div>

      <Show when={error()}>
        {(e) => (
          <p class="mt-2 rounded bg-red-50 p-2 text-xs text-red-900">{e()}</p>
        )}
      </Show>

      <Show
        when={!props.positions.loading}
        fallback={
          <p class="mt-2 text-xs text-nu-purple-700">Loading positions…</p>
        }
      >
        <Show
          when={(props.positions() ?? []).length > 0}
          fallback={
            <p class="mt-2 text-xs text-nu-purple-700">
              No positions yet for this committee.
            </p>
          }
        >
          <table class="mt-2 w-full text-xs">
            <thead class="text-left uppercase tracking-wide text-nu-purple-700">
              <tr>
                <th class="px-2 py-1">Name</th>
                <th class="w-28 px-2 py-1">Max delegates</th>
                <th class="w-28 px-2 py-1">Dual</th>
                <th class="w-44 px-2 py-1">Prestige</th>
                <th class="w-36 px-2 py-1 text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              <For each={props.positions() ?? []}>
                {(p) => (
                  <tr class="border-t border-nu-purple-100">
                    <td class="px-2 py-1">{p.name}</td>
                    <td class="px-2 py-1">{p.maxDelegates}</td>
                    <td class="px-2 py-1">{p.dualDelegation ? "Yes" : "No"}</td>
                    <td class="px-2 py-1">
                      <PrestigeBadge tier={p.prestigeTier} />
                    </td>
                    <td class="px-2 py-1 text-right">
                      <button
                        type="button"
                        onClick={() => setEditTarget(p)}
                        class="rounded border border-nu-purple-300 px-2 py-0.5 text-nu-purple-700"
                      >
                        Edit
                      </button>{" "}
                      <button
                        type="button"
                        onClick={() => setDeleteTarget(p)}
                        class="rounded border border-red-300 px-2 py-0.5 text-red-700"
                      >
                        Delete
                      </button>
                    </td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </Show>
      </Show>

      <Show when={addOpen()}>
        <PositionModal
          title="Add position"
          initial={{
            name: "",
            maxDelegates: 1,
            dualDelegation: false,
            prestigeTier: PrestigeTier.STANDARD,
          }}
          onClose={() => setAddOpen(false)}
          onSubmit={async (state) => {
            setError(null);
            try {
              await positionClient.createPosition({
                committeeId: props.committeeId,
                ...state,
              });
              setAddOpen(false);
              props.refetch();
            } catch (err) {
              setError(connectMessage(err) ?? "Failed to create position.");
            }
          }}
        />
      </Show>

      <Show when={editTarget()}>
        {(target) => (
          <PositionModal
            title="Edit position"
            initial={{
              name: target().name,
              maxDelegates: target().maxDelegates,
              dualDelegation: target().dualDelegation,
              prestigeTier: target().prestigeTier,
            }}
            onClose={() => setEditTarget(null)}
            onSubmit={async (state) => {
              setError(null);
              try {
                await positionClient.updatePosition({
                  positionId: target().id,
                  expectedVersion: target().version,
                  ...state,
                });
                setEditTarget(null);
                props.refetch();
              } catch (err) {
                setError(handleMutationError(err));
              }
            }}
          />
        )}
      </Show>

      <Show when={deleteTarget()}>
        {(target) => (
          <ConfirmModal
            title="Delete position?"
            message={`This will delete "${target().name}".`}
            onCancel={() => setDeleteTarget(null)}
            onConfirm={async () => {
              setError(null);
              try {
                await positionClient.deletePosition({
                  positionId: target().id,
                  expectedVersion: target().version,
                });
                setDeleteTarget(null);
                props.refetch();
              } catch (err) {
                setError(handleMutationError(err));
              }
            }}
          />
        )}
      </Show>
    </div>
  );
}

function CommitteeModal(props: {
  title: string;
  initial: CommitteeFormState;
  onClose: () => void;
  onSubmit: (s: CommitteeFormState) => Promise<void>;
}) {
  const [name, setName] = createSignal(props.initial.name);
  const [type, setType] = createSignal(props.initial.type);
  const [size, setSize] = createSignal(props.initial.size);
  const [guide, setGuide] = createSignal(props.initial.backgroundGuideRef);
  const [busy, setBusy] = createSignal(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await props.onSubmit({
        name: name(),
        type: type(),
        size: size(),
        backgroundGuideRef: guide(),
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell title={props.title} onClose={props.onClose}>
      <form onSubmit={submit} class="space-y-3 text-sm">
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wide text-nu-purple-700">
            Name
          </span>
          <input
            type="text"
            required
            value={name()}
            onInput={(e) => setName(e.currentTarget.value)}
            class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
          />
        </label>
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wide text-nu-purple-700">
            Type
          </span>
          <select
            value={String(type())}
            onChange={(e) =>
              setType(Number(e.currentTarget.value) as CommitteeType)
            }
            class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
          >
            <option value={String(CommitteeType.NON_CRISIS)}>Non-crisis</option>
            <option value={String(CommitteeType.CRISIS)}>Crisis</option>
          </select>
        </label>
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wide text-nu-purple-700">
            Size
          </span>
          <select
            value={String(size())}
            onChange={(e) =>
              setSize(Number(e.currentTarget.value) as CommitteeSize)
            }
            class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
          >
            <option value={String(CommitteeSize.SMALL)}>Small</option>
            <option value={String(CommitteeSize.MEDIUM)}>Medium</option>
            <option value={String(CommitteeSize.LARGE)}>Large</option>
          </select>
        </label>
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wide text-nu-purple-700">
            Background guide ref
          </span>
          <input
            type="text"
            value={guide()}
            onInput={(e) => setGuide(e.currentTarget.value)}
            placeholder="/content/background-guides/…"
            class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
          />
        </label>
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

function PositionModal(props: {
  title: string;
  initial: PositionFormState;
  onClose: () => void;
  onSubmit: (s: PositionFormState) => Promise<void>;
}) {
  const [name, setName] = createSignal(props.initial.name);
  const [maxDelegates, setMaxDelegates] = createSignal(
    props.initial.maxDelegates,
  );
  const [dual, setDual] = createSignal(props.initial.dualDelegation);
  const [tier, setTier] = createSignal(props.initial.prestigeTier);
  const [busy, setBusy] = createSignal(false);

  function toggleDual(next: boolean) {
    setDual(next);
    if (next) setMaxDelegates(2);
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await props.onSubmit({
        name: name(),
        maxDelegates: dual() ? 2 : maxDelegates(),
        dualDelegation: dual(),
        prestigeTier: tier(),
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell title={props.title} onClose={props.onClose}>
      <form onSubmit={submit} class="space-y-3 text-sm">
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wide text-nu-purple-700">
            Name
          </span>
          <input
            type="text"
            required
            value={name()}
            onInput={(e) => setName(e.currentTarget.value)}
            class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
          />
        </label>
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wide text-nu-purple-700">
            Max delegates
          </span>
          <input
            type="number"
            min="1"
            max="4"
            required
            disabled={dual()}
            value={maxDelegates()}
            onInput={(e) => setMaxDelegates(Number(e.currentTarget.value) || 1)}
            class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1 disabled:bg-nu-purple-50"
          />
          <Show when={dual()}>
            <span class="mt-1 block text-xs text-nu-purple-700">
              Locked to 2 because dual delegation is on.
            </span>
          </Show>
        </label>
        <label class="flex items-center gap-2">
          <input
            type="checkbox"
            checked={dual()}
            onChange={(e) => toggleDual(e.currentTarget.checked)}
          />
          <span>Dual delegation</span>
        </label>
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wide text-nu-purple-700">
            Prestige tier
          </span>
          <select
            value={String(tier())}
            onChange={(e) =>
              setTier(Number(e.currentTarget.value) as PrestigeTier)
            }
            class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
          >
            <option value={String(PrestigeTier.STANDARD)}>Standard</option>
            <option value={String(PrestigeTier.ELEVATED)}>Elevated</option>
            <option value={String(PrestigeTier.RESERVED)}>Reserved</option>
          </select>
          <Show when={tier() === PrestigeTier.RESERVED}>
            <span class="mt-1 block text-xs text-amber-700">
              Reserved — algorithm will skip; assign manually.
            </span>
          </Show>
        </label>
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

function ConfirmModal(props: {
  title: string;
  message: string;
  onCancel: () => void;
  onConfirm: () => Promise<void>;
}) {
  const [busy, setBusy] = createSignal(false);
  async function confirm() {
    setBusy(true);
    try {
      await props.onConfirm();
    } finally {
      setBusy(false);
    }
  }
  return (
    <ModalShell title={props.title} onClose={props.onCancel}>
      <p class="text-sm text-nu-purple-700">{props.message}</p>
      <div class="mt-4 flex justify-end gap-2">
        <button
          type="button"
          onClick={props.onCancel}
          class="rounded border border-nu-purple-300 px-3 py-1 text-sm text-nu-purple-700"
        >
          Cancel
        </button>
        <button
          type="button"
          disabled={busy()}
          onClick={confirm}
          class="rounded bg-red-700 px-3 py-1 text-sm text-white disabled:opacity-50"
        >
          {busy() ? "…" : "Delete"}
        </button>
      </div>
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

function PrestigeBadge(props: { tier: PrestigeTier }) {
  if (props.tier === PrestigeTier.RESERVED) {
    return (
      <span
        class="inline-block rounded bg-amber-100 px-2 py-0.5 text-amber-900"
        title="Algorithm will skip; assign manually."
      >
        Reserved
      </span>
    );
  }
  if (props.tier === PrestigeTier.ELEVATED) {
    return (
      <span class="inline-block rounded bg-nu-purple-50 px-2 py-0.5 text-nu-purple-700">
        Elevated
      </span>
    );
  }
  return (
    <span class="inline-block rounded bg-nu-purple-50 px-2 py-0.5 text-nu-purple-700">
      Standard
    </span>
  );
}

function committeeTypeLabel(t: CommitteeType): string {
  switch (t) {
    case CommitteeType.CRISIS:
      return "Crisis";
    case CommitteeType.NON_CRISIS:
      return "Non-crisis";
    default:
      return "—";
  }
}

function committeeSizeLabel(s: CommitteeSize): string {
  switch (s) {
    case CommitteeSize.SMALL:
      return "Small";
    case CommitteeSize.MEDIUM:
      return "Medium";
    case CommitteeSize.LARGE:
      return "Large";
    default:
      return "—";
  }
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
