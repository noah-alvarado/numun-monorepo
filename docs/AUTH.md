# AUTH.md

This document is the deep specification for **authentication and authorization** in the NUMUN portal: flows, session management, password policy, sign-out, authorization enforcement patterns, CSRF posture, and audit logging.

It builds on:
- [APPLICATION.md §5](./APPLICATION.md) — chose Cognito + HttpOnly cookies.
- [API.md §9](./API.md) — defines the API-level role matrix and cookie/CSRF wire details.
- [DATA_MODEL.md §2.2](./DATA_MODEL.md) — User entity and Cognito-backed roles.

Schema additions required by this document — `Session` entity and `AuthAuditEvent` entity — are listed in §13 as proposed amendments to DATA_MODEL.md.

---

## 1. Stack summary

| Concern | Choice |
|---|---|
| Identity provider | **Amazon Cognito User Pool** |
| Where sign-in screens live | The portal at `portal.numun.org` (no Cognito Hosted UI) |
| Cognito direct calls from browser | `amazon-cognito-identity-js` (or successor SDK) — used for sign-up, sign-in, email verification, password reset |
| Session transport browser ↔ API | Opaque-id HttpOnly cookie `numun_session` scoped to `.numun.org` |
| Where Cognito tokens live | **Backend only.** Refresh tokens are stored in a server-side `Session` row; the browser never sees them. |
| Access-token refresh | Performed server-side inside the API middleware when the cached access token has expired |
| Token lifetimes | Cognito defaults: **access 1h, refresh 30d, ID 1h** |
| CSRF | SameSite=Strict cookie + double-submit `X-CSRF-Token` header |
| Audit | New `AuthAuditEvent` entity in DDB (§9) |

---

## 2. Roles

Three roles, defined as Cognito custom attribute `custom:role`:

| Role | Source | Granted by |
|---|---|---|
| `advisor` | Cognito custom attribute | Defaulted on self-sign-up |
| `staff-staffer` | Cognito custom attribute | `AdminCreateUser` by staff-admin |
| `staff-admin` | Cognito custom attribute | `AdminCreateUser` by another staff-admin |

The role is **also mirrored on the User row in DynamoDB** (DATA_MODEL.md §2.2) for queryability, but Cognito is the authoritative source. On every token refresh, middleware compares the two and updates the DDB mirror if it has drifted.

Staffer **scope** (which delegations / committees a `staff-staffer` can see) is governed by the `StaffDelegationAssignment` and `StaffCommitteeAssignment` link entities, not by the role itself. See §7.

---

## 3. Sign-up

### 3.1 Advisor self-sign-up

The portal owns all auth screens at `portal.numun.org/sign-up`. The landing site links to this URL when a visitor expresses interest in registering a delegation.

**Form fields collected upfront** (all required):

- Email
- Password (subject to §6 policy)
- Full name
- Phone

The portal calls Cognito's `SignUp` API directly with the four fields. Cognito sends a verification email containing a 6-digit code.

**Verification screen** at `/sign-up/verify`:

- Email (pre-filled, editable)
- Verification code

Portal calls Cognito's `ConfirmSignUp`.

**Post-confirmation Lambda trigger.** Cognito invokes a small Lambda (separate from the Lambdalith — purpose-built for this trigger) on successful confirmation. The trigger:

1. Writes the DDB `User` row keyed by Cognito `sub`, populated with email/name/phone and `role: "advisor"`.
2. Writes an `AuthAuditEvent` of kind `signup_completed`.

This eliminates the "is the User row there yet?" race during the first authenticated API call.

**Uniform sign-up response (anti-enumeration).** Per the API portal, the visible result of submitting the sign-up form is always: *"Check your email for a verification link"* — regardless of whether the email was new or already in use. If the email exists, Cognito's `SignUp` errors with `UsernameExistsException`; the portal **swallows** this, does not send a duplicate email, and shows the same confirmation message. This prevents email enumeration via sign-up.

### 3.2 Staff invite

There is **no self-sign-up for staff** (PROJECT.md). A `staff-admin` invites new staff users.

**Flow:**

1. Admin opens `portal.numun.org/admin/staff` and submits a form with `{ email, name, role: "staff-admin" | "staff-staffer" }`.
2. Portal calls `UserService.InviteStaff` (API.md §10.3).
3. Backend calls Cognito `AdminCreateUser` with the email + the role as a custom attribute. Cognito's built-in flag `DesiredDeliveryMediums: ["EMAIL"]` causes Cognito to send the invitee an email with a temporary password.
4. Backend writes the DDB `User` row immediately (no post-confirmation trigger needed — the user already exists in Cognito at this point).
5. `AuthAuditEvent` of kind `staff_invited` is written with `actor_user_id = admin`.
6. Invitee clicks the link in the email, lands at `portal.numun.org/sign-in`, logs in with the temp password, and Cognito forces a `NEW_PASSWORD_REQUIRED` challenge. The portal handles this by showing a "set your password" screen and calling `RespondToAuthChallenge`.

`staff-staffer` accounts have no scope at creation. The admin must afterward use `DelegationService.AssignStaffer` and/or `CommitteeService.AssignStafferToCommittee` (API.md §10.5, §10.7) to grant scope. A staffer with no scope can log in but sees an empty dashboard with a message like *"No assignments yet — contact a NUMUN admin."*

---

## 4. Sign-in

### 4.1 Portal screens

- `portal.numun.org/sign-in` — email, password, **"Remember me"** checkbox.
- `portal.numun.org/sign-in/forgot` — forgot-password entry.
- `portal.numun.org/sign-in/reset` — code + new password screen.
- `portal.numun.org/sign-in/new-password` — `NEW_PASSWORD_REQUIRED` challenge screen for newly invited staff.

### 4.2 Sign-in flow

1. Portal calls Cognito `InitiateAuth` (`USER_PASSWORD_AUTH` flow) with email + password.
2. On success, Cognito returns `{ idToken, accessToken, refreshToken, expiresIn }`.
3. Portal `POST`s these to `/v1.AuthService/Exchange`, also passing the `rememberMe: boolean` flag.
4. Backend:
   - Validates the ID token (signature, `iss`, `aud`, `exp`) via JWKS.
   - Reads `sub`, `email`, `custom:role` from the token.
   - Reconciles the DDB `User` row's `role` mirror with the token's `custom:role`.
   - Generates an opaque `session_id` (UUIDv7).
   - Writes a `Session` row to DDB (see §13.1) with the refresh token, user id, expiration, and IP/user-agent.
   - Generates a fresh `csrf_token` (32 random bytes, base64url).
   - Sets two cookies on the response:
     - `numun_session=<sessionId>` — HttpOnly, Secure, SameSite=Strict, Domain=`.numun.org`, Path=`/`, with Max-Age determined by `rememberMe` (§4.3).
     - `csrf_token=<token>` — **not** HttpOnly (the client must read it), Secure, SameSite=Strict, same domain/path/age as the session cookie.
   - Writes `AuthAuditEvent` of kind `sign_in_succeeded`.
   - Returns HTTP 204.
5. On failure, writes `AuthAuditEvent` of kind `sign_in_failed` with the email (for staff to investigate brute-force).

### 4.3 "Remember me" semantics

| Checkbox state | Session cookie behavior | Session row TTL |
|---|---|---|
| Unchecked | **Session cookie** — no `Max-Age` / `Expires` attribute. Cleared when the browser closes. | 30 days (matches Cognito refresh token). The cookie may be gone but the server-side session is still valid until TTL — minor edge case, irrelevant in practice. |
| Checked | **Persistent cookie** — `Max-Age = 30 days` (matches the Cognito refresh token's max lifetime). | 30 days. |

The Cognito refresh token always carries its native lifetime (30 days by default); "remember me" only changes how long the browser holds the session cookie.

### 4.4 Multi-device sessions

A user may have any number of concurrent sessions (one `Session` row per browser/device). Each is independent. "Log out everywhere" is **not in v1** — would require iterating sessions by user, which needs an additional GSI lookup. Punt to a future iteration.

### 4.5 No inactivity timeout

v1 does not implement an inactivity timeout. The session lives until `expiresAt` regardless of activity. `lastUsedAt` is updated on every authenticated request and can be used to add an inactivity feature later without schema change.

### 4.6 No CAPTCHA in v1

Cognito's per-user rate limiting (5 failed attempts / minute) and per-IP global limits are considered sufficient. Cognito Advanced Security Features (adaptive auth, $0.05/MAU) are **not** enabled — revisit if logs show real abuse.

---

## 5. Session lifecycle

### 5.1 Authenticated request path

For every API request other than `HealthService.Check` and `AuthService.Exchange`:

1. Middleware reads `numun_session` cookie. Missing → `unauthenticated`.
2. Looks up the `Session` row by `sessionId`. Missing or `expiresAt < now` → delete cookie, `unauthenticated`.
3. Reads cached `accessToken` from the Session row.
4. If `accessTokenExpiresAt > now`, validate it (JWKS), pass through.
5. If `accessTokenExpiresAt <= now`, call Cognito `InitiateAuth` (REFRESH_TOKEN_AUTH) with the stored refresh token. On success: store the new access token + expiry on the Session row. On failure (refresh token revoked or expired): delete the Session, clear cookies, return `unauthenticated`.
6. For mutating RPCs, middleware enforces CSRF (§8).
7. Updates `lastUsedAt = now` on the Session row (best-effort; failure does not block the request).
8. Attaches `{ userId, role, sessionId }` to the request context.
9. Hands off to the handler.

### 5.2 Token caching

The access token is cached on the `Session` row so we don't call Cognito on every API request. Cache hit rate ≈ (3600 - reqInterval) / 3600 → effectively 100% for typical browsing.

### 5.3 What the browser sees

| Cookie | HttpOnly | Secure | SameSite | Domain | Path | Max-Age |
|---|---|---|---|---|---|---|
| `numun_session` | yes | yes | Strict | `.numun.org` | `/` | per §4.3 |
| `csrf_token` | **no** | yes | Strict | `.numun.org` | `/` | matches `numun_session` |

The browser never sees: ID tokens, access tokens, refresh tokens, Cognito `sub`, role, or any other identifying data via cookies. The portal calls `UserService.GetMe` after sign-in to populate its in-memory state.

### 5.4 Cross-subdomain implications

`Domain=.numun.org` means the cookie is sent on requests to `numun.org`, `www.numun.org`, `portal.numun.org`, `api.numun.org`, `cms.numun.org`, `assets.numun.org`. This is acceptable:

- `api.numun.org` legitimately consumes it.
- `portal.numun.org` reads only `csrf_token` (HttpOnly hides `numun_session`).
- `numun.org` / `www.numun.org` (static landing) doesn't read cookies.
- `cms.numun.org` (Decap CMS, separate GitHub OAuth) ignores it.
- `assets.numun.org` (CloudFront static files) ignores it.

CORS on `api.numun.org` allows only `https://portal.numun.org` as origin — cross-origin reads from other subdomains are blocked at the API layer.

---

## 6. Password policy

Cognito User Pool policy:

| Setting | Value | Reason |
|---|---|---|
| Minimum length | **12** | NIST SP 800-63B emphasis on length |
| Require lowercase | yes | Cognito doesn't allow turning all complexity off |
| Require digits | yes | Same |
| Require uppercase | **no** | Length + lowercase + digits is sufficient |
| Require symbols | **no** | Composition rules drive bad password reuse |
| Temporary password validity | 7 days | Long enough for invitees to act |

Passwords are validated client-side (for UX) and server-side (by Cognito, authoritatively). Both surfaces emit the same human-readable rule descriptions.

---

## 7. Authorization enforcement

API.md §9.2 defines **what** each role can do. AUTH.md specifies **how** that's enforced.

### 7.1 Hybrid model

- **Middleware** verifies authentication and a coarse RPC-level role check:
  - Is the caller signed in?
  - Does the caller's role appear in the role matrix for this RPC?
- **Handler** verifies resource-level scope:
  - Does this caller have scope on *this specific* `delegation_id` / `committee_id` / `delegate_id`?

The middleware layer cannot enforce resource scope because it does not know which parameter on the request is the entity id, nor what scoping rule applies to that entity. The handler is where domain knowledge lives.

### 7.2 Scope helpers

Every handler that takes an entity id parameter must, in its first lines, call a scope helper:

```go
// pseudocode — actual signatures in Go
mustHaveScopeOnDelegation(ctx, delegationId)
mustHaveScopeOnDelegate(ctx, delegateId)
mustHaveScopeOnCommittee(ctx, committeeId)
mustHaveScopeOnAssignment(ctx, assignmentId)
mustHaveScopeOnPayment(ctx, paymentId)
mustHaveScopeOnConference(ctx, conferenceId)
```

Each helper:

1. Resolves the caller from context.
2. Resolves the parent entity (e.g., for a delegate, fetch the parent delegation).
3. Applies the scope rule per the role matrix:
   - `staff-admin` → always pass.
   - `staff-staffer` → must have a `StaffDelegationAssignment` (case a) and/or, for case (c) entities, traverse `StaffCommitteeAssignment → Position → Assignment → Delegate → Delegation`.
   - `advisor` → must be a `DelegationAdvisor` on the delegation.
4. On failure, returns `not_found` (§7.3).

The hard rule: **no handler reads or mutates a scoped entity without first calling its scope helper.** This is enforced by code review and by a lint rule that flags handlers touching `delegationRepo.Get` etc. without a preceding `mustHaveScopeOn*` call.

### 7.3 `not_found` vs. `permission_denied`

- **Default to `not_found`** when a scope check fails. This prevents enumeration ("if I get 403, the resource exists").
- **Use `permission_denied`** only when the caller demonstrably knows the resource exists and is attempting a privileged state transition — e.g., an advisor calling `DelegationService.Approve` on their own delegation (they know it; they just can't approve it).

The handler decides which to emit based on whether the caller had read scope. If yes (knows the resource exists) but lacks write privilege → `permission_denied`. If no (cannot prove they should know) → `not_found`.

### 7.4 No client-trusted filtering

The API never relies on the client to pass scoping filters. Listing endpoints internally append the caller's scope to every query. The client supplies optional filters; the server intersects them with the caller's allowed scope.

---

## 8. Sign-out

`POST /v1.AuthService/Logout`:

1. Reads `numun_session` cookie.
2. Looks up the Session row.
3. Calls Cognito `RevokeToken` on the stored refresh token (best-effort; success not required for logout to proceed).
4. Deletes the Session row in DDB.
5. Writes `AuthAuditEvent` of kind `sign_out`.
6. Sets cookies to expire immediately:
   ```
   Set-Cookie: numun_session=; Path=/; Domain=.numun.org; Max-Age=0; Secure; HttpOnly; SameSite=Strict
   Set-Cookie: csrf_token=;     Path=/; Domain=.numun.org; Max-Age=0; Secure;            SameSite=Strict
   ```
7. Returns HTTP 204.

The portal handles the success by redirecting to `/sign-in`.

---

## 9. CSRF posture

Cookie-based auth requires CSRF defense. Defense-in-depth:

### 9.1 SameSite=Strict

`numun_session` carries `SameSite=Strict`. The browser will not send the cookie on cross-site requests at all, blocking the classic CSRF attack vector entirely.

Tradeoff: external links to the portal (e.g., from email or the landing site) start unauthenticated even if the user is logged in on `portal.numun.org`. The portal's app shell silently calls `UserService.GetMe`, gets the cookie sent on this same-site fetch, and re-renders. Practical impact: the first navigation may briefly show a sign-in screen flash if the page is rendered before the GetMe call returns — solved by gating render on the GetMe response.

### 9.2 Double-submit token

For every mutating RPC (everything except read-only `Get*` / `List*` / `ListAll*`), middleware requires:

1. `csrf_token` cookie present.
2. `X-CSRF-Token` request header present.
3. The two values **byte-for-byte equal**.

A request missing or mismatching the header is rejected with `permission_denied`.

The portal reads `csrf_token` from `document.cookie` (it's not HttpOnly) and attaches it to every Connect request via a client interceptor. The same value travels in both places.

### 9.3 Token rotation

- A fresh `csrf_token` is issued on each successful sign-in.
- The token is **not** rotated on every request — that would cause races with concurrent in-flight calls.
- The token is invalidated on sign-out.

---

## 10. Password & account recovery

### 10.1 Forgot password

1. User clicks "Forgot password" on `/sign-in`.
2. Portal calls Cognito `ForgotPassword` with the email.
3. Cognito sends a 6-digit code by email (only if the user exists — Cognito returns success either way to prevent enumeration, but it only sends mail when the user is real).
4. Portal shows a screen accepting code + new password.
5. Portal calls Cognito `ConfirmForgotPassword`.
6. Writes `AuthAuditEvent` of kind `password_reset_completed`.

### 10.2 Email change

Users can change their email from `/account/email`:

1. Portal calls Cognito `UpdateUserAttributes` with the new email.
2. Cognito sends a verification code to the new email.
3. User enters the code; portal calls `VerifyUserAttribute`.
4. The old email remains valid for login until the new email is verified.
5. After verification, the User row's `email` is updated by the post-update Lambda trigger (or by a re-call to GetMe which reconciles).
6. `AuthAuditEvent` of kind `email_changed`.

### 10.3 Account deletion

**No self-service deletion in v1.** A `staff-admin` can hard-delete:

1. Calls Cognito `AdminDeleteUser`.
2. Soft-deletes the DDB `User` row.
3. Any `DelegationAdvisor` link rows for this user — soft-delete. If this leaves a delegation with **zero advisors**, the delete is rejected (`failed_precondition`); admin must first reassign or add a replacement advisor.
4. `AuthAuditEvent` of kind `account_deleted`.

---

## 11. Audit log

A new `AuthAuditEvent` entity (DDB) records auth-relevant events:

| Kind | Trigger |
|---|---|
| `signup_completed` | Cognito post-confirmation Lambda |
| `staff_invited` | `UserService.InviteStaff` succeeds |
| `sign_in_succeeded` | After session creation in `AuthService.Exchange` |
| `sign_in_failed` | Backend after a failed Cognito `InitiateAuth`. Email is captured; password is **never** logged. |
| `sign_out` | `AuthService.Logout` |
| `password_reset_completed` | After successful `ConfirmForgotPassword` (recorded by a post-event Lambda trigger or by the portal calling a dedicated audit RPC — see §13.2 open item) |
| `email_changed` | After successful `VerifyUserAttribute` |
| `account_deleted` | After admin deletion completes |
| `role_changed` | When admin changes a user's `custom:role` |
| `scope_granted` / `scope_revoked` | Add/remove of `StaffDelegationAssignment` or `StaffCommitteeAssignment` |
| `bulk_import_previewed` / `bulk_import_committed` | Per BULK_IMPORT.md §8.1. Metadata includes delegation id, source type, and row counts (never row content). |

Additional high-value state-transition kinds — `delegation_approved` / `delegation_rejected`, `payment_recorded`, `assignment_approved` / `assignment_unapproved` / `assignment_manually_edited`, `award_created` / `award_modified` / `award_deleted`, `email_unsuppressed` — are enumerated in SECURITY.md §6.3 and use the same `AuthAuditEvent` shape.

Each event captures: `id` (UUIDv7), `userId` (subject), `actorUserId` (who did it; may be same as userId for self-actions), `kind`, `ip`, `userAgent`, `occurredAt`, `metadata` (free-form JSON for kind-specific fields).

**Retention:** 1 year via DDB TTL.

**Read access:** `staff-admin` only. Not exposed via API in v1 — read via direct DDB query when investigating an incident. A future API surface can be added.

---

## 12. Future SSO

PROJECT.md flags SSO via Google, Microsoft, and Apple as a planned upgrade. Nothing about today's design blocks that future:

- Cognito User Pools support adding identity providers (Google/Microsoft/Apple OIDC) without code changes to the backend.
- The portal's sign-in screen will need new "Sign in with Google / Microsoft / Apple" buttons that initiate Cognito's OAuth federation flow. The redirect comes back with a code that the portal exchanges for tokens — same `AuthService.Exchange` endpoint serves the resulting tokens, no backend change needed.
- The post-confirmation Lambda trigger already handles initial User row creation; it just sees a federated identity instead of a username/password sign-up.
- **No SSO code is written in v1.**

---

## 13. Proposed amendments to DATA_MODEL.md

These entities are required for AUTH.md but have not yet been added to DATA_MODEL.md.

### 13.1 Session

A server-side session row. Keyed by an opaque `sessionId` (UUIDv7).

| Attribute | Notes |
|---|---|
| `id` | UUIDv7 — the value placed in the `numun_session` cookie |
| `userId` | Cognito `sub` |
| `refreshToken` | Cognito refresh token (encrypted at rest via DDB AWS-managed key; backend stores ciphertext via the SDK's `attributevalue` boundary — see §13.3 open item) |
| `cachedAccessToken` | Cognito access token (encrypted at rest) |
| `cachedAccessTokenExpiresAt` | When to refresh |
| `csrfToken` | The current CSRF token for this session |
| `ip` | First-seen IP for the session |
| `userAgent` | First-seen UA |
| `createdAt` | |
| `lastUsedAt` | Updated best-effort on each request |
| `expiresAt` | TTL value; DDB removes the item automatically |

Keys:
- PK: `SESSION#<sessionId>`
- SK: `META`
- DDB TTL attribute: `expiresAt` (epoch seconds)

No GSI needed in v1 (lookup is always by `sessionId`). If "log out everywhere" is ever implemented, add a GSI on `GSI1PK = USER#<userId>`, `GSI1SK = SESSION#<sessionId>`.

### 13.2 AuthAuditEvent

| Attribute | Notes |
|---|---|
| `id` | UUIDv7 |
| `userId` | Subject |
| `actorUserId` | Performer (often same as `userId`) |
| `kind` | One of the values in §11 |
| `ip` | |
| `userAgent` | |
| `occurredAt` | |
| `metadata` | Free-form JSON |
| `expiresAt` | DDB TTL — 1 year out |

Keys:
- PK: `USER#<userId>`
- SK: `AUTH_EVENT#<occurredAt>#<id>`
- DDB TTL: `expiresAt`

This places events under their subject user, supporting "list a user's auth history" via Query without a GSI.

### 13.3 Common attribute additions

No changes to the existing common-attribute set. `Session` and `AuthAuditEvent` follow the standard fields (entity, version, isDeleted, timestamps) defined in DATA_MODEL.md §3.

---

## 14. Decisions log

| Decision | Choice | Reason |
|---|---|---|
| Identity provider | Cognito User Pool | Free at NUMUN scale; future SSO support |
| Sign-in surface | Custom screens in the portal, calling Cognito SDK directly | Full brand control; no Hosted UI dependency |
| Where refresh tokens live | Backend-only, in `Session` row | Browser never sees them; XSS impact bounded |
| Browser cookie | Opaque session id, HttpOnly, Secure, SameSite=Strict, `.numun.org` | Minimal exposure |
| "Remember me" | Checkbox controls cookie persistence (session vs. 30-day) | User requested |
| Token lifetimes | Cognito defaults (1h access, 30d refresh) | User requested |
| Password policy | Length-first (min 12, lowercase + digits) | NIST SP 800-63B |
| CSRF | SameSite=Strict + double-submit token | Defense in depth |
| Sign-up enumeration | Uniform "check your email" response | Anti-enumeration |
| Authorization model | Middleware (auth + role) + handler (resource scope) | Domain knowledge lives in handlers |
| Failure code | `not_found` for missing scope; `permission_denied` for forbidden state transition | Anti-enumeration |
| Multi-device sessions | Allowed, unlimited; no "log out everywhere" | Future feature |
| Inactivity timeout | None in v1 | Schema supports adding later |
| Audit log | `AuthAuditEvent` entity, 1-year TTL | Investigations + compliance posture |
| SSO | Not in v1; design accommodates future enablement | Per PROJECT.md |

---

## 15. Open items

- **Refresh-token / access-token encryption at rest.** §13.1 says "encrypted via DDB AWS-managed key" — DDB encrypts the whole table by default. Whether to additionally application-layer-encrypt these fields (e.g., with KMS envelope encryption) is a security-vs-complexity decision to make in SECURITY.md.
- **`password_reset_completed` audit event source.** Cognito doesn't natively emit a webhook for this; the portal would need to call a dedicated `AuditService.RecordEvent` RPC after success, or we wire a custom Cognito trigger. Punt the choice to implementation.
- **"Log out everywhere"** — a clear future feature; requires a GSI on Sessions by user. Not v1.
- **Cognito Advanced Security Features ($0.05/MAU)** — enabled if logs ever show brute-force or credential-stuffing. Off in v1.
- **API surface for the audit log** — read-only listing for admins is a likely v1.1 addition; not in current API.md.
- **Cookie partitioning (CHIPS)** — recent browser feature that further isolates cookies. Currently overkill given SameSite=Strict, but worth revisiting if cross-context features arrive.
