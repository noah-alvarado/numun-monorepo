# Runbook — Sign-up abuse

**Scope.** Automated sign-up abuse against `portal.numun.org/sign-up`. Three concrete attack shapes are in scope:

1. Mass automated registrations creating dummy `User` rows and flooding Cognito verification mail (SES reputation risk — see [`./email-reputation-collapse.md`](./email-reputation-collapse.md)).
2. Spammers using the sign-up form as an outbound mail vector — Cognito sends the 6-digit code to whatever address the attacker types, so the flow can be abused to deliver attacker-controlled "from-NUMUN" mail to arbitrary inboxes.
3. Account enumeration probing — submitting many emails to learn which already have NUMUN accounts.

CAPTCHA / Turnstile is **not** enabled in v1. This runbook is the activation procedure. See [`../SECURITY.md`](../SECURITY.md) §6.1 and §2.10 and `AUTH.md` §3 and §6.

---

## 1. Detection signals

Investigate when any of the following fire:

- **SES bounce-rate spike on `cognito@mail.numun.org`** — verification codes sent to non-existent inboxes. CloudWatch SES `Reputation.BounceRate` > 2% sustained, or `EmailEvent` rows of `kind = bounce` clustered in time. This is the strongest leading indicator and the link to [`./email-reputation-collapse.md`](./email-reputation-collapse.md).
- **`User` row count jumps vs. baseline.** Baseline growth is single-digit per day; sustained double- or triple-digit per-hour growth is anomalous outside the late-summer registration window.
- **`AuthAuditEvent` of `kind = signup_completed` clustering** from one source IP, one ASN, or one email domain (lots of `@mailinator.com` etc.).
- **Cognito `SignUpAttempts` metric** spiking in the Cognito console (succeeded + failed), especially when failures dominate (rate-limit rejections from Cognito's built-in caps).
- **M12 rate-limit middleware logging frequent 429s** in the anonymous-surface bucket for one IP — `AuthService.Exchange` and `/numun.v1.PublicService/*` are capped at 60/min/IP (SECURITY.md §2.10).
- **User reports**: _"I didn't try to sign up but got a verification email from NUMUN."_ This is a strong indicator that sign-up is being abused as an outbound mail vector — the attacker is enumerating victim inboxes.

---

## 2. Immediate containment

Goal: stop the bleeding within 15 minutes. Do these in parallel where possible.

### 2.1 Tighten the anonymous rate limit

Drop the per-IP cap on anonymous surfaces from 60/min to ~10/min via an emergency deploy of `api/internal/middleware/ratelimit/ratelimit.go::DefaultLimits()`:

```go
PerIPAnonymousPerMin: 10, // was 60 — incident <YYYY-MM-DD>, revert after
```

Open a PR titled `incident: tighten anon rate limit`, merge, deploy. Document the revert date in the PR description.

### 2.2 Block the offending IP at CloudFront

Same procedure as [`./ddos-or-api-abuse.md`](./ddos-or-api-abuse.md) — add the source IP/CIDR to the CloudFront IP-blocklist function and invalidate. Example:

```bash
aws cloudfront update-function \
  --name portal-ip-blocklist \
  --if-match "$ETAG" \
  --function-config Comment="incident",Runtime=cloudfront-js-2.0 \
  --function-code fileb://blocklist.js
aws cloudfront publish-function --name portal-ip-blocklist --if-match "$ETAG"
```

### 2.3 Pause new Cognito sign-ups

Disable self-service registration entirely while you investigate. Existing users can still sign in.

- AWS Console → Cognito → User pool `numun-portal-<env>` → **Sign-up experience** → set **Self-service sign-up** to _Off_.
- Or via CLI:

```bash
aws cognito-idp update-user-pool \
  --user-pool-id us-east-1_XXXXXXXXX \
  --admin-create-user-config AllowAdminCreateUserOnly=true
```

This makes the portal sign-up form return `NotAuthorizedException` until re-enabled.

### 2.4 Suspend abused recipient addresses

If the same email is being used as the verification target across many sign-ups (outbound-mail-vector pattern), add it to the SES suppression list so we stop sending to it:

```bash
aws sesv2 put-suppressed-destination \
  --email-address victim@example.com \
  --reason BOUNCE
```

Repeat for any other targeted addresses surfaced in §3.

---

## 3. Investigation

Once contained, characterize the attack.

### 3.1 Group `AuthAuditEvent` by IP / ASN / email domain

Query DDB for `signup_completed` events in the suspect window (PK = `USER#<userId>`, SK begins_with `AUTH_EVENT#`, kind = `signup_completed`). Easiest path is a Scan with filter for incident-scale analysis — yes, expensive, but acceptable for one-off forensics:

```bash
aws dynamodb scan \
  --table-name numun-main-prod \
  --filter-expression "kind = :k AND createdAt BETWEEN :start AND :end" \
  --expression-attribute-values '{":k":{"S":"signup_completed"},":start":{"S":"2026-06-17T00:00:00Z"},":end":{"S":"2026-06-17T06:00:00Z"}}'
```

Bucket the results by source IP, ASN (look up via `whois` or ipinfo), and email domain. The attack signature usually pops out: one ASN, or one email-domain template (`user01@`, `user02@`…).

### 3.2 Identify the User rows created during the window

Query the `entity = "User"` GSI for `createdAt BETWEEN` the suspect window. List the candidate spam accounts; the count should match the `signup_completed` count from §3.1.

### 3.3 Cross-reference `EmailEvent`

Bounce / delivery events on `cognito@mail.numun.org` are written as `EmailEvent` rows under `EMAIL_FEEDBACK#<emailLowercase>` (orphan-recipient variant, since these often have no real `User`). Pull these for the same window to see which target inboxes were hit.

### 3.4 Check CloudTrail if abuse precedes our Lambda

If something looks wrong upstream of our post-confirmation Lambda (e.g. Cognito `SignUp` calls succeeding but no `signup_completed` audit row), check CloudTrail for `eventSource = cognito-idp.amazonaws.com` and `eventName = SignUp`:

```bash
aws cloudtrail lookup-events \
  --lookup-attributes AttributeKey=EventName,AttributeValue=SignUp \
  --start-time 2026-06-17T00:00:00Z --end-time 2026-06-17T06:00:00Z
```

---

## 4. Remediation

### 4.1 Activate Cloudflare Turnstile on the sign-up form

This is the documented escalation path (SECURITY.md §6.1). Turnstile is free, privacy-respecting, and invisible to most users.

1. Sign up at <https://www.cloudflare.com/products/turnstile/> using the team Cloudflare account (the same one that fronts `numun.org` DNS).
2. Create a site key + secret for `portal.numun.org`. Choose the **Managed** widget mode.
3. Store the site key as `PORTAL_TURNSTILE_SITE_KEY` (public, baked into the portal build) and the secret as `API_TURNSTILE_SECRET` in SSM Parameter Store (`/numun/<env>/turnstile/secret`, `SecureString`).
4. **Portal change** (new PR): add the Turnstile widget to `portal/src/routes/SignUp.tsx`; require the resulting token to be present on the `AuthService.SignUp` request payload before submit.
5. **API change** (same or follow-up PR): in the sign-up RPC handler, call Cloudflare's verify endpoint before invoking Cognito `SignUp`:

```bash
curl -sS -X POST https://challenges.cloudflare.com/turnstile/v0/siteverify \
  -d "secret=$API_TURNSTILE_SECRET" \
  -d "response=$TOKEN_FROM_CLIENT" \
  -d "remoteip=$CLIENT_IP"
```

Reject the request with Connect `permission_denied` if `success != true`.

The runbook ends at activation. The code changes ship as a normal PR — link the PR back to this runbook entry.

### 4.2 Hard-delete abused User rows

The standard NUMUN pattern is soft-delete via staff-admin RPC (preserves the audit trail — see DATA_MODEL.md). For obvious bulk spam where the row was never legitimate, hard-delete is acceptable. Use the DDB CLI directly:

```bash
aws dynamodb delete-item \
  --table-name numun-main-prod \
  --key '{"pk":{"S":"USER#<userId>"},"sk":{"S":"USER#<userId>"}}'
```

Also delete the matching Cognito identity so the email can be re-registered legitimately later:

```bash
aws cognito-idp admin-delete-user \
  --user-pool-id us-east-1_XXXXXXXXX \
  --username <cognito-sub>
```

Leave `AuthAuditEvent` and `EmailEvent` rows in place — they are append-only forensic records.

### 4.3 Restore normal posture

Once the attack has stopped (no new `signup_completed` events from the offending signature for 24h):

- Revert the `DefaultLimits()` change from §2.1; redeploy.
- Re-enable Cognito self-service sign-up (`AllowAdminCreateUserOnly=false`).
- Leave the CloudFront IP block in place for at least 30 days, then re-review.
- Keep abused-address SES suppressions in place permanently — there's no benefit to ever sending to them again.

---

## 5. Post-mortem

Within one week:

- **Document the attack signature** — IP/ASN, email-domain pattern, request rate, timing, what the attacker appeared to be trying to accomplish (mass dummy accounts vs. outbound-mail vector vs. enumeration).
- **Decide whether to keep Turnstile permanently.** Recommendation: yes. The friction for legitimate advisors is near-zero (managed mode is usually invisible), and the cost of a second incident is high. If kept, remove the "no CAPTCHA in v1" line from `AUTH.md` §4.6 and `SECURITY.md` §6.1, and update the escalation language in §6.1 to reflect that Turnstile is now baseline rather than escalation.
- **Update [`../SECURITY.md`](../SECURITY.md) §6.1** if the escalation path changes (e.g. if Turnstile alone proved insufficient and we needed to add WAF, fold that learning back into the doc).
- **File a follow-up** if investigation surfaced a gap — e.g. missing metric, missing alarm threshold, an `AuthAuditEvent` attribute that would have made grouping easier.
