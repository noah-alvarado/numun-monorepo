// Connect client wiring for the NUMUN portal.
//
// API base URL is supplied at build time via VITE_API_BASE_URL. Defaults to
// the local SAM endpoint so the dev server "just works" against `make dev`.
//
// In production this is `https://api.numun.org`. Cookies (HttpOnly session,
// CSRF token) are scoped to `.numun.org` so cross-subdomain calls work
// natively — see AUTH.md §5.

import { createConnectTransport } from "@connectrpc/connect-web";
import { createClient } from "@connectrpc/connect";
import { HealthService } from "@/gen/numun/v1/health_pb";

const baseUrl = import.meta.env.VITE_API_BASE_URL ?? "http://localhost:3000";

export const transport = createConnectTransport({
  baseUrl,
  // Send the `.numun.org`-scoped session + CSRF cookies on every request.
  fetch: (input, init) => fetch(input, { ...init, credentials: "include" }),
});

export const healthClient = createClient(HealthService, transport);
