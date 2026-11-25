// Connect-JSON helpers for arranging test data. Calls authenticate with the
// X-Dev-User-Id header (DEV_BYPASS_AUTH), which skips the cookie/CSRF path —
// use them to set up state, and drive the behavior under test via the UI.

import { randomUUID } from "node:crypto";

import { seedConferenceId } from "./seed";

export const API_BASE_URL = "http://localhost:4173";

export async function rpc<T = Record<string, unknown>>(
  userId: string | null,
  method: string,
  body: Record<string, unknown> = {},
): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    "Connect-Protocol-Version": "1",
  };
  if (userId) headers["X-Dev-User-Id"] = userId;
  const resp = await fetch(`${API_BASE_URL}/numun.v1.${method}`, {
    method: "POST",
    headers,
    body: JSON.stringify(body),
  });
  const text = await resp.text();
  if (!resp.ok) {
    throw new Error(`${method} as ${userId}: HTTP ${resp.status} ${text}`);
  }
  return JSON.parse(text) as T;
}

export type TestUser = { id: string; name: string };

// Dev-bypass Exchange lazily creates an advisor for an unknown id, which
// gives each test its own advisor (and so its own delegation) without
// touching the seed users.
export async function createAdvisor(label: string): Promise<TestUser> {
  const id = randomUUID();
  await rpc(null, "AuthService/Exchange", {
    idToken: "dev-token",
    accessToken: `dev-${id}`,
    refreshToken: "dev-rt",
    expiresIn: 3600,
  });
  const name = `E2E ${label} ${id.slice(0, 8)}`;
  const { user } = await rpc<{ user: { version: number } }>(
    id,
    "UserService/GetMe",
  );
  await rpc(id, "UserService/UpdateUser", {
    userId: id,
    name,
    expectedVersion: user.version,
  });
  return { id, name };
}

export type Delegation = {
  id: string;
  school: string;
  status: string;
  version: number;
};

export async function createDelegation(
  advisorId: string,
  school: string,
  extra: Record<string, unknown> = {},
): Promise<Delegation> {
  const { delegation } = await rpc<{ delegation: Delegation }>(
    advisorId,
    "DelegationService/CreateDelegation",
    {
      conferenceId: seedConferenceId,
      school,
      address: {
        street: "633 Clark St",
        city: "Evanston",
        state: "IL",
        postalCode: "60208",
        country: "US",
      },
      estimatedDelegates: { total: 12, financiallyQualifying: 2 },
      ...extra,
    },
  );
  return delegation;
}

export function uniqueSchool(label: string): string {
  return `E2E ${label} ${randomUUID().slice(0, 8)}`;
}

export type Delegate = { id: string; firstName: string; lastName: string };

export async function createDelegate(
  advisorId: string,
  delegationId: string,
  firstName: string,
  lastName: string,
): Promise<Delegate> {
  const { delegate } = await rpc<{ delegate: Delegate }>(
    advisorId,
    "DelegateService/CreateDelegate",
    { delegationId, input: { firstName, lastName } },
  );
  return delegate;
}

// A last name no other test row will match in a name search.
export function uniqueSurname(): string {
  return `Zed${randomUUID()
    .replace(/[^a-f]/g, "")
    .slice(0, 8)}`;
}
