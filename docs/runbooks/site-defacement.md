# Runbook — Site defacement

The public landing page at `numun.org` has been visibly altered without authorization (vandalism, replaced copy, injected scripts, swapped images). This runbook recovers the site and identifies the vector.

References: [SECURITY.md](../SECURITY.md) §3.1 + §6.4, [CMS_CONTENT_MODEL.md](../subsystems/CMS_CONTENT_MODEL.md) §3 and §8.3, [breakglass-access.md](./breakglass-access.md), [data-breach-suspected.md](./data-breach-suspected.md).

## Vectors

1. **Compromised CMS editor** — vandalism pushed via Decap, lands as a normal `/content/**` commit on `main` from an editor's GitHub identity.
2. **Compromised GitHub-write developer** — direct push to `main` (under the PR-required protection rule; this implies either bypass or a merged malicious PR).
3. **Compromised AWS deploy role** — write straight into `numun-${ENV}-site` S3, bypassing the build pipeline. Git history is clean.
4. **CloudFront / DNS hijack** — domain or distribution taken over upstream. Mostly out of NUMUN's control; see "Stretch case" below.

---

## 1. Detection signals

- **A real user reports it.** This is by far the most likely signal — v1 has **no automated content-checksum monitoring**; flagged in [SECURITY.md](../SECURITY.md) §10 as an open item.
- A GitHub commit notification with unexpected content, an unfamiliar committer email, or a commit timestamp outside editor working hours.
- A CloudFront invalidation in the AWS console that no one on the team triggered.
- A CloudTrail `PutObject` / `DeleteObject` event against `numun-${ENV}-site` from a principal other than the `deploy-site` OIDC role.

If you suspect data exfiltration in addition to defacement, also open [data-breach-suspected.md](./data-breach-suspected.md) in parallel.

---

## 2. Immediate containment

First decide the vector by checking git:

```bash
git fetch origin
git log --oneline -n 20 origin/main -- content/ site/
```

If the defacement is visible in `git log` → **vector 1 or 2**. If git is clean but the live site is altered → **vector 3**.

### 2a. Git history shows the defacement (vector 1 or 2)

Revert on `main`; the `site.yml` workflow auto-redeploys:

```bash
SHA=<bad-commit-sha>
git revert --no-edit $SHA
git push origin main
```

For a **range** of bad commits (mass defacement) revert without committing, inspect, then commit once:

```bash
git revert --no-commit ${FIRST_BAD_SHA}^..${LAST_BAD_SHA}
git status                                 # confirm the inverse diff is what you expect
git commit -m "revert: site defacement incident $(date -u +%FT%TZ)"
git push origin main
```

Watch the workflow:

```bash
gh run watch -R numun/numun-monorepo
```

### 2b. Git is clean — S3 was written directly (vector 3)

The site bucket is **versioned with 90-day retention** ([SECURITY.md](../SECURITY.md) §3.1). Roll the affected objects back to their prior version. The CLI pattern is the same as the rollback section in `DEVELOPERS.md` §8.3:

```bash
ENV=prod           # or test
BUCKET=numun-${ENV}-site
DIST_ID=<cloudfront-distribution-id>

# List versions for the defaced key(s); identify the last-known-good versionId.
aws s3api list-object-versions --bucket $BUCKET --prefix index.html

# Restore that version (copy-onto-self with the prior versionId creates a new current version).
aws s3api copy-object \
  --bucket $BUCKET \
  --copy-source "${BUCKET}/index.html?versionId=<good-version-id>" \
  --key index.html

# Invalidate so viewers re-fetch.
aws cloudfront create-invalidation --distribution-id $DIST_ID --paths '/*'
```

For mass S3 defacement spanning many keys, an `aws s3 sync` from a fresh local build of a known-good commit is faster than per-object copies — do this from a clean checkout, then invalidate `/*`.

While restoring, you can optionally **remove write access from the suspect role** to prevent re-defacement mid-restore (detach its inline policy in IAM, or set `s3:Put*` Deny on the bucket policy for that role's ARN).

### 2c. Stretch case — CloudFront / DNS hijack (vector 4)

If the distribution itself or the Route 53 hosted zone has been altered: this implies AWS account compromise. Switch to [breakglass-access.md](./breakglass-access.md) for emergency AWS console access, verify the distribution origin still points at `numun-${ENV}-site`, verify Route 53 `A`/`AAAA` records still alias to the right distribution, and escalate per [SECURITY.md](../SECURITY.md) §6.5.

---

## 3. Investigation

Identify who, when, and how.

```bash
# Recent commits on main + their authors.
git log --since="48 hours ago" --pretty=format:'%h %ae %s' origin/main

# GitHub audit log (org-scoped). Filter by actor or action.
gh api -X GET /orgs/<org>/audit-log -f phrase='action:git.push created:>=2026-06-15'

# CloudTrail S3 writes to the site bucket (last 24h).
aws cloudtrail lookup-events \
  --lookup-attributes AttributeKey=ResourceName,AttributeValue=numun-${ENV}-site \
  --start-time $(date -u -v-1d +%FT%TZ)
```

- **Vector 1 (CMS editor):** the commit `Author` email maps to a GitHub collaborator. The Decap session is a GitHub OAuth token — revoking the user's repo access cuts off Decap within minutes ([CMS_CONTENT_MODEL.md](../subsystems/CMS_CONTENT_MODEL.md) §8.3).

  ```bash
  gh api -X DELETE repos/<org>/numun-monorepo/collaborators/<user>
  ```

- **Vector 2 (developer compromise):** same revocation as above. Additionally, rotate the GitHub OAuth App client secret that backs the `/cms-oauth/*` proxy so every existing Decap session is forced to re-authenticate:

  ```bash
  # Generate a new secret in GitHub's OAuth App settings, then:
  aws ssm put-parameter \
    --name /numun/${ENV}/cms_oauth/client_secret \
    --type SecureString --overwrite \
    --value '<new-secret>'
  ```

  The API reads this on cold start; bounce the Lambda or wait for the next deploy.

- **Vector 3 (S3 direct):** determine which IAM principal wrote the bad objects.

  ```bash
  aws cloudtrail lookup-events \
    --lookup-attributes AttributeKey=EventName,AttributeValue=PutObject \
    --start-time $(date -u -v-1d +%FT%TZ) \
    | jq '.Events[] | {time:.EventTime, user:.Username, src:.CloudTrailEvent}'
  ```

  If the actor is the `deploy-site` OIDC role from an unexpected source IP or workflow run, treat the GitHub repo as compromised too (vector 2 + 3). If it's a human IAM user, revoke its keys and follow [breakglass-access.md](./breakglass-access.md) for cleanup.

In all cases, decide whether the actor is **still in possession of credentials.** If yes, the containment in §2 is not enough — escalate to the AWS-account-compromise path in [SECURITY.md](../SECURITY.md) §6.5.

---

## 4. Remediation

1. **Re-secure the vector.** Revoke compromised access (above). Confirm org-level 2FA is still enforced on the repo. Re-read the branch-protection rules and confirm the `/content/**` exception has the right narrow allowlist ([SECURITY.md](../SECURITY.md) §6.4 — accepted risk, mitigated by audit log).
2. **Verify bucket state matches git.** Trigger the site workflow manually so a fresh build overwrites every object from the trusted git source:

   ```bash
   gh workflow run site.yml -R numun/numun-monorepo --ref main
   gh run watch -R numun/numun-monorepo
   ```

3. **Re-enable normal traffic.** No special step — CloudFront resumes serving the corrected version once the invalidation propagates (typically a few minutes). Spot-check `curl -sI https://numun.org/` and a few interior pages.
4. **Audit the entire `/content/**` tree\*\* for subtler vandalism that may have piggy-backed on the obvious change (a stealth link edit, a swapped PDF). Enumerate every touch since the incident window:

   ```bash
   git log --since="2026-06-15" --name-only --pretty=format:'%h %ae %s' -- content/
   ```

   Diff anything from an unexpected author against the prior version.

---

## 5. Post-mortem

- Document the vector, the dwell time (defacement-push time → user-report time → containment time), and the actor.
- File the incident in the team incident log; link the offending commits and CloudTrail event IDs.
- **Open items to consider after the third incident or the first serious one:**
  - Automated content monitoring — an hourly checksum poll of a stable page, alarming on diff. Currently absent ([SECURITY.md](../SECURITY.md) §10).
  - A GitHub Action on `/content/**` pushes that posts a PR-style notification of every Decap-originated commit to the staff distribution list, tightening the §6.4 exception without breaking Decap's direct-to-main requirement.
  - Quarterly review of repo collaborators (already on the calendar per [SECURITY.md](../SECURITY.md) §6.4) — confirm the review actually happened the quarter before the incident.
