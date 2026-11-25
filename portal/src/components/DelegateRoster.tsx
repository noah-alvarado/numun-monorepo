// The Delegates tab: the delegation's roster with each delegate's
// assignment, plus add / edit / remove. Staff-admins can show removed
// delegates (from Remove or a Full-sync import) and restore them.

import { createMemo, createSignal, For, onMount, Show } from "solid-js";
import { timestampDate } from "@bufbuild/protobuf/wkt";

import { assignmentClient, delegateClient } from "@/lib/api";
import { userSignal } from "@/lib/session";
import { conflictMessage, connectMessage, isConflict } from "@/lib/errors";
import Modal from "@/components/Modal";
import { ExperienceLevel, type Delegate } from "@/gen/numun/v1/delegates_pb";
import {
  AssignmentStatus,
  type Assignment,
} from "@/gen/numun/v1/assignments_pb";
import type { Delegation } from "@/gen/numun/v1/delegations_pb";
import { User_Role } from "@/gen/numun/v1/users_pb";

type Draft = {
  firstName: string;
  lastName: string;
  email: string;
  experienceLevel: ExperienceLevel;
};

const emptyDraft = (): Draft => ({
  firstName: "",
  lastName: "",
  email: "",
  experienceLevel: ExperienceLevel.INTERMEDIATE,
});

const LEVELS: [ExperienceLevel, string][] = [
  [ExperienceLevel.NOVICE, "Novice"],
  [ExperienceLevel.INTERMEDIATE, "Intermediate"],
  [ExperienceLevel.ADVANCED, "Advanced"],
];

const levelLabel = (l: ExperienceLevel) =>
  LEVELS.find(([v]) => v === l)?.[1] ?? "—";

const input = "w-full rounded border border-nu-purple-300 px-2 py-1 text-sm";

export default function DelegateRoster(props: {
  delegation: Delegation;
  readOnly: boolean;
}) {
  const [user] = userSignal;
  const isAdmin = () => user()?.role === User_Role.STAFF_ADMIN;

  const [delegates, setDelegates] = createSignal<Delegate[]>([]);
  const [assignments, setAssignments] = createSignal<Assignment[]>([]);
  const [loaded, setLoaded] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [notice, setNotice] = createSignal<string | null>(null);
  const [showRemoved, setShowRemoved] = createSignal(false);
  const [busy, setBusy] = createSignal(false);

  const [adding, setAdding] = createSignal(false);
  const [draft, setDraft] = createSignal<Draft>(emptyDraft());
  const [editingId, setEditingId] = createSignal<string | null>(null);
  const [editDraft, setEditDraft] = createSignal<Draft>(emptyDraft());
  const [removeTarget, setRemoveTarget] = createSignal<Delegate | null>(null);

  async function load() {
    setError(null);
    try {
      const [roster, assigned] = await Promise.all([
        delegateClient.listAllDelegates({
          delegationId: props.delegation.id,
          includeDeleted: isAdmin() && showRemoved(),
        }),
        // Assignments are extra context; the roster still loads without them.
        assignmentClient
          .listAssignments({
            conferenceId: props.delegation.conferenceId,
            delegationId: props.delegation.id,
          })
          .catch(() => ({ items: [] as Assignment[] })),
      ]);
      setDelegates(
        [...roster.items].sort(
          (a, b) =>
            Number(a.isDeleted) - Number(b.isDeleted) ||
            a.lastName.localeCompare(b.lastName) ||
            a.firstName.localeCompare(b.firstName),
        ),
      );
      setAssignments(assigned.items);
    } catch (err) {
      setError(connectMessage(err) ?? "Failed to load the roster.");
    } finally {
      setLoaded(true);
    }
  }

  onMount(load);

  const assignmentFor = createMemo(() => {
    const byDelegate = new Map<string, Assignment>();
    for (const a of assignments()) byDelegate.set(a.delegateId, a);
    return byDelegate;
  });

  const active = () => delegates().filter((d) => !d.isDeleted);

  function mutationError(err: unknown, fallback: string) {
    setError(connectMessage(err) ?? fallback);
  }

  async function run(fn: () => Promise<void>, fallback: string) {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await fn();
      await load();
    } catch (err) {
      if (isConflict(err)) {
        setEditingId(null);
        await load();
        setError(conflictMessage("this roster"));
        return;
      }
      mutationError(err, fallback);
    } finally {
      setBusy(false);
    }
  }

  function add(e: SubmitEvent) {
    e.preventDefault();
    const d = draft();
    void run(async () => {
      await delegateClient.createDelegate({
        delegationId: props.delegation.id,
        input: {
          firstName: d.firstName.trim(),
          lastName: d.lastName.trim(),
          email: d.email.trim(),
          experienceLevel: d.experienceLevel,
        },
      });
      setNotice(`Added ${d.firstName.trim()} ${d.lastName.trim()}.`);
      setDraft(emptyDraft());
      setAdding(false);
    }, "Failed to add the delegate.");
  }

  function startEdit(d: Delegate) {
    setEditingId(d.id);
    setEditDraft({
      firstName: d.firstName,
      lastName: d.lastName,
      email: d.email,
      experienceLevel: d.experienceLevel,
    });
  }

  function saveEdit(d: Delegate) {
    const e = editDraft();
    void run(async () => {
      await delegateClient.updateDelegate({
        delegateId: d.id,
        firstName: e.firstName.trim(),
        lastName: e.lastName.trim(),
        email: e.email.trim(),
        experienceLevel: e.experienceLevel,
        expectedVersion: d.version,
      });
      setEditingId(null);
    }, "Failed to save the delegate.");
  }

  function remove(d: Delegate) {
    void run(async () => {
      await delegateClient.deleteDelegate({
        delegateId: d.id,
        expectedVersion: d.version,
      });
      setRemoveTarget(null);
      setNotice(`Removed ${d.firstName} ${d.lastName}.`);
    }, "Failed to remove the delegate.");
  }

  function restore(d: Delegate) {
    void run(async () => {
      await delegateClient.restoreDelegate({
        delegationId: d.delegationId,
        delegateId: d.id,
        expectedVersion: d.version,
      });
      setNotice(`Restored ${d.firstName} ${d.lastName}.`);
    }, "Failed to restore the delegate.");
  }

  function toggleRemoved(on: boolean) {
    setShowRemoved(on);
    void load();
  }

  return (
    <div class="space-y-3 rounded border border-nu-purple-200 bg-white p-4 text-sm">
      <div class="flex flex-wrap items-center gap-3">
        <h2 class="text-base font-semibold text-nu-purple">
          Delegates{" "}
          <span class="font-normal text-nu-purple-700">
            ({active().length})
          </span>
        </h2>
        <Show when={isAdmin()}>
          <label class="flex items-center gap-1 text-xs text-nu-purple-700">
            <input
              type="checkbox"
              checked={showRemoved()}
              onChange={(e) => toggleRemoved(e.currentTarget.checked)}
            />
            Show removed
          </label>
        </Show>
        <Show when={!props.readOnly}>
          <div class="ml-auto flex gap-2">
            <a
              href={`/delegations/${props.delegation.id}/delegates/import`}
              class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700 hover:bg-nu-purple-50"
            >
              Bulk import delegates
            </a>
            <button
              type="button"
              onClick={() => setAdding(!adding())}
              class="rounded bg-nu-purple px-3 py-1 text-white"
            >
              Add delegate
            </button>
          </div>
        </Show>
      </div>

      <Show when={error()}>
        {(e) => <p class="rounded bg-red-50 p-2 text-red-900">{e()}</p>}
      </Show>
      <Show when={notice()}>
        {(n) => <p class="rounded bg-green-50 p-2 text-green-900">{n()}</p>}
      </Show>

      <Show when={adding()}>
        <form
          aria-label="New delegate"
          onSubmit={add}
          class="grid grid-cols-2 gap-2 rounded border border-nu-purple-200 bg-nu-purple-50 p-3 md:grid-cols-5"
        >
          <DraftFields draft={draft()} onChange={setDraft} />
          <div class="col-span-2 flex justify-end gap-2 md:col-span-5">
            <button
              type="button"
              onClick={() => setAdding(false)}
              class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={busy()}
              class="rounded bg-nu-purple px-3 py-1 text-white disabled:opacity-50"
            >
              {busy() ? "Saving…" : "Save delegate"}
            </button>
          </div>
        </form>
      </Show>

      <Show when={!loaded()}>
        <p class="text-nu-purple-700">Loading…</p>
      </Show>

      <Show when={loaded() && delegates().length === 0}>
        <p class="text-nu-purple-700">
          No delegates yet.
          <Show when={!props.readOnly}>
            {" "}
            Add them one at a time, or bulk import a roster from a spreadsheet.
          </Show>
        </p>
      </Show>

      <Show when={delegates().length > 0}>
        <table class="w-full">
          <thead>
            <tr class="border-b border-nu-purple-200 text-left text-xs uppercase tracking-wide text-nu-purple-700">
              <th class="py-2 pr-2">Name</th>
              <th class="py-2 pr-2">Email</th>
              <th class="py-2 pr-2">Experience</th>
              <th class="py-2 pr-2">Assignment</th>
              <th class="py-2 pr-2">Checked in</th>
              <th class="py-2" />
            </tr>
          </thead>
          <tbody>
            <For each={delegates()}>
              {(d) => (
                <Show
                  when={editingId() === d.id}
                  fallback={
                    <tr
                      class={
                        d.isDeleted
                          ? "border-b border-nu-purple-100 text-nu-purple-400"
                          : "border-b border-nu-purple-100"
                      }
                    >
                      <td class="py-2 pr-2">
                        {d.firstName} {d.lastName}
                        <Show when={d.isDeleted}>
                          <span class="ml-2 rounded bg-nu-purple-50 px-1 text-xs">
                            Removed
                          </span>
                        </Show>
                      </td>
                      <td class="py-2 pr-2">{d.email || "—"}</td>
                      <td class="py-2 pr-2">{levelLabel(d.experienceLevel)}</td>
                      <td class="py-2 pr-2">
                        <AssignmentCell a={assignmentFor().get(d.id)} />
                      </td>
                      <td class="py-2 pr-2">
                        {d.checkedInAt
                          ? timestampDate(d.checkedInAt).toLocaleString()
                          : "—"}
                      </td>
                      <td class="py-2 text-right">
                        <Show
                          when={!d.isDeleted}
                          fallback={
                            <button
                              type="button"
                              disabled={busy()}
                              onClick={() => restore(d)}
                              class="rounded border border-nu-purple-300 px-2 py-0.5 text-nu-purple-700 disabled:opacity-50"
                            >
                              Restore
                            </button>
                          }
                        >
                          <Show when={!props.readOnly}>
                            <div class="flex justify-end gap-1">
                              <button
                                type="button"
                                onClick={() => startEdit(d)}
                                aria-label={`Edit ${d.firstName} ${d.lastName}`}
                                class="rounded border border-nu-purple-300 px-2 py-0.5 text-nu-purple-700"
                              >
                                Edit
                              </button>
                              <button
                                type="button"
                                onClick={() => setRemoveTarget(d)}
                                aria-label={`Remove ${d.firstName} ${d.lastName}`}
                                class="rounded border border-nu-purple-300 px-2 py-0.5 text-nu-purple-700"
                              >
                                Remove
                              </button>
                            </div>
                          </Show>
                        </Show>
                      </td>
                    </tr>
                  }
                >
                  <tr class="border-b border-nu-purple-100 bg-nu-purple-50">
                    <td colSpan={6} class="py-2">
                      <form
                        aria-label={`Edit ${d.firstName} ${d.lastName}`}
                        onSubmit={(e) => {
                          e.preventDefault();
                          saveEdit(d);
                        }}
                        class="grid grid-cols-2 gap-2 md:grid-cols-5"
                      >
                        <DraftFields
                          draft={editDraft()}
                          onChange={setEditDraft}
                        />
                        <div class="col-span-2 flex justify-end gap-2 md:col-span-5">
                          <button
                            type="button"
                            onClick={() => setEditingId(null)}
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
                    </td>
                  </tr>
                </Show>
              )}
            </For>
          </tbody>
        </table>
      </Show>

      <Show when={removeTarget()}>
        {(d) => (
          <Modal
            title={`Remove ${d().firstName} ${d().lastName}?`}
            onClose={() => setRemoveTarget(null)}
          >
            <p class="mt-2 text-sm text-nu-purple-700">
              They'll leave the roster. NUMUN staff can restore them if this was
              a mistake.
            </p>
            <Show when={assignmentFor().get(d().id)}>
              {(a) => (
                <p class="mt-2 text-sm text-amber-900">
                  Their assignment ({a().committeeName} · {a().positionName})
                  stays in place until NUMUN staff change it.
                </p>
              )}
            </Show>
            <div class="mt-4 flex justify-end gap-2 text-sm">
              <button
                type="button"
                onClick={() => setRemoveTarget(null)}
                class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
              >
                Cancel
              </button>
              <button
                type="button"
                disabled={busy()}
                onClick={() => remove(d())}
                class="rounded bg-red-700 px-3 py-1 text-white disabled:opacity-50"
              >
                {busy() ? "Removing…" : "Remove"}
              </button>
            </div>
          </Modal>
        )}
      </Show>
    </div>
  );
}

function DraftFields(props: { draft: Draft; onChange: (d: Draft) => void }) {
  const set = (patch: Partial<Draft>) =>
    props.onChange({ ...props.draft, ...patch });
  return (
    <>
      <label class="block">
        <span class="text-xs text-nu-purple-700">First name</span>
        <input
          required
          value={props.draft.firstName}
          onInput={(e) => set({ firstName: e.currentTarget.value })}
          class={input}
        />
      </label>
      <label class="block">
        <span class="text-xs text-nu-purple-700">Last name</span>
        <input
          required
          value={props.draft.lastName}
          onInput={(e) => set({ lastName: e.currentTarget.value })}
          class={input}
        />
      </label>
      <label class="block md:col-span-2">
        <span class="text-xs text-nu-purple-700">Email (optional)</span>
        <input
          type="email"
          value={props.draft.email}
          onInput={(e) => set({ email: e.currentTarget.value })}
          class={input}
        />
      </label>
      <label class="block">
        <span class="text-xs text-nu-purple-700">Experience</span>
        <select
          value={String(props.draft.experienceLevel)}
          onChange={(e) =>
            set({
              experienceLevel: Number(e.currentTarget.value) as ExperienceLevel,
            })
          }
          class={input}
        >
          <For each={LEVELS}>
            {([v, label]) => <option value={String(v)}>{label}</option>}
          </For>
        </select>
      </label>
    </>
  );
}

function AssignmentCell(props: { a: Assignment | undefined }) {
  return (
    <Show when={props.a} fallback={<span class="text-nu-purple-400">—</span>}>
      {(a) => (
        <span>
          {a().committeeName} · {a().positionName}
          <Show when={a().status === AssignmentStatus.PROPOSED}>
            <span class="ml-1 rounded bg-amber-50 px-1 text-xs text-amber-900">
              proposed
            </span>
          </Show>
        </span>
      )}
    </Show>
  );
}
