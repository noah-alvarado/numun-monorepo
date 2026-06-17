# Runbooks

Operational procedures for running NUMUN. Each runbook is meant to be **followable cold** — a tired or unfamiliar operator should be able to execute it without rederiving context from design docs.

## Conventions

- **One file per procedure.** Filename matches the procedure (`account-takeover.md`, not `incident-response-1.md`).
- **Incident response** runbooks follow the SECURITY.md §7.1 template: **Detection signal → Immediate containment → Investigation → Remediation → Post-mortem.**
- **Setup / one-time procedures** use a numbered-steps format with explicit prerequisites and a verification check at the end.
- Bash commands use realistic placeholders (`$ENV`, `$POOL_ID`, `$USER_EMAIL`) rather than fake values.
- Cross-references use relative paths (`../SECURITY.md`, `./account-takeover.md`).

## Index

### Incident response

| Runbook | Trigger |
|---|---|
| [`account-takeover.md`](./account-takeover.md) | A user's credentials are compromised — unusual sign-ins, scope grants the user didn't request, real-user report. |
| [`data-breach-suspected.md`](./data-breach-suspected.md) | Suspected exfiltration or unauthorized data access; credential leak, S3 access anomaly, billing alarm on data-transfer egress. |
| [`ddos-or-api-abuse.md`](./ddos-or-api-abuse.md) | Sustained traffic spike from outside the conference-day pattern; rate-limit 429s above baseline; billing alarm; Lambda error-rate spike. |
| [`email-reputation-collapse.md`](./email-reputation-collapse.md) | SES Reputation Dashboard warning; bounce rate >5% or complaint rate >0.1%; DLQ depth alarm. |
| [`sign-up-abuse.md`](./sign-up-abuse.md) | Mass automated sign-ups; SES verification mail abused as a forwarding vector; spike in `User` row creation from one IP/ASN. |
| [`site-defacement.md`](./site-defacement.md) | Real-user report of unexpected content; unfamiliar GitHub commit; unscheduled CloudFront invalidation. |

### Setup / one-time

| Runbook | When |
|---|---|
| [`first-prod-deploy.md`](./first-prod-deploy.md) | Initial environment bootstrap — base stacks, OIDC roles, hosted zone, certs. |
| [`fresh-environment-deploy.md`](./fresh-environment-deploy.md) | Standing up a new environment (e.g., a real `prod` in a different AWS account). |
| [`dns-cutover.md`](./dns-cutover.md) | Pointing the registrar at Route 53 nameservers. |
| [`ses-domain-setup.md`](./ses-domain-setup.md) | Verifying `mail.numun.org`, publishing SPF/DKIM/DMARC, requesting SES sandbox exit. |
| [`github-app-setup.md`](./github-app-setup.md) | Registering the `numun-cms-bot` GitHub App for AwardService → CMS sync (M11). |
| [`first-admin-bootstrap.md`](./first-admin-bootstrap.md) | One-time creation of the first `staff-admin` Cognito user. |
| [`sentry-setup.md`](./sentry-setup.md) | Creating Sentry projects + populating `SENTRY_DSN` env-scoped secrets (M12). |

### Privileged access

| Runbook | When |
|---|---|
| [`breakglass-access.md`](./breakglass-access.md) | Emergency AWS console access outside the OIDC-federated CI flow. |
| [`mfa-enrollment.md`](./mfa-enrollment.md) | Staff self-enroll TOTP via the AWS CLI until the portal MFA UI ships in v1.1. |

### Operational walks

| Runbook | When |
|---|---|
| [`operational-launch-checklist.md`](./operational-launch-checklist.md) | DMARC tightening progression, HSTS preload submission, SES sandbox-exit verification, DDB PITR restore drill. |

## Authoring conventions

- Begin a runbook with a short paragraph stating what the procedure is and when it applies — no preamble about why runbooks exist generally.
- Keep procedures testable: when feasible, end with a one-line verification command whose output proves success.
- Cross-link to the design doc that authoritatively defines the system behavior; don't restate it.
- Update the runbook after each use. If a step was unclear or the actual procedure diverged from what's written, fix the runbook before closing the incident.
