// Admin Email Health — staff-admin reviews suppressed users (bounced /
// complained recipients) and can manually un-suppress them. EMAIL.md §9.

import { createSignal, For, onMount, Show } from "solid-js";
import { useNavigate } from "@solidjs/router";
import { ConnectError, Code } from "@connectrpc/connect";

import { emailHealthClient, userClient } from "@/lib/api";
import { userSignal } from "@/lib/session";
import { User_Role } from "@/gen/numun/v1/users_pb";
import type { SuppressedUser } from "@/gen/numun/v1/email_health_pb";

export default function AdminEmailHealth() {
  const [user] = userSignal;
  const navigate = useNavigate();

  const [items, setItems] = createSignal<SuppressedUser[]>([]);
  const [loaded, setLoaded] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [busy, setBusy] = createSignal<string | null>(null);

  async function load() {
    setError(null);
    try {
      const resp = await emailHealthClient.listSuppressed({});
      setItems(resp.items);
    } catch (err) {
      setError(connectMessage(err) ?? "Failed to load suppressed users.");
    } finally {
      setLoaded(true);
    }
  }

  onMount(load);

  async function unsuppress(row: SuppressedUser) {
    setBusy(row.userId);
    setError(null);
    try {
      // Resolve the current user version so the optimistic-lock preconditioned
      // update can land cleanly.
      const profile = await userClient.getUser({ userId: row.userId });
      const version = profile.user?.version ?? 1;
      await emailHealthClient.unsuppress({
        userId: row.userId,
        expectedVersion: version,
      });
      setItems(items().filter((it) => it.userId !== row.userId));
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.Aborted) {
        setError("Version mismatch. Reload and try again.");
      } else {
        setError(connectMessage(err) ?? "Unsuppress failed.");
      }
    } finally {
      setBusy(null);
    }
  }

  return (
    <main class="mx-auto max-w-4xl px-6 py-8">
      <header class="flex items-center justify-between">
        <h1 class="text-2xl font-bold text-nu-purple">Email Health</h1>
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
        <p class="mt-2 text-sm text-nu-purple-700">
          Users whose recent SES delivery feedback was a bounce or complaint.
          Subsequent backend sends to them are suppressed until you unsuppress
          here.
        </p>

        <Show when={error()}>
          {(e) => (
            <p class="mt-4 rounded bg-red-50 p-3 text-sm text-red-900">{e()}</p>
          )}
        </Show>

        <Show when={loaded()}>
          <Show
            when={items().length > 0}
            fallback={
              <p class="mt-6 text-sm text-nu-purple-700">
                No suppressed users. Inbox is healthy.
              </p>
            }
          >
            <table class="mt-6 w-full text-sm">
              <thead class="text-left text-nu-purple-700">
                <tr>
                  <th class="pb-2">Name</th>
                  <th class="pb-2">Email</th>
                  <th class="pb-2">Status</th>
                  <th class="pb-2"></th>
                </tr>
              </thead>
              <tbody>
                <For each={items()}>
                  {(row) => (
                    <tr class="border-t border-nu-purple-200">
                      <td class="py-2">{row.name || "—"}</td>
                      <td class="py-2">{row.email}</td>
                      <td class="py-2 capitalize">{row.status}</td>
                      <td class="py-2 text-right">
                        <button
                          type="button"
                          disabled={busy() === row.userId}
                          onClick={() => unsuppress(row)}
                          class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700 disabled:opacity-50"
                        >
                          {busy() === row.userId ? "…" : "Unsuppress"}
                        </button>
                      </td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
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
