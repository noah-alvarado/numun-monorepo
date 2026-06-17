# Runbook — Fresh-environment deploy

How to bring a new NUMUN environment up from zero in a fresh AWS account.
The same procedure is used for:

- **Staging (`Env=test`)** — what this document was written against during
  M2.7. Apex `test.numun.org`; subdomains `api.`, `portal.`, `cms.`,
  `assets.`. Lives in the personal account that currently hosts staging.
- **Production (`Env=prod`)** — future deploy into a new AWS account, with
  apex `numun.org` (no `EnvSubdomain`).

The procedure is also the **teardown + rebuild** runbook for the M2.7
in-account rename: do every Teardown step under `Env=prod` (current
state), then run the Rebuild under `Env=test`.

> **Org policy reminder.** Direct AWS console / CLI mutations against
> shared infrastructure must be performed by a human operator, not
> automated agents. This runbook captures the commands; you execute them.

## Inputs

Set these once before running any commands; they're referenced throughout.

| Variable        | Description                                          | Example (staging)                  |
| --------------- | ---------------------------------------------------- | ---------------------------------- |
| `ENV`           | Environment qualifier — value of `Env` SAM parameter | `test`                             |
| `ROOT_DOMAIN`   | Apex registered domain                               | `numun.org`                        |
| `ENV_SUBDOMAIN` | Subdomain between env and root (empty for prod)      | `test`                             |
| `APEX`          | Effective apex (composed)                            | `test.numun.org`                   |
| `AWS_PROFILE`   | Local CLI profile that reaches the target account    | `numun-prod` (for staging account) |
| `AWS_REGION`    | Primary region                                       | `us-east-2`                        |
| `ACCOUNT_ID`    | Target AWS account id                                | `034083889387`                     |
| `GH_REPO`       | GitHub repository slug                               | `noah-alvarado/numun-monorepo`     |
| `GH_ENV`        | GitHub environment name (= `$ENV`)                   | `test`                             |

---

## Pre-work (manual, out-of-band)

### P1. ACM certificates

Two certs needed. Use the same DNS-validation path that produced the M1
certs — `aws acm describe-certificate` against the existing cert ARNs
records what worked:

- **us-east-1** (CloudFront): SANs `${APEX}`, `*.${APEX}`.
- **us-east-2** (API Gateway custom domain): SAN `api.${APEX}`.

```bash
# Request the us-east-1 wildcard
aws acm request-certificate \
  --profile "$AWS_PROFILE" --region us-east-1 \
  --domain-name "$APEX" \
  --subject-alternative-names "*.$APEX" \
  --validation-method DNS

# Request the us-east-2 api cert
aws acm request-certificate \
  --profile "$AWS_PROFILE" --region us-east-2 \
  --domain-name "api.$APEX" \
  --validation-method DNS
```

Add the validation CNAMEs to the Route 53 hosted zone (or wherever DNS
is authoritative). Wait for `Status: ISSUED`.

Capture the two ARNs — they're inputs to the rebuild.

### P2. Route 53 hosted zone

The `numun.org` hosted zone already exists in the staging account. For a
fresh prod account, create it:

```bash
aws route53 create-hosted-zone \
  --profile "$AWS_PROFILE" \
  --name "$ROOT_DOMAIN" \
  --caller-reference "$(date +%s)"
```

Capture the zone id (looks like `Z04355982VH1XA9J992B0`).

### P3. Break-glass IAM user

Both the teardown and the bootstrap-stack deploy run from a human
operator's credentials, not from the CI OIDC roles (chicken-and-egg —
those roles get created by the bootstrap stack). The
`/docs/runbooks/breakglass-access.md` runbook captures setup.

---

## Teardown (skip in a truly fresh account)

If the target account already has a previous environment's resources,
remove them in dependency order. **All S3 buckets and the DDB table
carry `DeletionPolicy: Retain`**, so stack deletes do not drop them —
empty + delete manually before deleting the owning stack.

### T1. Detach Cognito triggers from the previous user pool

```bash
aws cognito-idp update-user-pool \
  --profile "$AWS_PROFILE" --region "$AWS_REGION" \
  --user-pool-id "$OLD_USER_POOL_ID" \
  --lambda-config '{}' \
  # ...preserve all other mutable fields (Policies, MfaConfiguration,
  # AutoVerifiedAttributes, AccountRecoverySetting,
  # UserAttributeUpdateSettings, AdminCreateUserConfig,
  # EmailConfiguration) per first-admin-bootstrap.md §1.
```

### T2. Delete the api stack

```bash
aws cloudformation delete-stack --stack-name "numun-${OLD_ENV}-api" \
  --profile "$AWS_PROFILE" --region "$AWS_REGION"
aws cloudformation wait stack-delete-complete \
  --stack-name "numun-${OLD_ENV}-api" \
  --profile "$AWS_PROFILE" --region "$AWS_REGION"
```

### T3. Delete the base-cdn stack

CloudFront distribution deletion is **slow** (10–15 min — distributions
must be disabled, propagated, then deleted).

```bash
aws cloudformation delete-stack --stack-name "numun-${OLD_ENV}-base-cdn" \
  --profile "$AWS_PROFILE" --region "$AWS_REGION"
aws cloudformation wait stack-delete-complete \
  --stack-name "numun-${OLD_ENV}-base-cdn" \
  --profile "$AWS_PROFILE" --region "$AWS_REGION"
```

### T4. Empty + delete the S3 buckets

```bash
for b in numun-${OLD_ENV}-{site,portal,cms,assets,uploads,artifacts} \
         numun-${OLD_ENV}-sam-deploys-${ACCOUNT_ID}; do
  aws s3 rm "s3://$b" --recursive --profile "$AWS_PROFILE" || true
  aws s3 rb "s3://$b" --profile "$AWS_PROFILE" || true
done
```

(The legacy staging account uses `numun-org-{site,portal,...}` for the
shared buckets — substitute accordingly during the M2.7 cutover.)

### T5. Delete the DDB table

```bash
aws dynamodb delete-table \
  --profile "$AWS_PROFILE" --region "$AWS_REGION" \
  --table-name "numun-${OLD_ENV}"
```

### T6. Delete the base-data stack

With S3 + DDB already gone, base-data drops only Cognito, SSM,
SNS, and alarms:

```bash
aws cloudformation delete-stack --stack-name "numun-${OLD_ENV}-base-data" \
  --profile "$AWS_PROFILE" --region "$AWS_REGION"
aws cloudformation wait stack-delete-complete \
  --stack-name "numun-${OLD_ENV}-base-data" \
  --profile "$AWS_PROFILE" --region "$AWS_REGION"
```

### T7. Delete the billing-alarm stack (us-east-1)

```bash
aws cloudformation delete-stack --stack-name "numun-${OLD_ENV}-billing-alarms" \
  --profile "$AWS_PROFILE" --region us-east-1
```

### T8. Delete the bootstrap stack

This is **last** — it removes the OIDC deploy roles. Until R3 below
re-creates them, no CI workflow can deploy anything.

```bash
aws cloudformation delete-stack --stack-name "numun-${OLD_ENV}-bootstrap" \
  --profile "$AWS_PROFILE" --region "$AWS_REGION"
```

### T9. Verify

```bash
aws cloudformation list-stacks --profile "$AWS_PROFILE" --region "$AWS_REGION" \
  --stack-status-filter CREATE_COMPLETE UPDATE_COMPLETE \
  --query "StackSummaries[?starts_with(StackName, 'numun-')].StackName"
```

Should show no stacks from the previous env.

---

## Rebuild

### R1. Create the GitHub environment

The environment exists for two reasons under the M14 pattern: it scopes
secrets (Sentry DSN was moved to SSM but anything that legitimately can't
live in SSM goes here), and it labels deploys for GitHub's Deployments
dashboard.

```bash
gh api -X PUT "/repos/${GH_REPO}/environments/${GH_ENV}"
gh api "/repos/${GH_REPO}/environments/${GH_ENV}"  # verify
```

No variables or secrets need to be set yet — the env YAML in R4 carries
everything, and Sentry DSN ships in SSM (see [sentry-setup.md](./sentry-setup.md)).

### R2. Land the IaC

The parameterized templates are on `main` from M2.7. For a future-prod
new-account deploy, ensure the templates haven't drifted since.

### R3. Deploy the bootstrap stack (under break-glass)

Creates the OIDC provider, four deploy roles, and the SAM artifacts
bucket. **One-time, runs from the break-glass IAM user.**

```bash
aws cloudformation deploy \
  --profile "$AWS_PROFILE" --region "$AWS_REGION" \
  --stack-name "numun-${ENV}-bootstrap" \
  --template-file infra/bootstrap/oidc-roles.yaml \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides \
    Env="$ENV" \
    GitHubOrg=noah-alvarado \
    GitHubRepo=numun-monorepo \
    GitHubEnvironment="$GH_ENV"

# Capture outputs
aws cloudformation describe-stacks \
  --profile "$AWS_PROFILE" --region "$AWS_REGION" \
  --stack-name "numun-${ENV}-bootstrap" \
  --query "Stacks[0].Outputs"
```

### R4. Author the env-config YAML

M14 moved per-env configuration out of GitHub Environment variables and
into the repo. Create `infra/envs/${ENV}.yaml` (mirror `infra/envs/test.yaml`
for the shape). All stable identifiers go here:

```yaml
ENV_NAME: ${ENV}
ENV_SUBDOMAIN: ${ENV_SUBDOMAIN} # empty for prod
ROOT_DOMAIN: ${ROOT_DOMAIN}
APEX_DOMAIN: ${APEX}
AWS_REGION: ${AWS_REGION}
AWS_ACCOUNT_ID: "${ACCOUNT_ID}"
ALARM_EMAIL: ops@example.com
SAM_ARTIFACTS_BUCKET: numun-${ENV}-sam-deploys-${ACCOUNT_ID}

HOSTED_ZONE_ID: ${HOSTED_ZONE_ID}
API_CERTIFICATE_ARN: <from P1: us-east-2 cert ARN>
CDN_CERTIFICATE_ARN: <from P1: us-east-1 cert ARN>
```

Commit and push. The deploy workflows read this file via
`scripts/load-env.sh` on every run.

Notes:

- **No GitHub variables** are needed under the M14 pattern. The
  deploy-role ARNs are derived from `AWS_ACCOUNT_ID` + the naming
  convention (`numun-${ENV}-deploy-<stack>`). Cognito IDs, distribution
  IDs, and bucket ARNs are read live via `aws cloudformation
describe-stacks` (see `scripts/load-stack-outputs.sh`).
- The env name itself (`test` / `prod`) is **not** stored — it's the
  GitHub Environment name and the workflows derive `ENV` from it directly.
- The repo path is **not** stored — the cms workflow substitutes
  `__GITHUB_REPO__` in `cms/config.yml` from the `${{ github.repository }}`
  workflow context.
- **Secrets** (Sentry DSN, etc.) go in SSM SecureString at
  `/numun/${ENV}/...` — see [sentry-setup.md](./sentry-setup.md) and the
  cms-oauth / email runbooks. **Do not** add secrets to the env YAML.

### R5. Deploy base-data (under break-glass)

There is no CI workflow for base-data (it's deployed rarely; the
deploy-api role doesn't carry permissions for Cognito/DDB/S3 creation).

```bash
aws cloudformation deploy \
  --profile "$AWS_PROFILE" --region "$AWS_REGION" \
  --stack-name "numun-${ENV}-base-data" \
  --template-file infra/base-data/template.yaml \
  --parameter-overrides \
    Env="$ENV" \
    RootDomain="$ROOT_DOMAIN" \
    EnvSubdomain="$ENV_SUBDOMAIN" \
    AlarmEmail=ops@example.com

# Capture Cognito + bucket exports
aws cloudformation describe-stacks \
  --profile "$AWS_PROFILE" --region "$AWS_REGION" \
  --stack-name "numun-${ENV}-base-data" \
  --query "Stacks[0].Outputs"
```

### R6. (removed in M14)

Cognito IDs no longer need to be mirrored into GitHub variables —
`scripts/load-stack-outputs.sh numun-${ENV}-base-data` reads them live
inside each deploy workflow.

### R7. Deploy base-cdn (under break-glass)

```bash
aws cloudformation deploy \
  --profile "$AWS_PROFILE" --region "$AWS_REGION" \
  --stack-name "numun-${ENV}-base-cdn" \
  --template-file infra/base-cdn/template.yaml \
  --parameter-overrides \
    Env="$ENV" \
    RootDomain="$ROOT_DOMAIN" \
    EnvSubdomain="$ENV_SUBDOMAIN" \
    HostedZoneId="$HOSTED_ZONE_ID" \
    CloudFrontCertificateArn="$CLOUDFRONT_CERT_ARN"

# Capture distribution ids
aws cloudformation describe-stacks \
  --profile "$AWS_PROFILE" --region "$AWS_REGION" \
  --stack-name "numun-${ENV}-base-cdn" \
  --query "Stacks[0].Outputs"
```

### R8. (removed in M14)

CloudFront distribution IDs no longer need to be mirrored into GitHub
variables — `scripts/load-stack-outputs.sh numun-${ENV}-base-cdn` reads
them live inside each deploy workflow.

### R9. Deploy the billing-alarm stack (us-east-1)

```bash
aws cloudformation deploy \
  --profile "$AWS_PROFILE" --region us-east-1 \
  --stack-name "numun-${ENV}-billing-alarms" \
  --template-file infra/billing-alarm/us-east-1.yaml \
  --parameter-overrides \
    Env="$ENV" \
    AlarmEmail=ops@example.com
```

### R10. Deploy the api stack via CI

Push to `main` (or trigger `workflow_dispatch` on `.github/workflows/api.yml`).
The deploy should succeed using the new OIDC role + the `infra/envs/${ENV}.yaml`
config + the base-data/base-cdn stack outputs read at deploy time.

Smoke-check the new endpoint (CI does this automatically, but
double-check):

```bash
ENDPOINT=$(aws cloudformation describe-stacks \
  --profile "$AWS_PROFILE" --region "$AWS_REGION" \
  --stack-name "numun-${ENV}-api" \
  --query "Stacks[0].Outputs[?OutputKey=='HttpApiEndpoint'].OutputValue | [0]" \
  --output text)
curl -s "$ENDPOINT/v1/health" | jq .
```

### R11. Wire the Cognito post-confirmation trigger

Same as `first-admin-bootstrap.md` §1, with the new pool id and function
ARN. Must preserve all other mutable pool config.

### R12. Bootstrap the first admin

Same as `first-admin-bootstrap.md` §2–§3, against the new pool. Capture
the new `sub`.

---

## Verification

V1. `aws cloudformation list-stacks` shows five stacks named
`numun-${ENV}-*` (bootstrap, base-data, base-cdn, billing-alarms, api),
all `CREATE_COMPLETE` or `UPDATE_COMPLETE`.

V2. `curl https://api.${APEX}/v1/health` returns 200 (or hit the API
Gateway invoke URL until DNS is live).

V3. `scripts/verify-deploy.sh` succeeds end-to-end with the new admin:

```bash
API_BASE_URL="$ENDPOINT" \
COGNITO_USER_POOL="$NEW_USER_POOL_ID" \
COGNITO_CLIENT_ID="$NEW_CLIENT_ID" \
AWS_PROFILE="$AWS_PROFILE" \
AWS_REGION="$AWS_REGION" \
ADMIN_EMAIL="$ADMIN_EMAIL" \
  scripts/verify-deploy.sh
```

V4. A no-op push to `main` triggers all four deploy workflows; each
completes cleanly against the new OIDC roles.

---

## DNS cutover (separate milestone)

This runbook stops at API-reachable-via-invoke-URL. Cutting GoDaddy
nameservers over to the Route 53 hosted zone happens in a later milestone
(`/docs/runbooks/dns-cutover.md`) and is the same procedure for staging
and prod — only the zone is different.

## Related

- `/docs/runbooks/first-admin-bootstrap.md`
- `/docs/runbooks/breakglass-access.md`
- `/docs/runbooks/dns-cutover.md`
- `scripts/verify-deploy.sh`
- `IMPLEMENTATION_PLAN.md` §M2.7
