// Staff — staff-admin sees every staff account and invites new ones. An
// invite creates the Cognito user, which emails a temporary password; the
// first sign-in asks them to choose their own.

import { createSignal, For, onMount, Show } from "solid-js";
import { Code, ConnectError } from "@connectrpc/connect";

import { userClient } from "@/lib/api";
import { connectMessage } from "@/lib/errors";
import { userSignal } from "@/lib/session";
import PageHeader from "@/components/PageHeader";
import { User_Role, type User } from "@/gen/numun/v1/users_pb";

const roleLabel = (r: User_Role) =>
  r === User_Role.STAFF_ADMIN ? "Administrator" : "Staffer";

export default function AdminStaff() {
  const [user] = userSignal;
  const [staff, setStaff] = createSignal<User[]>([]);
  const [loaded, setLoaded] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [notice, setNotice] = createSignal<string | null>(null);
  const [busy, setBusy] = createSignal(false);
  const [email, setEmail] = createSignal("");
  const [name, setName] = createSignal("");
  const [role, setRole] = createSignal(User_Role.STAFF_STAFFER);

  async function load() {
    try {
      const resp = await userClient.listStaff({});
      setStaff(resp.items);
    } catch (err) {
      setError(connectMessage(err) ?? "Failed to load staff.");
    } finally {
      setLoaded(true);
    }
  }

  onMount(load);

  async function invite(e: SubmitEvent) {
    e.preventDefault();
    const address = email().trim();
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await userClient.inviteStaff({
        email: address,
        name: name().trim(),
        role: role(),
      });
      setNotice(
        `Invited ${address}. They'll get an email with a temporary password.`,
      );
      setEmail("");
      setName("");
      setRole(User_Role.STAFF_STAFFER);
      await load();
    } catch (err) {
      setError(
        err instanceof ConnectError && err.code === Code.AlreadyExists
          ? "An account already uses that email."
          : (connectMessage(err) ?? "Failed to send the invite."),
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <main class="mx-auto max-w-3xl px-6 py-8">
      <PageHeader title="Staff" />
      <Show
        when={user()?.role === User_Role.STAFF_ADMIN}
        fallback={
          <p class="mt-6 rounded bg-amber-50 p-3 text-sm text-amber-900">
            Admin only.
          </p>
        }
      >
        <form
          aria-label="Invite staff"
          onSubmit={invite}
          class="mt-6 grid grid-cols-2 gap-3 rounded border border-nu-purple-200 bg-white p-4 text-sm"
        >
          <h2 class="col-span-2 text-base font-semibold text-nu-purple">
            Invite a staff member
          </h2>
          <label class="block">
            <span class="text-xs text-nu-purple-700">Email</span>
            <input
              type="email"
              required
              value={email()}
              onInput={(e) => setEmail(e.currentTarget.value)}
              class="w-full rounded border border-nu-purple-300 px-2 py-1"
            />
          </label>
          <label class="block">
            <span class="text-xs text-nu-purple-700">Name</span>
            <input
              value={name()}
              onInput={(e) => setName(e.currentTarget.value)}
              class="w-full rounded border border-nu-purple-300 px-2 py-1"
            />
          </label>
          <label class="block">
            <span class="text-xs text-nu-purple-700">Role</span>
            <select
              value={String(role())}
              onChange={(e) =>
                setRole(Number(e.currentTarget.value) as User_Role)
              }
              class="w-full rounded border border-nu-purple-300 px-2 py-1"
            >
              <option value={String(User_Role.STAFF_STAFFER)}>Staffer</option>
              <option value={String(User_Role.STAFF_ADMIN)}>
                Administrator
              </option>
            </select>
          </label>
          <div class="flex items-end justify-end">
            <button
              type="submit"
              disabled={busy()}
              class="rounded bg-nu-purple px-3 py-1 text-white disabled:opacity-50"
            >
              {busy() ? "Sending…" : "Send invite"}
            </button>
          </div>
          <p class="col-span-2 text-xs text-nu-purple-700">
            Staffers see and check in only the delegations and committees
            they're assigned to. Administrators can do everything.
          </p>
        </form>

        <Show when={error()}>
          {(e) => (
            <p class="mt-4 rounded bg-red-50 p-3 text-sm text-red-900">{e()}</p>
          )}
        </Show>
        <Show when={notice()}>
          {(n) => (
            <p class="mt-4 rounded bg-green-50 p-3 text-sm text-green-900">
              {n()}
            </p>
          )}
        </Show>

        <Show
          when={loaded()}
          fallback={<p class="mt-6 text-sm text-nu-purple-700">Loading…</p>}
        >
          <table class="mt-6 w-full rounded border border-nu-purple-200 bg-white text-sm">
            <thead>
              <tr class="border-b border-nu-purple-200 text-left text-xs uppercase tracking-wide text-nu-purple-700">
                <th class="p-2">Name</th>
                <th class="p-2">Email</th>
                <th class="p-2">Role</th>
              </tr>
            </thead>
            <tbody>
              <For each={staff()}>
                {(u) => (
                  <tr class="border-b border-nu-purple-100">
                    <td class="p-2">{u.name || "—"}</td>
                    <td class="p-2">{u.email || "—"}</td>
                    <td class="p-2">{roleLabel(u.role)}</td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </Show>
      </Show>
    </main>
  );
}
