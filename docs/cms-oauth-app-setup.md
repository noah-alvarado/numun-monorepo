# CMS OAuth app setup

One-time setup steps to wire Decap CMS authentication to GitHub for a given NUMUN environment (`test` for staging, `prod` for production). Performed manually per environment.

Background: Decap CMS uses GitHub OAuth so editors commit content as themselves. GitHub forbids the OAuth code-for-token exchange from a browser, so we run a small proxy on the Lambdalith — see [CMS_CONTENT_MODEL.md §8.3](./subsystems/CMS_CONTENT_MODEL.md#83-oauth-proxy-required-infrastructure).

---

## 1. Pick the environment apex

| Env | Apex domain | CMS host | API host |
|---|---|---|---|
| test (staging) | `test.numun.org` | `cms.test.numun.org` | `api.test.numun.org` |
| prod | `numun.org` | `cms.numun.org` | `api.numun.org` |

The rest of this guide uses `${apex}` for the apex and `${env}` for the SSM env qualifier (`test` or `prod`).

---

## 2. Register the GitHub OAuth App

1. Sign in to GitHub as a NUMUN-org owner (or as the personal account that owns the content repo if there's no org).
2. **For org-owned setups:** go to **Org Settings → Developer settings → OAuth Apps → New OAuth App**.
   **For personal-account setups:** go to **Settings → Developer settings → OAuth Apps → New OAuth App**.
3. Fill in:
   - **Application name:** `NUMUN CMS (${env})` — e.g., `NUMUN CMS (test)`.
   - **Homepage URL:** `https://cms.${apex}` — e.g., `https://cms.test.numun.org`.
   - **Authorization callback URL:** `https://api.${apex}/cms-oauth/callback` — e.g., `https://api.test.numun.org/cms-oauth/callback`.
   - **Application description:** (optional) `OAuth proxy for the NUMUN content editor.`
4. Click **Register application**.
5. On the next page, **note the Client ID** (visible).
6. Click **Generate a new client secret** and **copy it immediately** — GitHub only shows it once.
7. Requested scope is configured by the proxy itself, not the OAuth App registration. The proxy requests `repo` per spec.

If you ever rotate the client secret, repeat steps 6 and replace the SSM parameter (§3).

---

## 3. Generate the HMAC state secret

The proxy HMAC-signs the OAuth `state` parameter with a server-side secret as CSRF protection. Generate a fresh high-entropy value per environment:

```bash
openssl rand -hex 32
```

Hold on to this output for step 4.

---

## 4. Store the three secrets in SSM

In the AWS account that runs the environment, store all three values as **SecureString** parameters under `/numun/${env}/cms_oauth/`. CloudFormation cannot author SecureStrings natively, so this is a manual one-time step per environment.

```bash
aws ssm put-parameter \
  --name "/numun/${env}/cms_oauth/client_id" \
  --type SecureString \
  --value "<CLIENT_ID_FROM_STEP_2>" \
  --region us-east-2

aws ssm put-parameter \
  --name "/numun/${env}/cms_oauth/client_secret" \
  --type SecureString \
  --value "<CLIENT_SECRET_FROM_STEP_2>" \
  --region us-east-2

aws ssm put-parameter \
  --name "/numun/${env}/cms_oauth/state_secret" \
  --type SecureString \
  --value "<HEX_FROM_STEP_3>" \
  --region us-east-2
```

The Lambda execution role already has `ssm:GetParameter` on `/numun/${env}/*` per the API stack template, so no IAM changes are needed.

To rotate: `aws ssm put-parameter --overwrite --name ... --value ...`. The Lambda re-reads on cold start; force a deploy or wait out the warm-instance lifetime.

---

## 5. Verify

1. Deploy (or re-deploy) the `api` stack so the Lambda picks up the new parameters on cold start.
2. Visit `https://cms.${apex}` in a browser.
3. Click **Login with GitHub**. A popup opens.
4. Authorize the OAuth app.
5. The popup closes and you land in the Decap editor.

If the popup hangs or shows an error, check:
- CloudWatch logs for the api Lambda — search for `cms-oauth`.
- The OAuth app's **Authorization callback URL** matches the env's API host exactly (no trailing slash).
- All three SSM parameters exist and the Lambda IAM role can read them.

---

## 6. Add editors

Editor onboarding (adding a GitHub user as a repo collaborator with Write permission) is covered in [cms-editor-onboarding.md](./cms-editor-onboarding.md).

---

## 7. Local development

For `make dev`, the proxy reads the three values from environment variables instead of SSM:

| SSM parameter | Local env var |
|---|---|
| `/numun/${env}/cms_oauth/client_id` | `CMS_OAUTH_CLIENT_ID` |
| `/numun/${env}/cms_oauth/client_secret` | `CMS_OAUTH_CLIENT_SECRET` |
| `/numun/${env}/cms_oauth/state_secret` | `CMS_OAUTH_STATE_SECRET` |

Local dev requires `DEV_MODE=true`. The state secret can be any non-empty string locally; for end-to-end testing of the popup flow you'll also need a separate "dev" GitHub OAuth App pointed at `http://localhost:3000/cms-oauth/callback` and `http://localhost:8080` (or wherever you serve the Decap bundle).

---

## Related

- [CMS_CONTENT_MODEL.md §8.3](./subsystems/CMS_CONTENT_MODEL.md#83-oauth-proxy-required-infrastructure) — proxy behavior spec.
- [SECURITY.md](./SECURITY.md) — `/cms-oauth/*` security posture (HMAC state, signed cookie, postMessage origin lock).
- [cms-editor-onboarding.md](./cms-editor-onboarding.md) — end-user guide for editors.
