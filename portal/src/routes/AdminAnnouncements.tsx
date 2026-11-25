// Announcements — staff-admin emails the advisors of a conference's
// delegations. Preview shows who would receive it (advisors who opted in and
// whose email isn't suppressed) before anything is sent. The body is plain
// text; paragraphs and line breaks carry over to the HTML version.

import { createResource, createSignal, For, Show } from "solid-js";
import { timestampDate } from "@bufbuild/protobuf/wkt";

import { announcementClient } from "@/lib/api";
import { connectMessage } from "@/lib/errors";
import { activeConferenceSignal, userSignal } from "@/lib/session";
import PageHeader from "@/components/PageHeader";
import Modal from "@/components/Modal";
import { User_Role } from "@/gen/numun/v1/users_pb";

const AUDIENCES = [
  ["approved", "Advisors of approved delegations"],
  ["pending", "Advisors of pending delegations"],
] as const;

type Audience = (typeof AUDIENCES)[number][0];

const audienceLabel = (raw: string) => {
  try {
    const status = (JSON.parse(raw) as { delegationStatus?: string })
      .delegationStatus;
    return AUDIENCES.find(([v]) => v === status)?.[1] ?? "Advisors";
  } catch {
    return "Advisors";
  }
};

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

// Blank lines separate paragraphs; single newlines become <br>.
function textToHtml(text: string): string {
  return text
    .trim()
    .split(/\n\s*\n/)
    .map((p) => `<p>${escapeHtml(p.trim()).replace(/\n/g, "<br>")}</p>`)
    .join("\n");
}

export default function AdminAnnouncements() {
  const [user] = userSignal;
  const [conference] = activeConferenceSignal;
  const [subject, setSubject] = createSignal("");
  const [body, setBody] = createSignal("");
  const [audience, setAudience] = createSignal<Audience>("approved");
  const [preview, setPreview] = createSignal<{
    count: number;
    sample: string[];
  } | null>(null);
  const [error, setError] = createSignal<string | null>(null);
  const [notice, setNotice] = createSignal<string | null>(null);
  const [busy, setBusy] = createSignal(false);

  const [history, { refetch }] = createResource(async () => {
    const resp = await announcementClient.listAnnouncements({});
    return resp.items;
  });

  const filter = () => ({
    conferenceId: conference()?.conferenceId ?? "",
    delegationStatus: audience(),
  });

  async function onPreview(e: SubmitEvent) {
    e.preventDefault();
    setError(null);
    setNotice(null);
    setBusy(true);
    try {
      const resp = await announcementClient.previewSendAudience({
        audience: filter(),
      });
      setPreview({
        count: resp.recipientCount,
        sample: resp.sampleRecipients,
      });
    } catch (err) {
      setError(connectMessage(err) ?? "Failed to preview the audience.");
    } finally {
      setBusy(false);
    }
  }

  async function send() {
    setBusy(true);
    setError(null);
    try {
      const resp = await announcementClient.send({
        subject: subject().trim(),
        bodyText: body().trim(),
        bodyHtml: textToHtml(body()),
        audience: filter(),
      });
      const n = resp.announcement?.recipientCount ?? preview()?.count ?? 0;
      setNotice(
        `Sent "${subject().trim()}" to ${n} advisor${n === 1 ? "" : "s"}.`,
      );
      setSubject("");
      setBody("");
      setPreview(null);
      await refetch();
    } catch (err) {
      setError(connectMessage(err) ?? "Failed to send the announcement.");
      setPreview(null);
    } finally {
      setBusy(false);
    }
  }

  return (
    <main class="mx-auto max-w-3xl px-6 py-8">
      <PageHeader title="Announcements" subtitle={conference()?.name} />
      <Show
        when={user()?.role === User_Role.STAFF_ADMIN}
        fallback={
          <p class="mt-6 rounded bg-amber-50 p-3 text-sm text-amber-900">
            Admin only.
          </p>
        }
      >
        <Show
          when={conference()}
          fallback={
            <p class="mt-6 rounded bg-amber-50 p-3 text-sm text-amber-900">
              No active conference.
            </p>
          }
        >
          <form
            aria-label="New announcement"
            onSubmit={onPreview}
            class="mt-6 space-y-3 rounded border border-nu-purple-200 bg-white p-4 text-sm"
          >
            <label class="block">
              <span class="text-xs text-nu-purple-700">To</span>
              <select
                value={audience()}
                onChange={(e) => setAudience(e.currentTarget.value as Audience)}
                class="w-full rounded border border-nu-purple-300 px-2 py-1"
              >
                <For each={AUDIENCES}>
                  {([v, label]) => <option value={v}>{label}</option>}
                </For>
              </select>
            </label>
            <label class="block">
              <span class="text-xs text-nu-purple-700">Subject</span>
              <input
                required
                maxLength={200}
                value={subject()}
                onInput={(e) => setSubject(e.currentTarget.value)}
                class="w-full rounded border border-nu-purple-300 px-2 py-1"
              />
            </label>
            <label class="block">
              <span class="text-xs text-nu-purple-700">
                Message (blank lines start new paragraphs)
              </span>
              <textarea
                required
                rows={8}
                value={body()}
                onInput={(e) => setBody(e.currentTarget.value)}
                class="w-full rounded border border-nu-purple-300 px-2 py-1"
              />
            </label>
            <p class="text-xs text-nu-purple-700">
              Only advisors who opted in to announcements and whose email isn't
              bouncing receive it.
            </p>
            <div class="flex justify-end">
              <button
                type="submit"
                disabled={busy()}
                class="rounded bg-nu-purple px-3 py-1 text-white disabled:opacity-50"
              >
                Review and send
              </button>
            </div>
          </form>

          <Show when={error()}>
            {(e) => (
              <p class="mt-4 rounded bg-red-50 p-3 text-sm text-red-900">
                {e()}
              </p>
            )}
          </Show>
          <Show when={notice()}>
            {(n) => (
              <p class="mt-4 rounded bg-green-50 p-3 text-sm text-green-900">
                {n()}
              </p>
            )}
          </Show>

          <section class="mt-8">
            <h2 class="text-base font-semibold text-nu-purple">Sent</h2>
            <Show
              when={(history() ?? []).length > 0}
              fallback={
                <p class="mt-2 text-sm text-nu-purple-700">
                  {history.loading ? "Loading…" : "Nothing sent yet."}
                </p>
              }
            >
              <ul class="mt-2 divide-y divide-nu-purple-100 rounded border border-nu-purple-200 bg-white text-sm">
                <For each={history()}>
                  {(a) => (
                    <li class="p-3">
                      <div class="flex justify-between gap-4">
                        <span class="font-semibold">{a.subject}</span>
                        <span class="text-nu-purple-700">
                          {a.sentAt
                            ? timestampDate(a.sentAt).toLocaleString()
                            : ""}
                        </span>
                      </div>
                      <div class="text-nu-purple-700">
                        {audienceLabel(a.audienceFilter)} · {a.recipientCount}{" "}
                        recipient
                        {a.recipientCount === 1 ? "" : "s"}
                      </div>
                    </li>
                  )}
                </For>
              </ul>
            </Show>
          </section>
        </Show>
      </Show>

      <Show when={preview()}>
        {(p) => (
          <Modal
            title={`Send to ${p().count} advisor${p().count === 1 ? "" : "s"}?`}
            onClose={() => setPreview(null)}
          >
            <p class="mt-2 text-sm text-nu-purple-700">
              "{subject().trim()}" goes to{" "}
              {AUDIENCES.find(([v]) => v === audience())?.[1].toLowerCase()} in{" "}
              {conference()?.name}.
            </p>
            <Show when={p().sample.length > 0}>
              <p class="mt-2 text-xs text-nu-purple-700">
                Including {p().sample.join(", ")}
                {p().count > p().sample.length ? ", …" : ""}
              </p>
            </Show>
            <div class="mt-4 flex justify-end gap-2 text-sm">
              <button
                type="button"
                onClick={() => setPreview(null)}
                class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
              >
                Cancel
              </button>
              <button
                type="button"
                disabled={busy() || p().count === 0}
                onClick={send}
                class="rounded bg-nu-purple px-3 py-1 text-white disabled:opacity-50"
              >
                {busy() ? "Sending…" : "Send"}
              </button>
            </div>
          </Modal>
        )}
      </Show>
    </main>
  );
}
