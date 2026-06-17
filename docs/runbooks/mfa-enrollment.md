# Runbook — MFA enrollment (TOTP)

NUMUN's Cognito user pool has TOTP MFA enabled as an opt-in factor. The
self-service enrollment screen is deferred to v1.1; until it ships, staff
(particularly `staff-admin` accounts, who **should** enable MFA per
SECURITY.md §4.2 and §5.3) enroll a TOTP authenticator via the AWS CLI.
Advisors may follow the same steps; this runbook is staff-focused — see
SECURITY.md §7.1 ("Staff-only"). Once the final step
(`set-user-mfa-preference`) completes, Cognito requires the TOTP code on
every subsequent sign-in for that user — enforcement happens inside
Cognito itself, not in the portal.

## Do this when

- You are a `staff-admin` (strongly recommended per SECURITY.md §5.3).
- You handle financial data (payments) or process delegation approvals — both
  carry mass-state-mutation power.
- You are a `staff-staffer` with sensitive scopes (e.g. user invitations,
  conference configuration).

Advisors are welcome to enroll too; it is not required in v1.

## Prerequisites

- AWS CLI v2 installed (`aws --version` ≥ 2.x). No IAM credentials needed
  for Steps 1–6 — those calls authenticate with the Cognito _user's_
  password. The recovery section at the end is the only step that needs IAM.
- The Cognito user pool id and user-pool **client** id for your environment.
  Both are exposed as `vars.COGNITO_USER_POOL_ID` /
  `vars.COGNITO_USER_POOL_CLIENT_ID` on the GitHub Actions environment, and
  as outputs of the `numun-${ENV}-base-data` stack.
- Region is `us-east-2` (NUMUN's primary region per INFRASTRUCTURE.md).
- An authenticator app (1Password, Authy, Google Authenticator, or any
  RFC 6238 TOTP client).
- You already have a Cognito account — created via portal sign-up (advisors)
  or via `UserService.InviteStaff` / the first-admin bootstrap (staff). See
  `./first-admin-bootstrap.md`.

Export the common variables once:

```bash
export AWS_REGION=us-east-2
export USER_POOL_ID=<from vars.COGNITO_USER_POOL_ID>
export CLIENT_ID=<from vars.COGNITO_USER_POOL_CLIENT_ID>
export USER_EMAIL=you@example.org
```

## Procedure

### Step 1 — Sign in to get a fresh access token

TOTP association needs an access token from a recently signed-in session.
The simplest flow for manual enrollment is `USER_PASSWORD_AUTH` (the portal
client has `ALLOW_USER_PASSWORD_AUTH` enabled). The token is valid for one
hour by default — reuse the _same_ token through Steps 2, 4, and 5.

```bash
read -s -p "Cognito password: " COG_PASSWORD; echo

TOKEN=$(aws cognito-idp initiate-auth \
  --auth-flow USER_PASSWORD_AUTH \
  --client-id "$CLIENT_ID" \
  --auth-parameters "USERNAME=$USER_EMAIL,PASSWORD=$COG_PASSWORD" \
  --query "AuthenticationResult.AccessToken" \
  --output text)

unset COG_PASSWORD
test -n "$TOKEN" && echo "got access token"
```

If the response contains a `ChallengeName` (e.g. `NEW_PASSWORD_REQUIRED`),
finish that challenge in the portal first, then re-run the command.

### Step 2 — Associate the software token

```bash
aws cognito-idp associate-software-token --access-token "$TOKEN"
# => { "SecretCode": "JBSWY3DPEHPK3PXP...." }
```

Copy the `SecretCode` — you'll paste it into your authenticator next. Do
**not** share it; it is the seed for every future TOTP code.

### Step 3 — Add the secret to the authenticator app

Cleanest path: in your authenticator, add a new account → "enter setup key"
→ paste the `SecretCode`. Label it e.g. `numun (prod) — you@example.org`.

If your app prefers an `otpauth://` URI, construct one — 1Password and
Authy accept it in a new item's TOTP field, and `qrencode -t ANSI256
"<uri>"` renders it as a QR code:

```
otpauth://totp/numun:you@example.org?secret=JBSWY3DPEHPK3PXP....&issuer=numun
```

The app will start showing a fresh six-digit code every 30 seconds.

### Step 4 — Verify the code

Submit the current six digits within their 30-second window:

```bash
SIX_DIGITS=123456   # current code from the authenticator
DEVICE_NAME="1Password — laptop"   # descriptive, you'll see this in audit logs

aws cognito-idp verify-software-token \
  --access-token "$TOKEN" \
  --user-code "$SIX_DIGITS" \
  --friendly-device-name "$DEVICE_NAME"
# => { "Status": "SUCCESS" }
```

If you see `Status: ERROR` or `CodeMismatchException`, the code expired
mid-typing or the secret was pasted wrong — wait for a fresh code and
retry. Too many bad attempts will rate-limit you for a few minutes.

### Step 5 — Set MFA preference (this is the enforcing step)

Verification alone makes TOTP _available_ but not _required_. To make
Cognito demand the code on every future sign-in, set the preference:

```bash
aws cognito-idp set-user-mfa-preference \
  --access-token "$TOKEN" \
  --software-token-mfa-settings Enabled=true,PreferredMfa=true
```

No output on success. From this moment on, sign-ins for `$USER_EMAIL` will
fail without a TOTP code.

### Step 6 — Confirm by signing in again

1. Sign out of `portal.numun.org` (or clear your session cookie).
2. Sign back in with email + password.
3. Cognito should respond with `SOFTWARE_TOKEN_MFA`; the portal will prompt
   for the six-digit code. Per AUTH.md §3, the portal exchanges Cognito
   tokens for a server-side session only _after_ the MFA challenge succeeds.
4. Enter the code; you should land on `/dashboard` as before.

If the portal does **not** prompt for a code, MFA is not actually on for
your user — re-check Step 5. Confirm with
`aws cognito-idp get-user --access-token "$TOKEN"` (look at
`UserMFASettingList` and `PreferredMfaSetting`).

## Recovery — lost access to the authenticator

If you can no longer produce TOTP codes (phone lost, secret deleted, etc.),
Cognito sign-in is broken for that user — only an operator with
`cognito-idp:AdminSetUserMFAPreference` can clear it.

**Another `staff-admin`** can disable MFA for you with their own AWS
credentials:

```bash
aws cognito-idp admin-set-user-mfa-preference \
  --user-pool-id "$USER_POOL_ID" \
  --username "$USER_EMAIL" \
  --software-token-mfa-settings Enabled=false
```

You can then sign in with password alone and re-run this runbook from Step
1 with a fresh authenticator.

**If you are the only `staff-admin`** (or no other is reachable), this is
a break-glass scenario — a holder of the break-glass IAM credentials runs
the same `admin-set-user-mfa-preference` command. Follow
`./breakglass-access.md` for retrieval and post-incident audit; do **not**
share break-glass credentials over chat.

## Related

- `../AUTH.md` §3 (sign-in / token exchange), §4 (MFA)
- `../SECURITY.md` §4.2 (account hardening), §5.3 (staff-admin requirements),
  §7.1 (operational runbooks index)
- `./first-admin-bootstrap.md`
- `./breakglass-access.md`
