# Sentry setup

One-time procedure to wire Sentry into the API + portal. Until the DSN is
stored in SSM (per §2 below), deploys succeed but Sentry init no-ops (both
`observability.InitFromEnv` and `initSentry()` return false when no DSN
resolves). Errors flow to CloudWatch Logs as usual; only the Sentry-
specific UX (grouped issues, deploy hashes, breadcrumbs) is absent.

## 1. Create the Sentry projects

Done for org `numun` (https://numun.sentry.io). Test and prod share both
projects; events are separated by the `environment` tag.

| Project          | Platform   | Used by                   | SSM parameter                     |
| ---------------- | ---------- | ------------------------- | --------------------------------- |
| `numun-backend`  | go         | API + all Lambdas         | `/numun/${ENV}/sentry/dsn`        |
| `numun-frontend` | javascript | portal (inlined at build) | `/numun/${ENV}/sentry/dsn_portal` |

To recreate: sign up at https://sentry.io (free tier: 5k errors/mo), create
org `numun`, then with the Sentry CLI (`sentry auth login`):

```bash
sentry project create numun/numun-backend:go
sentry project create numun/numun-frontend:javascript
```

The DSN is technically a non-secret (it only authorizes ingestion, not read
access), but treat it as scoped config to avoid public abuse.

## 2. Populate the SSM parameters

The API reads `/numun/${ENV}/sentry/dsn` at Lambda cold start; the portal
reads `/numun/${ENV}/sentry/dsn_portal` at build time. For each env
(`test`, future `prod`), copy each project's active DSN without printing it:

```bash
ENV=test
AWS_PROFILE=numun-prod  # whichever local profile reaches the env's account
for pair in numun-backend:dsn numun-frontend:dsn_portal; do
  DSN=$(sentry dsn list "numun/${pair%%:*}" --json | jq -r '[.data[] | select(.isActive)][0].dsn')
  aws ssm put-parameter --profile "$AWS_PROFILE" --region us-east-2 --overwrite --type SecureString \
    --name "/numun/$ENV/sentry/${pair##*:}" --value "$DSN" --query Version
  unset DSN
done
```

The portal deploy role has `ssm:GetParameter` on
`/numun/${Env}/sentry/*` (granted in `infra/bootstrap/oidc-roles.yaml`).
The API Lambda's wildcard read on `/numun/${Env}/*` already covers it.

Rotation: `aws ssm put-parameter --overwrite` updates the value. The
next deploy picks it up (Lambda reads at cold-start; portal at build).
No code or IAM change.

**Portal tunnel.** Ad blockers and Firefox tracking protection block
`*.ingest.sentry.io`, so the portal sends envelopes to the API at
`POST /v1/diag` (`observability.TunnelPath`; named to dodge blocklists),
which relays them to Sentry. The route is public (no session) and
rate-limited at the anonymous 60/min/IP tier. It only forwards envelopes
whose header DSN matches `dsn_portal`'s host and project, always to the
upstream URL built from `dsn_portal`, through the SSRF-hardened
`httpclient` with that one host allowlisted; bodies over 512 KiB get 413. The API also reads `dsn_portal` at cold start, so rotating it needs
both a portal and an API deploy. Without `dsn_portal` the route isn't
mounted.

**Portal source maps.** The portal deploy uploads source maps to
`numun-frontend` with `@sentry/vite-plugin` (`portal/vite.config.ts`), then
deletes the `.map` files so they never reach the public bucket. It
authenticates with an org auth token (Settings → Developer Settings →
Organization Tokens; the default `org:ci` scope is enough) stored per env:

```bash
ENV=test
AWS_PROFILE=numun-prod  # whichever local profile reaches the env's account
read -rs TOKEN  # paste the sntrys_... token
aws ssm put-parameter --profile "$AWS_PROFILE" --region us-east-2 --overwrite --type SecureString \
  --name "/numun/$ENV/sentry/auth_token" --value "$TOKEN" --query Version
unset TOKEN
```

The deploy role's `/numun/${Env}/sentry/*` grant covers it. Without the
parameter, `portal.yml` warns and builds as before (public maps, no
upload). Upload failures only warn: the deploy succeeds, but that
release's stack traces stay minified.

**Code mappings** (Settings → Integrations → GitHub → Code Mappings) turn
stack frames into links to `noah-alvarado/numun-monorepo` on `main`:

| Project          | Stack trace prefix            | Repository prefix |
| ---------------- | ----------------------------- | ----------------- |
| `numun-backend`  | `github.com/numun/numun/api/` | `api/`            |
| `numun-frontend` | `src/`                        | `portal/src/`     |

The backend prefix is the Go module path, which shows up in stack frames
because `api/Makefile` builds with `-trimpath`. Frontend frames come from
the uploaded source maps. If an event shows them as `./src/...`, use
`./src/` as the prefix.

## 3. Verify

After the next deploy:

1. **API** — only RPCs failing with `internal` / `unknown` / `data_loss`
   (and panics) are reported, via `observability.ConnectInterceptor`;
   client errors such as a malformed body (`invalid_argument`) are not.
   No RPC fails that way on demand, so verification needs a temporary
   trigger: see #22 (admin-only `GetUser` with `user_id:
"sentry-test-error"`), reverted in #23. Expect an issue in
   `numun-backend` tagged `environment=<env>` and `rpc.procedure`, with
   the commit SHA as `release`.
2. **Portal** — open `https://portal.<apex>` with devtools and run
   `setTimeout(() => { throw new Error("Sentry test error " + Date.now()) })`.
   A bare `throw` typed into the console is not an app error and isn't
   reported. Expect an issue in `numun-frontend` tagged `environment=<env>`.
   Real app errors should show frames under `src/...` (source maps applied)
   with working "Open in GitHub" links. The console-thrown test error has
   no app frames.

If errors don't show up, check:

- The Lambda log for the request — `observability: init` lines indicate
  whether Sentry was active.
- The `dist/assets/index-*.js` bundle for the literal DSN string. If
  absent, Vite tree-shook Sentry out because the env var was empty at
  build time.
- The browser's Network tab for the request to `ingest.us.sentry.io`. Ad
  blockers and Firefox tracking protection block it; retry in a private
  window with protection off.

### Grouping and in-app frames (`numun-backend`)

API events all share the interceptor's stack, so the interceptor adds the
RPC procedure to the fingerprint (`{{ default }}` + procedure) to keep one
issue per RPC. The project's stack-trace rules (Settings → Projects →
`numun-backend` → Issue Grouping) mark our packages in-app:

```
stack.module:github.com/numun/numun/** +app
stack.module:main +app
stack.module:github.com/numun/numun/api/internal/gen/** -app
```

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
