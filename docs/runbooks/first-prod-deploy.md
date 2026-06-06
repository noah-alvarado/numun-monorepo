# Runbook — First production deploy (M1)

End-to-end one-time orchestration. Brings the entire NUMUN prod infrastructure online for the first time. Subsequent deploys flow through GitHub Actions OIDC; this runbook is only for the initial bootstrap.

References: [IMPLEMENTATION_PLAN.md](../../IMPLEMENTATION_PLAN.md) M1, [INFRASTRUCTURE.md](../INFRASTRUCTURE.md), [SECURITY.md](../SECURITY.md) §3.2.

## Prerequisites

- AWS account, owner-level access (root) to confirm the prerequisites below.
- Domain `numun.org` registered at GoDaddy.
- `numun-break-glass` IAM user created and MFA-enrolled (see `breakglass-access.md`).
- Repo pushed to GitHub at `<org>/<repo>` and protected `main` branch.
- Local tools: AWS CLI, SAM CLI, `make`, Go 1.22+ (per `.nvmrc` / `go.mod`).

## Order of operations

These steps must run in order. Each is gated on the previous.

```
1. DNS cutover  ──►  2. ACM certs  ──►  3. OIDC bootstrap stack
                                              │
                                              ▼
                                4. Configure GitHub repo variables
                                              │
                                              ▼
                                5. Base SAM stack
                                              │
                          ┌───────────────────┼───────────────────┐
                          ▼                   ▼                   ▼
                  6. Billing alarm     7. SES domain      8. API SAM stack
                     (us-east-1)            setup           (via GH Actions)
                                                                │
                                                                ▼
                                                       9. Smoke check
```

## Detailed steps

### 1. DNS cutover

Run [`dns-cutover.md`](dns-cutover.md). Capture the hosted-zone id; you'll set it as a GitHub repo variable in step 4.

### 2. ACM certificates (manual; two regions)

CloudFront requires a cert in `us-east-1`; the API Gateway custom domain requires a cert in `us-east-2`. Both are DNS-validated against the Route 53 zone created in step 1.

**us-east-1 wildcard cert** (used by all four CloudFront distributions):

```bash
aws acm request-certificate \
  --region us-east-1 \
  --domain-name numun.org \
  --subject-alternative-names "*.numun.org" \
  --validation-method DNS \
  --idempotency-token numun-cf-cert
```

**us-east-2 cert for the API custom domain**:

```bash
aws acm request-certificate \
  --region us-east-2 \
  --domain-name api.numun.org \
  --validation-method DNS \
  --idempotency-token numun-api-cert
```

For each, fetch the DNS-validation records and create them as Route 53 CNAMEs:

```bash
aws acm describe-certificate \
  --region us-east-1 \
  --certificate-arn <ARN> \
  --query "Certificate.DomainValidationOptions[].ResourceRecord"
```

ACM flips to `ISSUED` once the validation CNAMEs propagate (5–30 minutes). Capture both cert ARNs for step 5.

### 3. OIDC bootstrap stack

Authenticate as `numun-break-glass` (12-hour STS session), then:

```bash
aws cloudformation deploy \
  --region us-east-2 \
  --stack-name numun-prod-oidc-roles \
  --template-file infra/bootstrap/oidc-roles.yaml \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides \
    GitHubOrg=<your-github-org> \
    GitHubRepo=<your-repo-name> \
    GitHubBranch=main
```

Capture the outputs:

```bash
aws cloudformation describe-stacks \
  --region us-east-2 \
  --stack-name numun-prod-oidc-roles \
  --query 'Stacks[0].Outputs'
```

You'll use the four role ARNs + the SAM artifacts bucket name in step 4.

### 4. Configure GitHub repository variables

GitHub → repo → Settings → Secrets and variables → Actions → Variables tab. Add **repository variables** (not secrets — these are non-sensitive ARNs and ids):

| Variable | Source |
|---|---|
| `DEPLOY_ROLE_API_ARN` | Output `DeployRoleApiArn` from step 3 |
| `DEPLOY_ROLE_SITE_ARN` | Output `DeployRoleSiteArn` from step 3 |
| `DEPLOY_ROLE_PORTAL_ARN` | Output `DeployRolePortalArn` from step 3 |
| `DEPLOY_ROLE_CMS_ARN` | Output `DeployRoleCmsArn` from step 3 |
| `SAM_ARTIFACTS_BUCKET` | Output `SamArtifactsBucketName` from step 3 |
| `HOSTED_ZONE_ID` | Hosted-zone id from step 1 |
| `SITE_DISTRIBUTION_ID` | Filled after step 5 |
| `PORTAL_DISTRIBUTION_ID` | Filled after step 5 |
| `CMS_DISTRIBUTION_ID` | Filled after step 5 |

The four distribution-id variables stay empty until the base stack is deployed in step 5.

### 5. Base SAM stack

Still authenticated as `numun-break-glass`:

```bash
aws cloudformation deploy \
  --region us-east-2 \
  --stack-name numun-prod-base \
  --template-file infra/base/template.yaml \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides \
    HostedZoneId=<id-from-step-1> \
    CloudFrontCertificateArn=<us-east-1-cert-arn-from-step-2> \
    ApiCertificateArn=<us-east-2-cert-arn-from-step-2> \
    AlarmEmail=<your-ops-email>
```

CloudFront distributions take 5–10 minutes to fully provision. Confirm the SNS topic subscription email (AWS sends a one-time "confirm subscription" email).

Capture the four distribution ids and fill them into the GitHub repo variables from step 4.

### 6. Billing alarm (us-east-1)

```bash
aws cloudformation deploy \
  --region us-east-1 \
  --stack-name numun-prod-billing-alarm \
  --template-file infra/billing-alarm/us-east-1.yaml \
  --parameter-overrides \
    AlarmEmail=<your-ops-email> \
    ThresholdUsd=10
```

Confirm the SNS subscription email.

### 7. SES domain setup

Run [`ses-domain-setup.md`](ses-domain-setup.md). The sandbox-exit request can sit while you continue with step 8; it doesn't block the first health-endpoint deploy.

### 8. API SAM stack (via GitHub Actions)

Now that the deploy role and variables exist, the api workflow can take it from here.

- Push to `main` (any change touching `/api/**` or `/infra/api/**`).
- Or manually trigger: GitHub → Actions → "deploy api" → Run workflow.

The workflow `sam build`s the Go binary, `sam deploy`s the api stack, then smoke-checks `https://api.numun.org/v1/health` until it returns 200.

### 9. Final smoke check

```bash
curl -s https://api.numun.org/v1/health
# expected: {"status":"ok","commit":"<sha>","version":"<run-number>","time":"..."}
```

Confirm CloudFront distributions for the site, portal, cms, and assets domains resolve. They'll return 403 until something is uploaded (the site, portal, and cms workflows do that on first push).

## Cleanup if a step fails

- ACM cert in PENDING_VALIDATION → DNS records missing or wrong. Delete and recreate.
- Base stack in UPDATE_ROLLBACK_FAILED → see [SECURITY.md](../SECURITY.md) §6.4 procedure; usually `continue-update-rollback` and re-attempt.
- Api stack 504 / 5xx → check CloudWatch Logs for `/aws/lambda/numun-prod-api`. Common first-deploy issues: cert ARN region mismatch, custom-domain DNS propagation lag, missing `bootstrap` binary in the package.

## When to re-run this runbook

Never, ideally. Subsequent deploys flow through GitHub Actions. If the AWS account is ever destroyed and recreated, run end-to-end again.
