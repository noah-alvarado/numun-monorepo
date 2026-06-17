# Runbook — Account takeover (suspected)

A `staff-admin` invokes this runbook when there is reasonable belief that a NUMUN portal account has been compromised — stolen password, hijacked session, or unauthorized device using the account. Goal: contain blast radius in minutes, then investigate and remediate.

References: [SECURITY.md](../SECURITY.md) §4.1, §6.3, §7.1, [AUTH.md](../AUTH.md) §3, §5, §11, §13.

Set these once at the top of your shell:

```bash
export POOL_ID=us-east-2_xxxxxxxxx          # Cognito user pool id
export TABLE=numun-prod                     # DDB single table
export USER_EMAIL=advisor@example.edu       # the compromised account
export USER_ID=<cognito-sub>                # resolve via admin-get-user below
export REGION=us-east-2
```

## 1. Detection signals

Any one of these is enough to start the runbook. Two or more should be treated as confirmed.

- **`AuthAuditEvent` `sign_in_failed` spike on a single `userId`** — per [SECURITY.md](../SECURITY.md) §7.2; CloudWatch metric filter alarms on >10/minute for one user.
- **`sign_in_succeeded` from an unfamiliar `ip` / `userAgent`** — compare against the user's prior rows in `AuthAuditEvent`.
- **Sentry spike of `permission_denied` or unusual mutation patterns tagged with the userId** — script kiddies probing scope boundaries after credential theft.
- **User self-report** — advisor or staffer emails the staff-admin distribution list saying "I didn't sign in" or "things changed I didn't change".
- **State-transition events the real user wouldn't make** — `scope_granted`, `role_changed`, `delegation_approved`, `payment_recorded`, `award_modified`, `email_changed` ([SECURITY.md](../SECURITY.md) §6.3) attributed to the user but inconsistent with their role or schedule.

## 2. Immediate containment (within minutes)

Resolve `USER_ID` from the email first; everything else keys off it.

```bash
aws cognito-idp admin-get-user \
  --user-pool-id "$POOL_ID" --username "$USER_EMAIL" --region "$REGION" \
  --query 'UserAttributes[?Name==`sub`].Value' --output text
# copy the value into $USER_ID
```

### 2a. Force a password reset

```bash
aws cognito-idp admin-reset-user-password \
  --user-pool-id "$POOL_ID" --username "$USER_EMAIL" --region "$REGION"
```

The user's current password stops working immediately. Cognito emails a reset code to the address of record.

### 2b. Revoke all refresh tokens

```bash
aws cognito-idp admin-user-global-sign-out \
  --user-pool-id "$POOL_ID" --username "$USER_EMAIL" --region "$REGION"
```

All Cognito refresh tokens for the user are invalidated. Currently issued access tokens remain valid for up to 1 hour ([AUTH.md](../AUTH.md) §5) — step 2c closes that window for the portal.

### 2c. Delete the user's Session rows

`Session` rows are keyed `PK = SESSION#<sessionId>`, `SK = META` ([AUTH.md](../AUTH.md) §13.1). v1 has no user→session GSI ([AUTH.md](../AUTH.md) §4.4), so the only path to "log out everywhere" today is a filtered scan of the session partition keyspace. Acceptable: TTL caps the partition at 30 days of live rows.

```bash
aws dynamodb scan \
  --table-name "$TABLE" --region "$REGION" \
  --filter-expression "begins_with(PK, :p) AND userId = :u" \
  --expression-attribute-values "{\":p\":{\"S\":\"SESSION#\"},\":u\":{\"S\":\"$USER_ID\"}}" \
  --projection-expression "PK, SK" \
  --output json > /tmp/sessions.json

jq -r '.Items[] | [.PK.S, .SK.S] | @tsv' /tmp/sessions.json | \
  while IFS=$'\t' read -r pk sk; do
    aws dynamodb delete-item \
      --table-name "$TABLE" --region "$REGION" \
      --key "{\"PK\":{\"S\":\"$pk\"},\"SK\":{\"S\":\"$sk\"}}"
  done
```

Once the GSI proposed in [AUTH.md](../AUTH.md) §13.1 (`GSI1PK = USER#<userId>`) lands, replace the scan with `query --index-name GSI1`. Until then, scan-and-delete is the documented v1 mechanism. There is no admin UI for session deletion in v1.

### 2d. Notify the user via the email of record

Send a short note from `noreply@mail.numun.org` to the address on the `User` row (not any email recently added by the suspected attacker — pull the prior value from `AuthAuditEvent` kind `email_changed` if needed). State: account locked, password reset in flight, next step is the reset email, contact `staff-admin` if you didn't expect this.

## 3. Investigation

### 3a. Read the user's `AuthAuditEvent` partition

```bash
aws dynamodb query \
  --table-name "$TABLE" --region "$REGION" \
  --key-condition-expression "PK = :p AND begins_with(SK, :s)" \
  --expression-attribute-values "{\":p\":{\"S\":\"USER#$USER_ID\"},\":s\":{\"S\":\"AUTH_EVENT#\"}}" \
  --output json | jq '.Items[] | {kind:.kind.S, actor:.actorUserId.S, ip:.ip.S, ua:.userAgent.S, at:.occurredAt.S}'
```

Look for: clusters of `sign_in_failed` followed by a `sign_in_succeeded` from a new IP/UA; `password_reset_completed` the real user didn't initiate; `email_changed`; `scope_granted`, `scope_revoked`, `role_changed`, `account_deleted` ([AUTH.md](../AUTH.md) §11); and the state-transition kinds in [SECURITY.md](../SECURITY.md) §6.3 (`delegation_approved`, `payment_recorded`, `assignment_*`, `award_*`, `bulk_import_committed`, `email_unsuppressed`).

### 3b. CloudWatch logs filtered by userId

```bash
aws logs filter-log-events \
  --log-group-name /aws/lambda/numun-api-prod --region "$REGION" \
  --start-time $(($(date -u +%s) * 1000 - 7*24*3600*1000)) \
  --filter-pattern "\"$USER_ID\""
```

Correlate request ids and source IPs against the audit-event IPs from 3a.

### 3c. Sentry events tagged with the userId

In Sentry, filter `user.id:$USER_ID` over the same window. Cross-reference exceptions and breadcrumbs against the same request ids.

### 3d. Decide scope of compromise

- Credential-only (no successful sign-in from attacker) → containment in §2 is sufficient.
- Successful sign-in from attacker → continue to §4.

## 4. Remediation

Once §3 has identified what the attacker actually did:

- **Revoke unauthorized scope grants.** Any `StaffDelegationAssignment` or `StaffCommitteeAssignment` row tied to a `scope_granted` event from the attacker session — soft-delete it. Write a corresponding `scope_revoked` event with `actorUserId` = the responding admin.
- **Reverse unauthorized role changes.** `role_changed` events from the attacker session → set the affected user back to their prior role via the admin RPC.
- **Restore mutated data.** For each unauthorized state-transition (delegation approve/reject, payment record, award create/modify/delete, assignment edit), use DDB PITR to read the pre-incident value into a side table and then write the correct value back via the normal RPC. Procedure documented in [SECURITY.md](../SECURITY.md) §5.5 — PITR window is 35 days.
- **Audit downstream effects.** Approved delegations may have triggered notification emails; recorded payments may have affected balances visible to advisors. Decide whether to send corrections.
- **Push the user to enable MFA.** If `cognito:mfa_enabled` was not set on the user, point them at [./mfa-enrollment.md](./mfa-enrollment.md). Cognito MFA is opt-in TOTP in v1 ([SECURITY.md](../SECURITY.md) §4.2) — no enforcement, but strongly recommend it post-incident.
- **Confirm the email of record is correct.** If `email_changed` appears in §3a from the attacker session, manually correct it via `admin-update-user-attributes` and confirm with the user out-of-band.

## 5. Post-mortem

Within 48h of containment, the responding admin writes a short post-mortem to the staff-admin distribution list covering:

- Timeline (first attacker action, detection, containment, remediation).
- Initial vector (phishing, credential reuse, weak password, session theft — best guess).
- Data touched (entities, fields, blast radius).
- Whether external notification is owed.

**Escalate to [./data-breach-suspected.md](./data-breach-suspected.md) if any of:**

- Attacker accessed PII belonging to users other than the compromised account (advisor or delegate roster data outside the user's normal scope).
- Multiple accounts compromised in the same window (suggests credential-stuffing or a broader leak — see [SECURITY.md](../SECURITY.md) §4.4).
- Evidence the attacker exfiltrated data (large `List*` traffic from an unfamiliar IP, bulk exports).

Track recurring patterns. If `sign_in_failed` spikes become routine, re-evaluate Cognito Advanced Security Features ($0.05/MAU, currently disabled per [SECURITY.md](../SECURITY.md) §4.4).
