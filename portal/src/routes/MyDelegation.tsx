// My Delegation — the first authenticated screen an advisor sees post-sign-in.
//
// Flow:
//   1. AppShell guarantees a user + (best-effort) active conference.
//   2. On mount we look up the caller's delegations within the active
//      conference via DelegationService.ListAllDelegations. Server-side scope
//      filtering keeps advisors to their own rows; admins see everything in
//      the conference, but this screen always picks the first row matching
//      the conference. Cross-conference history is M11.
//   3. Zero rows → render the create form. Non-zero → render the edit form
//      preloaded from the existing delegation.
//   4. Edits go through UpdateDelegation with expected_version. A 409
//      (Connect Aborted) prompts the user to re-fetch.

import { onMount, Show, createSignal } from "solid-js";
import { useNavigate } from "@solidjs/router";
import { ConnectError, Code } from "@connectrpc/connect";

import {
  activeConferenceSignal,
  loadingSignal,
  userSignal,
} from "@/lib/session";
import { delegationClient } from "@/lib/api";
import type { Delegation } from "@/gen/numun/v1/delegations_pb";
import { User_Role } from "@/gen/numun/v1/users_pb";
import DelegationForm from "@/components/DelegationForm";
import { type DelegationFormValues } from "@/lib/delegation-schema";
import {
  delegationToForm,
  formToAddress,
  formToEstimated,
  formToPrefs,
} from "@/lib/delegation-mappers";

export default function MyDelegation() {
  const [user] = userSignal;
  const [conference] = activeConferenceSignal;
  const [, setLoading] = loadingSignal;
  const navigate = useNavigate();

  const [delegation, setDelegation] = createSignal<Delegation | null>(null);
  const [loaded, setLoaded] = createSignal(false);
  const [banner, setBanner] = createSignal<string | null>(null);

  async function loadMyDelegation() {
    setBanner(null);
    const conf = conference();
    if (!conf) {
      setLoaded(true);
      return;
    }
    setLoading(true);
    try {
      const resp = await delegationClient.listAllDelegations({
        conferenceId: conf.conferenceId,
      });
      // For advisors the server already scopes to their own delegations.
      // For admins we'd usually filter further; M4 is advisor-focused so we
      // just grab the first row.
      setDelegation(resp.items[0] ?? null);
    } catch (err) {
      setBanner(connectMessage(err) ?? "Failed to load delegation.");
    } finally {
      setLoading(false);
      setLoaded(true);
    }
  }

  onMount(loadMyDelegation);

  async function handleCreate(values: DelegationFormValues) {
    const conf = conference();
    if (!conf) {
      setBanner("No active conference; cannot create a delegation.");
      return;
    }
    setBanner(null);
    try {
      const resp = await delegationClient.createDelegation({
        conferenceId: conf.conferenceId,
        school: values.school,
        address: formToAddress(values),
        estimatedDelegates: formToEstimated(values),
        committeePreferences: formToPrefs(values),
      });
      setDelegation(resp.delegation ?? null);
    } catch (err) {
      setBanner(connectMessage(err) ?? "Failed to create delegation.");
      throw err;
    }
  }

  async function handleUpdate(values: DelegationFormValues) {
    const current = delegation();
    if (!current) return;
    setBanner(null);
    try {
      const resp = await delegationClient.updateDelegation({
        delegationId: current.id,
        school: values.school,
        address: formToAddress(values),
        estimatedDelegates: formToEstimated(values),
        committeePreferences: formToPrefs(values),
        expectedVersion: current.version,
      });
      setDelegation(resp.delegation ?? null);
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.Aborted) {
        setBanner(
          "Another change was saved meanwhile. Reload to fetch the latest, then re-apply your edits.",
        );
      } else {
        setBanner(connectMessage(err) ?? "Failed to save changes.");
      }
      throw err;
    }
  }

  return (
    <main class="mx-auto max-w-3xl px-6 py-8">
      <header class="flex items-center justify-between">
        <div>
          <h1 class="text-2xl font-bold text-nu-purple">My Delegation</h1>
          <Show when={conference()}>
            {(c) => (
              <p class="mt-1 text-sm text-nu-purple-700">
                {c().name} · Edition {c().editionNumber} · {c().year}
              </p>
            )}
          </Show>
        </div>
        <div class="flex gap-2 text-sm">
          <button
            type="button"
            onClick={() => navigate("/dashboard")}
            class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
          >
            Back
          </button>
          <Show when={delegation()}>
            <button
              type="button"
              onClick={loadMyDelegation}
              class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
            >
              Reload
            </button>
          </Show>
        </div>
      </header>

      <Show
        when={
          user()?.role !== User_Role.ADVISOR &&
          user()?.role !== User_Role.STAFF_ADMIN
        }
      >
        <p class="mt-6 rounded bg-amber-50 p-3 text-sm text-amber-900">
          This page is for advisors. Other roles should use their admin tools.
        </p>
      </Show>

      <Show when={!conference() && loaded()}>
        <p class="mt-6 rounded bg-amber-50 p-3 text-sm text-amber-900">
          No active conference. Contact NUMUN leadership.
        </p>
      </Show>

      <Show when={loaded() && conference()}>
        <section class="mt-6">
          <Show
            when={delegation()}
            fallback={
              <CreateBlock
                onSubmit={handleCreate}
                banner={
                  <Show when={banner()}>
                    {(b) => <Banner message={b()} kind="error" />}
                  </Show>
                }
              />
            }
          >
            {(d) => (
              <EditBlock
                delegation={d()}
                onSubmit={handleUpdate}
                banner={
                  <Show when={banner()}>
                    {(b) => <Banner message={b()} kind="error" />}
                  </Show>
                }
              />
            )}
          </Show>
        </section>
      </Show>
    </main>
  );
}

function CreateBlock(props: {
  onSubmit: (v: DelegationFormValues) => Promise<void>;
  banner: ReturnType<typeof Show>;
}) {
  return (
    <div class="rounded border border-nu-purple-200 bg-white p-6">
      <h2 class="text-lg font-semibold text-nu-purple-700">
        Register your school
      </h2>
      <p class="mt-1 text-sm text-nu-purple-700">
        Once submitted, NUMUN staff will review and approve your delegation.
      </p>
      <div class="mt-4">
        <DelegationForm
          onSubmit={props.onSubmit}
          submitLabel="Submit registration"
          banner={props.banner}
        />
      </div>
    </div>
  );
}

function EditBlock(props: {
  delegation: Delegation;
  onSubmit: (v: DelegationFormValues) => Promise<void>;
  banner: ReturnType<typeof Show>;
}) {
  const statusLabel = (s: Delegation["status"]): string => {
    // 1=pending, 2=approved, 3=rejected (mirror of generated enum order)
    switch (s) {
      case 1:
        return "Pending review";
      case 2:
        return "Approved";
      case 3:
        return "Rejected";
      default:
        return "—";
    }
  };
  return (
    <div class="space-y-4">
      <div class="flex items-center gap-3 rounded border border-nu-purple-200 bg-white p-4 text-sm">
        <span class="rounded bg-nu-purple-50 px-2 py-1 text-xs uppercase tracking-wide text-nu-purple-700">
          {statusLabel(props.delegation.status)}
        </span>
        <span class="text-nu-purple-700">
          Version {props.delegation.version}
        </span>
      </div>
      <div class="rounded border border-nu-purple-200 bg-white p-6">
        <DelegationForm
          initialValues={delegationToForm(props.delegation)}
          onSubmit={props.onSubmit}
          submitLabel="Save changes"
          banner={props.banner}
        />
      </div>
    </div>
  );
}

function Banner(props: { message: string; kind: "error" | "info" }) {
  return (
    <div
      class={
        props.kind === "error"
          ? "rounded bg-red-50 p-3 text-sm text-red-900"
          : "rounded bg-nu-purple-50 p-3 text-sm text-nu-purple-700"
      }
    >
      {props.message}
    </div>
  );
}

function connectMessage(err: unknown): string | null {
  if (err instanceof ConnectError) {
    return err.message;
  }
  if (err instanceof Error) {
    return err.message;
  }
  return null;
}
