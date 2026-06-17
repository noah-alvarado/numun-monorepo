# Runbook — operational launch checklist

The M12 hardening milestone is mostly code (rate limits, idempotency, slog redaction, Sentry, pnpm audit). A small subset is **operational** work that cannot be deployed: actions you, the operator, perform in AWS / DNS / Sentry consoles on a schedule. This runbook is the punch list — written for M12 but maintained as the durable pre-launch checklist for any environment.

Each item is independent: tick them off as you complete them. Reference: [IMPLEMENTATION_PLAN.md §M12](../../IMPLEMENTATION_PLAN.md), [SECURITY.md §10](../SECURITY.md).

---

## 1. DMARC tightening (multi-week walk)

Starting point: `_dmarc.mail.numun.org` TXT = `v=DMARC1; p=quarantine; pct=10; rua=mailto:dmarc-reports@numun.org; ruf=mailto:dmarc-forensic@numun.org; adkim=s; aspf=s` (set in M1 per [`ses-domain-setup.md`](./ses-domain-setup.md)).

The progression is intentional — at each step, **wait long enough to see DMARC aggregate reports** at `dmarc-reports@numun.org` before tightening further. A bad alignment under `p=reject` silently blackholes mail; the progression catches misalignment under the safer policy first.

### Schedule

| Step             | After                                              | Target TXT              |
| ---------------- | -------------------------------------------------- | ----------------------- |
| Tier 1 (current) | M1                                                 | `p=quarantine; pct=10`  |
| Tier 2           | At least **2 weeks** of clean reports after M9     | `p=quarantine; pct=100` |
| Tier 3 (final)   | At least **2 weeks** of clean reports after Tier 2 | `p=reject; pct=100`     |

### Procedure

For each step, replace the TXT in Route 53:

```bash
ENV=prod            # or test
ROOT=numun.org      # or test.numun.org
HZ=$(aws route53 list-hosted-zones-by-name --dns-name "$ROOT." \
  --query 'HostedZones[0].Id' --output text | sed 's|/hostedzone/||')

# Tier 2 example. For Tier 3, change "p=quarantine" → "p=reject".
aws route53 change-resource-record-sets \
  --hosted-zone-id "$HZ" \
  --change-batch '{
    "Changes": [{
      "Action": "UPSERT",
      "ResourceRecordSet": {
        "Name": "_dmarc.mail.'"$ROOT"'.",
        "Type": "TXT",
        "TTL": 300,
        "ResourceRecords": [{
          "Value": "\"v=DMARC1; p=quarantine; pct=100; rua=mailto:dmarc-reports@numun.org; ruf=mailto:dmarc-forensic@numun.org; adkim=s; aspf=s\""
        }]
      }
    }]
  }'
```

### Verification

After each change, wait 5 minutes for DNS propagation, then:

```bash
dig +short TXT "_dmarc.mail.${ROOT}"
```

Within 24 hours, check the DMARC aggregate reports inbox (`dmarc-reports@numun.org`) for failures or alignment issues from major receivers (Gmail, Outlook, Yahoo). If anything is flagged, **roll back to the previous tier** ([`email-reputation-collapse.md`](./email-reputation-collapse.md) §DMARC rollback) and investigate before re-attempting.

### Tracking

Date completed Tier 2: **\_\_\_\_**
Date completed Tier 3: **\_\_\_\_**

---

## 2. HSTS preload submission

HSTS is already configured at CloudFront on all four distributions (site, portal, cms, assets) via the shared response-headers policy in `infra/base-cdn/template.yaml`:

```
Strict-Transport-Security: max-age=31536000; includeSubDomains; preload
```

The `preload` directive on its own does nothing — the apex domain must be **submitted to** https://hstspreload.org for browsers to ship it preloaded.

### Pre-submission checklist (the site enforces these)

- `numun.org` (apex) serves a valid HTTPS response.
- All subdomains serve HTTPS (portal, cms, assets, api).
- HTTP → HTTPS 301 redirect on the apex.
- Valid HSTS header with `max-age >= 31536000`, `includeSubDomains`, `preload`.
- The apex has been stable in production for **at least 30 days** (per [SECURITY.md §3.4](../SECURITY.md)) so we have confidence we won't need to roll back.

### Procedure

1. Open https://hstspreload.org and enter `numun.org`.
2. The site auto-checks the four requirements above.
3. If all green: submit.
4. Preload status reaches Chrome / Firefox / Safari in 6–12 weeks via browser release cycles.

### Verification

After submission, the form shows "Status: Pending submission". After a browser release picks it up, https://hstspreload.org/?domain=numun.org shows "Status: Preloaded".

### Tracking

Date submitted: **\_\_\_\_**
First confirmed-preloaded date: **\_\_\_\_**

### Reverting

Submitting is mostly one-way. Removal requires an opt-out request via the same site and takes several browser-release cycles. **Do not submit until you're confident the apex is permanently committed to HTTPS.**

---

## 3. SES sandbox-exit verification

SES sandbox-exit was submitted at the end of M1 per [`ses-domain-setup.md`](./ses-domain-setup.md) §"SES sandbox-exit request". Approval typically takes 24–72 h; AWS sometimes asks follow-up questions. By M12, this should already be approved — this step confirms.

### Verification

```bash
# Expected output: SendingEnabled=true, ProductionAccessEnabled=true.
aws sesv2 get-account --region us-east-2 \
  --query 'ProductionAccessEnabled' --output text
```

Or via Console: SES → Account dashboard → "Sending statistics" shows the daily quota above 200 (sandbox cap is 200/day) and the per-second send rate above 1.

If still sandboxed: pull up the original support case, respond to any open questions, and escalate by reopening the ticket. The portal flows that hit the Cognito SES path (sign-up verification, password reset) silently degrade at scale while sandboxed.

### Tracking

Date confirmed exited: **\_\_\_\_**

---

## 4. DDB PITR restore drill

Point-in-Time Recovery is enabled on the `numun-${ENV}` table ([SECURITY.md §5.5](../SECURITY.md)). The drill confirms that we can actually restore from PITR — a backup you never exercise is one you can't rely on.

### Procedure

Restore the live table to a side table at a recent timestamp, query a known-stable item, then delete the side table.

```bash
ENV=test   # always drill against test, not prod
REGION=us-east-2
SRC=numun-${ENV}
DST=numun-${ENV}-pitr-drill-$(date +%Y%m%d-%H%M)
AT=$(date -u -v-1H +%Y-%m-%dT%H:%M:%SZ)   # one hour ago

aws dynamodb restore-table-to-point-in-time \
  --region "$REGION" \
  --source-table-name "$SRC" \
  --target-table-name "$DST" \
  --restore-date-time "$AT"

# Restore takes 5-15 min for our table size. Poll:
aws dynamodb describe-table --region "$REGION" --table-name "$DST" \
  --query 'Table.TableStatus' --output text
```

When `TableStatus=ACTIVE`:

```bash
# Pick any known-stable item — a seed user works well.
aws dynamodb get-item \
  --region "$REGION" \
  --table-name "$DST" \
  --key '{"PK":{"S":"USER#0190a000-0000-7000-8000-000000000001"},"SK":{"S":"META"}}'
```

Expect a JSON response with the seed advisor's User row. If empty, the restore window is older than the seed; pick a different known item.

### Cleanup

```bash
aws dynamodb delete-table --region "$REGION" --table-name "$DST"
```

### Tracking

Date drill completed (test env): **\_\_\_\_**
Notes (anything unexpected during restore): **********\_\_\_\_**********

---

## 5. Sentry verification (post-DSN bootstrap)

After completing [`sentry-setup.md`](./sentry-setup.md), confirm both sides are sending events.

### API

Find a way to trigger a controlled error in prod. The simplest path: hit an RPC with a request body that fails `protovalidate`, e.g.,

```bash
curl -s -X POST "https://api.${ROOT}/numun.v1.DelegationService/CreateDelegation" \
  -H "Content-Type: application/json" \
  --data '{"unknown":"field"}'
```

Expect HTTP 400 with a validation error. The error should show up in Sentry within 60 s, tagged `component=api`, `environment=<env>`, `release=<commit-sha>`.

### Portal

In the deployed portal, open browser devtools and run:

```js
throw new Error("sentry-smoke-test");
```

The unhandled error should reach Sentry tagged `environment=<env>`, `release=<release-version>`.

### Verification of redaction

Trigger an error that has a sensitive header. From devtools console:

```js
fetch("/some-endpoint", {
  headers: { "X-CSRF-Token": "should-not-leak-to-sentry" },
});
```

Confirm the Sentry event's request → headers shows `X-CSRF-Token: [REDACTED]`.

### Tracking

Date API events confirmed flowing: **\_\_\_\_**
Date portal events confirmed flowing: **\_\_\_\_**
Date redaction verified: **\_\_\_\_**

---

## 6. Final M12 sign-off

M12 is done when:

- [ ] All [`docs/runbooks/`](.) files are filled out (not stubs).
- [ ] Section 1 above: Tier 3 DMARC reached.
- [ ] Section 2: HSTS preload submitted (preloaded-confirmation not required to call M12 done).
- [ ] Section 3: SES production access confirmed.
- [ ] Section 4: PITR restore drill completed against `test`.
- [ ] Section 5: Sentry events confirmed flowing from both surfaces; redaction verified.
- [ ] CI gates green: `go vet`, `go test`, `govulncheck`, `pnpm audit --audit-level=high`, `pnpm lint`, `pnpm typecheck`, `buf lint`.
- [ ] [SECURITY.md §10](../SECURITY.md) open items reviewed; any that became "do before launch" are filed as their own task.

Date M12 closed: **\_\_\_\_**
