import type { FullConfig } from "@playwright/test";

import { rpc } from "./support/connect";
import { e2eSchool, seedConferenceId, seedUsers } from "./support/seed";

type Delegation = { id: string; school: string; status: string };

// Verifies the seed dataset is loaded and gives the seed advisor a pending
// delegation (the seed itself creates none) for the advisor and admin flows.
export default async function globalSetup(config: FullConfig) {
  const baseURL = config.projects[0].use.baseURL!;

  for (const role of ["advisor", "staffer", "admin"] as const) {
    try {
      await rpc(baseURL, role, "UserService/GetMe");
    } catch (err) {
      throw new Error(
        `Seed user ${role} (${seedUsers[role].id}) is not usable — run \`make seed\`.\n${String(err)}`,
      );
    }
  }

  const { items = [] } = await rpc<{ items?: Delegation[] }>(
    baseURL,
    "advisor",
    "DelegationService/ListAllDelegations",
    { conferenceId: seedConferenceId },
  );

  const existing = items[0];
  if (!existing) {
    await rpc(baseURL, "advisor", "DelegationService/CreateDelegation", {
      conferenceId: seedConferenceId,
      school: e2eSchool,
      address: {
        street: "633 Clark St",
        city: "Evanston",
        state: "IL",
        postalCode: "60208",
        country: "US",
      },
      estimatedDelegates: { total: 12, financiallyQualifying: 2 },
    });
    return;
  }
  if (existing.status !== "STATUS_PENDING") {
    throw new Error(
      `The seed advisor's delegation "${existing.school}" is ${existing.status}; ` +
        "the admin flow needs it pending. Run `make reset` for a clean dataset.",
    );
  }
}
