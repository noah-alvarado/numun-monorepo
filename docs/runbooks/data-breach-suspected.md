# Runbook — Data breach (suspected)

Use this runbook when you have reason to believe data has left the system without authorization: exfiltration, accidental exposure, or a third-party report of leaked credentials or PII. For a _single user's_ credentials being compromised without evidence of broader data movement, use [`./account-takeover.md`](./account-takeover.md) instead — this runbook is the "we think data left the system" case.

References: [SECURITY.md](../SECURITY.md) §1.2 (asset sensitivity), §1.3 (containment vs. forensics tradeoff), §3.7 (logging + retention), §5.5 (backups / PITR), §7 (incident response), §8 (compliance posture). [AUTH.md](../AUTH.md) §11 (`AuthAuditEvent`, the forensic backbone).

## 1. Detection signals

Any one of the following warrants opening this runbook:

- **CloudWatch alarms.** Lambda error-rate spike on `api`, DDB throttling on `numun-${ENV}` (could indicate scan-style enumeration), DLQ depth > 0 on `numun-${ENV}-email-send-dlq` (unusual queue activity), or the **billing alarm** at $10/mo firing on data-transfer egress.
- **Unusual S3 access patterns.** Spikes in `s3:GetObject` on `numun-org-uploads` or `numun-org-artifacts` — especially from non-CloudFront principals — or large `GetObject` ranges against `numun-org-assets`.
- **Third-party reports.** A user reports receiving spam at an address only NUMUN had. Sentry shows requests from a country with no known users. A delegate's PII appears in a paste site or social-media screenshot.
- **Credential leak signals.** GitHub secret-scanning alert on the repo; a `refresh_token`, SSM SecureString value, or AWS access-key id shows up in a paste site, public Gist, container image, or a screenshot. Cognito Advanced Security or HaveIBeenPwned-style notifications for a `staff-admin` email.
- **`AuthAuditEvent` anomalies.** A `staff-admin` account showing `sign_in_succeeded` from a new country, mass `bulk_import_committed` events from a single actor in a short window, or `scope_granted` events made outside business hours.

Treat as suspected breach if **two or more** weak signals correlate, or if any single signal is high-confidence (e.g., a confirmed paste-site leak).

## 2. Immediate containment (target: within 30 minutes)

### 2.1 Tradeoff: containment vs. forensic preservation

Per [SECURITY.md](../SECURITY.md) §1.3, aggressive containment (rotating every secret, signing every session out, deleting potentially-bad rows) destroys evidence the investigation needs. Walk through this decision tree before acting:

- If the breach is **active and ongoing** (data is leaving the system right now, you can see the requests in CloudWatch) → contain first, preserve in parallel.
- If the breach is **suspected but not confirmed active** (e.g., a paste-site leak of unclear age) → preserve forensic state first (§2.3), then contain.
- If in doubt → preserve. The credentials being rotated are also captured in the snapshot; you can rotate two minutes later.

Document the decision and the timestamp in the incident log.

### 2.2 Containment actions

Whichever order, do all of these:

**Rotate plausibly-compromised credentials.** If scope is unclear, rotate the full set:

```bash
# Cognito app client secret (forces all clients to re-auth via the new secret).
aws cognito-idp update-user-pool-client \
  --user-pool-id "$POOL_ID" \
  --client-id "$CLIENT_ID" \
  --generate-secret

# Every SSM SecureString under /numun/$ENV/*. List first, then put-parameter
# --overwrite with a freshly-generated value for each.
aws ssm get-parameters-by-path \
  --path "/numun/$ENV" --recursive --with-decryption \
  --query 'Parameters[].Name' --output text
```

Cover at minimum: `/numun/$ENV/cms_oauth/state_secret`, `/numun/$ENV/cms_oauth/client_secret`, `/numun/$ENV/email/unsubscribe_secret`, `/numun/$ENV/github_app/private_key`, `/numun/$ENV/sentry/dsn` (rotate the Sentry project's DSN via the Sentry UI; treat it as scoped config), plus the GitHub OAuth app client secret (GitHub UI → OAuth Apps → Generate a new client secret).

If a break-glass console session is suspected compromised, rotate that IAM user's MFA and any active STS sessions — see [`./breakglass-access.md`](./breakglass-access.md).

**Sign out plausibly-compromised users.** For every user with anomalous `AuthAuditEvent`:

```bash
aws cognito-idp admin-user-global-sign-out \
  --user-pool-id "$POOL_ID" --username "$COGNITO_SUB"
```

Then purge their DDB `Session` rows so the cached access token can't be replayed:

```bash
aws dynamodb query \
  --table-name "numun-$ENV" \
  --index-name GSI1 \
  --key-condition-expression "GSI1PK = :u" \
  --expression-attribute-values "{\":u\":{\"S\":\"USER#$USER_ID\"}}" \
  --query 'Items[?starts_with(SK.S, `SESSION#`)].[PK.S,SK.S]' --output text
# Then delete-item each PK/SK pair returned.
```

(If the GSI on Sessions-by-user doesn't exist yet — AUTH.md §13.1 leaves it for "log out everywhere" — fall back to a `Scan` filtered on `entity = "Session"` and `userId`.)

**If GitHub repo write access is suspected compromised.** Revoke the GitHub App / OAuth app secret as above; in the GitHub UI, audit and remove any unexpected collaborators or deploy keys; review recent commits to `main` and `/content/**` for unauthorized changes.

### 2.3 Forensic preservation (do BEFORE any destructive remediation)

**Snapshot DDB via PITR** into a side table. This freezes the state at a known moment so subsequent remediation (deletions, soft-deletes) does not destroy evidence.

```bash
NOW=$(date -u +%Y-%m-%dT%H:%M:%S)
aws dynamodb restore-table-to-point-in-time \
  --source-table-name "numun-$ENV" \
  --target-table-name "numun-$ENV-forensic-$NOW" \
  --use-latest-restorable-time
```

If you know roughly when the breach started, pick a `--restore-date-time` slightly before that window so the snapshot captures the pre-breach state too.

**Copy CloudWatch Lambda logs to S3** (default retention is 30 days per SECURITY.md §3.7; archive now so a later rotation doesn't lose them):

```bash
aws logs create-export-task \
  --log-group-name "/aws/lambda/numun-$ENV-api" \
  --from $(date -u -v-7d +%s)000 --to $(date -u +%s)000 \
  --destination "numun-org-artifacts" \
  --destination-prefix "forensics/$NOW/api-logs"
```

Repeat for `email-worker`, `email-feedback`, `cognito-post-confirmation`.

**Capture API Gateway access logs.** The 1-year S3 archive (SECURITY.md §3.7) already preserves these; copy the relevant window into `numun-org-artifacts/forensics/$NOW/apigw/` so it survives any later lifecycle policy.

**Capture Sentry events.** Sentry UI → Issues → filter by time window, export to JSON, or use the Sentry API:

```bash
curl -H "Authorization: Bearer $SENTRY_TOKEN" \
  "https://sentry.io/api/0/projects/numun/numun-api/events/?statsPeriod=7d" \
  > "sentry-events-$NOW.json"
```

**Capture S3 access logs** for any bucket where `aws s3api get-bucket-logging` shows logging enabled. In v1, S3 access logging is **not enabled by default** on `numun-org-uploads` / `-artifacts` — note the gap in the incident log and add to remediation. **CloudFront access logs are not enabled in v1** (SECURITY.md §3.7, §10) — this is a known forensic blind spot.

**Capture the GitHub audit log** (Organization → Settings → Audit log → Export) for the breach window.

## 3. Investigation

Each source answers a different question:

- **DDB PITR snapshot (`numun-$ENV-forensic-$NOW`).** What did the data look like during the breach window? Compare counts, look for unexpected rows (e.g., new `StaffDelegationAssignment` rows that shouldn't exist, modified `User.role` values), and diff against current state.
- **`AuthAuditEvent` rows** (AUTH.md §11). The forensic backbone. Query by `userId` for each flagged user and by `actorUserId` to find who made changes. The kinds of interest here: `sign_in_succeeded` (from where, what IP, what UA), `role_changed`, `scope_granted`, `bulk_import_committed` (metadata includes delegation id and row counts — never row content, so this bounds what the actor could have seen), `delegation_approved`, `payment_recorded`.
- **CloudWatch Lambda logs.** Filtered by request-id, `userId`, or client IP. Use the `slog` structured fields (logging hygiene per SECURITY.md §5.4 strips passwords / tokens / Authorization headers, so logs are safe to share within the incident team but still show RPC name, caller userId, duration, response code).
- **API Gateway access logs.** Request shapes, IPs, user-agents, response codes. Look for `4xx` bursts (probing) followed by a stable `2xx` stream (success after finding a working request shape).
- **S3 access logs** (where enabled). Object keys and accessing principals.
- **GitHub audit log.** Repo-write actions, secret access, OAuth app authorizations, collaborator changes.

Reconstruct a timeline: who acted, what they touched, what data could plausibly have been read. Write it into the incident doc as you go — do not rely on memory.

## 4. Remediation

Once scope is known:

- **If scope is unclear** → rotate the full credential set (every SSM SecureString under `/numun/$ENV/*`, Cognito client secret, GitHub OAuth app secret) and global-sign-out **all** users. The user-facing pain is real but bounded; the alternative is leaving a known-bad credential live.
- **If scope is bounded to a few users** → rotate only those users' sessions (per §2.2) and the secrets they could have touched.
- **Deactivate compromised accounts.** For a `staff-admin` whose credentials are confirmed leaked, set `custom:role` to `none`, soft-delete the `User` row, and write an `account_deleted` `AuthAuditEvent`.
- **Patch the vulnerability.** If the breach exploited a code path (IDOR miss, missing scope check, etc.), open a fix PR with the same urgency as a production outage; treat the runbook timeline as the deploy gating signal.
- **Record affected-party set.** For each exfiltrated record, capture the `userId` / `delegateId` / `delegationId` and the affected fields. This list drives §5 notification.

## 5. Stakeholder notification

NUMUN is not certified under FERPA, GDPR, COPPA, SOC 2, or any other compliance framework (SECURITY.md §8). There is **no regulatory clock running**. Notification posture is judgment-driven and best-effort, consistent with the privacy notice's minimization promise.

Sensible defaults:

- **Delegate or advisor PII touched** → notify the affected advisors by email (they are the contact-of-record for their delegates), and the staff-admin distribution list internally. Use the announcement composer or a one-off SES send. Be specific about what was touched, what was not, and what the recipient should do (rotate password, watch for spam).
- **Cognito refresh tokens or session data touched** → in addition, force-sign-out everyone (§2.2) and surface a banner in the portal next sign-in explaining the precautionary action.
- **Payment ledger or financial records touched** → notify the affected advisors and the staff-admin distribution list; also notify the conference treasurer separately.
- **Materially sensitive breach involving Northwestern-affiliated individuals** → escalate to the Northwestern University compliance / information-security office. NUMUN is a student org, not a university unit, but where Northwestern individuals' data is involved the right pose is to surface it.
- **GitHub or AWS account compromise** → notify the full staff-admin distribution list; consider whether to disclose publicly via the landing page (rare; consult before doing).

A dedicated `security@numun.org` mailbox is an open item (SECURITY.md §7.3, §10). Until it exists, the staff-admin distribution list is the established channel for both inbound reports and outbound notifications. **Do not promise** a regulatory disclosure timeline — describe what was done, when, and why.

## 6. Post-mortem

Within five business days of containment, document:

- **Timeline.** First detection signal, containment action, forensic preservation, scope determination, notifications sent. Timestamps in UTC.
- **Scope.** Which records were accessed or exfiltrated, by which actor (if known), via which mechanism. Cite the forensic sources that proved each claim.
- **What changed.** Credentials rotated, sessions revoked, code patched, IAM tightened.
- **Accountability.** Who owned each step. (For NUMUN's scale this is usually one or two staff-admins.)

**Recurrence-prevention.** Cross-reference SECURITY.md §10 open items and pick at least one to close as a direct response:

- **Enable CloudFront access logs** (SECURITY.md §10) — closes the static-asset / SPA forensic blind spot.
- **Enable GuardDuty** (~$3–5/mo, SECURITY.md §10) — AWS-native threat detection for credential misuse.
- **Enable WAF on CloudFront** (~$5/mo + per-rule, SECURITY.md §2.10, §10) — if the breach involved abuse of an HTTP surface.
- **Enable S3 access logging** on `numun-org-uploads` / `-artifacts` — closes the S3 forensic gap noted in §2.3.
- **Application-layer encryption of refresh tokens** in DDB Session rows (SECURITY.md §10) — closes the "DDB read access = token access" path.
- **Stand up `security@numun.org`** if this incident relied on the staff-admin list as the inbound channel and friction was felt.
- **Enforce MFA for `staff-admin`** (SECURITY.md §10, AUTH.md §15) if the breach involved an admin without MFA.

File the post-mortem under `/docs/postmortems/YYYY-MM-DD-<slug>.md` (create the directory if it doesn't exist; this runbook's existence implies eventual postmortems). Link it from the next staff-admin sync agenda.
