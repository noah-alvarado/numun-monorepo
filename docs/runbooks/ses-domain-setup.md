# Runbook — SES domain identity setup

One-time procedure. Verifies `mail.numun.org` as a sending domain, creates three purpose-scoped sender identities, publishes SPF/DKIM/DMARC, and submits the SES sandbox-exit request. Region: `us-east-2`.

**Per environment.** The sending domain is `mail.<apex>`: `mail.numun.org` for prod, `mail.test.numun.org` for `test` (derived from `EnvSubdomain` in `infra/api/template.yaml`). Run this procedure once per AWS account, substituting that env's mail domain below and publishing records in that env's hosted zone.

References: [EMAIL.md](../subsystems/EMAIL.md) §2 and §13, [SECURITY.md](../SECURITY.md) §3.7.

## Prerequisites

- Route 53 hosted zone for `numun.org` exists and is the authoritative DNS (see `dns-cutover.md`).
- AWS console access (break-glass IAM user is fine).

## Procedure

### 1. Verify the sending domain `mail.numun.org`

```bash
aws ses verify-domain-identity --domain mail.numun.org --region us-east-2
aws ses verify-domain-dkim     --domain mail.numun.org --region us-east-2
```

The DKIM verify returns three CNAME tokens. Capture them.

### 2. Publish DNS records in Route 53

For each of the three DKIM CNAMEs returned:

```
Name:  <token>._domainkey.mail.numun.org
Type:  CNAME
Value: <token>.dkim.amazonses.com
TTL:   300
```

For SPF (TXT on `mail.numun.org`):

```
v=spf1 include:amazonses.com ~all
```

For DMARC (TXT on `_dmarc.mail.numun.org`). **Start at `pct=10`** — the progressive tightening is intentional and tracked in M12:

```
v=DMARC1; p=quarantine; pct=10;
  rua=mailto:dmarc-reports@numun.org;
  ruf=mailto:dmarc-forensic@numun.org;
  adkim=s; aspf=s
```

(The `rua`/`ruf` aggregation addresses can be a shared inbox; aggregate reports help calibrate the percentage tightening.)

No MX record is needed on `mail.numun.org` — bounces are routed via SNS, not SMTP.

### 3. Wait for verification

```bash
aws ses get-identity-verification-attributes \
  --identities mail.numun.org --region us-east-2
```

Status flips to `Success` once DNS propagates (usually under 10 minutes).

### 4. Create the three sender identities

```bash
for addr in noreply@mail.numun.org announcements@mail.numun.org cognito@mail.numun.org; do
  aws ses verify-email-identity --email-address "$addr" --region us-east-2
done
```

Because `mail.numun.org` is a verified domain, these inherit verification automatically — no per-address email click-through. Confirm:

```bash
aws ses list-identities --region us-east-2
```

### 4a. Switch Cognito mail to SES (after §5 is approved)

By default (`COGNITO_EMAIL_SENDER: cognito` in `infra/envs/<env>.yaml`)
Cognito sends verification/reset mail from `no-reply@verificationemail.com`,
capped at 50/day. A custom sender under that mode would need a
separately-verified `cognito@` address identity, which can't receive the
confirmation link, so we don't use it.

Once the account has SES production access:

1. Redeploy the bootstrap stack if it predates the deploy-infra
   `iam:CreateServiceLinkedRole` grant for `email.cognito-idp.amazonaws.com`.
2. Set `COGNITO_EMAIL_SENDER: ses` in the env YAML and redeploy base-data.
   The pool switches to `DEVELOPER` mode: `From: NUMUN <cognito@mail.<apex>>`
   via the verified domain identity, using the `numun-<env>-email`
   configuration set so bounces reach the feedback Lambda.

In the sandbox, `ses` mode can only reach verified recipients, so sign-ups
from anyone else would never get their code.

### 5. Submit the SES sandbox-exit request

New SES accounts can only send to verified recipients. **Submit the production-access request now** so it's approved by the time M2 needs it for real signups.

AWS console → SES → Account dashboard → "Request production access". Submit with:

- Use case: "Transactional and announcement email for the Northwestern University Model United Nations conference portal. Volume estimate: ~5,000 emails/year."
- Recipient list: opted-in advisors and staff for the annual conference.
- Bounce/complaint handling: SNS-fed Lambda updates `User.emailStatus` and suppresses future sends; admin email-health surface for unsuppression (M9).

Approval typically takes 24–48h.

### 6. Verify deliverability end-to-end

After SES is out of sandbox:

```bash
aws ses send-email \
  --from noreply@mail.numun.org \
  --destination ToAddresses=YOUR_PERSONAL_EMAIL@example.com \
  --message 'Subject={Data="NUMUN SES test",Charset=UTF-8},Body={Text={Data="Deliverability test",Charset=UTF-8}}' \
  --region us-east-2
```

Then check headers in your inbox:

- `Authentication-Results: ... dkim=pass`
- `Authentication-Results: ... spf=pass`
- `Authentication-Results: ... dmarc=pass`

## DMARC progression (tracked through M12)

| Stage      | When               | Record                  |
| ---------- | ------------------ | ----------------------- |
| Initial    | M1                 | `p=quarantine; pct=10`  |
| Tightening | 2–4 weeks after M9 | `p=quarantine; pct=100` |
| Final      | M12                | `p=reject; pct=100`     |

Each tightening is a TXT update on `_dmarc.mail.numun.org`.

## Cost notes

- SES free tier: 62,000 emails/month sent from Lambda. NUMUN's volume (~5,000/year) is comfortably under.
- DKIM CNAMEs and SPF/DMARC TXT records: no marginal cost beyond the Route 53 hosted zone.
