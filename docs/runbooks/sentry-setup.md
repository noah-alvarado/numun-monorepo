# Sentry setup

One-time procedure to wire Sentry into the API + portal. Until the GitHub
environment secrets are populated, deploys succeed but Sentry init no-ops
(both `observability.InitFromEnv` and `initSentry()` return false when the
DSN env var is empty). Errors flow to CloudWatch Logs as usual; only the
Sentry-specific UX (grouped issues, deploy hashes, breadcrumbs) is absent.

## 1. Create the Sentry project

1. Sign up at https://sentry.io (free tier covers 5 k events / mo, well
   beyond our expected volume).
2. Create an organization (e.g., `numun`).
3. Create two projects:
   - `numun-api` — platform: Go.
   - `numun-portal` — platform: JavaScript → Browser.
4. For each project, copy the DSN from **Settings → Client Keys (DSN)**.
   They look like `https://<key>@oXXX.ingest.sentry.io/<projectId>`.

The DSN is technically a non-secret (it only authorizes ingestion, not read
access), but treat it as scoped config to avoid public abuse.

## 2. Populate GitHub environment secrets

The two deploy workflows (`api.yml`, `portal.yml`) read `secrets.SENTRY_DSN`
under their `environment:` block (`test` or `prod`).

For each of `test` and `prod`:

1. Repository **Settings → Environments → `<env>` → Add secret**.
2. Name: `SENTRY_DSN`. Value: the API DSN. The portal deploy reads the same
   secret name but you may want **separate** DSNs (one per Sentry project)
   to keep errors disjoint:
   - If you keep one secret, create a single Sentry project that both
     surfaces post to. Workable, but issue lists mix API and browser
     errors.
   - If you want two, add a second secret (e.g., `SENTRY_DSN_PORTAL`) and
     update the portal workflow's `VITE_SENTRY_DSN: ${{ secrets.SENTRY_DSN_PORTAL }}`.

The current default uses one secret name for both. Pick the two-project
shape if you have more than ~50 errors/mo from either surface.

## 3. Verify

After the next deploy:

1. **API** — `curl https://api.<apex>/v1/health` (should still 200). To
   trigger a real error, run any RPC that intentionally fails (e.g., POST
   `/numun.v1.DelegationService/GetDelegation` with a malformed body). The
   error should appear in Sentry within a minute, tagged
   `component=api`, `environment=<env>`, with the deploy's commit SHA as
   the `release`.
2. **Portal** — open `https://portal.<apex>` with browser devtools. In the
   console, run `throw new Error("sentry-smoketest")`. Reload — the error
   should land in Sentry tagged `environment=<env>`.

If errors don't show up, check:

- The Lambda log for the request — `observability: init` lines indicate
  whether Sentry was active.
- The `dist/assets/index-*.js` bundle for the literal DSN string. If
  absent, Vite tree-shook Sentry out because the env var was empty at
  build time.

## 4. What the BeforeSend hook scrubs

Both runtimes share an allowlist with the slog redaction wrapper
(`api/internal/log.RedactedFields`):

`password`, `refresh_token`, `access_token`, `id_token`, `client_secret`,
`csrf_token`, `x-csrf-token`, `authorization`, `cookie`, `set-cookie`,
`session_cookie` (matched case-insensitively, with dash/underscore
collapsed).

If a new sensitive field is introduced, add it to **both**:

- `api/internal/log/redact.go` → `RedactedFields`
- `portal/src/lib/sentry.ts` → `REDACTED_KEYS`

Tests in `api/internal/log/redact_test.go` and
`api/internal/observability/sentry_test.go` guard against accidental
shrinkage of the set.

## 5. Cost notes

- Free tier: 5 k errors / mo, 50 k performance units. Tracing is disabled
  (`SENTRY_TRACES_SAMPLE_RATE=0`) — only errors are sent.
- If we exceed the free tier, the next step is Sentry's $26/mo Team plan
  (50 k events). At our expected scale that's unnecessary; the cost
  ceiling in SECURITY.md / INFRASTRUCTURE.md does not budget for it.
- Sentry's data is stored in their cloud; no PII should ever reach it
  thanks to the BeforeSend scrubber. If a Sentry event ever shows a
  suspected leak, follow `data-breach-suspected.md` per the standard
  procedure.
