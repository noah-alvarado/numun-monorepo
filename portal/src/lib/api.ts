// Connect client wiring for the NUMUN portal.
//
// API base URL is supplied at build time via VITE_API_BASE_URL. Defaults to
// the local SAM endpoint so the dev server "just works" against `make dev`.
//
// In production this is `https://api.numun.org`. Cookies (HttpOnly session,
// CSRF token) are scoped to `.numun.org` so cross-subdomain calls work
// natively — see AUTH.md §5.

import { createConnectTransport } from "@connectrpc/connect-web";
import { createClient, type Interceptor } from "@connectrpc/connect";
import { HealthService } from "@/gen/numun/v1/health_pb";
import { AuthService } from "@/gen/numun/v1/auth_pb";
import { UserService } from "@/gen/numun/v1/users_pb";

const baseUrl = import.meta.env.VITE_API_BASE_URL ?? "http://localhost:3000";

function readCookie(name: string): string {
  if (typeof document === "undefined") return "";
  const prefix = name + "=";
  for (const part of document.cookie.split("; ")) {
    if (part.startsWith(prefix)) {
      return decodeURIComponent(part.slice(prefix.length));
    }
  }
  return "";
}

function isReadRPC(methodName: string): boolean {
  return /^(Get|List|Search)/.test(methodName);
}

// csrfInterceptor adds the X-CSRF-Token header to every mutating call. Reads
// the value out of the non-HttpOnly cookie the server set on Exchange.
const csrfInterceptor: Interceptor = (next) => async (req) => {
  if (!isReadRPC(req.method.name)) {
    const token = readCookie("csrf_token");
    if (token) {
      req.header.set("X-CSRF-Token", token);
    }
  }
  return next(req);
};

// devUserInterceptor — when VITE_DEV_USER_ID is set at build time, attaches
// X-Dev-User-Id to every request. Used by the "Sign in as…" shortcut in
// local dev. Stripped in prod builds (the env var is not set).
const devUserId = import.meta.env.VITE_DEV_USER_ID as string | undefined;
const devUserInterceptor: Interceptor = (next) => async (req) => {
  if (devUserId) {
    req.header.set("X-Dev-User-Id", devUserId);
  }
  return next(req);
};

export const transport = createConnectTransport({
  baseUrl,
  fetch: (input, init) => fetch(input, { ...init, credentials: "include" }),
  interceptors: [csrfInterceptor, devUserInterceptor],
});

export const healthClient = createClient(HealthService, transport);
export const authClient = createClient(AuthService, transport);
export const userClient = createClient(UserService, transport);
