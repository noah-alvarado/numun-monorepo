# Runbook — First-admin bootstrap

When the Cognito user pool is freshly created, no `staff-admin` exists. The
portal cannot self-recover from this state: `UserService.InviteStaff` is
admin-only, so without a first admin nobody can mint subsequent staff users.
This one-time procedure mints the first `staff-admin` directly via the
AWS CLI under the break-glass IAM user.

Run this **once per environment**. After it's done, all future staff
invitations flow through `staff-admin` calling `UserService.InviteStaff`
in the portal.

## Prerequisites

- Break-glass IAM user credentials staged (see `breakglass-access.md`).
- Cognito user pool id and the api Lambda stack already deployed.
- The post-confirmation trigger has been wired (step 1 below); without it
  the User mirror row will need a manual insert (step 3) for self-sign-ups,
  and `AuthService.Exchange` will lazy-create on first sign-in.

## Procedure

### 1. Wire the post-confirmation trigger (once per pool)

SAM cannot manage the user pool's `LambdaConfig` because the pool lives in
the `base-data` stack while the trigger function lives in the `api` stack.
The api stack publishes the function ARN as the
`CognitoPostConfirmationFunctionArn` output; attach it manually:

```bash
# Substitute $ENV (e.g. `test` or `prod`) and the actual pool id.
USER_POOL_ID=<from cloudformation describe-stacks numun-${ENV}-base-data>

aws cognito-idp update-user-pool \
  --user-pool-id "$USER_POOL_ID" \
  --lambda-config "PostConfirmation=$(aws cloudformation describe-stacks \
      --stack-name numun-${ENV}-api \
      --query \"Stacks[0].Outputs[?OutputKey=='CognitoPostConfirmationFunctionArn'].OutputValue | [0]\" \
      --output text)"
```

`update-user-pool` is a **full replacement** of the pool's mutable
configuration; if other mutable settings have drifted (MFA, password policy,
etc.), pass them again or you will silently revert them. Cross-check
against `infra/base-data/template.yaml` before running.

### 2. Mint the first staff-admin in Cognito

```bash
ADMIN_EMAIL="ops@numun.org"               # the first human admin
ADMIN_NAME="NUMUN Ops"
# USER_POOL_ID already set from §1 above.

aws cognito-idp admin-create-user \
  --user-pool-id "$USER_POOL_ID" \
  --username "$ADMIN_EMAIL" \
  --user-attributes \
      Name=email,Value="$ADMIN_EMAIL" \
      Name=email_verified,Value=true \
      Name=name,Value="$ADMIN_NAME" \
      Name=custom:role,Value=staff-admin \
  --desired-delivery-mediums EMAIL
```

Cognito emails the user a temporary password. They land at
`portal.numun.org/sign-in/new-password` and set their real password on first
sign-in.

Capture the new user's `sub` for the next step:

```bash
SUB=$(aws cognito-idp admin-get-user \
  --user-pool-id "$USER_POOL_ID" \
  --username "$ADMIN_EMAIL" \
  --query "UserAttributes[?Name=='sub'].Value | [0]" \
  --output text)
echo "first admin sub = $SUB"
```

### 3. Pre-seed the User mirror row (optional but recommended)

`admin-create-user` does **not** fire the post-confirmation trigger (only
self-sign-up `ConfirmSignUp` does). The mirror row will be lazy-created by
`AuthService.Exchange` on first sign-in, but pre-seeding makes the admin
queryable beforehand and avoids race conditions in scripts that immediately
look up the row:

```bash
TABLE="numun-${ENV}"
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

aws dynamodb put-item \
  --table-name "$TABLE" \
  --item "{
    \"PK\":{\"S\":\"USER#${SUB}\"},
    \"SK\":{\"S\":\"PROFILE\"},
    \"entity\":{\"S\":\"User\"},
    \"id\":{\"S\":\"${SUB}\"},
    \"role\":{\"S\":\"staff-admin\"},
    \"email\":{\"S\":\"${ADMIN_EMAIL}\"},
    \"name\":{\"S\":\"${ADMIN_NAME}\"},
    \"phone\":{\"S\":\"\"},
    \"emailStatus\":{\"S\":\"ok\"},
    \"announcementsOptIn\":{\"BOOL\":true},
    \"isDeleted\":{\"BOOL\":false},
    \"version\":{\"N\":\"1\"},
    \"createdAt\":{\"S\":\"${NOW}\"},
    \"updatedAt\":{\"S\":\"${NOW}\"}
  }" \
  --condition-expression "attribute_not_exists(PK)"
```

The `attribute_not_exists` condition makes the put idempotent — if the row
already exists, the call fails harmlessly with
`ConditionalCheckFailedException`.

### 4. Verify end-to-end

1. Admin opens `portal.numun.org/sign-in`, enters email + temp password.
2. Cognito returns `NEW_PASSWORD_REQUIRED`; portal asks for a new password.
3. After setting it, the admin lands on `/dashboard`.
4. `UserService.GetMe` returns `{ role: ROLE_STAFF_ADMIN }`.
5. Admin opens `/admin/staff` and invites a second staff user via
   `UserService.InviteStaff` to confirm the invite path works end-to-end.

### 5. Audit the action

Tail CloudWatch logs for `numun-${ENV}-api-cognito-post-confirmation` and
`numun-${ENV}-api` to confirm clean operation, and check the audit table for
the `sign_in_succeeded` event:

```bash
aws dynamodb query \
  --table-name "numun-${ENV}" \
  --key-condition-expression "PK = :pk AND begins_with(SK, :sk)" \
  --expression-attribute-values "{
    \":pk\":{\"S\":\"USER#${SUB}\"},
    \":sk\":{\"S\":\"AUTH_EVENT#\"}
  }"
```

## Recovery: lost first-admin

If the bootstrap admin loses access (password reset failure, etc.), repeat
this runbook with a different email — the procedure is non-destructive and
multiple `staff-admin`s are supported. Demote the obsolete one afterward by
calling `aws cognito-idp admin-update-user-attributes` to flip
`custom:role` to `staff-staffer`, or `aws cognito-idp admin-delete-user`
plus a corresponding `UpdateItem isDeleted=true` on the DDB row.

## Related

- AUTH.md §3.2 (Staff invite flow)
- SECURITY.md §7.1 (Operational runbooks index)
- `/docs/runbooks/breakglass-access.md`
- `/docs/runbooks/mfa-enrollment.md`
