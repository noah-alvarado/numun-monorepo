// Delegations — staff browse the conference's delegations by status and open
// one for its roster and payments. Admins see all of them and approve or
// reject pending ones inline; staffers see the delegations they oversee
// (ListDelegations scopes the results server-side).

import { createEffect, createSignal, For, on, Show } from "solid-js";
import { useNavigate, useSearchParams } from "@solidjs/router";
import { ConnectError, Code } from "@connectrpc/connect";

import { delegationClient } from "@/lib/api";
import { activeConferenceSignal, userSignal } from "@/lib/session";
import {
  Delegation_Status,
  type Delegation,
} from "@/gen/numun/v1/delegations_pb";
import { User_Role } from "@/gen/numun/v1/users_pb";
import { listScopedDelegations } from "@/lib/delegations";
import RejectDelegationDialog from "@/components/RejectDelegationDialog";
import { conflictMessage, connectMessage, isConflict } from "@/lib/errors";
import { formatMoney, moneyToCents } from "@/lib/money";

const FILTERS = [
  ["pending", "Pending", Delegation_Status.PENDING],
  ["approved", "Approved", Delegation_Status.APPROVED],
  ["rejected", "Rejected", Delegation_Status.REJECTED],
  ["all", "All", Delegation_Status.UNSPECIFIED],
] as const;

const STATUS_LABEL: Record<number, string> = {
  [Delegation_Status.PENDING]: "Pending",
  [Delegation_Status.APPROVED]: "Approved",
  [Delegation_Status.REJECTED]: "Rejected",
};

export default function AdminDelegations() {
  const [user] = userSignal;
  const [conference] = activeConferenceSignal;
  const navigate = useNavigate();

  const [items, setItems] = createSignal<Delegation[]>([]);
  const [loaded, setLoaded] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [busy, setBusy] = createSignal<string | null>(null);

  const isAdmin = () => user()?.role === User_Role.STAFF_ADMIN;
  const isStaff = () => isAdmin() || user()?.role === User_Role.STAFF_STAFFER;

  // Admins mostly come here to review registrations; staffers can't, so
  // they start with everything they oversee.
  const [params, setParams] = useSearchParams<{ status?: string }>();
  const filter = () =>
    FILTERS.find(([key]) => key === params.status) ??
    FILTERS.find(([key]) => key === (isAdmin() ? "pending" : "all"))!;

  async function load() {
    const conf = conference();
    if (!conf) {
      setLoaded(true);
      return;
    }
    setError(null);
    try {
      setItems(await listScopedDelegations(conf.conferenceId, filter()[2]));
    } catch (err) {
      setError(connectMessage(err) ?? "Failed to load delegations.");
    } finally {
      setLoaded(true);
    }
  }

  createEffect(
    on(
      () => filter()[0],
      () => {
        if (isStaff()) void load();
      },
    ),
  );

  async function approve(d: Delegation) {
    setBusy(d.id);
    setError(null);
    try {
      await delegationClient.approve({
        delegationId: d.id,
        expectedVersion: d.version,
      });
      await load();
    } catch (err) {
      await handleMutationError(err);
    } finally {
      setBusy(null);
    }
  }

  // Rejection can't be undone, so it goes through a confirm dialog.
  const [rejectTarget, setRejectTarget] = createSignal<Delegation | null>(null);

  async function reject(d: Delegation, reason: string) {
    setBusy(d.id);
    setError(null);
    try {
      await delegationClient.reject({
        delegationId: d.id,
        expectedVersion: d.version,
        reason,
      });
      await load();
    } catch (err) {
      await handleMutationError(err);
    } finally {
      setBusy(null);
      setRejectTarget(null);
    }
  }

  async function handleMutationError(err: unknown) {
    if (isConflict(err)) {
      await load();
      setError(conflictMessage("this delegation"));
      return;
    }
    if (err instanceof ConnectError && err.code === Code.FailedPrecondition) {
      setError(err.rawMessage);
      return;
    }
    setError(connectMessage(err) ?? "Failed to update delegation.");
  }

  return (
    <main class="mx-auto max-w-4xl px-6 py-8">
      <header class="flex items-center justify-between">
        <div>
          <h1 class="text-2xl font-bold text-nu-purple">Delegations</h1>
          <Show when={conference()}>
            {(c) => <p class="mt-1 text-sm text-nu-purple-700">{c().name}</p>}
          </Show>
        </div>
        <div class="flex gap-2 text-sm">
          <button
            type="button"
            onClick={() => navigate("/dashboard")}
            class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
          >
            Back
          </button>
          <Show when={isStaff()}>
            <button
              type="button"
              onClick={load}
              class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
            >
              Reload
            </button>
          </Show>
        </div>
      </header>

      <Show when={!isStaff()}>
        <p class="mt-6 rounded bg-amber-50 p-3 text-sm text-amber-900">
          Staff only.
        </p>
      </Show>

      <Show when={isStaff()}>
        <Show when={error()}>
          {(e) => (
            <p class="mt-4 rounded bg-red-50 p-3 text-sm text-red-900">{e()}</p>
          )}
        </Show>

        <Show when={loaded() && !conference()}>
          <p class="mt-6 rounded bg-amber-50 p-3 text-sm text-amber-900">
            No active conference.
          </p>
        </Show>

        <Show when={conference()}>
          <label class="mt-6 flex items-center gap-2 text-sm text-nu-purple-700">
            Status
            <select
              value={filter()[0]}
              onChange={(e) => setParams({ status: e.currentTarget.value })}
              class="rounded border border-nu-purple-300 px-2 py-1"
            >
              <For each={FILTERS}>
                {([key, label]) => <option value={key}>{label}</option>}
              </For>
            </select>
          </label>
        </Show>

        <Show when={!loaded()}>
          <p class="mt-6 text-sm text-nu-purple-700">Loading…</p>
        </Show>

        <Show when={loaded() && conference()}>
          <Show
            when={items().length > 0}
            fallback={
              <p class="mt-6 text-sm text-nu-purple-700">
                {isAdmin()
                  ? filter()[0] === "all"
                    ? "No delegations yet."
                    : `No ${filter()[1].toLowerCase()} delegations.`
                  : "No delegations are assigned to you here."}
              </p>
            }
          >
            <ul class="mt-4 space-y-3">
              <For each={items()}>
                {(d) => (
                  <li class="flex items-start justify-between gap-4 rounded border border-nu-purple-200 bg-white p-4">
                    <div>
                      <div class="flex items-center gap-2">
                        <span class="text-base font-semibold">{d.school}</span>
                        <span class="rounded bg-nu-purple-50 px-2 py-0.5 text-xs font-semibold uppercase tracking-wide text-nu-purple-700">
                          {STATUS_LABEL[d.status] ?? "—"}
                        </span>
                      </div>
                      <div class="text-sm text-nu-purple-700">
                        <Show when={d.address}>
                          {(a) => (
                            <>
                              {a().city}
                              {a().state ? `, ${a().state}` : ""} {a().country}
                            </>
                          )}
                        </Show>
                      </div>
                      <div class="mt-1 text-xs text-nu-purple-700">
                        Est. {d.estimatedDelegates?.total ?? 0} delegates
                        <Show when={moneyToCents(d.balanceDue) !== 0n}>
                          {" "}
                          · Balance due {formatMoney(d.balanceDue)}
                        </Show>
                      </div>
                    </div>
                    <div class="flex gap-2">
                      <a
                        href={`/delegations/${d.id}`}
                        class="rounded border border-nu-purple-300 px-3 py-1 text-sm text-nu-purple-700 hover:bg-nu-purple-50"
                      >
                        Open
                      </a>
                      <Show
                        when={
                          isAdmin() && d.status === Delegation_Status.PENDING
                        }
                      >
                        <button
                          type="button"
                          disabled={busy() === d.id}
                          onClick={() => approve(d)}
                          class="rounded bg-nu-purple px-3 py-1 text-sm text-white disabled:opacity-50"
                        >
                          {busy() === d.id ? "…" : "Approve"}
                        </button>
                        <button
                          type="button"
                          disabled={busy() === d.id}
                          onClick={() => setRejectTarget(d)}
                          class="rounded border border-nu-purple-300 px-3 py-1 text-sm text-nu-purple-700 disabled:opacity-50"
                        >
                          Reject
                        </button>
                      </Show>
                    </div>
                  </li>
                )}
              </For>
            </ul>
          </Show>
        </Show>
      </Show>

      <Show when={rejectTarget()}>
        {(d) => (
          <RejectDelegationDialog
            school={d().school}
            busy={busy() === d().id}
            onCancel={() => setRejectTarget(null)}
            onReject={(reason) => reject(d(), reason)}
          />
        )}
      </Show>
    </main>
  );
}
