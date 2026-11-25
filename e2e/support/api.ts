// Connect-JSON helpers for arranging test data. Calls authenticate with the
// X-Dev-User-Id header (DEV_BYPASS_AUTH), which skips the cookie/CSRF path —
// use them to set up state, and drive the behavior under test via the UI.

import { randomUUID } from "node:crypto";

import { seedConferenceId, seedUsers } from "./seed";

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

const admin = seedUsers.admin.id;

export async function approveDelegation(d: Delegation): Promise<void> {
  await rpc(admin, "DelegationService/Approve", {
    delegationId: d.id,
    expectedVersion: d.version,
  });
}

// A draft conference never becomes the active one, so tests can run the
// assignment algorithm over exactly the data they create.
export async function createDraftConference(label: string): Promise<string> {
  const now = Date.now();
  const { conference } = await rpc<{ conference: { id: string } }>(
    admin,
    "ConferenceService/CreateConference",
    {
      name: `E2E ${label} ${randomUUID().slice(0, 8)}`,
      editionNumber: 99,
      year: new Date(now).getUTCFullYear(),
      startsAt: new Date(now + 30 * 86_400_000).toISOString(),
      endsAt: new Date(now + 33 * 86_400_000).toISOString(),
      status: "STATUS_DRAFT",
    },
  );
  return conference.id;
}

export async function createCommittee(
  conferenceId: string,
  name: string,
  type: "CRISIS" | "NON_CRISIS",
  positions: string[],
): Promise<string> {
  const { committee } = await rpc<{ committee: { id: string } }>(
    admin,
    "CommitteeService/CreateCommittee",
    {
      conferenceId,
      name,
      type: `COMMITTEE_TYPE_${type}`,
      size: "COMMITTEE_SIZE_SMALL",
    },
  );
  for (const position of positions) {
    await rpc(admin, "PositionService/CreatePosition", {
      committeeId: committee.id,
      name: position,
      maxDelegates: 1,
    });
  }
  return committee.id;
}

export async function createDelegationAward(
  name: string,
  delegation: Delegation,
): Promise<{ id: string }> {
  const { award } = await rpc<{ award: { id: string } }>(
    admin,
    "AwardService/CreateAward",
    {
      conferenceId: seedConferenceId,
      name,
      category: "Delegation Awards",
      recipients: [
        {
          kind: "AWARD_RECIPIENT_KIND_DELEGATION",
          id: delegation.id,
          displayName: delegation.school,
        },
      ],
    },
  );
  return award;
}

export async function recordCharge(
  delegationId: string,
  units: number,
  reference: string,
): Promise<void> {
  await rpc(admin, "PaymentService/RecordPayment", {
    delegationId,
    amount: { currency: "USD", units: String(units), cents: 0 },
    kind: "PAYMENT_KIND_CHARGE",
    method: "PAYMENT_METHOD_OTHER",
    reference,
  });
}
