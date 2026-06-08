// Admin Pending Delegations — staff-admin reviews pending registrations and
// can approve or reject inline. Backed by GSI2 (DelegationService.ListDelegations
// with status=PENDING).

import { createSignal, For, onMount, Show } from "solid-js";
import { useNavigate } from "@solidjs/router";
import { ConnectError, Code } from "@connectrpc/connect";

import { delegationClient } from "@/lib/api";
import { activeConferenceSignal, userSignal } from "@/lib/session";
import {
  Delegation_Status,
  type Delegation,
} from "@/gen/numun/v1/delegations_pb";
import { User_Role } from "@/gen/numun/v1/users_pb";

export default function AdminDelegations() {
  const [user] = userSignal;
  const [conference] = activeConferenceSignal;
  const navigate = useNavigate();

  const [items, setItems] = createSignal<Delegation[]>([]);
  const [loaded, setLoaded] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [busy, setBusy] = createSignal<string | null>(null);

  async function load() {
    const conf = conference();
    if (!conf) {
      setLoaded(true);
      return;
    }
    setError(null);
    try {
      const resp = await delegationClient.listDelegations({
        conferenceId: conf.conferenceId,
        status: Delegation_Status.PENDING,
      });
      setItems(resp.items);
    } catch (err) {
      setError(connectMessage(err) ?? "Failed to load delegations.");
    } finally {
      setLoaded(true);
    }
  }

  onMount(load);

  async function approve(d: Delegation) {
    setBusy(d.id);
    setError(null);
    try {
      await delegationClient.approve({
        delegationId: d.id,
        expectedVersion: d.version,
      });
      setItems(items().filter((it) => it.id !== d.id));
    } catch (err) {
      handleMutationError(err);
    } finally {
      setBusy(null);
    }
  }

  async function reject(d: Delegation) {
    setBusy(d.id);
    setError(null);
    try {
      await delegationClient.reject({
        delegationId: d.id,
        expectedVersion: d.version,
      });
      setItems(items().filter((it) => it.id !== d.id));
    } catch (err) {
      handleMutationError(err);
    } finally {
      setBusy(null);
    }
  }

  function handleMutationError(err: unknown) {
    if (err instanceof ConnectError && err.code === Code.Aborted) {
      setError(
        "Another change happened first. Reload to fetch the latest state.",
      );
      return;
    }
    if (err instanceof ConnectError && err.code === Code.FailedPrecondition) {
      setError(err.message);
      return;
    }
    setError(connectMessage(err) ?? "Failed to update delegation.");
  }

  return (
    <main class="mx-auto max-w-4xl px-6 py-8">
      <header class="flex items-center justify-between">
        <div>
          <h1 class="text-2xl font-bold text-nu-purple">Pending Delegations</h1>
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
          <button
            type="button"
            onClick={load}
            class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
          >
            Reload
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

        <Show when={loaded() && !conference()}>
          <p class="mt-6 rounded bg-amber-50 p-3 text-sm text-amber-900">
            No active conference.
          </p>
        </Show>

        <Show when={loaded() && conference()}>
          <Show
            when={items().length > 0}
            fallback={
              <p class="mt-6 text-sm text-nu-purple-700">
                No pending delegations.
              </p>
            }
          >
            <ul class="mt-6 space-y-3">
              <For each={items()}>
                {(d) => (
                  <li class="flex items-start justify-between gap-4 rounded border border-nu-purple-200 bg-white p-4">
                    <div>
                      <div class="text-base font-semibold">{d.school}</div>
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
                        Est. {d.estimatedDelegates?.total ?? 0} delegates · v
                        {d.version}
                      </div>
                    </div>
                    <div class="flex gap-2">
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
                        onClick={() => reject(d)}
                        class="rounded border border-nu-purple-300 px-3 py-1 text-sm text-nu-purple-700 disabled:opacity-50"
                      >
                        Reject
                      </button>
                    </div>
                  </li>
                )}
              </For>
            </ul>
          </Show>
        </Show>
      </Show>
    </main>
  );
}

function connectMessage(err: unknown): string | null {
  if (err instanceof ConnectError) return err.message;
  if (err instanceof Error) return err.message;
  return null;
}
