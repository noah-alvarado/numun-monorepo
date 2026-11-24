# NUMUN Production Plan

What's left to stand up `prod` (`numun.org`) in its own AWS account. `test` (`test.numun.org`, account 034083889387) is fully working and is the reference for every step below. The same templates and workflows deploy both; only `infra/envs/<env>.yaml` and the account differ.

Runbooks referenced: [fresh-environment-deploy.md](docs/runbooks/fresh-environment-deploy.md) (the authoritative order; `R*` steps below), [ses-domain-setup.md](docs/runbooks/ses-domain-setup.md), [dns-cutover.md](docs/runbooks/dns-cutover.md), [first-admin-bootstrap.md](docs/runbooks/first-admin-bootstrap.md), [operational-launch-checklist.md](docs/runbooks/operational-launch-checklist.md). [first-prod-deploy.md](docs/runbooks/first-prod-deploy.md) predates M14 (it still configures GitHub variables); prefer fresh-environment-deploy where they disagree.

## Gate

- [ ] SECURITY.md §3.2.1 fix 3 (CMS editors can't push code to `main`) is done. Every `main` merge deploys to every environment, so this has to land before prod exists.

## 1. Account and access

- [ ] Create the prod AWS account; enable MFA on root and lock it away.
- [ ] Create the break-glass IAM user with MFA ([breakglass-access.md](docs/runbooks/breakglass-access.md)) and an AWS CLI profile for it.
- [ ] Fill in `infra/envs/prod.yaml`: `AWS_ACCOUNT_ID`, `ALARM_EMAIL`, `SAM_ARTIFACTS_BUCKET`. `HOSTED_ZONE_ID` and the cert ARNs are filled in during §2.

## 2. DNS and certificates

`numun.org` is still on GoDaddy nameservers (`ns07/ns08.domaincontrol.com`). `test.numun.org` works because GoDaddy delegates just that subdomain to Route 53.

- [ ] Create the `numun.org` Route 53 hosted zone in the **prod** account (P2). The existing `numun.org` zone (Z04355982VH1XA9J992B0) lives in the test account; decide whether to move it or recreate it.
- [ ] Mirror every GoDaddy record first, especially the **Zoho MX/SPF/DKIM** records (`registration@`, `info@`, … depend on them) and the `test` NS delegation ([dns-cutover.md](docs/runbooks/dns-cutover.md) §2).
- [ ] Flip the nameservers at GoDaddy, then wait for propagation and verify.
- [ ] Request the ACM certs: us-east-1 for CloudFront (`numun.org`, `*.numun.org`) and us-east-2 for `api.numun.org` (P1). Record the ARNs in `prod.yaml`.

## 3. GitHub

- [ ] Create the `prod` GitHub environment with a deployment branch policy limited to `main` (R1; SECURITY.md §3.2.1 fix 1).
- [ ] Decide how prod deploys are triggered. Push-triggered runs default to `test` (`inputs.environment || 'test'`); prod is reachable only via `workflow_dispatch -f environment=prod`. Options: keep prod manual-dispatch only, or deploy both on merge with a required reviewer on the `prod` environment.

## 4. Stacks (break-glass, then CI)

- [ ] R3: bootstrap stack (`numun-prod-oidc-roles`), which includes the Lambda permissions boundary (fix 2).
- [ ] R5: base-data with `PostConfirmationTrigger=disabled` (the api stack doesn't exist yet).
- [ ] R7: base-cdn. R9: billing-alarm (us-east-1, `$10/mo`).
- [ ] R10: api stack via CI.
- [ ] R11: redeploy base-data with the trigger enabled.

## 5. Email

- [ ] [ses-domain-setup.md](docs/runbooks/ses-domain-setup.md) §1–3: verify `mail.numun.org` (DKIM, SPF, DMARC) in the prod account.
- [ ] §5: request SES production access (same use-case text as test). Expect about 24h.
- [ ] Once approved: set `COGNITO_EMAIL_SENDER: ses` in `prod.yaml` and redeploy base-data (§4a). Until then Cognito sends from `no-reply@verificationemail.com`, capped at 50/day.
- [ ] Create `/numun/prod/email/unsubscribe_secret` (SecureString, 32 random bytes).
- [ ] Verify end to end: approval email, password reset from `NUMUN Portal <accounts@mail.numun.org>`, and a bounce-simulator send that writes a `bounce_received` EmailEvent.
- [ ] DMARC progression: `p=quarantine; pct=10` → `pct=100` → `p=reject` over a few weeks of clean reports (ses-domain-setup.md, "DMARC progression").

## 6. Secrets and integrations

- [ ] `/numun/prod/cms_oauth/*`: GitHub OAuth app for Decap ([cms-oauth-app-setup.md](docs/cms-oauth-app-setup.md)).
- [ ] `/numun/prod/github_app/*`: CMS award write-back ([github-app-setup.md](docs/runbooks/github-app-setup.md)).
- [ ] `/numun/prod/sentry/dsn` ([sentry-setup.md](docs/runbooks/sentry-setup.md)).

## 7. Data and first users

- [ ] R12: bootstrap the first staff-admin ([first-admin-bootstrap.md](docs/runbooks/first-admin-bootstrap.md)).
- [ ] Create the active conference (status `open-for-registration`) so the site and portal have something to show.

## 8. Launch checks

- [ ] `scripts/verify-deploy.sh` against prod.
- [ ] Work through [operational-launch-checklist.md](docs/runbooks/operational-launch-checklist.md) (alarms, Sentry smoke, backups/PITR).
- [ ] Confirm the billing alarm subscription email was accepted.
