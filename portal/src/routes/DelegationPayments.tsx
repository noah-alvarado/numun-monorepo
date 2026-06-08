// DelegationPayments — the Payments tab inside the delegation view. Renders
// the running ledger (newest-first) plus an admin-only Record / Edit / Delete
// flow. Balance + paidInFull are read off the parent Delegation; the row-level
// running balance is computed client-side by walking oldest→newest.

import { createMemo, createResource, createSignal, For, Show } from "solid-js";
import { ConnectError, Code } from "@connectrpc/connect";

import { paymentClient } from "@/lib/api";
import { userSignal } from "@/lib/session";
import { formatMoney, moneyToCents, parseAmountInput } from "@/lib/money";
import type { Delegation } from "@/gen/numun/v1/delegations_pb";
import {
  PaymentKind,
  PaymentMethod,
  type PaymentRecord,
} from "@/gen/numun/v1/payments_pb";
import { User_Role } from "@/gen/numun/v1/users_pb";

type LedgerRow = {
  payment: PaymentRecord;
  runningBalance: bigint;
  currency: string;
};

export default function DelegationPayments(props: { delegation: Delegation }) {
  const [user] = userSignal;
  const isAdmin = () => user()?.role === User_Role.STAFF_ADMIN;

  const [payments, { refetch }] = createResource(
    () => props.delegation.id,
    async (delegationId) => {
      const resp = await paymentClient.listAllPayments({ delegationId });
      return resp.items;
    },
  );

  const rows = createMemo<LedgerRow[]>(() => {
    const items = payments();
    if (!items) return [];
    return computeLedger(items);
  });

  const [recordOpen, setRecordOpen] = createSignal(false);
  const [editTarget, setEditTarget] = createSignal<PaymentRecord | null>(null);
  const [deleteTarget, setDeleteTarget] = createSignal<PaymentRecord | null>(
    null,
  );
  const [banner, setBanner] = createSignal<string | null>(null);

  async function reload() {
    await refetch();
  }

  return (
    <section class="space-y-4">
      <header class="flex flex-wrap items-center justify-between gap-3 rounded border border-nu-purple-200 bg-white p-4">
        <div>
          <div class="text-base font-semibold text-nu-purple">
            {props.delegation.school}
          </div>
          <div class="mt-1 flex items-center gap-2 text-sm">
            <span class="text-nu-purple-700">Balance due:</span>
            <span class="font-mono font-semibold">
              {formatMoney(props.delegation.balanceDue)}
            </span>
            <span
              class={
                props.delegation.paidInFull
                  ? "ml-2 rounded bg-nu-green-dark px-2 py-0.5 text-xs font-semibold uppercase tracking-wide text-white"
                  : "ml-2 rounded bg-nu-purple-50 px-2 py-0.5 text-xs font-semibold uppercase tracking-wide text-nu-purple-700"
              }
            >
              {props.delegation.paidInFull ? "Paid in full" : "Outstanding"}
            </span>
          </div>
        </div>
        <div class="flex gap-2 text-sm">
          <button
            type="button"
            onClick={reload}
            class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
          >
            Reload
          </button>
          <Show when={isAdmin()}>
            <button
              type="button"
              onClick={() => setRecordOpen(true)}
              class="rounded bg-nu-purple px-3 py-1 text-white"
            >
              Record payment
            </button>
          </Show>
        </div>
      </header>

      <Show when={banner()}>
        {(b) => <p class="rounded bg-red-50 p-3 text-sm text-red-900">{b()}</p>}
      </Show>

      <Show
        when={!payments.loading}
        fallback={
          <p class="rounded border border-nu-purple-200 bg-white p-4 text-sm text-nu-purple-700">
            Loading ledger…
          </p>
        }
      >
        <Show
          when={rows().length > 0}
          fallback={
            <p class="rounded border border-nu-purple-200 bg-white p-4 text-sm text-nu-purple-700">
              No ledger entries yet.
            </p>
          }
        >
          <div class="overflow-x-auto rounded border border-nu-purple-200 bg-white">
            <table class="min-w-full text-sm">
              <thead class="bg-nu-purple-50 text-left text-xs uppercase tracking-wide text-nu-purple-700">
                <tr>
                  <th class="px-3 py-2 font-semibold">Recorded</th>
                  <th class="px-3 py-2 font-semibold">Kind</th>
                  <th class="px-3 py-2 font-semibold">Method</th>
                  <th class="px-3 py-2 text-right font-semibold">Amount</th>
                  <th class="px-3 py-2 font-semibold">Reference</th>
                  <th class="px-3 py-2 font-semibold">Notes</th>
                  <th class="px-3 py-2 font-semibold">Recorded by</th>
                  <th class="px-3 py-2 text-right font-semibold">
                    Running balance
                  </th>
                  <Show when={isAdmin()}>
                    <th class="px-3 py-2 font-semibold">Actions</th>
                  </Show>
                </tr>
              </thead>
              <tbody>
                <For each={rows()}>
                  {(row) => (
                    <tr class="border-t border-nu-purple-100">
                      <td class="px-3 py-2 font-mono text-xs text-nu-purple-700">
                        {formatTimestamp(row.payment.recordedAt)}
                      </td>
                      <td class="px-3 py-2">
                        <KindChip kind={row.payment.kind} />
                      </td>
                      <td class="px-3 py-2 text-nu-purple-700">
                        {methodLabel(row.payment.method)}
                      </td>
                      <td class="px-3 py-2 text-right font-mono">
                        {signedAmount(row.payment)}
                      </td>
                      <td
                        class="max-w-[14rem] truncate px-3 py-2 text-nu-purple-700"
                        title={row.payment.reference}
                      >
                        {truncate(row.payment.reference, 24)}
                      </td>
                      <td
                        class="max-w-[18rem] truncate px-3 py-2 text-nu-purple-700"
                        title={row.payment.notes}
                      >
                        {truncate(row.payment.notes, 40)}
                      </td>
                      <td
                        class="max-w-[10rem] truncate px-3 py-2 font-mono text-xs text-nu-purple-700"
                        title={row.payment.recordedBy}
                      >
                        {row.payment.recordedBy}
                      </td>
                      <td class="px-3 py-2 text-right font-mono">
                        {formatCents(row.runningBalance, row.currency)}
                      </td>
                      <Show when={isAdmin()}>
                        <td class="px-3 py-2">
                          <div class="flex gap-2 text-xs">
                            <button
                              type="button"
                              onClick={() => setEditTarget(row.payment)}
                              class="rounded border border-nu-purple-300 px-2 py-1 text-nu-purple-700"
                            >
                              Edit
                            </button>
                            <button
                              type="button"
                              onClick={() => setDeleteTarget(row.payment)}
                              class="rounded border border-red-300 px-2 py-1 text-red-700"
                            >
                              Delete
                            </button>
                          </div>
                        </td>
                      </Show>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
          </div>
        </Show>
      </Show>

      <Show when={recordOpen() && isAdmin()}>
        <RecordModal
          delegationId={props.delegation.id}
          onClose={() => setRecordOpen(false)}
          onSaved={async () => {
            setRecordOpen(false);
            await reload();
          }}
          onError={setBanner}
        />
      </Show>

      <Show when={isAdmin() ? editTarget() : null}>
        {(target) => (
          <EditModal
            payment={target()}
            onClose={() => setEditTarget(null)}
            onSaved={async () => {
              setEditTarget(null);
              await reload();
            }}
            onError={setBanner}
          />
        )}
      </Show>

      <Show when={isAdmin() ? deleteTarget() : null}>
        {(target) => (
          <DeleteModal
            payment={target()}
            onClose={() => setDeleteTarget(null)}
            onConfirmed={async () => {
              setDeleteTarget(null);
              await reload();
            }}
            onError={setBanner}
          />
        )}
      </Show>
    </section>
  );
}

// computeLedger walks the records oldest→newest to compute a running balance
// (positive = balance due), then returns the rows reversed for newest-first
// display. Pure function so it's trivially memoizable.
export function computeLedger(items: PaymentRecord[]): LedgerRow[] {
  const sortedAsc = [...items].sort((a, b) => {
    const aMs = tsMillis(a.recordedAt);
    const bMs = tsMillis(b.recordedAt);
    if (aMs !== bMs) return aMs - bMs;
    return a.id.localeCompare(b.id);
  });
  const out: LedgerRow[] = [];
  let running = 0n;
  for (const p of sortedAsc) {
    running += signedCents(p);
    out.push({
      payment: p,
      runningBalance: running,
      currency: p.amount?.currency || "USD",
    });
  }
  return out.reverse();
}

function signedCents(p: PaymentRecord): bigint {
  const abs = moneyToCents(p.amount);
  switch (p.kind) {
    case PaymentKind.CHARGE:
      return abs >= 0n ? abs : -abs;
    case PaymentKind.PAYMENT:
      return abs >= 0n ? -abs : abs;
    case PaymentKind.ADJUSTMENT:
      return abs;
    default:
      return abs;
  }
}

function signedAmount(p: PaymentRecord): string {
  const cents = signedCents(p);
  const currency = p.amount?.currency || "USD";
  const prefix = cents > 0n ? "+" : cents < 0n ? "-" : "";
  const abs = cents < 0n ? -cents : cents;
  return `${prefix}${formatCents(abs, currency)}`;
}

function formatCents(cents: bigint, currency: string): string {
  const negative = cents < 0n;
  const abs = negative ? -cents : cents;
  const wholeUnits = Number(abs / 100n);
  const fractional = Number(abs - BigInt(wholeUnits) * 100n);
  const formatted = new Intl.NumberFormat("en-US", {
    style: "currency",
    currency,
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(wholeUnits + fractional / 100);
  return negative ? `-${formatted}` : formatted;
}

function tsMillis(ts: { seconds: bigint; nanos: number } | undefined): number {
  if (!ts) return 0;
  return Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1_000_000);
}

function formatTimestamp(
  ts: { seconds: bigint; nanos: number } | undefined,
): string {
  if (!ts) return "—";
  const d = new Date(tsMillis(ts));
  const pad = (n: number) => n.toString().padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function truncate(s: string, n: number): string {
  if (!s) return "";
  return s.length <= n ? s : s.slice(0, n - 1) + "…";
}

function methodLabel(m: PaymentMethod): string {
  switch (m) {
    case PaymentMethod.CHECK:
      return "Check";
    case PaymentMethod.WIRE:
      return "Wire";
    case PaymentMethod.CASH:
      return "Cash";
    case PaymentMethod.OTHER:
      return "Other";
    default:
      return "—";
  }
}

function kindLabel(k: PaymentKind): string {
  switch (k) {
    case PaymentKind.CHARGE:
      return "Charge";
    case PaymentKind.PAYMENT:
      return "Payment";
    case PaymentKind.ADJUSTMENT:
      return "Adjustment";
    default:
      return "—";
  }
}

function KindChip(props: { kind: PaymentKind }) {
  const cls = (): string => {
    switch (props.kind) {
      case PaymentKind.CHARGE:
        return "bg-red-50 text-red-900";
      case PaymentKind.PAYMENT:
        return "bg-emerald-50 text-emerald-900";
      case PaymentKind.ADJUSTMENT:
        return "bg-nu-purple-50 text-nu-purple-700";
      default:
        return "bg-nu-purple-50 text-nu-purple-700";
    }
  };
  return (
    <span
      class={`rounded px-2 py-0.5 text-xs font-semibold uppercase tracking-wide ${cls()}`}
    >
      {kindLabel(props.kind)}
    </span>
  );
}

function ModalShell(props: {
  title: string;
  onClose: () => void;
  children: import("solid-js").JSX.Element;
}) {
  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <div class="w-full max-w-lg rounded border border-nu-purple-200 bg-white shadow-lg">
        <header class="flex items-center justify-between border-b border-nu-purple-100 px-4 py-3">
          <h3 class="text-base font-semibold text-nu-purple">{props.title}</h3>
          <button
            type="button"
            onClick={props.onClose}
            class="text-sm text-nu-purple-700"
          >
            Close
          </button>
        </header>
        <div class="p-4">{props.children}</div>
      </div>
    </div>
  );
}

function RecordModal(props: {
  delegationId: string;
  onClose: () => void;
  onSaved: () => Promise<void>;
  onError: (msg: string | null) => void;
}) {
  const [amount, setAmount] = createSignal("");
  const [kind, setKind] = createSignal<PaymentKind>(PaymentKind.PAYMENT);
  const [method, setMethod] = createSignal<PaymentMethod>(PaymentMethod.CHECK);
  const [reference, setReference] = createSignal("");
  const [notes, setNotes] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const [localErr, setLocalErr] = createSignal<string | null>(null);

  async function submit(ev: Event) {
    ev.preventDefault();
    setLocalErr(null);
    const money = parseAmountInput(amount());
    if (!money) {
      setLocalErr("Enter a non-negative amount with up to 2 decimals.");
      return;
    }
    setBusy(true);
    try {
      await paymentClient.recordPayment({
        delegationId: props.delegationId,
        amount: money,
        kind: kind(),
        method: method(),
        reference: reference(),
        notes: notes(),
      });
      props.onError(null);
      await props.onSaved();
    } catch (err) {
      setLocalErr(connectMessage(err) ?? "Failed to record payment.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell title="Record payment" onClose={props.onClose}>
      <form class="space-y-3 text-sm" onSubmit={submit}>
        <div class="grid grid-cols-[1fr_6rem] gap-2">
          <label class="block">
            <span class="block text-xs font-semibold text-nu-purple-700">
              Amount
            </span>
            <input
              type="text"
              inputmode="decimal"
              value={amount()}
              onInput={(e) => setAmount(e.currentTarget.value)}
              placeholder="0.00"
              class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
              required
            />
          </label>
          <label class="block">
            <span class="block text-xs font-semibold text-nu-purple-700">
              Currency
            </span>
            <select
              disabled
              class="mt-1 w-full rounded border border-nu-purple-300 bg-nu-purple-50 px-2 py-1 text-nu-purple-700"
            >
              <option>USD</option>
            </select>
          </label>
        </div>

        <fieldset>
          <legend class="text-xs font-semibold text-nu-purple-700">Kind</legend>
          <div class="mt-1 flex gap-3">
            <KindRadio
              label="Charge"
              value={PaymentKind.CHARGE}
              current={kind()}
              onChange={setKind}
            />
            <KindRadio
              label="Payment"
              value={PaymentKind.PAYMENT}
              current={kind()}
              onChange={setKind}
            />
            <KindRadio
              label="Adjustment"
              value={PaymentKind.ADJUSTMENT}
              current={kind()}
              onChange={setKind}
            />
          </div>
        </fieldset>

        <label class="block">
          <span class="block text-xs font-semibold text-nu-purple-700">
            Method
          </span>
          <select
            value={method()}
            onChange={(e) =>
              setMethod(Number(e.currentTarget.value) as PaymentMethod)
            }
            class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
          >
            <option value={PaymentMethod.CHECK}>Check</option>
            <option value={PaymentMethod.WIRE}>Wire</option>
            <option value={PaymentMethod.CASH}>Cash</option>
            <option value={PaymentMethod.OTHER}>Other</option>
          </select>
        </label>

        <label class="block">
          <span class="block text-xs font-semibold text-nu-purple-700">
            Reference
          </span>
          <input
            type="text"
            value={reference()}
            onInput={(e) => setReference(e.currentTarget.value)}
            class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
          />
        </label>

        <label class="block">
          <span class="block text-xs font-semibold text-nu-purple-700">
            Notes
          </span>
          <textarea
            value={notes()}
            maxlength={500}
            onInput={(e) => setNotes(e.currentTarget.value)}
            class="mt-1 h-20 w-full rounded border border-nu-purple-300 px-2 py-1"
          />
        </label>

        <Show when={localErr()}>
          {(e) => (
            <p class="rounded bg-red-50 p-2 text-xs text-red-900">{e()}</p>
          )}
        </Show>

        <div class="flex justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={props.onClose}
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
    </ModalShell>
  );
}

function KindRadio(props: {
  label: string;
  value: PaymentKind;
  current: PaymentKind;
  onChange: (v: PaymentKind) => void;
}) {
  return (
    <label class="flex items-center gap-1 text-sm">
      <input
        type="radio"
        name="kind"
        checked={props.current === props.value}
        onChange={() => props.onChange(props.value)}
      />
      <span>{props.label}</span>
    </label>
  );
}

function EditModal(props: {
  payment: PaymentRecord;
  onClose: () => void;
  onSaved: () => Promise<void>;
  onError: (msg: string | null) => void;
}) {
  const [reference, setReference] = createSignal(props.payment.reference);
  const [notes, setNotes] = createSignal(props.payment.notes);
  const [busy, setBusy] = createSignal(false);
  const [localErr, setLocalErr] = createSignal<string | null>(null);

  async function submit(ev: Event) {
    ev.preventDefault();
    setLocalErr(null);
    setBusy(true);
    try {
      await paymentClient.updatePayment({
        paymentId: props.payment.id,
        reference: reference(),
        notes: notes(),
        expectedVersion: props.payment.version,
      });
      props.onError(null);
      await props.onSaved();
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.Aborted) {
        setLocalErr(
          "Another change happened first. Close this dialog, reload the ledger, and retry.",
        );
      } else {
        setLocalErr(connectMessage(err) ?? "Failed to save changes.");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell title="Edit payment" onClose={props.onClose}>
      <form class="space-y-3 text-sm" onSubmit={submit}>
        <p class="text-xs text-nu-purple-700">
          Only reference and notes can be edited. To change an amount or kind,
          delete this entry and record a new one.
        </p>
        <label class="block">
          <span class="block text-xs font-semibold text-nu-purple-700">
            Reference
          </span>
          <input
            type="text"
            value={reference()}
            onInput={(e) => setReference(e.currentTarget.value)}
            class="mt-1 w-full rounded border border-nu-purple-300 px-2 py-1"
          />
        </label>
        <label class="block">
          <span class="block text-xs font-semibold text-nu-purple-700">
            Notes
          </span>
          <textarea
            value={notes()}
            maxlength={500}
            onInput={(e) => setNotes(e.currentTarget.value)}
            class="mt-1 h-20 w-full rounded border border-nu-purple-300 px-2 py-1"
          />
        </label>
        <Show when={localErr()}>
          {(e) => (
            <p class="rounded bg-red-50 p-2 text-xs text-red-900">{e()}</p>
          )}
        </Show>
        <div class="flex justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={props.onClose}
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
    </ModalShell>
  );
}

function DeleteModal(props: {
  payment: PaymentRecord;
  onClose: () => void;
  onConfirmed: () => Promise<void>;
  onError: (msg: string | null) => void;
}) {
  const [busy, setBusy] = createSignal(false);
  const [localErr, setLocalErr] = createSignal<string | null>(null);

  async function confirm() {
    setLocalErr(null);
    setBusy(true);
    try {
      await paymentClient.deletePayment({
        paymentId: props.payment.id,
        expectedVersion: props.payment.version,
      });
      props.onError(null);
      await props.onConfirmed();
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.Aborted) {
        setLocalErr(
          "Another change happened first. Close this dialog and reload the ledger.",
        );
      } else {
        setLocalErr(connectMessage(err) ?? "Failed to delete entry.");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell title="Delete payment" onClose={props.onClose}>
      <div class="space-y-3 text-sm">
        <p>
          Delete the {kindLabel(props.payment.kind).toLowerCase()} of{" "}
          <span class="font-mono font-semibold">
            {formatMoney(props.payment.amount)}
          </span>{" "}
          recorded on{" "}
          <span class="font-mono">
            {formatTimestamp(props.payment.recordedAt)}
          </span>
          ?
        </p>
        <p class="text-xs text-nu-purple-700">
          The delegation balance will be adjusted accordingly.
        </p>
        <Show when={localErr()}>
          {(e) => (
            <p class="rounded bg-red-50 p-2 text-xs text-red-900">{e()}</p>
          )}
        </Show>
        <div class="flex justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={props.onClose}
            class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700"
          >
            Cancel
          </button>
          <button
            type="button"
            disabled={busy()}
            onClick={confirm}
            class="rounded bg-red-600 px-3 py-1 text-white disabled:opacity-50"
          >
            {busy() ? "Deleting…" : "Delete"}
          </button>
        </div>
      </div>
    </ModalShell>
  );
}

function connectMessage(err: unknown): string | null {
  if (err instanceof ConnectError) return err.message;
  if (err instanceof Error) return err.message;
  return null;
}
