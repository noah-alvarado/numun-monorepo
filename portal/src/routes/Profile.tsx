// Profile — the signed-in user edits their name, phone and whether they get
// NUMUN announcement emails. Email is the sign-in identity, so it's shown but
// not editable here.

import { createSignal, Show } from "solid-js";

import { userClient } from "@/lib/api";
import { conflictMessage, connectMessage, isConflict } from "@/lib/errors";
import { normalizePhone } from "@/lib/phone";
import { userSignal } from "@/lib/session";
import PageHeader from "@/components/PageHeader";

export default function Profile() {
  const [user, setUser] = userSignal;
  const [name, setName] = createSignal(user()?.name ?? "");
  const [phone, setPhone] = createSignal(user()?.phone ?? "");
  const [optIn, setOptIn] = createSignal(user()?.announcementsOptIn ?? false);
  const [phoneError, setPhoneError] = createSignal<string | null>(null);
  const [error, setError] = createSignal<string | null>(null);
  const [saved, setSaved] = createSignal(false);
  const [busy, setBusy] = createSignal(false);

  async function showLatest() {
    const latest = (await userClient.getMe({}).catch(() => null))?.user;
    if (!latest) return;
    setUser(latest);
    setName(latest.name);
    setPhone(latest.phone);
    setOptIn(latest.announcementsOptIn);
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    const me = user();
    if (!me) return;
    setSaved(false);
    setError(null);
    let e164 = "";
    if (phone().trim()) {
      const result = normalizePhone(phone());
      if (!result.ok) {
        setPhoneError(result.reason);
        return;
      }
      e164 = result.e164;
    }
    setPhoneError(null);
    setBusy(true);
    try {
      const resp = await userClient.updateUser({
        userId: me.id,
        name: name().trim(),
        phone: e164,
        announcementsOptIn: optIn(),
        expectedVersion: me.version,
      });
      if (resp.user) {
        setUser(resp.user);
        setPhone(resp.user.phone);
      }
      setSaved(true);
    } catch (err) {
      if (isConflict(err)) {
        await showLatest();
        setError(conflictMessage("your profile"));
        return;
      }
      setError(connectMessage(err) ?? "Failed to save your profile.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main class="mx-auto max-w-xl px-6 py-8">
      <PageHeader title="Profile" />
      <form
        onSubmit={save}
        class="mt-6 space-y-4 rounded border border-nu-purple-200 bg-white p-6 text-sm"
      >
        <div>
          <span class="text-xs text-nu-purple-700">Email</span>
          <p class="mt-1">{user()?.email || "—"}</p>
        </div>
        <label class="block">
          <span class="text-xs text-nu-purple-700">Name</span>
          <input
            required
            value={name()}
            onInput={(e) => setName(e.currentTarget.value)}
            class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
          />
        </label>
        <label class="block">
          <span class="text-xs text-nu-purple-700">Phone</span>
          <input
            type="tel"
            value={phone()}
            onInput={(e) => setPhone(e.currentTarget.value)}
            aria-invalid={phoneError() ? "true" : undefined}
            aria-describedby={phoneError() ? "phone-error" : undefined}
            class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
          />
          <Show when={phoneError()}>
            <p id="phone-error" class="mt-1 text-red-700">
              {phoneError()}
            </p>
          </Show>
        </label>
        <label class="flex items-start gap-2">
          <input
            type="checkbox"
            checked={optIn()}
            onChange={(e) => setOptIn(e.currentTarget.checked)}
            class="mt-0.5"
          />
          <span>
            Email me NUMUN announcements
            <span class="block text-xs text-nu-purple-700">
              Registration and payment emails about your delegation are always
              sent.
            </span>
          </span>
        </label>
        <Show when={error()}>
          {(e) => <p class="rounded bg-red-50 p-2 text-red-900">{e()}</p>}
        </Show>
        <Show when={saved()}>
          <p class="rounded bg-green-50 p-2 text-green-900">Profile saved.</p>
        </Show>
        <div class="flex justify-end">
          <button
            type="submit"
            disabled={busy()}
            class="rounded bg-nu-purple px-3 py-1 text-white disabled:opacity-50"
          >
            {busy() ? "Saving…" : "Save profile"}
          </button>
        </div>
      </form>
    </main>
  );
}
