// Sentry initialization for the portal. Mirrors api/internal/observability:
//   - No-op when VITE_SENTRY_DSN is empty (local dev, preview builds with no
//     project assigned).
//   - BeforeSend hook scrubs the same field set the slog redaction wrapper
//     covers on the API. Keeps logs and Sentry events aligned.
//
// Wire from `portal/src/main.tsx` — call initSentry() before render().

import * as Sentry from "@sentry/browser";
import type { ErrorEvent } from "@sentry/browser";

// Same set as api/internal/log.RedactedFields. Lowercase, dash- and
// underscore-collapsed for robust matching against header-style and
// snake_case keys alike.
const REDACTED_KEYS = new Set([
  "password",
  "refresh_token",
  "refreshtoken",
  "access_token",
  "accesstoken",
  "id_token",
  "idtoken",
  "client_secret",
  "clientsecret",
  "csrf_token",
  "csrftoken",
  "x-csrf-token",
  "authorization",
  "cookie",
  "set-cookie",
  "session_cookie",
  "sessioncookie",
]);

const REDACTED_VALUE = "[REDACTED]";

function isSensitiveKey(k: string): boolean {
  const lk = k.toLowerCase();
  if (REDACTED_KEYS.has(lk)) return true;
  const stripped = lk.replace(/[-_]/g, "");
  return REDACTED_KEYS.has(stripped);
}

function scrubRecord(rec: Record<string, unknown> | undefined): void {
  if (!rec) return;
  for (const k of Object.keys(rec)) {
    if (isSensitiveKey(k)) {
      rec[k] = REDACTED_VALUE;
      continue;
    }
    const v = rec[k];
    if (v && typeof v === "object" && !Array.isArray(v)) {
      scrubRecord(v as Record<string, unknown>);
    }
  }
}

function beforeSend(event: ErrorEvent): ErrorEvent | null {
  scrubRecord(event.tags as Record<string, unknown> | undefined);
  scrubRecord(event.extra as Record<string, unknown> | undefined);
  if (event.contexts) {
    for (const k of Object.keys(event.contexts)) {
      scrubRecord(event.contexts[k] as Record<string, unknown>);
    }
  }
  if (event.request) {
    scrubRecord(event.request.headers as Record<string, unknown> | undefined);
    // Cookies is a single header string on browser events; nuke whole when set.
    if (event.request.cookies) {
      event.request.cookies =
        REDACTED_VALUE as unknown as typeof event.request.cookies;
    }
  }
  if (event.breadcrumbs) {
    for (const bc of event.breadcrumbs) {
      scrubRecord(bc.data as Record<string, unknown> | undefined);
    }
  }
  return event;
}

export function initSentry(): boolean {
  const dsn = import.meta.env.VITE_SENTRY_DSN;
  if (!dsn) return false;

  Sentry.init({
    dsn,
    environment: import.meta.env.VITE_ENV_NAME ?? "unknown",
    release: import.meta.env.VITE_RELEASE_VERSION,
    sendDefaultPii: false,
    tracesSampleRate: 0,
    beforeSend,
  });
  return true;
}

// Exported for tests.
export const __testing = { isSensitiveKey, scrubRecord, beforeSend };
