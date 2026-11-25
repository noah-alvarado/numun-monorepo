// Awards — single route serving three role variants per M11:
//
// - Advisor: read-only list of conference awards, plus a permanent
//   congratulatory hero when one of their delegates / delegations won.
//   Dismissal persists server-side via UserService.DismissAward.
// - Staff-staffer: can award delegates / delegations tied to delegations
//   they're assigned to. Reads see everything.
// - Staff-admin: full CRUD, any recipient kind.
//
// Recipients are picked by name (delegates via search), never typed as ids.
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
  committeeClient,
  delegateClient,
  userClient,
} from "@/lib/api";
import { activeConferenceSignal, userSignal } from "@/lib/session";
import {
  AwardRecipientKind,
  type Award,
  type AwardRecipient,
  type CmsSyncStatus,
} from "@/gen/numun/v1/awards_pb";
import { User_Role, type User } from "@/gen/numun/v1/users_pb";
import type { Delegation } from "@/gen/numun/v1/delegations_pb";
import type { Committee } from "@/gen/numun/v1/committees_pb";
import type { SearchDelegatesResponse_Hit } from "@/gen/numun/v1/delegates_pb";
import { listScopedDelegations } from "@/lib/delegations";
import { conflictMessage, connectMessage, isConflict } from "@/lib/errors";

type RecipientFormRow = {
  kind: AwardRecipientKind;
  id: string;
  displayName: string;
};

// What the recipient pickers offer, by kind. Delegates are searched by name
// instead (there can be thousands).
type PickerOptions = {
  conference: { id: string; name: string } | null;
  delegations: Delegation[];
  committees: Committee[];
  staff: User[];
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
        return await listScopedDelegations(id);
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

  // Committees and staff are admin-only recipient kinds.
  const [committees] = createResource(
    () => (user()?.role === User_Role.STAFF_ADMIN ? conferenceId() : ""),
    async (id) => {
      if (!id) return [] as Committee[];
      try {
        return (await committeeClient.listCommittees({ conferenceId: id }))
          .items;
      } catch {
        return [] as Committee[];
      }
    },
  );
  const [staff] = createResource(
    () => user()?.role === User_Role.STAFF_ADMIN,
    async (admin) => {
      if (!admin) return [] as User[];
      try {
        return (await userClient.listStaff({})).items;
      } catch {
        return [] as User[];
      }
    },
  );
  const pickerOptions = (): PickerOptions => {
    const c = conference();
    return {
      conference: c ? { id: c.conferenceId, name: c.name } : null,
      delegations: myDelegations() ?? [],
      committees: committees() ?? [],
      staff: staff() ?? [],
    };
  };

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
        if (r.kind === AwardRecipientKind.DELEGATE && myDgIds.has(r.id))
          return true;
        if (r.kind === AwardRecipientKind.DELEGATION && myDelIds.has(r.id))
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
  const [cmsVerb, setCmsVerb] = createSignal<"Saved" | "Deleted">("Saved");
  const modalOpen = () => createOpen() || !!editTarget() || !!deleteTarget();
  function closeModals() {
    setCreateOpen(false);
    setEditTarget(null);
    setDeleteTarget(null);
    setError(null);
  }

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
      setCmsVerb("Saved");
      setCmsBanner(resp.cmsSync ?? null);
      await refetchAwards();
    } catch (err) {
      setError(handleMutationError(err));
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
      setCmsVerb("Saved");
      setCmsBanner(resp.cmsSync ?? null);
      await refetchAwards();
    } catch (err) {
      if (isConflict(err)) {
        setEditTarget(null);
        await refetchAwards();
        setError(conflictMessage("this award"));
        return;
      }
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
      setCmsVerb("Deleted");
      setCmsBanner(resp.cmsSync ?? null);
      await refetchAwards();
    } catch (err) {
      if (isConflict(err)) {
        setDeleteTarget(null);
        await refetchAwards();
        setError(conflictMessage("this award"));
        return;
      }
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

  const anyWritable = createMemo(() => (awards() ?? []).some(canWrite));

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

      <Show when={!modalOpen() && error()}>
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
                {cmsVerb()} in the database, but CMS sync failed after{" "}
                {s().attempts} attempts: {s().finalError}. Retry by editing the
                award.
              </p>
            }
          >
            <p class="mt-4 rounded bg-green-50 p-3 text-sm text-green-900">
              {cmsVerb()} and synced to the public site.
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
                  <Show when={anyWritable()}>
                    <th class="w-40 px-2 py-2 text-right">Actions</th>
                  </Show>
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
                          fallback={<span class="text-nu-purple-300">—</span>}
                        >
                          {a.category}
                        </Show>
                      </td>
                      <td class="px-2 py-2">
                        <RecipientList recipients={a.recipients} />
                      </td>
                      <Show when={anyWritable()}>
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
                      </Show>
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
          options={pickerOptions()}
          error={error()}
          onClose={closeModals}
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
            options={pickerOptions()}
            error={error()}
            onClose={closeModals}
            onSubmit={(s) => handleUpdate(target(), s)}
          />
        )}
      </Show>

      <Show when={deleteTarget()}>
        {(target) => (
          <ConfirmModal
            title="Delete award?"
            message={`This will delete "${target().name}" from both the database and the public site.`}
            error={error()}
            onCancel={closeModals}
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
            <span class="font-medium">{r.displayName || "Unnamed"}</span>{" "}
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
  options: PickerOptions;
  error: string | null;
  onClose: () => void;
  onSubmit: (s: AwardFormState) => Promise<void>;
}) {
  const [localError, setLocalError] = createSignal<string | null>(null);
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
    const chosen = recipients().filter((r) => r.id.trim() !== "");
    if (chosen.length === 0) {
      setLocalError("Choose at least one recipient.");
      return;
    }
    setLocalError(null);
    setBusy(true);
    try {
      await props.onSubmit({
        name: name().trim(),
        category: category().trim(),
        recipients: chosen.map((r) => ({
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
              <div class="mt-2 grid grid-cols-[8rem_minmax(0,1fr)_auto] items-start gap-2">
                <select
                  aria-label="Recipient type"
                  value={String(row.kind)}
                  onChange={(e) =>
                    updateRow(i(), {
                      kind: Number(e.currentTarget.value) as AwardRecipientKind,
                      id: "",
                      displayName: "",
                    })
                  }
                  class="rounded border border-nu-purple-300 px-2 py-1"
                >
                  <For each={props.allowedKinds}>
                    {(k) => <option value={String(k)}>{kindLabel(k)}</option>}
                  </For>
                </select>
                <RecipientPicker
                  row={row}
                  options={props.options}
                  onPick={(id, displayName) =>
                    updateRow(i(), { id, displayName })
                  }
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

        <ModalError message={localError() ?? props.error} />

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

function RecipientPicker(props: {
  row: RecipientFormRow;
  options: PickerOptions;
  onPick: (id: string, displayName: string) => void;
}) {
  const choices = (): [string, string][] => {
    const o = props.options;
    switch (props.row.kind) {
      case AwardRecipientKind.DELEGATION:
        return o.delegations.map((d) => [d.id, d.school]);
      case AwardRecipientKind.COMMITTEE:
        return o.committees.map((c) => [c.id, c.name]);
      case AwardRecipientKind.USER:
        return o.staff.map((u) => [u.id, u.name || u.email]);
      case AwardRecipientKind.CONFERENCE:
        return o.conference ? [[o.conference.id, o.conference.name]] : [];
      default:
        return [];
    }
  };
  // Keep an existing recipient selectable even if it's no longer offered.
  const withCurrent = (): [string, string][] => {
    const list = choices();
    const { id, displayName } = props.row;
    return id && !list.some(([v]) => v === id)
      ? [[id, displayName || "Current recipient"], ...list]
      : list;
  };
  return (
    <Show
      when={props.row.kind !== AwardRecipientKind.DELEGATE}
      fallback={<DelegateSearch row={props.row} onPick={props.onPick} />}
    >
      <select
        aria-label="Recipient"
        required
        value={props.row.id}
        onChange={(e) => {
          const id = e.currentTarget.value;
          props.onPick(id, withCurrent().find(([v]) => v === id)?.[1] ?? "");
        }}
        class="rounded border border-nu-purple-300 px-2 py-1"
      >
        <option value="">Choose…</option>
        {/* `selected` rather than the select's value: the value is applied
            before these options render, which would leave "Choose…". */}
        <For each={withCurrent()}>
          {([id, label]) => (
            <option value={id} selected={id === props.row.id}>
              {label}
            </option>
          )}
        </For>
      </select>
    </Show>
  );
}

// Delegates are found by name; the server limits results to the caller's
// scope (all delegates for admins, assigned delegations for staffers).
function DelegateSearch(props: {
  row: RecipientFormRow;
  onPick: (id: string, displayName: string) => void;
}) {
  const [conference] = activeConferenceSignal;
  const [query, setQuery] = createSignal("");
  const [hits, setHits] = createSignal<SearchDelegatesResponse_Hit[]>([]);
  let seq = 0;

  async function search(q: string) {
    setQuery(q);
    const conferenceId = conference()?.conferenceId;
    if (q.trim().length < 2 || !conferenceId) {
      setHits([]);
      return;
    }
    const mine = ++seq;
    try {
      const resp = await delegateClient.searchDelegates({
        conferenceId,
        query: q.trim(),
        limit: 10,
      });
      if (mine === seq) setHits(resp.items);
    } catch {
      if (mine === seq) setHits([]);
    }
  }

  return (
    <Show
      when={!props.row.id}
      fallback={
        <div class="flex items-center justify-between rounded border border-nu-purple-200 px-2 py-1">
          <span>{props.row.displayName || "Selected delegate"}</span>
          <button
            type="button"
            onClick={() => props.onPick("", "")}
            class="text-xs text-nu-purple-700 underline"
          >
            Change
          </button>
        </div>
      }
    >
      <div>
        <input
          type="search"
          aria-label="Search delegates by name"
          placeholder="Search delegates by name…"
          value={query()}
          onInput={(e) => void search(e.currentTarget.value)}
          class="w-full rounded border border-nu-purple-300 px-2 py-1"
        />
        <Show when={hits().length > 0}>
          <ul class="mt-1 max-h-40 overflow-y-auto rounded border border-nu-purple-200">
            <For each={hits()}>
              {(h) => (
                <li>
                  <button
                    type="button"
                    onClick={() =>
                      props.onPick(
                        h.delegate!.id,
                        `${h.delegate!.firstName} ${h.delegate!.lastName}`,
                      )
                    }
                    class="w-full px-2 py-1 text-left hover:bg-nu-purple-50"
                  >
                    {h.delegate!.firstName} {h.delegate!.lastName}
                    <span class="text-xs text-nu-purple-700">
                      {" "}
                      — {h.delegationSchool}
                    </span>
                  </button>
                </li>
              )}
            </For>
          </ul>
        </Show>
        <Show when={query().trim().length >= 2 && hits().length === 0}>
          <p class="mt-1 text-xs text-nu-purple-700">No matching delegates.</p>
        </Show>
      </div>
    </Show>
  );
}

function ConfirmModal(props: {
  title: string;
  message: string;
  error: string | null;
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
      <ModalError message={props.error} />
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

function ModalError(props: { message: string | null }) {
  return (
    <Show when={props.message}>
      {(m) => (
        <p class="mt-3 rounded bg-red-50 p-2 text-sm text-red-900">{m()}</p>
      )}
    </Show>
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
    return [AwardRecipientKind.DELEGATE, AwardRecipientKind.DELEGATION];
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

function handleMutationError(err: unknown): string {
  if (err instanceof ConnectError && err.code === Code.PermissionDenied) {
    return "You don't have permission to write that award.";
  }
  return connectMessage(err) ?? "Operation failed.";
}
