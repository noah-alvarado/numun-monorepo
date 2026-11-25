// Advisors who manage a delegation. Any advisor on the delegation can add a
// co-advisor by email (they must already have a NUMUN account); only a lead
// (or staff-admin) can change roles or remove advisors.

import { createSignal, For, onMount, Show } from "solid-js";

import { delegationClient } from "@/lib/api";
import { userSignal } from "@/lib/session";
import { connectMessage } from "@/lib/errors";
import Modal from "@/components/Modal";
import {
  DelegationAdvisor_Role,
  type Delegation,
  type DelegationAdvisor,
} from "@/gen/numun/v1/delegations_pb";
import { User_Role } from "@/gen/numun/v1/users_pb";

const roleLabel = (r: DelegationAdvisor_Role) =>
  r === DelegationAdvisor_Role.LEAD ? "Lead" : "Co-advisor";

export default function DelegationAdvisors(props: {
  delegation: Delegation;
  readOnly: boolean;
}) {
  const [user] = userSignal;
  const [advisors, setAdvisors] = createSignal<DelegationAdvisor[]>([]);
  const [loaded, setLoaded] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [notice, setNotice] = createSignal<string | null>(null);
  const [busy, setBusy] = createSignal(false);
  const [email, setEmail] = createSignal("");
  const [newRole, setNewRole] = createSignal(DelegationAdvisor_Role.SECONDARY);
  const [removeTarget, setRemoveTarget] =
    createSignal<DelegationAdvisor | null>(null);

  const isAdmin = () => user()?.role === User_Role.STAFF_ADMIN;
  const me = () => advisors().find((a) => a.userId === user()?.id);
  const canAdd = () => !props.readOnly && (isAdmin() || !!me());
  const canManage = () =>
    !props.readOnly &&
    (isAdmin() || me()?.role === DelegationAdvisor_Role.LEAD);
  // Every delegation needs a lead; the API refuses to demote or remove the
  // last one, so don't offer it.
  const isOnlyLead = (a: DelegationAdvisor) =>
    a.role === DelegationAdvisor_Role.LEAD &&
    advisors().filter((x) => x.role === DelegationAdvisor_Role.LEAD).length ===
      1;

  async function load() {
    try {
      const resp = await delegationClient.listAdvisors({
        delegationId: props.delegation.id,
      });
      setAdvisors(
        [...resp.items].sort(
          (a, b) => a.role - b.role || a.name.localeCompare(b.name),
        ),
      );
    } catch (err) {
      setError(connectMessage(err) ?? "Failed to load advisors.");
    } finally {
      setLoaded(true);
    }
  }

  onMount(load);

  async function run(fn: () => Promise<string>, fallback: string) {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      setNotice(await fn());
      await load();
    } catch (err) {
      setError(connectMessage(err) ?? fallback);
    } finally {
      setBusy(false);
    }
  }

  function add(e: SubmitEvent) {
    e.preventDefault();
    const address = email().trim();
    void run(async () => {
      const resp = await delegationClient.addAdvisor({
        delegationId: props.delegation.id,
        email: address,
        role: newRole(),
      });
      setEmail("");
      setNewRole(DelegationAdvisor_Role.SECONDARY);
      return `Added ${resp.advisor?.name || address}.`;
    }, "Failed to add the advisor.");
  }

  function setRole(a: DelegationAdvisor, role: DelegationAdvisor_Role) {
    void run(async () => {
      await delegationClient.setAdvisorRole({
        delegationId: props.delegation.id,
        userId: a.userId,
        role,
        expectedVersion: a.version,
      });
      return `${displayName(a)} is now ${roleLabel(role).toLowerCase()}.`;
    }, "Failed to change the role.");
  }

  function remove(a: DelegationAdvisor) {
    void run(async () => {
      await delegationClient.removeAdvisor({
        delegationId: props.delegation.id,
        userId: a.userId,
      });
      setRemoveTarget(null);
      return `Removed ${displayName(a)}.`;
    }, "Failed to remove the advisor.");
  }

  return (
    <div class="space-y-3 rounded border border-nu-purple-200 bg-white p-4 text-sm">
      <h2 class="text-base font-semibold text-nu-purple">Advisors</h2>
      <p class="text-nu-purple-700">
        Everyone listed here can manage this delegation. Leads can also change
        roles and remove advisors.
      </p>

      <Show when={error()}>
        {(e) => <p class="rounded bg-red-50 p-2 text-red-900">{e()}</p>}
      </Show>
      <Show when={notice()}>
        {(n) => <p class="rounded bg-green-50 p-2 text-green-900">{n()}</p>}
      </Show>

      <Show
        when={loaded()}
        fallback={<p class="text-nu-purple-700">Loading…</p>}
      >
        <table class="w-full">
          <thead>
            <tr class="border-b border-nu-purple-200 text-left text-xs uppercase tracking-wide text-nu-purple-700">
              <th class="py-2 pr-2">Name</th>
              <th class="py-2 pr-2">Email</th>
              <th class="py-2 pr-2">Role</th>
              <th class="py-2" />
            </tr>
          </thead>
          <tbody>
            <For each={advisors()}>
              {(a) => (
                <tr class="border-b border-nu-purple-100">
                  <td class="py-2 pr-2">
                    {displayName(a)}
                    <Show when={a.userId === user()?.id}>
                      <span class="ml-1 text-xs text-nu-purple-700">(you)</span>
                    </Show>
                  </td>
                  <td class="py-2 pr-2">{a.email || "—"}</td>
                  <td class="py-2 pr-2">{roleLabel(a.role)}</td>
                  <td class="py-2 text-right">
                    <Show
                      when={
                        canManage() && a.userId !== user()?.id && isOnlyLead(a)
                      }
                    >
                      <span
                        class="text-xs text-nu-purple-700"
                        title="Make another advisor lead before changing or removing this one."
                      >
                        Only lead
                      </span>
                    </Show>
                    <Show
                      when={
                        canManage() && a.userId !== user()?.id && !isOnlyLead(a)
                      }
                    >
                      <div class="flex justify-end gap-1">
                        <button
                          type="button"
                          disabled={busy()}
                          onClick={() =>
                            setRole(
                              a,
                              a.role === DelegationAdvisor_Role.LEAD
                                ? DelegationAdvisor_Role.SECONDARY
                                : DelegationAdvisor_Role.LEAD,
                            )
                          }
                          class="rounded border border-nu-purple-300 px-2 py-0.5 text-nu-purple-700 disabled:opacity-50"
                        >
                          {a.role === DelegationAdvisor_Role.LEAD
                            ? "Make co-advisor"
                            : "Make lead"}
                        </button>
                        <button
                          type="button"
                          disabled={busy()}
                          onClick={() => setRemoveTarget(a)}
                          aria-label={`Remove ${displayName(a)}`}
                          class="rounded border border-nu-purple-300 px-2 py-0.5 text-nu-purple-700 disabled:opacity-50"
                        >
                          Remove
                        </button>
                      </div>
                    </Show>
                  </td>
                </tr>
              )}
            </For>
          </tbody>
        </table>
      </Show>

      <Show when={canAdd()}>
        <form
          aria-label="Add an advisor"
          onSubmit={add}
          class="flex flex-wrap items-end gap-2 rounded border border-nu-purple-200 bg-nu-purple-50 p-3"
        >
          <label class="block min-w-64 flex-1">
            <span class="text-xs text-nu-purple-700">
              Co-advisor's email (they need a NUMUN account)
            </span>
            <input
              type="email"
              required
              value={email()}
              onInput={(e) => setEmail(e.currentTarget.value)}
              class="w-full rounded border border-nu-purple-300 px-2 py-1"
            />
          </label>
          <Show when={canManage()}>
            <label class="block">
              <span class="text-xs text-nu-purple-700">Role</span>
              <select
                value={String(newRole())}
                onChange={(e) =>
                  setNewRole(
                    Number(e.currentTarget.value) as DelegationAdvisor_Role,
                  )
                }
                class="w-full rounded border border-nu-purple-300 px-2 py-1"
              >
                <option value={String(DelegationAdvisor_Role.SECONDARY)}>
                  Co-advisor
                </option>
                <option value={String(DelegationAdvisor_Role.LEAD)}>
                  Lead
                </option>
              </select>
            </label>
          </Show>
          <button
            type="submit"
            disabled={busy()}
            class="rounded bg-nu-purple px-3 py-1 text-white disabled:opacity-50"
          >
            Add advisor
          </button>
        </form>
      </Show>

      <Show when={removeTarget()}>
        {(a) => (
          <Modal
            title={`Remove ${displayName(a())}?`}
            onClose={() => setRemoveTarget(null)}
          >
            <p class="mt-2 text-sm text-nu-purple-700">
              They'll no longer be able to see or manage this delegation.
            </p>
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
                onClick={() => remove(a())}
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

function displayName(a: DelegationAdvisor): string {
  return a.name || a.email || "Unnamed advisor";
}
