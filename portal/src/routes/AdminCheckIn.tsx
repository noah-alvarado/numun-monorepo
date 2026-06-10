// Day-of Check-In — staff (admin or staffer) searches delegates by name and
// taps Check In. Backed by DelegateService.SearchDelegates and CheckIn.
//
// Scope: any signed-in user with scope on the active conference. The server
// filters results to the caller's scope; advisors get a near-empty result set
// for delegations they don't advise, which is fine — they won't reach this
// page via the normal navigation.

import { createSignal, For, Show } from "solid-js";
import { useNavigate } from "@solidjs/router";
import { ConnectError } from "@connectrpc/connect";

import { delegateClient } from "@/lib/api";
import { activeConferenceSignal, userSignal } from "@/lib/session";
import { User_Role } from "@/gen/numun/v1/users_pb";
import type {
  Delegate,
  SearchDelegatesResponse_Hit,
} from "@/gen/numun/v1/delegates_pb";

export default function AdminCheckIn() {
  const [user] = userSignal;
  const [conference] = activeConferenceSignal;
  const navigate = useNavigate();

  const [query, setQuery] = createSignal("");
  const [hits, setHits] = createSignal<SearchDelegatesResponse_Hit[]>([]);
  const [searched, setSearched] = createSignal(false);
  const [truncated, setTruncated] = createSignal(false);
  const [busy, setBusy] = createSignal<string | null>(null);
  const [error, setError] = createSignal<string | null>(null);

  async function search(e?: SubmitEvent) {
    if (e) e.preventDefault();
    const conf = conference();
    const q = query().trim();
    if (!conf) {
      setError("No active conference.");
      return;
    }
    if (q.length < 2) {
      setError("Type at least 2 characters.");
      return;
    }
    setError(null);
    try {
      const resp = await delegateClient.searchDelegates({
        conferenceId: conf.conferenceId,
        query: q,
        limit: 50,
      });
      setHits(resp.items);
      setTruncated(resp.truncated);
      setSearched(true);
    } catch (err) {
      setError(connectMessage(err) ?? "Search failed.");
    }
  }

  async function checkIn(d: Delegate) {
    setBusy(d.id);
    setError(null);
    try {
      const resp = await delegateClient.checkIn({ delegateId: d.id });
      // Replace the hit's delegate with the server-returned version so the
      // checked_in_at timestamp renders without a re-search.
      setHits(
        hits().map((h) =>
          h.delegate?.id === d.id && resp.delegate
            ? { ...h, delegate: resp.delegate }
            : h,
        ),
      );
    } catch (err) {
      setError(connectMessage(err) ?? "Check-in failed.");
    } finally {
      setBusy(null);
    }
  }

  return (
    <main class="mx-auto max-w-3xl px-6 py-8">
      <header class="flex items-center justify-between">
        <div>
          <h1 class="text-2xl font-bold text-nu-purple">Day-of Check-In</h1>
          <Show when={conference()}>
            {(c) => <p class="mt-1 text-sm text-nu-purple-700">{c().name}</p>}
          </Show>
        </div>
        <button
          type="button"
          onClick={() => navigate("/dashboard")}
          class="rounded border border-nu-purple-300 px-3 py-1 text-sm text-nu-purple-700"
        >
          Back
        </button>
      </header>

      <Show
        when={
          user()?.role === User_Role.STAFF_ADMIN ||
          user()?.role === User_Role.STAFF_STAFFER
        }
        fallback={
          <p class="mt-6 rounded bg-amber-50 p-3 text-sm text-amber-900">
            Staff only.
          </p>
        }
      >
        <form onSubmit={search} class="mt-6 flex items-center gap-2">
          <input
            type="search"
            autofocus
            value={query()}
            onInput={(e) => setQuery(e.currentTarget.value)}
            placeholder="Search by first or last name…"
            class="flex-1 rounded border border-nu-purple-300 px-3 py-2 text-sm"
          />
          <button
            type="submit"
            class="rounded bg-nu-purple px-3 py-2 text-sm text-white"
          >
            Search
          </button>
        </form>

        <Show when={error()}>
          {(e) => (
            <p class="mt-3 rounded bg-red-50 p-3 text-sm text-red-900">{e()}</p>
          )}
        </Show>

        <Show when={searched()}>
          <Show
            when={hits().length > 0}
            fallback={
              <p class="mt-6 text-sm text-nu-purple-700">No matches.</p>
            }
          >
            <ul class="mt-6 space-y-2">
              <For each={hits()}>
                {(hit) => {
                  const d = hit.delegate!;
                  const checkedIn = d.checkedInAt !== undefined;
                  return (
                    <li class="flex items-center justify-between rounded border border-nu-purple-200 bg-white px-4 py-3">
                      <div>
                        <div class="font-semibold">
                          {d.lastName}, {d.firstName}
                        </div>
                        <div class="text-xs text-nu-purple-700">
                          {hit.delegationSchool}
                          {checkedIn ? " · checked in" : ""}
                        </div>
                      </div>
                      <button
                        type="button"
                        disabled={busy() === d.id || checkedIn}
                        onClick={() => checkIn(d)}
                        class="rounded bg-nu-purple px-3 py-1 text-sm text-white disabled:opacity-50"
                      >
                        {checkedIn
                          ? "✓ Checked in"
                          : busy() === d.id
                            ? "…"
                            : "Check in"}
                      </button>
                    </li>
                  );
                }}
              </For>
            </ul>
            <Show when={truncated()}>
              <p class="mt-3 text-xs text-nu-purple-700">
                Showing the first 50 matches. Narrow the query to see more.
              </p>
            </Show>
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
