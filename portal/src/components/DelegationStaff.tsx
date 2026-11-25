// Staffers overseeing a delegation (staff-admin only). Assigned staffers can
// see and help with the delegation; see AUTH.md §9 "case a".

import { createMemo, createSignal, For, onMount, Show } from "solid-js";

import { delegationClient, userClient } from "@/lib/api";
import { connectMessage } from "@/lib/errors";
import type { Delegation } from "@/gen/numun/v1/delegations_pb";
import { User_Role, type User } from "@/gen/numun/v1/users_pb";

export default function DelegationStaff(props: { delegation: Delegation }) {
  const [assigned, setAssigned] = createSignal<User[]>([]);
  const [staff, setStaff] = createSignal<User[]>([]);
  const [loaded, setLoaded] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [busy, setBusy] = createSignal(false);
  const [pick, setPick] = createSignal("");

  async function load() {
    try {
      const [a, s] = await Promise.all([
        delegationClient.listStaffers({ delegationId: props.delegation.id }),
        userClient.listStaff({}),
      ]);
      setAssigned(a.items);
      setStaff(s.items.filter((u) => u.role === User_Role.STAFF_STAFFER));
    } catch (err) {
      setError(connectMessage(err) ?? "Failed to load staff.");
    } finally {
      setLoaded(true);
    }
  }

  onMount(load);

  const available = createMemo(() => {
    const taken = new Set(assigned().map((u) => u.id));
    return staff().filter((u) => !taken.has(u.id));
  });

  async function run(fn: () => Promise<unknown>, fallback: string) {
    setBusy(true);
    setError(null);
    try {
      await fn();
      await load();
    } catch (err) {
      setError(connectMessage(err) ?? fallback);
    } finally {
      setBusy(false);
    }
  }

  function assign(e: SubmitEvent) {
    e.preventDefault();
    const userId = pick();
    if (!userId) return;
    void run(async () => {
      await delegationClient.assignStaffer({
        delegationId: props.delegation.id,
        userId,
      });
      setPick("");
    }, "Failed to assign the staffer.");
  }

  function unassign(u: User) {
    void run(
      () =>
        delegationClient.unassignStaffer({
          delegationId: props.delegation.id,
          userId: u.id,
        }),
      "Failed to unassign the staffer.",
    );
  }

  return (
    <div class="space-y-3 rounded border border-nu-purple-200 bg-white p-4 text-sm">
      <h2 class="text-base font-semibold text-nu-purple">Assigned staff</h2>
      <p class="text-nu-purple-700">
        Staffers assigned here can see this delegation, check its delegates in
        and give its awards.
      </p>
      <Show when={error()}>
        {(e) => <p class="rounded bg-red-50 p-2 text-red-900">{e()}</p>}
      </Show>
      <Show
        when={loaded()}
        fallback={<p class="text-nu-purple-700">Loading…</p>}
      >
        <Show
          when={assigned().length > 0}
          fallback={<p class="text-nu-purple-700">No staffers assigned.</p>}
        >
          <ul class="divide-y divide-nu-purple-100">
            <For each={assigned()}>
              {(u) => (
                <li class="flex items-center justify-between py-2">
                  <span>
                    {u.name || u.email}
                    <Show when={u.name && u.email}>
                      <span class="ml-2 text-nu-purple-700">{u.email}</span>
                    </Show>
                  </span>
                  <button
                    type="button"
                    disabled={busy()}
                    onClick={() => unassign(u)}
                    aria-label={`Unassign ${u.name || u.email}`}
                    class="rounded border border-nu-purple-300 px-2 py-0.5 text-nu-purple-700 disabled:opacity-50"
                  >
                    Unassign
                  </button>
                </li>
              )}
            </For>
          </ul>
        </Show>
        <form
          aria-label="Assign a staffer"
          onSubmit={assign}
          class="flex flex-wrap items-end gap-2"
        >
          <label class="block min-w-64 flex-1">
            <span class="text-xs text-nu-purple-700">Staffer</span>
            <select
              value={pick()}
              onChange={(e) => setPick(e.currentTarget.value)}
              class="w-full rounded border border-nu-purple-300 px-2 py-1"
            >
              <option value="">
                {available().length ? "Choose a staffer…" : "No other staffers"}
              </option>
              <For each={available()}>
                {(u) => (
                  <option value={u.id}>
                    {u.name || u.email}
                    {u.name && u.email ? ` (${u.email})` : ""}
                  </option>
                )}
              </For>
            </select>
          </label>
          <button
            type="submit"
            disabled={busy() || !pick()}
            class="rounded bg-nu-purple px-3 py-1 text-white disabled:opacity-50"
          >
            Assign
          </button>
        </form>
      </Show>
    </div>
  );
}
