// Phone-number normalization for Cognito sign-up.
//
// Cognito requires E.164 (`+<country><digits>`). NUMUN is US-centric so we
// auto-prepend `+1` when the user types a bare 10-digit number. Numbers that
// already carry a `+` country prefix are accepted as-is after digit-stripping.
//
// Anything else (too few digits, contains letters, suspicious country code)
// returns a structured error the caller can surface inline.

export type PhoneResult =
  | { ok: true; e164: string }
  | { ok: false; reason: string };

export function normalizePhone(input: string): PhoneResult {
  const trimmed = input.trim();
  if (!trimmed) return { ok: false, reason: "Phone number is required." };

  // Caller-provided E.164 (`+...`): strip everything except + and digits.
  if (trimmed.startsWith("+")) {
    const digits = trimmed.slice(1).replace(/\D/g, "");
    if (digits.length < 7 || digits.length > 15) {
      return {
        ok: false,
        reason: "Enter a valid international phone number, e.g. +18472462882.",
      };
    }
    return { ok: true, e164: `+${digits}` };
  }

  const digits = trimmed.replace(/\D/g, "");
  // 11 digits starting with 1 → US/Canada with country code already typed.
  if (digits.length === 11 && digits.startsWith("1")) {
    return { ok: true, e164: `+${digits}` };
  }
  // 10 digits → assume US and prepend +1.
  if (digits.length === 10) {
    return { ok: true, e164: `+1${digits}` };
  }
  return {
    ok: false,
    reason:
      "Enter a 10-digit US phone (e.g. 8472462882) or an international number starting with +.",
  };
}
