# GitHub App setup — `numun-cms-bot`

The Lambdalith writes back to the CMS git tree on every `AwardService`
mutation (M11). Authentication uses a GitHub App with `contents: write`
on the monorepo. This runbook captures the one-time setup performed by an
admin before the first prod deploy of M11 (and again per fresh env).

## Prerequisites

- Org-level admin access to the GitHub org that owns the monorepo.
- AWS CLI authenticated to the target account.
- The env name (`prod`, `test`, …) you're configuring.

## Steps

### 1. Register the GitHub App

1. GitHub → **Settings → Developer settings → GitHub Apps → New GitHub App**.
2. Name: `numun-cms-bot` (or `numun-cms-bot-test` for non-prod envs).
3. Homepage URL: any (e.g., `https://numun.org`).
4. **Webhook**: uncheck "Active" — we don't use webhooks.
5. **Repository permissions → Contents: Read & write**.
6. **Repository permissions → Metadata: Read-only** (default).
7. **Where can this GitHub App be installed?** → Only on this account.
8. Create the app and record the **App ID** shown at the top of the
   settings page (e.g., `123456`).

### 2. Generate a private key

1. On the App's settings page, scroll to **Private keys → Generate a
   private key**. Download the `.pem` file.
2. The file is the **only** copy of the private key — treat it like a
   secret. Do not commit, do not paste into Slack, etc.

### 3. Install the App on the monorepo

1. App settings → **Install App** → choose the org → "Only select
   repositories" → select `numun-monorepo`.
2. After installation, the URL bar shows
   `…/installations/<installation_id>`. Record that **Installation ID**.

### 4. Store the params in SSM

```bash
ENV=prod  # or test

aws ssm put-parameter \
  --name "/numun/${ENV}/github_app/app_id" \
  --type String --overwrite \
  --value "123456"

aws ssm put-parameter \
  --name "/numun/${ENV}/github_app/installation_id" \
  --type String --overwrite \
  --value "87654321"

aws ssm put-parameter \
  --name "/numun/${ENV}/github_app/repo" \
  --type String --overwrite \
  --value "numun/numun-monorepo"

# Optional. Omit to use "main".
aws ssm put-parameter \
  --name "/numun/${ENV}/github_app/branch" \
  --type String --overwrite \
  --value "main"

# Private key — SecureString. Paste the full PEM body, including the
# BEGIN/END lines.
aws ssm put-parameter \
  --name "/numun/${ENV}/github_app/private_key" \
  --type SecureString --overwrite \
  --value "$(cat /path/to/numun-cms-bot.<id>.private-key.pem)"
```

The Lambdalith IAM role already permits `ssm:GetParameter` on
`/numun/${ENV}/*` (see `infra/api/template.yaml`), so no IAM change is
needed.

### 5. Verify

After the next deploy, create an Award via the portal and confirm:

1. The portal response shows `cmsSync.ok = true` and a commit SHA.
2. `git pull` on the monorepo locally surfaces a new commit by
   `numun-cms-bot[bot]` adding `content/awards-archive/<awardId>.md`.
3. The site rebuild workflow runs and the `/awards` page picks up the
   new entry.

## Rotation

To rotate the private key:

1. App settings → **Generate a new private key**.
2. Update `/numun/${ENV}/github_app/private_key` via `aws ssm put-parameter`.
3. Trigger a new deploy or wait for the next cold start (the Lambdalith
   re-reads SSM at boot).
4. Delete the old key from the App settings page once you've confirmed
   sync still works.

## Tear down

When decommissioning the env:

1. Uninstall the App from the repo (App settings → Configure → Suspend
   or Uninstall).
2. Delete the SSM params (`aws ssm delete-parameters …`).
3. Delete the App registration itself when no env still uses it.

## Failure modes

| Symptom                                                | Likely cause                                                                                                             |
| ------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------ |
| `cmsSync.ok = false`, `final_error: "401 …"`           | Bad / rotated private key. Step 5 of "Rotation".                                                                         |
| `cmsSync.ok = false`, `final_error: "404 …"` on upsert | App not installed on the repo. Step 3.                                                                                   |
| `cmsSync.ok = false`, `final_error: "403 …"`           | App lacks `contents: write`. Re-check Step 1.5.                                                                          |
| `make dev` sync attempts fail                          | Expected without SSM credentials; the Lambdalith falls back to the stub client and reports `ok=true` with zero attempts. |
