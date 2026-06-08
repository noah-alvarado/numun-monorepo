// Money formatting helpers. The wire shape (numun.v1.Money) splits a signed
// amount into `units` (bigint dollars) + `cents` (int32). For v1 every amount
// is USD; the helper still respects the currency field for forward-compat.

import { create } from "@bufbuild/protobuf";
import { MoneySchema, type Money } from "@/gen/numun/v1/common_pb";

/**
 * moneyToCents collapses a Money value into a signed integer number of cents.
 * Returns `0n` if the input is undefined. The result is a bigint so a sum
 * across a ledger cannot lose precision.
 */
export function moneyToCents(m: Money | undefined): bigint {
  if (!m) return 0n;
  return m.units * 100n + BigInt(m.cents);
}

/**
 * centsToMoney builds a Money from a signed bigint cent count. Both `units`
 * and `cents` carry the sign so the wire shape round-trips correctly.
 */
export function centsToMoney(cents: bigint, currency = "USD"): Money {
  const units = cents / 100n;
  const remainder = Number(cents - units * 100n);
  return create(MoneySchema, { currency, units, cents: remainder });
}

/**
 * formatMoney renders a Money value as a localized currency string. Negative
 * amounts get a leading "-" before the currency symbol (e.g., "-$1,234.56").
 */
export function formatMoney(m: Money | undefined): string {
  const currency = m?.currency || "USD";
  const cents = moneyToCents(m);
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

/**
 * parseAmountInput converts a free-text dollar amount (e.g., "1234.56") into
 * a Money value. Returns null when the input cannot be parsed or is negative.
 * The caller decides how to apply a sign based on PaymentKind.
 */
export function parseAmountInput(
  input: string,
  currency = "USD",
): Money | null {
  const trimmed = input.trim();
  if (!trimmed) return null;
  if (!/^\d+(\.\d{1,2})?$/.test(trimmed)) return null;
  const [whole, frac = ""] = trimmed.split(".");
  const units = BigInt(whole);
  const cents = Number((frac + "00").slice(0, 2));
  return create(MoneySchema, { currency, units, cents });
}
