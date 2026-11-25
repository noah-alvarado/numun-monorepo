// Mirrors api/cmd/seed/main.go and docs/seed-users.md — keep in sync.

export const seedUsers = {
  advisor: {
    id: "0190a000-0000-7000-8000-000000000001",
    name: "Seed Advisor",
    email: "advisor@seed.numun.local",
  },
  staffer: {
    id: "0190a000-0000-7000-8000-000000000002",
    name: "Seed Staffer",
    email: "staffer@seed.numun.local",
  },
  admin: {
    id: "0190a000-0000-7000-8000-000000000003",
    name: "Seed Admin",
    email: "admin@seed.numun.local",
  },
} as const;

export type SeedRole = keyof typeof seedUsers;

// Global setup registers the seed users in the fake Cognito with this.
export const seedPassword = "seed password 1";

export const seedConferenceId = "0190a000-0000-7000-9000-000000000001";

// The pending delegation global-setup ensures the seed advisor owns.
export const e2eSchool = "E2E Academy";

export const seedConferenceName = "NUMUN XXIV (Seed)";
