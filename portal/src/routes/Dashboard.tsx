// Dashboard — role-aware landing after sign-in.
//
//   - Advisors get a CTA to their delegation.
//   - Staff-admins get the admin nav (pending delegations for M4; more in
//     later milestones).
//   - Staff-staffers see a placeholder until M7 lands committee scope.

import { useNavigate } from "@solidjs/router";
import { Show } from "solid-js";

import { logout, userSignal, activeConferenceSignal } from "@/lib/session";
import { User_Role } from "@/gen/numun/v1/users_pb";

export default function Dashboard() {
  const [user] = userSignal;
  const [conference] = activeConferenceSignal;
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
          <>
            <section class="mt-6 rounded border border-nu-purple-200 bg-white p-4">
              <h2 class="text-lg font-semibold">Signed in as</h2>
              <dl class="mt-2 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
                <dt class="text-nu-purple-700">Name</dt>
                <dd>{u().name}</dd>
                <dt class="text-nu-purple-700">Email</dt>
                <dd>{u().email}</dd>
                <dt class="text-nu-purple-700">Role</dt>
                <dd>{roleLabel(u().role)}</dd>
              </dl>
            </section>

            <Show when={conference()}>
              {(c) => (
                <section class="mt-4 rounded border border-nu-purple-200 bg-nu-purple-50 p-4 text-sm">
                  <span class="font-semibold text-nu-purple-700">
                    Active conference:
                  </span>{" "}
                  {c().name} ({c().year})
                </section>
              )}
            </Show>

            <Show when={u().role === User_Role.ADVISOR}>
              <section class="mt-6 grid gap-3">
                <NavCard
                  title="My Delegation"
                  description="Register your school, edit details, and track approval status."
                  onClick={() => navigate("/delegation")}
                />
              </section>
            </Show>

            <Show when={u().role === User_Role.STAFF_ADMIN}>
              <section class="mt-6 grid gap-3">
                <NavCard
                  title="Pending Delegations"
                  description="Review and approve school registrations."
                  onClick={() => navigate("/admin/delegations")}
                />
              </section>
            </Show>

            <Show when={u().role === User_Role.STAFF_STAFFER}>
              <p class="mt-6 text-sm text-nu-purple-700">
                Staffer-scoped views land in a later milestone.
              </p>
            </Show>
          </>
        )}
      </Show>
    </main>
  );
}

function NavCard(props: {
  title: string;
  description: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={props.onClick}
      class="rounded border border-nu-purple-200 bg-white p-4 text-left transition hover:border-nu-purple-400"
    >
      <div class="text-base font-semibold text-nu-purple">{props.title}</div>
      <div class="mt-1 text-sm text-nu-purple-700">{props.description}</div>
    </button>
  );
}

function roleLabel(r: User_Role): string {
  switch (r) {
    case User_Role.ADVISOR:
      return "Advisor";
    case User_Role.STAFF_STAFFER:
      return "Staffer";
    case User_Role.STAFF_ADMIN:
      return "Administrator";
    default:
      return "Unknown";
  }
}
