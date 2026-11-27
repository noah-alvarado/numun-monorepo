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
//      tab navigation (Overview / Delegates / Advisors / Payments). Admins
//      also approve or reject here and assign staffers. A rejected delegation
//      is read-only for advisors.
//   4. Edits go through UpdateDelegation with expected_version. A 409
//      (Connect Aborted) prompts the user to re-fetch.

import { For, onMount, Show, createSignal, createMemo } from "solid-js";
import { useNavigate, useParams, useSearchParams } from "@solidjs/router";
import { ConnectError, Code } from "@connectrpc/connect";

import {
  activeConferenceSignal,
  loadingSignal,
  userSignal,
} from "@/lib/session";
import { delegationClient } from "@/lib/api";
import {
  Delegation_Status,
  type Delegation,
} from "@/gen/numun/v1/delegations_pb";
import { User_Role } from "@/gen/numun/v1/users_pb";
import DelegationForm from "@/components/DelegationForm";
import DelegateRoster from "@/components/DelegateRoster";
import DelegationAdvisors from "@/components/DelegationAdvisors";
import DelegationStaff from "@/components/DelegationStaff";
import RejectDelegationDialog from "@/components/RejectDelegationDialog";
import DelegationPayments from "@/routes/DelegationPayments";
import { type DelegationFormValues } from "@/lib/delegation-schema";
import {
  delegationToForm,
  formToAddress,
  formToEstimated,
  formToPrefs,
} from "@/lib/delegation-mappers";
import { connectMessage } from "@/lib/errors";
import { conferenceLabel } from "@/lib/conference";

type TabKey = "overview" | "delegates" | "advisors" | "payments";

const TABS: { key: TabKey; label: string }[] = [
  { key: "overview", label: "Overview" },
  { key: "delegates", label: "Delegates" },
  { key: "advisors", label: "Advisors" },
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
  // The advisor's delegations in the active conference. Usually one; more
  // when they also co-advise another school.
  const [mine, setMine] = createSignal<Delegation[]>([]);
  const [loaded, setLoaded] = createSignal(false);
  const [banner, setBanner] = createSignal<string | null>(null);
  // Set when loading failed, so we don't offer to register a delegation the
  // advisor may already have.
  const [loadFailed, setLoadFailed] = createSignal(false);

  const activeTab = createMemo<TabKey>(() => {
    const raw = Array.isArray(searchParams.tab)
      ? searchParams.tab[0]
      : searchParams.tab;
    if (TABS.some((t) => t.key === raw)) return raw as TabKey;
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
      setBanner(
        err instanceof ConnectError && err.code === Code.NotFound
          ? "Delegation not found."
          : (connectMessage(err) ?? "Failed to load delegation."),
      );
    } finally {
      setLoading(false);
      setLoaded(true);
    }
  }

  async function loadMyDelegation() {
    setBanner(null);
    setLoadFailed(false);
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
      setMine(resp.items);
      const chosen = Array.isArray(searchParams.delegation)
        ? searchParams.delegation[0]
        : searchParams.delegation;
      setDelegation(
        resp.items.find((d) => d.id === chosen) ?? resp.items[0] ?? null,
      );
    } catch (err) {
      setLoadFailed(true);
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

  // A delegation opened by id may belong to a conference other than the
  // session's active one.
  const delegationConference = conferenceLabel(
    () => delegation()?.conferenceId,
  );

  const isAdmin = () => user()?.role === User_Role.STAFF_ADMIN;
  // Rejected registrations are read-only for the school (the API enforces
  // it too); staff can still edit.
  const readOnly = () =>
    !isAdmin() && delegation()?.status === Delegation_Status.REJECTED;

  const [deciding, setDeciding] = createSignal(false);
  const [rejecting, setRejecting] = createSignal(false);

  async function decide(kind: "approve" | "reject", reason = "") {
    const current = delegation();
    if (!current) return;
    setDeciding(true);
    setBanner(null);
    try {
      const resp =
        kind === "approve"
          ? await delegationClient.approve({
              delegationId: current.id,
              expectedVersion: current.version,
            })
          : await delegationClient.reject({
              delegationId: current.id,
              expectedVersion: current.version,
              reason,
            });
      setDelegation(resp.delegation ?? null);
    } catch (err) {
      setBanner(
        err instanceof ConnectError && err.code === Code.Aborted
          ? "Another change was saved meanwhile. Reload and try again."
          : (connectMessage(err) ?? "Failed to update the delegation."),
      );
    } finally {
      setDeciding(false);
      setRejecting(false);
    }
  }

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
            {params.delegationId
              ? (delegation()?.school ?? "Delegation")
              : "My Delegation"}
          </h1>
          <Show
            when={!params.delegationId}
            fallback={
              <Show when={delegationConference()}>
                {(label) => (
                  <p class="mt-1 text-sm text-nu-purple-700">{label()}</p>
                )}
              </Show>
            }
          >
            <Show when={conference()}>
              {(c) => (
                <p class="mt-1 text-sm text-nu-purple-700">
                  {c().name} · Edition {c().editionNumber} · {c().year}
                </p>
              )}
            </Show>
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
          <Show when={delegation() || loadFailed()}>
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

      <Show when={!params.delegationId && mine().length > 1}>
        <label class="mt-4 flex items-center gap-2 text-sm text-nu-purple-700">
          You advise {mine().length} delegations:
          <select
            aria-label="Delegation"
            value={delegation()?.id ?? ""}
            onChange={(e) => {
              const id = e.currentTarget.value;
              setDelegation(mine().find((d) => d.id === id) ?? null);
              setSearchParams({ delegation: id, tab: undefined });
            }}
            class="rounded border border-nu-purple-300 px-2 py-1"
          >
            <For each={mine()}>
              {(d) => <option value={d.id}>{d.school}</option>}
            </For>
          </select>
        </label>
      </Show>

      <Show when={!loaded()}>
        <p class="mt-6 text-sm text-nu-purple-700">Loading…</p>
      </Show>

      <Show when={loaded()}>
        <Show
          when={delegation()}
          fallback={
            <>
              <Show when={(params.delegationId || loadFailed()) && banner()}>
                {(b) => (
                  <section class="mt-6">
                    <Banner message={b()} kind="error" />
                  </section>
                )}
              </Show>
              <Show
                when={!params.delegationId && !loadFailed() && conference()}
              >
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
            </>
          }
        >
          {(d) => (
            <section class="mt-6 space-y-4">
              <TabStrip current={activeTab()} onSelect={selectTab} />
              <Show when={banner()}>
                {(b) => <Banner message={b()} kind="error" />}
              </Show>
              <Show when={activeTab() === "overview"}>
                {/* Keyed: a reload or save hands back a new Delegation, and
                    the form must re-init from it — otherwise stale edits
                    would be saved against the newer expected_version. */}
                <Show when={delegation()} keyed>
                  {(current) => (
                    <EditBlock
                      delegation={current}
                      onSubmit={handleUpdate}
                      readOnly={readOnly()}
                      canDecide={isAdmin()}
                      deciding={deciding()}
                      onApprove={() => decide("approve")}
                      onReject={() => setRejecting(true)}
                    />
                  )}
                </Show>
                <Show when={isAdmin()}>
                  <DelegationStaff delegation={d()} />
                </Show>
              </Show>
              <Show when={activeTab() === "delegates"}>
                <DelegateRoster delegation={d()} readOnly={readOnly()} />
              </Show>
              <Show when={activeTab() === "advisors"}>
                <DelegationAdvisors delegation={d()} readOnly={readOnly()} />
              </Show>
              <Show when={activeTab() === "payments"}>
                <DelegationPayments delegation={d()} onLedgerChanged={reload} />
              </Show>
            </section>
          )}
        </Show>
      </Show>
      <Show when={rejecting() && delegation()}>
        {(d) => (
          <RejectDelegationDialog
            school={d().school}
            busy={deciding()}
            onCancel={() => setRejecting(false)}
            onReject={(reason) => decide("reject", reason)}
          />
        )}
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
  readOnly: boolean;
  canDecide: boolean;
  deciding: boolean;
  onApprove: () => void;
  onReject: () => void;
}) {
  const statusLabel = (s: Delegation["status"]): string => {
    switch (s) {
      case Delegation_Status.PENDING:
        return "Pending review";
      case Delegation_Status.APPROVED:
        return "Approved";
      case Delegation_Status.REJECTED:
        return "Rejected";
      default:
        return "—";
    }
  };
  const pending = () => props.delegation.status === Delegation_Status.PENDING;
  return (
    <div class="space-y-4">
      <div class="flex items-center gap-3 rounded border border-nu-purple-200 bg-white p-4 text-sm">
        <span class="rounded bg-nu-purple-50 px-2 py-1 text-xs uppercase tracking-wide text-nu-purple-700">
          {statusLabel(props.delegation.status)}
        </span>
        <span class="text-nu-purple-700">
          Version {props.delegation.version}
        </span>
        <Show when={props.canDecide && pending()}>
          <div class="ml-auto flex gap-2">
            <button
              type="button"
              disabled={props.deciding}
              onClick={() => props.onApprove()}
              class="rounded bg-nu-purple px-3 py-1 text-white disabled:opacity-50"
            >
              {props.deciding ? "…" : "Approve"}
            </button>
            <button
              type="button"
              disabled={props.deciding}
              onClick={() => props.onReject()}
              class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700 disabled:opacity-50"
            >
              Reject
            </button>
          </div>
        </Show>
      </div>
      <Show
        when={!props.readOnly}
        fallback={<RejectedSummary delegation={props.delegation} />}
      >
        <div class="rounded border border-nu-purple-200 bg-white p-6">
          <DelegationForm
            initialValues={delegationToForm(props.delegation)}
            onSubmit={props.onSubmit}
            submitLabel="Save changes"
          />
        </div>
      </Show>
    </div>
  );
}

// What a school sees once its registration is rejected: the details it
// submitted, read-only, and who to ask. The rejection reason stays in the
// audit log.
function RejectedSummary(props: { delegation: Delegation }) {
  const d = () => props.delegation;
  return (
    <div class="space-y-4">
      <p class="rounded bg-amber-50 p-3 text-sm text-amber-900">
        NUMUN staff didn't approve this registration, so it can no longer be
        changed. If you have questions, contact NUMUN staff.
      </p>
      <dl class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-2 rounded border border-nu-purple-200 bg-white p-6 text-sm">
        <dt class="text-nu-purple-700">School</dt>
        <dd>{d().school}</dd>
        <dt class="text-nu-purple-700">Address</dt>
        <dd>
          <Show when={d().address} fallback="—">
            {(a) => (
              <>
                {a().street}, {a().city}
                {a().state ? `, ${a().state}` : ""} {a().postalCode}{" "}
                {a().country}
              </>
            )}
          </Show>
        </dd>
        <dt class="text-nu-purple-700">Estimated delegates</dt>
        <dd>{d().estimatedDelegates?.total ?? 0}</dd>
      </dl>
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
