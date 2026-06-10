// Awards — single route serving three role variants per M11:
//
// - Advisor: read-only list of conference awards, plus a permanent
//   congratulatory hero when one of their delegates / delegations won.
//   Dismissal persists server-side via UserService.DismissAward.
// - Staff-staffer: can award delegates / delegate-pairs / delegations /
//   staffers tied to delegations they're assigned to. Reads see everything.
// - Staff-admin: full CRUD, any recipient kind, any ID.
//
// CMS sync status from each mutation is surfaced inline so admins know
// whether the public site was updated.

import {
  createMemo,
  createResource,
  createSignal,
  For,
  Show,
  type JSX,
} from "solid-js";
import { useNavigate } from "@solidjs/router";
import { ConnectError, Code } from "@connectrpc/connect";

import {
  awardClient,
  delegationClient,
  delegateClient,
  exportUrls,
  userClient,
} from "@/lib/api";
import { activeConferenceSignal, userSignal } from "@/lib/session";
import {
  AwardRecipientKind,
  type Award,
  type AwardRecipient,
  type CmsSyncStatus,
} from "@/gen/numun/v1/awards_pb";
import { User_Role } from "@/gen/numun/v1/users_pb";
import type { Delegation } from "@/gen/numun/v1/delegations_pb";

type RecipientFormRow = {
  kind: AwardRecipientKind;
  id: string;
  displayName: string;
};

type AwardFormState = {
  name: string;
  category: string;
  recipients: RecipientFormRow[];
};

export default function Awards() {
  const [user] = userSignal;
  const [conference] = activeConferenceSignal;
  const navigate = useNavigate();

  const conferenceId = () => conference()?.conferenceId ?? "";

  const [awards, { refetch: refetchAwards }] = createResource(
    () => conferenceId(),
    async (id) => {
      if (!id) return [] as Award[];
      const resp = await awardClient.listAwards({ conferenceId: id });
      return resp.items;
    },
  );

  // Caller's in-scope delegations — used by advisor + staffer flows.
  const [myDelegations] = createResource(
    () => conferenceId(),
    async (id) => {
      if (!id) return [] as Delegation[];
      try {
        const resp = await delegationClient.listDelegations({
          conferenceId: id,
        });
        return resp.items;
      } catch {
        return [] as Delegation[];
      }
    },
  );

  // For the advisor hero: union of delegate IDs in the advisor's delegations.
  // Best-effort — failures collapse to "no winners shown".
  const [myDelegateIds] = createResource(
    () => myDelegations(),
    async (delegations) => {
      if (!delegations || delegations.length === 0) return new Set<string>();
      const ids = new Set<string>();
      for (const d of delegations) {
        try {
          const resp = await delegateClient.listAllDelegates({
            delegationId: d.id,
          });
          for (const dg of resp.items) ids.add(dg.id);
        } catch {
          /* surface nothing */
        }
      }
      return ids;
    },
  );

  const isAdvisor = () => user()?.role === User_Role.ADVISOR;
  const isStaffer = () => user()?.role === User_Role.STAFF_STAFFER;
  const isAdmin = () => user()?.role === User_Role.STAFF_ADMIN;

  // Filter awards relevant to the advisor for the hero. An award is "mine"
  // when a recipient is a delegate from one of my delegations OR is one of
  // my delegations directly.
  const advisorWinners = createMemo<Award[]>(() => {
    if (!isAdvisor()) return [];
    const list = awards() ?? [];
    const myDelIds = new Set((myDelegations() ?? []).map((d) => d.id));
    const myDgIds = myDelegateIds() ?? new Set<string>();
    const dismissed = new Set(user()?.dismissedAwardIds ?? []);
    return list.filter((a) => {
      if (dismissed.has(a.id)) return false;
      return a.recipients.some((r) => {
        if (
          r.kind === AwardRecipientKind.DELEGATE &&
          myDgIds.has(r.id)
        )
          return true;
        if (
          r.kind === AwardRecipientKind.DELEGATION &&
          myDelIds.has(r.id)
        )
          return true;
        return false;
      });
    });
  });

  const [error, setError] = createSignal<string | null>(null);
  const [createOpen, setCreateOpen] = createSignal(false);
  const [editTarget, setEditTarget] = createSignal<Award | null>(null);
  const [deleteTarget, setDeleteTarget] = createSignal<Award | null>(null);
  const [cmsBanner, setCmsBanner] = createSignal<CmsSyncStatus | null>(null);

  async function handleCreate(state: AwardFormState) {
    setError(null);
    try {
      const resp = await awardClient.createAward({
        conferenceId: conferenceId(),
        name: state.name,
        category: state.category,
        recipients: state.recipients.map(formRowToProto),
      });
      setCreateOpen(false);
      setCmsBanner(resp.cmsSync ?? null);
      await refetchAwards();
    } catch (err) {
      setError(connectMessage(err) ?? "Failed to create award.");
    }
  }

  async function handleUpdate(target: Award, state: AwardFormState) {
    setError(null);
    try {
      const resp = await awardClient.updateAward({
        awardId: target.id,
        expectedVersion: target.version,
        name: state.name,
        category: state.category,
        recipients: state.recipients.map(formRowToProto),
        recipientsSet: true,
      });
      setEditTarget(null);
      setCmsBanner(resp.cmsSync ?? null);
      await refetchAwards();
    } catch (err) {
      setError(handleMutationError(err));
    }
  }

  async function handleDelete(target: Award) {
    setError(null);
    try {
      const resp = await awardClient.deleteAward({
        awardId: target.id,
        expectedVersion: target.version,
      });
      setDeleteTarget(null);
      setCmsBanner(resp.cmsSync ?? null);
      await refetchAwards();
    } catch (err) {
      setError(handleMutationError(err));
    }
  }

  async function dismiss(awardId: string) {
    try {
      await userClient.dismissAward({ awardId });
      // The user signal is refreshed by re-reading the response — but for
      // simplicity, hide the local card optimistically by reloading the user.
      // The next page render will see the updated dismissedAwardIds.
      const me = await userClient.getMe({});
      if (me.user) {
        userSignal[1](me.user);
      }
    } catch {
      /* best-effort; surface nothing */
    }
  }

  // Per-row "can I edit / delete this?" check. Admin: yes. Staffer: yes if
  // every recipient is within their scope (the server will re-check; this is
  // just to hide buttons that would 403). Advisor: no.
  function canWrite(award: Award): boolean {
    if (isAdmin()) return true;
    if (isAdvisor()) return false;
    if (!isStaffer()) return false;
    const myDelIds = new Set((myDelegations() ?? []).map((d) => d.id));
    const myDgIds = myDelegateIds() ?? new Set<string>();
    return award.recipients.every((r) => {
      if (r.kind === AwardRecipientKind.DELEGATE) return myDgIds.has(r.id);
      if (r.kind === AwardRecipientKind.DELEGATION) return myDelIds.has(r.id);
      // USER recipients: too expensive to verify client-side; let server gate.
      if (r.kind === AwardRecipientKind.USER) return true;
      return false;
    });
  }

  return (
    <main class="mx-auto max-w-5xl px-6 py-8">
      <header class="flex flex-wrap items-baseline justify-between gap-2">
        <div>
          <h1 class="text-2xl font-bold text-nu-purple">Awards</h1>
          <p class="mt-1 text-sm text-nu-purple-700">
            <Show
              when={conference()?.conferenceId}
              fallback="No active conference."
            >
              {conference()?.name}
            </Show>
          </p>
        </div>
        <div class="flex flex-wrap gap-2 text-sm">
          <button
            type="button"
            onClick={() => navigate("/dashboard")}
            class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
          >
            Back
          </button>
          <Show when={isAdmin() && conferenceId()}>
            <a
              href={exportUrls.assignmentsCsv(conferenceId())}
              class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
            >
              Export assignments.csv
            </a>
            <a
              href={exportUrls.delegatesCsv(conferenceId())}
              class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
            >
              Export delegates.csv
            </a>
            <a
              href={exportUrls.paymentsCsv(conferenceId())}
              class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
            >
              Export payments.csv
            </a>
          </Show>
          <Show when={!isAdvisor() && conferenceId()}>
            <button
              type="button"
              onClick={() => setCreateOpen(true)}
              class="rounded bg-nu-purple px-3 py-1 text-white"
            >
              New award
            </button>
          </Show>
        </div>
      </header>

      <Show when={error()}>
        {(e) => (
          <p class="mt-4 rounded bg-red-50 p-3 text-sm text-red-900">{e()}</p>
        )}
      </Show>

      <Show when={cmsBanner()}>
        {(s) => (
          <Show
            when={s().ok}
            fallback={
              <p class="mt-4 rounded bg-amber-50 p-3 text-sm text-amber-900">
                Saved to database, but CMS sync failed after {s().attempts}{" "}
                attempts: {s().finalError}. Retry by editing the award.
              </p>
            }
          >
            <p class="mt-4 rounded bg-green-50 p-3 text-sm text-green-900">
              Saved and synced to the public site.
              <Show when={s().commitSha}>
                {" "}
                <code class="text-xs">{s().commitSha.slice(0, 7)}</code>
              </Show>
            </p>
          </Show>
        )}
      </Show>

      <Show when={isAdvisor() && advisorWinners().length > 0}>
        <section class="mt-6 rounded border border-nu-purple-300 bg-nu-purple-50 p-4">
          <h2 class="font-display text-xl font-semibold text-nu-purple">
            Congratulations!
          </h2>
          <p class="mt-1 text-sm text-nu-purple-700">
            One of your delegations won at {conference()?.name}.
          </p>
          <ul class="mt-3 space-y-2">
            <For each={advisorWinners()}>
              {(a) => (
                <li class="flex items-start justify-between gap-3 rounded border border-nu-purple-200 bg-white p-3">
                  <div>
                    <div class="font-semibold text-nu-purple">{a.name}</div>
                    <RecipientList recipients={a.recipients} />
                  </div>
                  <button
                    type="button"
                    onClick={() => dismiss(a.id)}
                    class="rounded border border-nu-purple-300 px-2 py-0.5 text-xs text-nu-purple-700"
                  >
                    Dismiss
                  </button>
                </li>
              )}
            </For>
          </ul>
        </section>
      </Show>

      <Show
        when={!awards.loading}
        fallback={
          <p class="mt-6 text-sm text-nu-purple-700">Loading awards…</p>
        }
      >
        <Show
          when={(awards() ?? []).length > 0}
          fallback={
            <p class="mt-6 text-sm text-nu-purple-700">
              No awards recorded for this conference yet.
            </p>
          }
        >
          <section class="mt-6 overflow-x-auto rounded border border-nu-purple-200 bg-white">
            <table class="w-full table-fixed text-sm">
              <thead class="bg-nu-purple-50 text-left text-xs uppercase tracking-wide text-nu-purple-700">
                <tr>
                  <th class="px-2 py-2">Name</th>
                  <th class="px-2 py-2">Category</th>
                  <th class="px-2 py-2">Recipients</th>
                  <th class="w-40 px-2 py-2 text-right">Actions</th>
                </tr>
              </thead>
              <tbody>
                <For each={awards() ?? []}>
                  {(a) => (
                    <tr class="border-t border-nu-purple-100 align-top">
                      <td class="px-2 py-2 font-semibold text-nu-purple">
                        {a.name}
                      </td>
                      <td class="px-2 py-2 text-nu-purple-700">
                        <Show
                          when={a.category}
                          fallback={
                            <span class="text-nu-purple-300">—</span>
                          }
                        >
                          {a.category}
                        </Show>
                      </td>
                      <td class="px-2 py-2">
                        <RecipientList recipients={a.recipients} />
                      </td>
                      <td class="px-2 py-2 text-right">
                        <Show when={canWrite(a)}>
                          <button
                            type="button"
                            onClick={() => setEditTarget(a)}
                            class="rounded border border-nu-purple-300 px-2 py-0.5 text-xs text-nu-purple-700"
                          >
                            Edit
                          </button>{" "}
                          <button
                            type="button"
                            onClick={() => setDeleteTarget(a)}
                            class="rounded border border-red-300 px-2 py-0.5 text-xs text-red-700"
                          >
                            Delete
                          </button>
                        </Show>
                      </td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
          </section>
        </Show>
      </Show>

      <Show when={createOpen()}>
        <AwardModal
          title="Create award"
          initial={{
            name: "",
            category: "",
            recipients: [
              { kind: AwardRecipientKind.DELEGATE, id: "", displayName: "" },
            ],
          }}
          allowedKinds={kindsForCaller(user()?.role)}
          onClose={() => setCreateOpen(false)}
          onSubmit={handleCreate}
        />
      </Show>

      <Show when={editTarget()}>
        {(target) => (
          <AwardModal
            title="Edit award"
            initial={{
              name: target().name,
              category: target().category,
              recipients: target().recipients.map((r) => ({
                kind: r.kind,
                id: r.id,
                displayName: r.displayName,
              })),
            }}
            allowedKinds={kindsForCaller(user()?.role)}
            onClose={() => setEditTarget(null)}
            onSubmit={(s) => handleUpdate(target(), s)}
          />
        )}
      </Show>

      <Show when={deleteTarget()}>
        {(target) => (
          <ConfirmModal
            title="Delete award?"
            message={`This will delete "${target().name}" from both the database and the public site.`}
            onCancel={() => setDeleteTarget(null)}
            onConfirm={() => handleDelete(target())}
          />
        )}
      </Show>
    </main>
  );
}

function RecipientList(props: { recipients: AwardRecipient[] }) {
  return (
    <ul class="text-sm text-nu-purple-700">
      <For each={props.recipients}>
        {(r) => (
          <li>
            <span class="font-medium">{r.displayName || r.id}</span>{" "}
            <span class="text-xs text-nu-purple-400">
              ({kindLabel(r.kind)})
            </span>
          </li>
        )}
      </For>
    </ul>
  );
}

function AwardModal(props: {
  title: string;
  initial: AwardFormState;
  allowedKinds: AwardRecipientKind[];
  onClose: () => void;
  onSubmit: (s: AwardFormState) => Promise<void>;
}) {
  const [name, setName] = createSignal(props.initial.name);
  const [category, setCategory] = createSignal(props.initial.category);
  const [recipients, setRecipients] = createSignal<RecipientFormRow[]>(
    props.initial.recipients.length > 0
      ? props.initial.recipients
      : [{ kind: props.allowedKinds[0], id: "", displayName: "" }],
  );
  const [busy, setBusy] = createSignal(false);

  function updateRow(i: number, patch: Partial<RecipientFormRow>) {
    setRecipients((cur) =>
      cur.map((r, idx) => (idx === i ? { ...r, ...patch } : r)),
    );
  }
  function addRow() {
    setRecipients((cur) => [
      ...cur,
      { kind: props.allowedKinds[0], id: "", displayName: "" },
    ]);
  }
  function removeRow(i: number) {
    setRecipients((cur) => cur.filter((_, idx) => idx !== i));
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await props.onSubmit({
        name: name().trim(),
        category: category().trim(),
        recipients: recipients()
          .filter((r) => r.id.trim() !== "")
          .map((r) => ({
            kind: r.kind,
            id: r.id.trim(),
            displayName: r.displayName.trim(),
          })),
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
            Category
          </span>
          <input
            type="text"
            value={category()}
            onInput={(e) => setCategory(e.currentTarget.value)}
            class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
          />
        </label>

        <fieldset class="rounded border border-nu-purple-200 p-3">
          <legend class="px-1 text-xs font-semibold uppercase tracking-wide text-nu-purple-700">
            Recipients
          </legend>
          <For each={recipients()}>
            {(row, i) => (
              <div class="mt-2 grid grid-cols-[8rem_1fr_1fr_auto] gap-2">
                <select
                  value={String(row.kind)}
                  onChange={(e) =>
                    updateRow(i(), {
                      kind: Number(e.currentTarget.value) as AwardRecipientKind,
                    })
                  }
                  class="rounded border border-nu-purple-300 px-2 py-1"
                >
                  <For each={props.allowedKinds}>
                    {(k) => (
                      <option value={String(k)}>{kindLabel(k)}</option>
                    )}
                  </For>
                </select>
                <input
                  type="text"
                  required
                  placeholder="ID"
                  value={row.id}
                  onInput={(e) => updateRow(i(), { id: e.currentTarget.value })}
                  class="rounded border border-nu-purple-300 px-2 py-1"
                />
                <input
                  type="text"
                  placeholder="Display name (optional)"
                  value={row.displayName}
                  onInput={(e) =>
                    updateRow(i(), { displayName: e.currentTarget.value })
                  }
                  class="rounded border border-nu-purple-300 px-2 py-1"
                />
                <button
                  type="button"
                  onClick={() => removeRow(i())}
                  class="rounded border border-nu-purple-300 px-2 text-nu-purple-700"
                  aria-label="Remove recipient"
                >
                  ×
                </button>
              </div>
            )}
          </For>
          <button
            type="button"
            onClick={addRow}
            class="mt-3 rounded border border-nu-purple-300 px-2 py-0.5 text-xs text-nu-purple-700"
          >
            Add recipient
          </button>
        </fieldset>

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
  children: JSX.Element;
}) {
  return (
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4"
      onClick={props.onClose}
    >
      <div
        class="w-full max-w-lg rounded border border-nu-purple-200 bg-white p-4 shadow-lg"
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

function kindsForCaller(role: User_Role | undefined): AwardRecipientKind[] {
  if (role === User_Role.STAFF_ADMIN) {
    return [
      AwardRecipientKind.DELEGATE,
      AwardRecipientKind.DELEGATION,
      AwardRecipientKind.COMMITTEE,
      AwardRecipientKind.USER,
      AwardRecipientKind.CONFERENCE,
    ];
  }
  if (role === User_Role.STAFF_STAFFER) {
    return [
      AwardRecipientKind.DELEGATE,
      AwardRecipientKind.DELEGATION,
      AwardRecipientKind.USER,
    ];
  }
  return [];
}

function kindLabel(k: AwardRecipientKind): string {
  switch (k) {
    case AwardRecipientKind.DELEGATE:
      return "delegate";
    case AwardRecipientKind.DELEGATION:
      return "delegation";
    case AwardRecipientKind.COMMITTEE:
      return "committee";
    case AwardRecipientKind.USER:
      return "user";
    case AwardRecipientKind.CONFERENCE:
      return "conference";
    default:
      return "unknown";
  }
}

function formRowToProto(r: RecipientFormRow): AwardRecipient {
  return {
    kind: r.kind,
    id: r.id,
    displayName: r.displayName,
  } as AwardRecipient;
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
  if (err instanceof ConnectError && err.code === Code.PermissionDenied) {
    return "You don't have permission to write that award.";
  }
  return connectMessage(err) ?? "Operation failed.";
}
