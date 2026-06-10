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
import { ConferenceService } from "@/gen/numun/v1/conferences_pb";
import { DelegationService } from "@/gen/numun/v1/delegations_pb";
import { DelegateService } from "@/gen/numun/v1/delegates_pb";
import { UploadService } from "@/gen/numun/v1/uploads_pb";
import { PublicService } from "@/gen/numun/v1/public_pb";
import {
  CommitteeService,
  PositionService,
} from "@/gen/numun/v1/committees_pb";
import {
  AssignmentService,
  AssignmentRunService,
} from "@/gen/numun/v1/assignments_pb";
import { PaymentService } from "@/gen/numun/v1/payments_pb";
import { AnnouncementService } from "@/gen/numun/v1/announcements_pb";
import { EmailHealthService } from "@/gen/numun/v1/email_health_pb";
import { AwardService } from "@/gen/numun/v1/awards_pb";

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
export const conferenceClient = createClient(ConferenceService, transport);
export const delegationClient = createClient(DelegationService, transport);
export const delegateClient = createClient(DelegateService, transport);
export const uploadClient = createClient(UploadService, transport);
export const publicClient = createClient(PublicService, transport);
export const committeeClient = createClient(CommitteeService, transport);
export const positionClient = createClient(PositionService, transport);
export const assignmentClient = createClient(AssignmentService, transport);
export const assignmentRunClient = createClient(
  AssignmentRunService,
  transport,
);
export const paymentClient = createClient(PaymentService, transport);
export const announcementClient = createClient(AnnouncementService, transport);
export const emailHealthClient = createClient(EmailHealthService, transport);
export const awardClient = createClient(AwardService, transport);

// CSV download URL builders for the parallel non-Connect HTTP routes
// (API.md §12). Use these from <a download> links or window.location.href
// so the browser drives the download flow.
export const exportUrls = {
  assignmentsCsv(conferenceId: string): string {
    return `${baseUrl}/v1/exports/assignments.csv?conference_id=${encodeURIComponent(conferenceId)}`;
  },
  delegatesCsv(conferenceId: string): string {
    return `${baseUrl}/v1/exports/delegates.csv?conference_id=${encodeURIComponent(conferenceId)}`;
  },
  paymentsCsv(conferenceId: string): string {
    return `${baseUrl}/v1/exports/payments.csv?conference_id=${encodeURIComponent(conferenceId)}`;
  },
};
