// Conferences — staff-admin creates conferences and moves them through their
// lifecycle (draft → open for registration → in progress → closed →
// archived). Only one conference may be open or in progress at a time; the
// API refuses a second and names the one to close.

import { createSignal, For, onMount, Show } from "solid-js";
import { Code, ConnectError } from "@connectrpc/connect";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";

import { conferenceClient } from "@/lib/api";
import { connectMessage } from "@/lib/errors";
import { loadActiveConference, userSignal } from "@/lib/session";
import PageHeader from "@/components/PageHeader";
import Modal from "@/components/Modal";
import {
  Conference_Status,
  type Conference,
} from "@/gen/numun/v1/conferences_pb";
import { User_Role } from "@/gen/numun/v1/users_pb";

const STATUSES: [Conference_Status, string][] = [
  [Conference_Status.DRAFT, "Draft"],
  [Conference_Status.OPEN_FOR_REGISTRATION, "Open for registration"],
  [Conference_Status.IN_PROGRESS, "In progress"],
  [Conference_Status.CLOSED, "Closed"],
  [Conference_Status.ARCHIVED, "Archived"],
];

const statusLabel = (s: Conference_Status) =>
  STATUSES.find(([v]) => v === s)?.[1] ?? "—";

type Draft = {
  name: string;
  editionNumber: string;
  year: string;
  startsOn: string;
  endsOn: string;
  status: Conference_Status;
};

const day = (c: Conference, which: "startsAt" | "endsAt") => {
  const ts = c[which];
  return ts ? timestampDate(ts).toISOString().slice(0, 10) : "";
};

const input = "w-full rounded border border-nu-purple-300 px-2 py-1";

export default function AdminConferences() {
  const [user] = userSignal;
  const [items, setItems] = createSignal<Conference[]>([]);
  const [loaded, setLoaded] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [notice, setNotice] = createSignal<string | null>(null);
  // null = closed; "new" = create; otherwise the conference being edited.
  const [editing, setEditing] = createSignal<Conference | "new" | null>(null);
  const [draft, setDraft] = createSignal<Draft | null>(null);
  const [formError, setFormError] = createSignal<string | null>(null);
  const [busy, setBusy] = createSignal(false);

  async function load() {
    setError(null);
    try {
      const resp = await conferenceClient.listAllConferences({});
      // The active conference first, then newest first.
      const active = (c: Conference) =>
        c.status === Conference_Status.OPEN_FOR_REGISTRATION ||
        c.status === Conference_Status.IN_PROGRESS
          ? 0
          : 1;
      setItems(
        [...resp.items].sort(
          (a, b) =>
            active(a) - active(b) ||
            b.year - a.year ||
            b.editionNumber - a.editionNumber,
        ),
      );
    } catch (err) {
      setError(connectMessage(err) ?? "Failed to load conferences.");
    } finally {
      setLoaded(true);
    }
  }

  onMount(load);

  function open(target: Conference | "new") {
    setFormError(null);
    setNotice(null);
    setEditing(target);
    const year = new Date().getFullYear() + 1;
    setDraft(
      target === "new"
        ? {
            name: "",
            editionNumber: "",
            year: String(year),
            startsOn: "",
            endsOn: "",
            status: Conference_Status.DRAFT,
          }
        : {
            name: target.name,
            editionNumber: String(target.editionNumber),
            year: String(target.year),
            startsOn: day(target, "startsAt"),
            endsOn: day(target, "endsAt"),
            status: target.status,
          },
    );
  }

  const set = (patch: Partial<Draft>) => setDraft({ ...draft()!, ...patch });

  async function save(e: SubmitEvent) {
    e.preventDefault();
    const d = draft()!;
    const target = editing();
    if (d.endsOn <= d.startsOn) {
      setFormError("The conference must end after it starts.");
      return;
    }
    const fields = {
      name: d.name.trim(),
      editionNumber: Number(d.editionNumber),
      year: Number(d.year),
      startsAt: timestampFromDate(new Date(`${d.startsOn}T00:00:00Z`)),
      endsAt: timestampFromDate(new Date(`${d.endsOn}T00:00:00Z`)),
      status: d.status,
    };
    setBusy(true);
    setFormError(null);
    try {
      if (target === "new") {
        await conferenceClient.createConference(fields);
        setNotice(`Created ${fields.name}.`);
      } else if (target) {
        await conferenceClient.updateConference({
          conferenceId: target.id,
          ...fields,
          expectedVersion: target.version,
        });
        setNotice(`Saved ${fields.name}.`);
      }
      setEditing(null);
      await load();
      // The status change may have opened or closed the active conference.
      await loadActiveConference();
    } catch (err) {
      setFormError(
        err instanceof ConnectError && err.code === Code.Aborted
          ? "Someone else changed this conference meanwhile. Close and reopen it to see their changes."
          : (connectMessage(err) ?? "Failed to save the conference."),
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <main class="mx-auto max-w-4xl px-6 py-8">
      <PageHeader
        title="Conferences"
        actions={
          <Show when={user()?.role === User_Role.STAFF_ADMIN}>
            <button
              type="button"
              onClick={() => open("new")}
              class="rounded bg-nu-purple px-3 py-1 text-white"
            >
              New conference
            </button>
          </Show>
        }
      />

      <Show
        when={user()?.role === User_Role.STAFF_ADMIN}
        fallback={
          <p class="mt-6 rounded bg-amber-50 p-3 text-sm text-amber-900">
            Admin only.
          </p>
        }
      >
        <p class="mt-4 text-sm text-nu-purple-700">
          Only one conference can be open for registration or in progress at a
          time; that one is what the site and portal show.
        </p>
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
          <table class="mt-4 w-full rounded border border-nu-purple-200 bg-white text-sm">
            <thead>
              <tr class="border-b border-nu-purple-200 text-left text-xs uppercase tracking-wide text-nu-purple-700">
                <th class="p-2">Name</th>
                <th class="p-2">Edition</th>
                <th class="p-2">Dates</th>
                <th class="p-2">Status</th>
                <th class="p-2" />
              </tr>
            </thead>
            <tbody>
              <For each={items()}>
                {(c) => (
                  <tr class="border-b border-nu-purple-100">
                    <td class="p-2 font-semibold">{c.name}</td>
                    <td class="p-2">
                      {c.editionNumber} · {c.year}
                    </td>
                    <td class="p-2">
                      {day(c, "startsAt")} → {day(c, "endsAt")}
                    </td>
                    <td class="p-2">{statusLabel(c.status)}</td>
                    <td class="p-2 text-right">
                      <button
                        type="button"
                        onClick={() => open(c)}
                        aria-label={`Edit ${c.name}`}
                        class="rounded border border-nu-purple-300 px-2 py-0.5 text-nu-purple-700"
                      >
                        Edit
                      </button>
                    </td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </Show>
      </Show>

      <Show when={editing() && draft()}>
        {(d) => (
          <Modal
            title={
              editing() === "new"
                ? "New conference"
                : `Edit ${(editing() as Conference).name}`
            }
            onClose={() => setEditing(null)}
          >
            <form onSubmit={save} class="mt-3 grid grid-cols-2 gap-3 text-sm">
              <label class="col-span-2 block">
                <span class="text-xs text-nu-purple-700">Name</span>
                <input
                  required
                  value={d().name}
                  onInput={(e) => set({ name: e.currentTarget.value })}
                  class={input}
                />
              </label>
              <label class="block">
                <span class="text-xs text-nu-purple-700">Edition</span>
                <input
                  required
                  type="number"
                  min="1"
                  value={d().editionNumber}
                  onInput={(e) => set({ editionNumber: e.currentTarget.value })}
                  class={input}
                />
              </label>
              <label class="block">
                <span class="text-xs text-nu-purple-700">Year</span>
                <input
                  required
                  type="number"
                  min="2000"
                  max="2100"
                  value={d().year}
                  onInput={(e) => set({ year: e.currentTarget.value })}
                  class={input}
                />
              </label>
              <label class="block">
                <span class="text-xs text-nu-purple-700">Starts</span>
                <input
                  required
                  type="date"
                  value={d().startsOn}
                  onInput={(e) => set({ startsOn: e.currentTarget.value })}
                  class={input}
                />
              </label>
              <label class="block">
                <span class="text-xs text-nu-purple-700">Ends</span>
                <input
                  required
                  type="date"
                  value={d().endsOn}
                  onInput={(e) => set({ endsOn: e.currentTarget.value })}
                  class={input}
                />
              </label>
              <label class="col-span-2 block">
                <span class="text-xs text-nu-purple-700">Status</span>
                <select
                  value={String(d().status)}
                  onChange={(e) =>
                    set({
                      status: Number(
                        e.currentTarget.value,
                      ) as Conference_Status,
                    })
                  }
                  class={input}
                >
                  <For each={STATUSES}>
                    {([v, label]) => <option value={String(v)}>{label}</option>}
                  </For>
                </select>
              </label>
              <Show when={formError()}>
                {(e) => (
                  <p class="col-span-2 rounded bg-red-50 p-2 text-red-900">
                    {e()}
                  </p>
                )}
              </Show>
              <div class="col-span-2 flex justify-end gap-2">
                <button
                  type="button"
                  onClick={() => setEditing(null)}
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
          </Modal>
        )}
      </Show>
    </main>
  );
}
