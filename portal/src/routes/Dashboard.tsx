// Placeholder dashboard. M4 fleshes it out with real screens — for M2 the
// goal is simply: prove that the authenticated state round-trips through
// UserService.GetMe and the user can log out.

import { useNavigate } from "@solidjs/router";
import { Show } from "solid-js";

import { logout, userSignal } from "@/lib/session";

export default function Dashboard() {
  const [user] = userSignal;
  const navigate = useNavigate();

  async function onLogout() {
    await logout();
    navigate("/sign-in", { replace: true });
  }

  return (
    <main class="mx-auto max-w-2xl px-6 py-12">
      <header class="flex items-center justify-between">
        <h1 class="text-3xl font-bold text-nu-purple">Dashboard</h1>
        <button
          type="button"
          onClick={onLogout}
          class="rounded border border-nu-purple-300 px-3 py-1 text-sm text-nu-purple-700 hover:bg-nu-purple-50"
        >
          Sign out
        </button>
      </header>
      <Show when={user()} fallback={<p class="mt-4">Loading…</p>}>
        {(u) => (
          <section class="mt-6 rounded border border-nu-purple-200 bg-white p-4">
            <h2 class="text-lg font-semibold">Signed in as</h2>
            <dl class="mt-2 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
              <dt class="text-nu-purple-700">Name</dt>
              <dd>{u().name}</dd>
              <dt class="text-nu-purple-700">Email</dt>
              <dd>{u().email}</dd>
              <dt class="text-nu-purple-700">Role</dt>
              <dd>{u().role}</dd>
              <dt class="text-nu-purple-700">User id</dt>
              <dd class="font-mono text-xs">{u().id}</dd>
            </dl>
            <p class="mt-4 text-sm text-nu-purple-700">
              M2 milestone: auth end-to-end is working. M4 builds the real
              dashboard here.
            </p>
          </section>
        )}
      </Show>
    </main>
  );
}
