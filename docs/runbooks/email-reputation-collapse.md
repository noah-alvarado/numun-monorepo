# Runbook — Email reputation collapse

Triggered when Amazon SES reputation metrics for the NUMUN account drift toward the warning thresholds (bounce rate > 5%, complaint rate > 0.1%), or AWS has already issued a warning / throttle / pause. Sustained breach risks pausing the entire SES account, which simultaneously breaks sign-ups, password resets, delegation notifications, **and** announcements — Cognito uses the same SES account.

References: [../subsystems/EMAIL.md](../subsystems/EMAIL.md) §2, §5, §6; [./ses-domain-setup.md](./ses-domain-setup.md); [../SECURITY.md](../SECURITY.md) §3.7, §7.

Placeholders used below: `$ENV` (e.g. `prod`, `staging`), `$REGION` (e.g. `us-east-2`), `$HOSTED_ZONE_ID` (Route 53 hosted zone for `numun.org`).

---

## 1. Detection signals

Any one of these is sufficient to open the runbook; usually two or three fire together.

- **SES Reputation Dashboard warning.** AWS emails the account root + delivers an in-console banner under SES → Account dashboard → "Reputation metrics". The two tracked metrics are **bounce rate** (warn ≥ 5%, pause ≥ 10%) and **complaint rate** (warn ≥ 0.1%, pause ≥ 0.5%).
- **DLQ depth alarm.** CloudWatch alarm on `numun-${ENV}-email-send-dlq` depth > 0 (configured per SECURITY.md §3.7). Indicates `email-worker` is failing repeatedly.
- **Spike in `User.emailStatus` flips** to `"bounced"` or `"complained"` — driven by the `email-feedback` Lambda (EMAIL.md §6). Visible by scanning DDB for the attribute, or by the row count growth in `EmailEvent` rows of kind `bounce_received` / `complaint_received`.
- **User reports** via the staff-admin distribution list ("I didn't get my verification code", "I can't reset my password").
- **Cognito sign-up flow failures.** Cognito uses SES for verification mail (EMAIL.md §4.1). A SES pause **silently** breaks new sign-ups — the Cognito API returns a generic failure; users see "code never arrived". Worth checking proactively if any other signal fires.
- **Sandbox-out revocation.** AWS occasionally reverts a sandbox-exited account that misbehaves. If `aws sesv2 get-account` shows `ProductionAccessEnabled=false` unexpectedly, treat as paused.

---

## 2. Immediate containment

The goal is to stop adding fuel before investigating. Order matters.

1. **Pause the announcement queue** by disabling the SQS event source for the worker. This drains in-flight messages without losing them — messages stay in SQS until reprocessed.

   ```bash
   # Find the event-source-mapping UUID
   aws lambda list-event-source-mappings \
     --function-name numun-${ENV}-email-worker \
     --region ${REGION}

   # Disable it
   aws lambda update-event-source-mapping \
     --uuid <UUID-from-above> \
     --enabled false \
     --region ${REGION}
   ```

2. **Halt staff-driven cohort sends out-of-band.** Identify any `AnnouncementService.Send` RPC the staff-admin may be about to fire (`portal.numun.org/admin/announcements`). Communicate via Slack / phone: "Do not press Send until further notice." There is no kill-switch on the RPC itself in v1 — verbal hold is the control.

3. **Do NOT pause transactional sends** (T1–T7 from EMAIL.md §1) unless AWS explicitly asks. Transactional mail is the legitimate side of the traffic; stopping it does not improve reputation and breaks active users. The `email-feedback` Lambda's per-user suppression already prevents resending to known-bad addresses.

4. **Snapshot the metrics now** so post-mortem has a "before" reading. Open SES Console → Reputation metrics → screenshot the 14-day window.

---

## 3. Investigation

### 3.1 Bounce / complaint distribution by recipient domain

```bash
aws sesv2 get-suppressed-destination-summary --region ${REGION}

# Or list individual entries, paginated
aws sesv2 list-suppressed-destinations --region ${REGION}
```

If 80%+ of bounces are on a single domain (e.g., a school district's mail server), suspect a stale or misconfigured advisor address list. If bounces are broad across domains, suspect a deliverability config regression (DKIM key drift, DMARC alignment failure, recent DMARC tightening).

### 3.2 Query `EmailEvent` in DDB for the recent window

Each bounce / complaint is captured as an `EmailEvent` row with `kind = "bounce_received" | "complaint_received"`, `recipientEmail`, and `sentAt` (EMAIL.md §8). Scan with `FilterExpression: kind IN ("bounce_received","complaint_received") AND sentAt > <window-start>`. Group by `recipientEmail` domain to corroborate §3.1.

### 3.3 Confirm DNS records are still published

A nameserver migration or accidental Route 53 record delete is the most common foot-gun.

```bash
dig +short TXT mail.numun.org           # expect SPF
dig +short TXT _dmarc.mail.numun.org    # expect DMARC
dig +short CNAME <token1>._domainkey.mail.numun.org  # one per DKIM token
```

Cross-reference against `./ses-domain-setup.md` §2.

### 3.4 Check DMARC aggregate reports

`rua=mailto:dmarc-reports@numun.org` (see `_dmarc.mail.numun.org`). Receiver-side DMARC failures show up here per recipient mail provider — Gmail, Outlook, Yahoo all send daily XML reports. Look for `dkim=fail` or `spf=fail` alignment results that did not exist before the incident window.

### 3.5 Check the SES configuration set

```bash
aws sesv2 get-configuration-set \
  --configuration-set-name numun-${ENV}-emails \
  --region ${REGION}

aws sesv2 get-configuration-set-event-destinations \
  --configuration-set-name numun-${ENV}-emails \
  --region ${REGION}
```

Confirm the event destination still publishes Bounce / Complaint / Delivery to `numun-${ENV}-email-feedback`. If the destination was removed, the `email-feedback` Lambda has been blind — suppression has not been updating, and bounce rate ramped while we didn't notice.

---

## 4. Remediation

Apply in order; re-check the SES Reputation dashboard between steps.

1. **Roll back any recent DMARC tightening.** If the most recent change to `_dmarc.mail.numun.org` moved from `pct=10` → `pct=100` or to `p=reject`, revert. See §5 below for the exact command. The progression in `ses-domain-setup.md` is intentional and reversible.

2. **Manually unsuppress soft-bounced users.** Transient failures (mailbox-full, greylisting) may have been incorrectly classified. Either:
   - Use the admin email-health UI at `portal.numun.org/admin/email-health` (EMAIL.md §9), or
   - For SES-side suppression list entries (separate from `User.emailStatus`):

   ```bash
   aws sesv2 delete-suppressed-destination \
     --email-address user@example.com \
     --region ${REGION}
   ```

3. **Clean the recipient list before re-enabling announcements.** If announcements went out to a stale list, the fix is data hygiene, not deliverability. Export the affected `Announcement` row's recipient set, intersect with current `emailStatus = "ok"` users, and validate domains.

4. **Re-enable the SQS event source** only after §3 and the above are verified.

   ```bash
   aws lambda update-event-source-mapping \
     --uuid <UUID> --enabled true --region ${REGION}
   ```

5. **If SES has already paused the account:** open an AWS Support case under SES with the bounce-rate explanation and the remediation plan. Restoration is human-reviewed at AWS and can take 1–3 business days. While paused, the portal is degraded: sign-ups, password resets, delegation notifications, and announcements all fail. Communicate this to staff and to advisors via the landing page and Slack.

---

## 5. DMARC tightening rollback procedure

Revert `_dmarc.mail.numun.org` TXT to the previous tier. The current and prior values come from the table in `./ses-domain-setup.md` §"DMARC progression".

```bash
cat > /tmp/dmarc-rollback.json <<'JSON'
{
  "Comment": "Roll back DMARC tightening during reputation incident",
  "Changes": [{
    "Action": "UPSERT",
    "ResourceRecordSet": {
      "Name": "_dmarc.mail.numun.org",
      "Type": "TXT",
      "TTL": 300,
      "ResourceRecords": [{
        "Value": "\"v=DMARC1; p=quarantine; pct=10; rua=mailto:dmarc-reports@numun.org; ruf=mailto:dmarc-forensic@numun.org; adkim=s; aspf=s\""
      }]
    }
  }]
}
JSON

aws route53 change-resource-record-sets \
  --hosted-zone-id ${HOSTED_ZONE_ID} \
  --change-batch file:///tmp/dmarc-rollback.json
```

Replace the quoted `Value` with whatever tier you are rolling back **to** (not from). Confirm with `dig +short TXT _dmarc.mail.numun.org` once the change propagates (≤ 5 min at TTL 300).

---

## 6. Post-mortem

Within 48h of the incident closing, document in a new file under `/docs/postmortems/YYYY-MM-DD-email-reputation.md`:

- **Timeline** — when each detection signal fired, when containment was applied, when reputation returned to green.
- **Root cause** — list misconfig, DMARC step-up too soon, DNS record loss, single-domain bounce storm, etc.
- **Blast radius** — number of recipients affected, whether SES warned vs. throttled vs. paused, downstream impact on sign-ups.
- **What worked / what didn't** — was the DLQ alarm useful? Did the `email-feedback` Lambda catch up in time?

Feed the findings back into:

- **`ses-domain-setup.md` DMARC progression schedule.** If a tightening step caused the incident, lengthen the observation window or add a manual checkpoint.
- **Pre-send recipient-list scrubbing.** Today we trust the advisor list as-is and rely on the post-send feedback loop. If a stale list was the cause, consider adding a pre-send scrub: cross-reference against SES's suppression list and against `User.emailStatus` _at enqueue time_ in `AnnouncementService.Send` (EMAIL.md §5.3 already filters; tighten the filter or add a domain-level allowlist).
- **Alarms.** If a signal that should have fired didn't, file an issue against SECURITY.md §3.7 to add the missing alarm (e.g., complaint-rate watch alarm on the SES feedback SNS topic with a non-zero threshold).
- **DMARC report monitoring.** If aggregate reports were the canary nobody read, add a recurring task to skim them weekly during DMARC progression.
