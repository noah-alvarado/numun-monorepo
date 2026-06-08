// My Delegation — the first authenticated screen an advisor sees post-sign-in,
// and the per-delegation drilldown reachable via /delegations/:delegationId
// for staff-admin (and direct deep-links).
//
// Flow:
//   1. AppShell guarantees a user + (best-effort) active conference.
//   2. If the URL carries :delegationId we load that specific delegation via
//      DelegationService.GetDelegation. Otherwise we look up the caller's own
//      delegations via ListAllDelegations and pick the first row matching the
//      active conference (M4 advisor flow).
//   3. Zero rows on the advisor flow → render the create form. Non-zero →
//      render the edit form preloaded from the existing delegation, alongside
//      tab navigation (Overview / Delegates / Payments).
//   4. Edits go through UpdateDelegation with expected_version. A 409
//      (Connect Aborted) prompts the user to re-fetch.

import { onMount, Show, createSignal, createMemo } from "solid-js";
import { useNavigate, useParams, useSearchParams } from "@solidjs/router";
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
import DelegationPayments from "@/routes/DelegationPayments";
import { type DelegationFormValues } from "@/lib/delegation-schema";
import {
  delegationToForm,
  formToAddress,
  formToEstimated,
  formToPrefs,
} from "@/lib/delegation-mappers";

type TabKey = "overview" | "delegates" | "payments";

const TABS: { key: TabKey; label: string }[] = [
  { key: "overview", label: "Overview" },
  { key: "delegates", label: "Delegates" },
  { key: "payments", label: "Payments" },
];

export default function MyDelegation() {
  const [user] = userSignal;
  const [conference] = activeConferenceSignal;
  const [, setLoading] = loadingSignal;
  const navigate = useNavigate();
  const params = useParams<{ delegationId?: string }>();
  const [searchParams, setSearchParams] = useSearchParams();

  const [delegation, setDelegation] = createSignal<Delegation | null>(null);
  const [loaded, setLoaded] = createSignal(false);
  const [banner, setBanner] = createSignal<string | null>(null);

  const activeTab = createMemo<TabKey>(() => {
    const raw = Array.isArray(searchParams.tab)
      ? searchParams.tab[0]
      : searchParams.tab;
    if (raw === "delegates" || raw === "payments" || raw === "overview") {
      return raw;
    }
    return "overview";
  });

  function selectTab(t: TabKey) {
    setSearchParams(
      { tab: t === "overview" ? undefined : t },
      { replace: true },
    );
  }

  async function loadByParam(delegationId: string) {
    setBanner(null);
    setLoading(true);
    try {
      const resp = await delegationClient.getDelegation({ delegationId });
      setDelegation(resp.delegation ?? null);
    } catch (err) {
      setBanner(connectMessage(err) ?? "Failed to load delegation.");
    } finally {
      setLoading(false);
      setLoaded(true);
    }
  }

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
      setDelegation(resp.items[0] ?? null);
    } catch (err) {
      setBanner(connectMessage(err) ?? "Failed to load delegation.");
    } finally {
      setLoading(false);
      setLoaded(true);
    }
  }

  async function reload() {
    const id = params.delegationId;
    if (id) {
      await loadByParam(id);
    } else {
      await loadMyDelegation();
    }
  }

  onMount(reload);

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
    <main class="mx-auto max-w-5xl px-6 py-8">
      <header class="flex items-center justify-between">
        <div>
          <h1 class="text-2xl font-bold text-nu-purple">
            {params.delegationId ? "Delegation" : "My Delegation"}
          </h1>
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
              onClick={reload}
              class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
            >
              Reload
            </button>
          </Show>
        </div>
      </header>

      <Show
        when={
          !params.delegationId &&
          user()?.role !== User_Role.ADVISOR &&
          user()?.role !== User_Role.STAFF_ADMIN
        }
      >
        <p class="mt-6 rounded bg-amber-50 p-3 text-sm text-amber-900">
          This page is for advisors. Other roles should use their admin tools.
        </p>
      </Show>

      <Show when={!conference() && loaded() && !params.delegationId}>
        <p class="mt-6 rounded bg-amber-50 p-3 text-sm text-amber-900">
          No active conference. Contact NUMUN leadership.
        </p>
      </Show>

      <Show when={loaded()}>
        <Show
          when={delegation()}
          fallback={
            <Show when={!params.delegationId && conference()}>
              <section class="mt-6">
                <CreateBlock
                  onSubmit={handleCreate}
                  banner={
                    <Show when={banner()}>
                      {(b) => <Banner message={b()} kind="error" />}
                    </Show>
                  }
                />
              </section>
            </Show>
          }
        >
          {(d) => (
            <section class="mt-6 space-y-4">
              <TabStrip current={activeTab()} onSelect={selectTab} />
              <Show when={banner()}>
                {(b) => <Banner message={b()} kind="error" />}
              </Show>
              <Show when={activeTab() === "overview"}>
                <EditBlock delegation={d()} onSubmit={handleUpdate} />
              </Show>
              <Show when={activeTab() === "delegates"}>
                <DelegatesPlaceholder delegationId={d().id} />
              </Show>
              <Show when={activeTab() === "payments"}>
                <DelegationPayments delegation={d()} />
              </Show>
            </section>
          )}
        </Show>
      </Show>
    </main>
  );
}

function TabStrip(props: { current: TabKey; onSelect: (t: TabKey) => void }) {
  return (
    <nav class="flex gap-1 border-b border-nu-purple-200">
      {TABS.map((t) => (
        <button
          type="button"
          onClick={() => props.onSelect(t.key)}
          class={
            props.current === t.key
              ? "border-b-2 border-nu-purple px-3 py-2 text-sm font-semibold text-nu-purple"
              : "border-b-2 border-transparent px-3 py-2 text-sm text-nu-purple-700 hover:border-nu-purple-300"
          }
        >
          {t.label}
        </button>
      ))}
    </nav>
  );
}

function DelegatesPlaceholder(props: { delegationId: string }) {
  return (
    <div class="rounded border border-nu-purple-200 bg-white p-6 text-sm">
      <p class="text-nu-purple-700">Delegate roster lives on its own screen.</p>
      <a
        href={`/delegations/${props.delegationId}/delegates/import`}
        class="mt-3 inline-block rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700 hover:bg-nu-purple-50"
      >
        Bulk import delegates
      </a>
    </div>
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
}) {
  const statusLabel = (s: Delegation["status"]): string => {
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
        <a
          href={`/delegations/${props.delegation.id}/delegates/import`}
          class="ml-auto rounded border border-nu-purple-300 px-3 py-1 text-xs text-nu-purple-700 hover:bg-nu-purple-50"
        >
          Bulk import delegates
        </a>
      </div>
      <div class="rounded border border-nu-purple-200 bg-white p-6">
        <DelegationForm
          initialValues={delegationToForm(props.delegation)}
          onSubmit={props.onSubmit}
          submitLabel="Save changes"
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
