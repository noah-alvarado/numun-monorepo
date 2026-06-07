# Seed users for local development

`make dev` boots the API with `DEV_BYPASS_AUTH=true` and `DEV_MODE=true`,
which makes the auth middleware accept an `X-Dev-User-Id` header instead of
a Cognito session cookie. The seed dataset gives us three deterministic users
— one of each role — that the portal's "Sign in as…" dev shortcut and any
hand-rolled `curl` calls can target.

Populate the dataset:

```bash
make dev          # bring DDB Local + LocalStack + MailHog up
make seed         # idempotent; safe to re-run
```

The seed runner is `/api/cmd/seed`. Source of truth for the IDs is its
`Users` slice — keep this document and that slice in sync.

## Roster

| Role            | `X-Dev-User-Id`                          | Email                       | Notes |
|-----------------|------------------------------------------|-----------------------------|---|
| `advisor`       | `0190a000-0000-7000-8000-000000000001`   | `advisor@seed.numun.local`  | Lead advisor on the seed delegation (added in M3). |
| `staff-staffer` | `0190a000-0000-7000-8000-000000000002`   | `staffer@seed.numun.local`  | Will receive `StaffDelegationAssignment` + `StaffCommitteeAssignment` rows in M3. Empty scope in M2. |
| `staff-admin`   | `0190a000-0000-7000-8000-000000000003`   | `admin@seed.numun.local`    | Full access. |

## Using a seed user

Direct API call (e.g., GetMe) against the local SAM Local endpoint:

```bash
curl -s -X POST http://localhost:3000/numun.v1.UserService/GetMe \
  -H "Content-Type: application/json" \
  -H "Connect-Protocol-Version: 1" \
  -H "X-Dev-User-Id: 0190a000-0000-7000-8000-000000000003" \
  -d '{}'
```

Authenticated via the cookie flow (Exchange handshake) — useful when testing
the cookie path itself:

```bash
# Mint a session by hitting AuthService.Exchange in dev-bypass mode.
# id_token must start with "dev-"; access_token is the seed user id prefixed
# with "dev-".
curl -i -X POST http://localhost:3000/numun.v1.AuthService/Exchange \
  -H "Content-Type: application/json" \
  -H "Connect-Protocol-Version: 1" \
  -d '{
    "idToken": "dev-token",
    "accessToken": "dev-0190a000-0000-7000-8000-000000000003",
    "refreshToken": "dev-rt",
    "expiresIn": 3600,
    "rememberMe": true
  }'
# Use the returned numun_session + csrf_token cookies on subsequent calls.
```

## Re-seeding

`make seed` is idempotent — re-running with existing rows logs
`seed: user exists` and exits 0. To start fresh:

```bash
make reset    # wipes DDB Local and LocalStack volumes
make seed
```

## Production note

This dataset is **local-only**. The seed runner refuses to execute unless
`AWS_ENDPOINT_URL_DYNAMODB` is set. The first production admin is created
out-of-band via the procedure in
[`/docs/runbooks/first-admin-bootstrap.md`](runbooks/first-admin-bootstrap.md).
