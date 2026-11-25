import { createDelegation, rpc, type Delegation } from "./support/api";
import { e2eSchool, seedConferenceId, seedUsers } from "./support/seed";

// Verifies the seed dataset is loaded and gives the seed advisor a pending
// delegation (the seed itself creates none) for the advisor and admin flows.
export default async function globalSetup() {
  for (const role of ["advisor", "staffer", "admin"] as const) {
    try {
      await rpc(seedUsers[role].id, "UserService/GetMe");
    } catch (err) {
      throw new Error(
        `Seed user ${role} (${seedUsers[role].id}) is not usable — run \`make seed\`.\n${String(err)}`,
      );
    }
  }

  const { items = [] } = await rpc<{ items?: Delegation[] }>(
    seedUsers.advisor.id,
    "DelegationService/ListAllDelegations",
    { conferenceId: seedConferenceId },
  );

  const existing = items[0];
  if (!existing) {
    await createDelegation(seedUsers.advisor.id, e2eSchool);
    return;
  }
  if (existing.status !== "STATUS_PENDING") {
    throw new Error(
      `The seed advisor's delegation "${existing.school}" is ${existing.status}; ` +
        "the admin flow needs it pending. Run `make reset` for a clean dataset.",
    );
  }
}
