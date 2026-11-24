# GitHub App setup — `numun-cms-bot`

The Lambdalith writes back to the CMS git tree on every `AwardService`
mutation (M11). Authentication uses a GitHub App with `contents: write`
on the monorepo. One App (`numun-cms-bot`, App ID `5911898`, installation
`185555463`) serves every env; each env gets its own private key and
branch:

| Env    | Branch         | Ruleset bypass needed     |
| ------ | -------------- | ------------------------- |
| `test` | `cms-bot/test` | no (rulesets target main) |
| `prod` | `main`         | yes, see step 5           |

Test writes to its own branch so test awards never reach the site's
`content/` on `main`.

## Prerequisites

- Admin on `noah-alvarado/numun-monorepo`.
- AWS CLI authenticated to the target account.
- The env name (`prod`, `test`, …) you're configuring.

## Steps

### 1. Register the GitHub App (once)

Skip if `numun-cms-bot` already exists (https://github.com/settings/apps/numun-cms-bot).

1. GitHub → **Settings → Developer settings → GitHub Apps → New GitHub App**.
2. Name: `numun-cms-bot`. Homepage URL: `https://numun.org`.
3. Leave "Identifying and authorizing users" and "Post installation"
   empty; the API authenticates as the App, never as a user.
4. **Webhook**: uncheck "Active" — we don't use webhooks.
5. **Repository permissions → Contents: Read & write**.
6. **Repository permissions → Metadata: Read-only** (default).
7. **Where can this GitHub App be installed?** → Only on this account.
8. Create the app and record the **App ID** shown at the top of the
   settings page. The App is private, so `gh api /apps/numun-cms-bot`
   returns 404; read the ID from the settings page.

### 2. Generate a private key (per env)

1. On the App's settings page, scroll to **Private keys → Generate a
   private key**. Download the `.pem` file.
2. The file is the **only** copy of the private key — treat it like a
   secret. Do not commit, do not paste into Slack, etc. `chmod 600` it,
   and delete it after step 4.

### 3. Install the App on the monorepo (once)

1. App settings → **Install App** → your account → "Only select
   repositories" → select `numun-monorepo`.
2. After installation, the URL bar shows
   `…/installations/<installation_id>`. Record that **Installation ID**.
3. For a non-prod env, create its branch from `main`:
   `gh api repos/noah-alvarado/numun-monorepo/git/refs -f ref=refs/heads/cms-bot/${ENV} -f sha="$(gh api repos/noah-alvarado/numun-monorepo/git/ref/heads/main --jq .object.sha)"`

### 4. Store the params in SSM

```bash
ENV=prod  # or test

aws ssm put-parameter \
  --name "/numun/${ENV}/github_app/app_id" \
  --type String --overwrite \
  --value "5911898"

aws ssm put-parameter \
  --name "/numun/${ENV}/github_app/installation_id" \
  --type String --overwrite \
  --value "185555463"

aws ssm put-parameter \
  --name "/numun/${ENV}/github_app/repo" \
  --type String --overwrite \
  --value "noah-alvarado/numun-monorepo"

# Optional; omit to use "main" (prod only). Non-prod: cms-bot/${ENV}.
aws ssm put-parameter \
  --name "/numun/${ENV}/github_app/branch" \
  --type String --overwrite \
  --value "cms-bot/${ENV}"

# Private key — SecureString, read from the file so it never hits argv.
aws ssm put-parameter \
  --name "/numun/${ENV}/github_app/private_key" \
  --type SecureString --overwrite \
  --value "file:///path/to/numun-cms-bot.<date>.private-key.pem"
```

The Lambdalith IAM role already permits `ssm:GetParameter` on
`/numun/${ENV}/*` (see `infra/api/template.yaml`), so no IAM change is
needed.

### 5. Let the App past the `main` rulesets (prod only)

`main` requires PRs and passing checks (rulesets `main: protections` and
`main: code owner review`), which would reject the App's direct commits.
Add the App as a bypass actor on both:

```bash
API=repos/noah-alvarado/numun-monorepo/rulesets
for id in $(gh api $API --jq '.[].id'); do
  gh api $API/$id --jq '{name,target,enforcement,conditions,rules,
    bypass_actors: (.bypass_actors + [{actor_id: 5911898, actor_type: "Integration", bypass_mode: "always"}] | unique)}' \
    | gh api -X PUT $API/$id --input - --jq '"\(.name): \(.bypass_actors|length) bypass actors"'
done
```

### 6. Verify

After the next deploy, create an Award via the portal and confirm:

1. The portal response shows `cmsSync.ok = true` and a commit SHA.
2. The env's branch has a new commit by `numun-cms-bot[bot]` adding
   `content/awards-archive/<awardId>.md`.
3. Prod only: the site rebuild workflow runs and the `/awards` page
   picks up the new entry.

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

| Symptom                                                                          | Likely cause                                                                                                             |
| -------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| `cmsSync.ok = false`, `final_error: "401 …"`                                     | Bad / rotated private key. Step 5 of "Rotation".                                                                         |
| `cmsSync.ok = false`, `final_error: "404 …"` on upsert                           | App not installed on the repo. Step 3.                                                                                   |
| `cmsSync.ok = false`, `final_error: "403 …"`                                     | App lacks `contents: write`. Re-check Step 1.5.                                                                          |
| `cmsSync.ok = false`, `final_error` mentions "Repository rule violations" (prod) | The App isn't a bypass actor on the `main` rulesets. Step 5.                                                             |
| `make dev` sync attempts fail                                                    | Expected without SSM credentials; the Lambdalith falls back to the stub client and reports `ok=true` with zero attempts. |
