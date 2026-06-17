# API.md

This document defines the **API contract** between the SolidJS portal and the Go Lambdalith backend. It builds on [APPLICATION.md](./APPLICATION.md) (stack), [DATA_MODEL.md](./DATA_MODEL.md) (entities), and [ASSIGNMENT_ALGORITHM.md](./subsystems/ASSIGNMENT_ALGORITHM.md) (algorithm semantics). Auth flow details, bulk import, email, and security are out of scope here — they get their own documents.

---

## 1. Stack & wire protocol

| Layer | Choice |
|---|---|
| Service framework | **Connect** ([connectrpc.com](https://connectrpc.com/)) |
| Contract format | **Protobuf** (.proto files) |
| Wire transport | **HTTP/1.1** (Connect's default — works on API Gateway HTTP API + Lambda; no HTTP/2 needed) |
| Wire formats supported | **JSON** (`application/json`) and **Protobuf binary** (`application/proto`). Clients negotiate via `Content-Type`. |
| Server | `connect-go` plugged into the existing `net/http.ServeMux` in `/api` |
| Web client | `@connectrpc/connect-web` in the SolidJS portal; TS types generated from `.proto` |
| Tooling | **Buf** (`buf.build`) for lint, breaking-change detection, code-gen |
| API root URL | `https://api.numun.org` |
| API version prefix | `/v1` — embedded in the Protobuf package name (`numun.v1`); appears in URLs as `/v1.<service>/<method>` |

### Why Connect over true gRPC?

Real HTTP/2 gRPC requires an ALB or self-hosted compute (~$16+/mo idle), which blows the $100/yr cost ceiling. Connect gives us Protobuf-defined services + generated typed clients while staying on plain HTTP/1.1 + API Gateway HTTP API + Lambda. It's also gRPC-wire-compatible for any future server-to-server consumer that wants real gRPC.

### Why not REST?

The user requirement is a contract-first typed API. Protobuf gives us a single source of truth (`.proto` files) that generates both the Go server stubs and the TS client code with no hand-written API client.

---

## 2. Repository & code-gen

Proto files live in the monorepo:

```
/api/proto/numun/v1/
  common.proto          — shared messages (Cursor, Money, Address, Page, etc.)
  errors.proto          — application error details (see §6)
  auth.proto            — AuthService
  users.proto           — UserService
  conferences.proto     — ConferenceService
  delegations.proto     — DelegationService
  delegates.proto       — DelegateService
  committees.proto      — CommitteeService, PositionService
  assignments.proto     — AssignmentService, AssignmentRunService
  payments.proto        — PaymentService
  announcements.proto   — AnnouncementService
  awards.proto          — AwardService
  exports.proto         — ExportService (CSV download shims)
  email_health.proto    — EmailHealthService (admin email-health surface)
  public.proto          — PublicService (unauthenticated, CORS-open, rate-limited)
  health.proto          — HealthService
  buf.yaml              — Buf module config
  buf.gen.yaml          — code-gen config
```

- **Go server code** (`*_pb.go`, `*connect.go`) is generated into `/api/internal/gen/` and committed to the repo.
- **TS client code** is generated into `/portal/src/gen/` and committed to the repo.
- **Lint & breaking-change checks** run in CI via `buf lint` and `buf breaking --against ".git#branch=main"`.

Source-of-truth: the `.proto` files. Generated code is reviewed but never hand-edited.

---

## 3. Conventions

### 3.1 URL paths

Connect's default URL scheme is `/<package>.<service>/<method>` — e.g., `POST /numun.v1.DelegationService/CreateDelegation`. These are **method calls, not REST resources**, so URL casing conventions for resources don't apply. The portal never constructs these URLs by hand; the generated client takes care of it.

(Users will never see these URLs except in network tabs while debugging.)

### 3.2 Identifiers

- All IDs are **UUIDv7**, including `conferenceId`. The earlier "human-readable conference ID" idea is dropped per the interview.
- Protobuf field type: `string`, conventionally named `<entity>_id` (snake_case in proto → `<entity>Id` in JSON/TS, `<Entity>ID` in Go).

### 3.3 Field naming & casing

- `.proto` fields: **snake_case** (Protobuf convention).
- JSON wire format: **camelCase** (Protobuf JSON default).
- Generated Go fields: **PascalCase**.
- Generated TS fields: **camelCase**.

This is the standard Protobuf-everywhere convention and matches the prior DATA_MODEL.md JSON examples.

### 3.4 Time

- All timestamps as Protobuf `google.protobuf.Timestamp`. RFC 3339 on the JSON wire.
- All times are UTC. Server never emits local times.
- Dates without time-of-day (rare here — possibly check-in dates) use `google.type.Date`.

### 3.5 Money

- A custom `Money` message in `common.proto`:
  ```
  message Money {
    string currency = 1;   // ISO 4217, always "USD" for v1
    int64  units    = 2;   // whole dollars
    int32  cents    = 3;   // 0–99
  }
  ```
- No floats anywhere. Balances and payment amounts use `Money`.

### 3.6 Optionality

- Fields that are **logically optional** use Protobuf `optional` (proto3 explicit presence) so the client can distinguish "not set" from "zero value."
- Required-shaped semantics are enforced via `protovalidate` rules (§6.2), not Protobuf syntax.

---

## 4. Pagination

Cursor-based, opaque, server-controlled. Maps onto DynamoDB's `LastEvaluatedKey`.

### 4.1 Request

Every paginated `List<X>Request` carries:
```
message ListXRequest {
  int32  page_size = 1;   // default 100, max 500
  string cursor    = 2;   // opaque; empty for the first page
  // ... endpoint-specific filters
}
```

- `page_size`: default **100**, max **500**. Server clamps if exceeded.
- `cursor`: opaque base64-encoded DynamoDB exclusive-start-key. Treat as an opaque token.

### 4.2 Response

```
message ListXResponse {
  repeated X      items       = 1;
  string          next_cursor = 2;   // empty when no more pages
  int32           page_size   = 3;   // echo of effective page size
}
```

### 4.3 `:all` convenience endpoints

For small-bounded scopes (delegations in a conference, committees in a conference, advisors on a delegation) where the entire set comfortably fits in one response, we offer a parallel **non-paginated** RPC suffixed `All`:

| Paginated | Non-paginated |
|---|---|
| `ListDelegations(ListDelegationsRequest)` | `ListAllDelegations(ListAllDelegationsRequest)` |
| `ListCommittees(ListCommitteesRequest)` | `ListAllCommittees(ListAllCommitteesRequest)` |
| `ListAdvisors(ListAdvisorsRequest)` | `ListAllAdvisors(ListAllAdvisorsRequest)` |

The non-paginated variant has a hard server-side cap (e.g., 1000 items) and returns `RESOURCE_EXHAUSTED` if the set is larger — at which point clients must use the paginated variant.

### 4.4 Sorting

- **Sorting is client-side.** Server returns items in a documented, fixed order per endpoint (typically the natural DynamoDB sort).
- Examples of documented orders:
  - `ListDelegations` — by school ascending
  - `ListPayments` — by `recorded_at` descending
  - `ListAnnouncements` — by `sent_at` descending
  - `ListAssignmentRuns` — by `triggered_at` descending
- The SolidJS portal re-sorts in the browser when a user clicks a column header.

---

## 5. Filtering

- Listing RPCs accept filter fields **only on attributes that are indexed in DynamoDB** (see DATA_MODEL.md §5). Examples: `delegation_status`, `assignment_status`, `committee_id`.
- Unrecognized filter combinations return `INVALID_ARGUMENT` to prevent accidental Scans.
- Each filter parameter is documented per endpoint in §10.

---

## 6. Error model

### 6.1 Codes

Connect uses gRPC-style status codes mapped to HTTP statuses:

| Connect code | HTTP | When the server returns it |
|---|---|---|
| `unauthenticated` | 401 | Missing or invalid auth cookie |
| `permission_denied` | 403 | Authenticated but role/scope insufficient |
| `not_found` | 404 | Entity does not exist or is soft-deleted |
| `already_exists` | 409 | Unique-key violation (e.g., duplicate email) |
| `aborted` | 409 | **Optimistic-lock conflict** (`version` mismatch) |
| `failed_precondition` | 412 | State machine violation (e.g., approving an already-approved delegation) |
| `invalid_argument` | 400 | Validation failure (per `protovalidate`) |
| `resource_exhausted` | 429 | Rate limit; or `ListAll*` exceeded cap |
| `unavailable` | 503 | Downstream (DDB, Cognito) failure |
| `internal` | 500 | Unexpected server error |
| `unimplemented` | 501 | RPC defined but not yet implemented |
| `deadline_exceeded` | 408 | Run exceeded its budget (e.g., assignment algorithm) |

### 6.2 Error details (validation)

For `invalid_argument`, the server attaches a `BadRequest` detail (Protobuf's `google.rpc.BadRequest`) listing every failing field with a path and a reason. Validation is driven by **`protovalidate`** annotations on the `.proto` messages — single source of truth, enforced server-side, surfaced verbatim in `BadRequest.field_violations`.

Example wire shape (JSON mode):
```json
{
  "code": "invalid_argument",
  "message": "validation failed",
  "details": [{
    "type": "google.rpc.BadRequest",
    "value": {
      "fieldViolations": [
        { "field": "delegation.school", "description": "must not be empty" },
        { "field": "delegation.address.postal_code", "description": "must match pattern ^[0-9]{5}(-[0-9]{4})?$" }
      ]
    }
  }]
}
```

### 6.3 Domain error details

For domain errors (e.g., `ALREADY_EXISTS` on a duplicate school registration), the server attaches a `numun.v1.ErrorInfo` message defined in `errors.proto` with a stable machine-readable `reason` enum + per-error fields.

---

## 7. Optimistic locking

Per DATA_MODEL.md §3, every mutable entity has a `version: number`. The API surface:

- **Reads** return `version` in the response.
- **Updates** require the client to send the prior `version` in the request body.
- **Version mismatch** → `aborted` (HTTP 409).

Example (`delegations.proto`):
```
message UpdateDelegationRequest {
  string delegation_id = 1;
  Delegation patch     = 2;
  int32 expected_version = 3;   // required
}
```

This is in the body, not a header — easier for the generated TS client.

---

## 8. Idempotency

- All mutating RPCs accept a client-generated `Idempotency-Key` HTTP header (a UUIDv7 the client picks).
- Backend writes an **in-flight lock** on `IDEMPOTENCY#<key>` with a 60-second TTL via a conditional `PutItem`. A concurrent retry sees the lock and is rejected with Connect `already_exists` (HTTP 409, message `duplicate request in flight`).
- After the original call completes, the lock expires naturally. A later retry after the TTL window proceeds as a fresh call.
- Optional. Calls without the header behave normally (no dedupe).

This catches the common double-submit case (a user clicks "Submit" twice in succession) without paying the storage cost of full response replay. Late retries — e.g., the client times out after 30 s and resubmits at minute 5 — are *not* deduplicated; if that case becomes a real source of duplicates, the middleware upgrades to a completed-status replay (store `{status, completedAt}` for 24 h) with no change to the protocol. Decision rationale captured in IMPLEMENTATION_PLAN.md M12.

Implementation lives outside the Protobuf contract — it's a transport-level header handled by middleware.

---

## 9. Authentication & authorization

### 9.1 Wire-level

All RPCs except `HealthService`, `AuthService.Exchange`, and `PublicService.*` require an authenticated session.

- **Session transport:** HttpOnly + Secure cookie `numun_session` scoped to `.numun.org`.
- **CSRF defense:** SameSite=Strict on the session cookie **plus** a double-submit token. The server sets a non-HttpOnly `csrf_token` cookie on session creation; mutating RPCs (everything except `GET`-equivalent reads) require the client to send the same value in an `X-CSRF-Token` header.
- **No Bearer tokens** in v1.

### 9.2 Role matrix

Roles from DATA_MODEL.md §2.2: `advisor`, `staff-admin`, `staff-staffer`. Staffer scope is restricted via two link entities (DATA_MODEL.md §2.6, §2.7).

| RPC group | advisor | staff-staffer | staff-admin |
|---|---|---|---|
| `AuthService.*` | ✅ | ✅ | ✅ |
| `UserService.GetMe` | ✅ self | ✅ self | ✅ self |
| `UserService.GetUser`/`UpdateUser` | self only | self only | full |
| `UserService.InviteStaff` | ❌ | ❌ | ✅ |
| `ConferenceService` reads | ✅ active conferences | ✅ active conferences | ✅ all |
| `ConferenceService` writes | ❌ | ❌ | ✅ |
| `DelegationService.CreateDelegation` | ✅ (becomes lead) | ❌ | ✅ |
| `DelegationService.Get*`/`List*` | own delegations | scoped (a) + (c) | all |
| `DelegationService.Approve`/`Reject` | ❌ | ❌ | ✅ |
| `DelegationService.AddAdvisor` / `RemoveAdvisor` / `SetAdvisorRole` | ✅ own delegation | ❌ | ✅ |
| `DelegateService.*` reads | own delegation | scoped | all |
| `DelegateService.UpsertBulk` | own delegation | ❌ | ✅ |
| `DelegateService.CheckIn` | ❌ | ✅ scoped | ✅ |
| `CommitteeService` / `PositionService` reads | ✅ | ✅ | ✅ |
| `CommitteeService` / `PositionService` writes | ❌ | ❌ | ✅ |
| `AssignmentService.Propose` | ❌ | ❌ | ✅ |
| `AssignmentService.Approve`/`Unapprove`/`Update` | ❌ | ✅ scoped (c) | ✅ |
| `AssignmentService.List*` | own delegation only | scoped | all |
| `AssignmentRunService` reads | ❌ | ✅ scoped | ✅ |
| `PaymentService` reads | own delegation | scoped (a) | all |
| `PaymentService` writes | ❌ | ❌ | ✅ |
| `AnnouncementService.Send` / `PreviewSendAudience` | ❌ | ❌ | ✅ |
| `AnnouncementService` reads | ✅ recipients of | ✅ all in scope | ✅ |
| `AwardService` reads | ✅ public after conference | ✅ | ✅ |
| `AwardService` writes | ❌ | ❌ | ✅ |
| `ExportService.*` | own delegation | scoped | all |
| `EmailHealthService.*` | ❌ | ❌ | ✅ |
| `PublicService.*` | ✅ (unauthenticated; same surface for everyone) | ✅ | ✅ |

Authorization is enforced server-side in middleware + per-RPC handlers. The client cannot be trusted to filter results.

---

## 10. Service catalog

Each subsection below lists the RPCs for one service. Request/response message details live in the `.proto` files; this catalog is the human-readable index.

### 10.1 HealthService

| RPC | Purpose |
|---|---|
| `Check` | Liveness check. No auth. Returns server version and build commit. |

### 10.1b PublicService

Unauthenticated, CORS-open, rate-limited (60 req/min per source IP). Feeds the static landing page at build time and — optionally — at runtime when the page wants live data without a rebuild. See CMS_CONTENT_MODEL.md §9.

| RPC | Purpose |
|---|---|
| `GetActiveConference` | Returns the unique `Conference` row whose `status` is `open-for-registration` or `in-progress`. Response is a safe public subset: `{ conferenceId, name, editionNumber, year, startsAt, endsAt, registrationStatus, themeMetadata }`. Returns null fields (or a `not_found` payload) if no conference is active; returns `failed_precondition` if more than one matches (data integrity issue requiring staff intervention). |

CORS for `PublicService` allows any origin (`Access-Control-Allow-Origin: *`), unlike the rest of the API which restricts to `https://portal.numun.org`. The GitHub Actions site-build workflow consumes this RPC over plain HTTPS with no auth headers.

### 10.2 AuthService

| RPC | Purpose |
|---|---|
| `Exchange` | Exchange a Cognito ID token (sent in body) for the `numun_session` cookie. **No auth required** (this *is* the auth handshake). |
| `Logout` | Invalidate the current session cookie. |

(Sign-up, password reset, email verification go directly to Cognito's APIs from the SolidJS portal; not part of our service.)

### 10.3 UserService

| RPC | Purpose |
|---|---|
| `GetMe` | Returns the caller's User profile + role. |
| `GetUser` | Get a user by id. Self or admin. |
| `UpdateUser` | Update profile fields. Self or admin. |
| `InviteStaff` | Admin invites a new staff user (creates Cognito user, sends invite email). |

### 10.4 ConferenceService

| RPC | Purpose |
|---|---|
| `ListConferences` / `ListAllConferences` | List conferences. |
| `GetConference` | Get by id. |
| `CreateConference` | Admin only. |
| `UpdateConference` | Admin only. |

### 10.5 DelegationService

| RPC | Purpose |
|---|---|
| `ListDelegations` | Paginated list within a conference. Filterable by `status`. |
| `ListAllDelegations` | Non-paginated. |
| `GetDelegation` | By id. |
| `CreateDelegation` | Advisor (becomes lead) or admin. |
| `UpdateDelegation` | Patch school, address, preferences. Optimistic-lock. |
| `Approve` / `Reject` | Admin. |
| `AddAdvisor` / `RemoveAdvisor` / `SetAdvisorRole` | Manage the `DelegationAdvisor` link. Lead can manage own delegation's advisors. |
| `ListAdvisors` / `ListAllAdvisors` | List a delegation's advisors. |
| `AssignStaffer` / `UnassignStaffer` | Admin manages `StaffDelegationAssignment` link. |

### 10.6 DelegateService

| RPC | Purpose |
|---|---|
| `ListDelegates` / `ListAllDelegates` | Within a delegation. |
| `SearchDelegates` | Across delegations in a conference. Filter by name prefix (uses GSI2). |
| `GetDelegate` | By id. |
| `CreateDelegate` | Single-row create (rare — bulk is the primary path). |
| `UpsertDelegatesBulk` | Bulk upsert (all-or-nothing transaction). Body carries the parsed rows; parsing of CSV/XLSX/Sheets is **out of scope here** — see BULK_IMPORT.md. |
| `UpdateDelegate` | Patch fields. |
| `DeleteDelegate` | Soft delete. |
| `CheckIn` | Day-of. Sets `checkedInAt`. |

### 10.7 CommitteeService

| RPC | Purpose |
|---|---|
| `ListCommittees` / `ListAllCommittees` | Within a conference. |
| `GetCommittee` | By id. |
| `CreateCommittee` / `UpdateCommittee` / `DeleteCommittee` | Admin. |
| `AssignStafferToCommittee` / `UnassignStafferFromCommittee` | Admin manages `StaffCommitteeAssignment` link. |

### 10.8 PositionService

| RPC | Purpose |
|---|---|
| `ListPositions` / `ListAllPositions` | Within a committee. |
| `GetPosition` | By id. |
| `CreatePosition` / `UpdatePosition` / `DeletePosition` | Admin. Validates `dualDelegation ⇒ maxDelegates == 2`. |

### 10.9 AssignmentService

| RPC | Purpose |
|---|---|
| `ListAssignments` | Paginated. Filterable by `committee_id`, `delegation_id`, `position_id`, `status`. |
| `GetAssignment` | By id. |
| `Propose` | Run the algorithm. Params: `conference_id`, `dry_run: bool`, optional `seed: uint64`. Returns the proposal plus an `AssignmentRun` summary. See ASSIGNMENT_ALGORITHM.md §5. |
| `UpdateAssignment` | Manual edit on a proposed assignment. |
| `Approve` / `Unapprove` | Per ASSIGNMENT_ALGORITHM.md §7. |

### 10.10 AssignmentRunService

| RPC | Purpose |
|---|---|
| `ListAssignmentRuns` / `ListAllAssignmentRuns` | Within a conference. Newest first. |
| `GetAssignmentRun` | By id. |
| `GetCurrentRun` | Returns the run with `status = running` if any, else null. Backed by the GSI2 lookup added in DATA_MODEL.md §5. |

### 10.11 PaymentService

| RPC | Purpose |
|---|---|
| `ListPayments` / `ListAllPayments` | Within a delegation. Newest first. |
| `RecordPayment` | Append a ledger entry. TransactWrite with Delegation balance update. |
| `UpdatePayment` | Patch a record (notes, reference). |
| `DeletePayment` | Soft delete. |

### 10.12 AnnouncementService

| RPC | Purpose |
|---|---|
| `ListAnnouncements` / `ListAllAnnouncements` | Conference-scoped or global. |
| `GetAnnouncement` | By id. |
| `Send` | Admin. Persists the record and triggers SES dispatch (see EMAIL.md §5.3). |
| `PreviewSendAudience` | Admin. Returns the resolved recipient count (after suppression and opt-out filters) and a sample rendering for one recipient. Confirms staff intent before `Send` (see EMAIL.md §5.3). |

### 10.13 AwardService

| RPC | Purpose |
|---|---|
| `ListAwards` / `ListAllAwards` | Within a conference. |
| `GetAward` | By id. |
| `CreateAward` / `UpdateAward` / `DeleteAward` | Admin. |

### 10.14 ExportService

CSV exports. See §12 for details.

| RPC | Purpose |
|---|---|
| `ExportAssignmentsCsv` | Conference assignments as CSV. Scoped by caller's role. |
| `ExportDelegatesCsv` | Delegates as CSV. Scoped. |
| `ExportPaymentsCsv` | Payment ledger as CSV. Scoped. |

### 10.15 EmailHealthService

Surfaces users whose emails have hard-bounced or been flagged as complaints, for `staff-admin` remediation. See EMAIL.md §9.

| RPC | Purpose |
|---|---|
| `ListSuppressed` | List users with `emailStatus ≠ "ok"`. Admin only. |
| `Unsuppress` | Flip a single user's `emailStatus` back to `"ok"`. Writes an `AuthAuditEvent` of kind `email_unsuppressed`. Admin only. |

---

## 11. Bulk operations

Bulk endpoints follow a consistent pattern:

- One RPC per bulk-able resource (currently only `DelegateService.UpsertDelegatesBulk` in v1).
- Request carries the parsed rows as a `repeated` Protobuf message.
- Server applies the change in `TransactWriteItems` batches of 25 items per DDB transaction.
- **All-or-nothing semantics within a request.** If any row fails validation or any DDB transaction fails, the entire request returns `invalid_argument` / `aborted` and **nothing is persisted**.
- Per-row validation errors are returned as `BadRequest.field_violations` keyed by row index (`rows[3].first_name: must not be empty`).
- The maximum rows per request is **500** (matches `page_size` max). For larger imports the client must chunk.

File parsing (CSV / XLSX / Google Sheets) is **not** an API concern — the parser runs in the portal or in a separate parsing endpoint specified in BULK_IMPORT.md.

---

## 12. CSV exports

### 12.1 Wire shape

CSV is not a natural fit for Protobuf wire. We expose exports through a **parallel non-Connect HTTP surface** mounted on the same `http.ServeMux`:

```
GET /v1/exports/assignments.csv?conference_id=<uuid>
GET /v1/exports/delegates.csv?conference_id=<uuid>
GET /v1/exports/payments.csv?conference_id=<uuid>
```

- **Auth:** same cookie + CSRF stack as the RPC surface. Middleware shared.
- **Response:** `Content-Type: text/csv; charset=utf-8`, `Content-Disposition: attachment; filename="assignments-<conferenceId>.csv"`.
- **Encoding:** UTF-8 with BOM (Excel-friendly).
- **Newlines:** CRLF (RFC 4180).
- **At NUMUN scale** (~850 rows max), exports run synchronously in the Lambda; no async/polling pattern needed.

The Connect `ExportService.Export*Csv` RPCs in §10.14 are thin shims that emit the same payload via Connect's `Content-Type` negotiation — they exist primarily to give the TS client a typed handle for triggering downloads. The portal will use the plain `GET .../exports/...csv` URLs directly via `window.location.href` to leverage the browser's native download flow.

### 12.2 Scoping

Authorization scoping per §9.2:

| Role | Assignments | Delegates | Payments |
|---|---|---|---|
| advisor | own delegation(s) | own delegation(s) | own delegation(s) |
| staff-staffer | scoped (a) + (c) — delegations they oversee or via committee | scoped (a) | scoped (a) only — committee-only staffers (c) do not see payments |
| staff-admin | all | all | all |

A staffer attempting to export outside their scope receives `permission_denied`. A staffer with **partial** scope receives a CSV containing only the rows they're authorized to see.

### 12.3 Columns

Documented in `exports.proto` as a parallel message structure (one message per export, fields ordered to define column order). The same source of truth feeds both the CSV writer and the OpenAPI-style docs.

**`assignments.csv` columns (v1):**
`assignment_id, conference_id, delegation_id, school, delegate_id, delegate_first_name, delegate_last_name, committee_id, committee_name, position_id, position_name, status, score, reason, run_id, approved_at, approved_by`

**`delegates.csv` columns (v1):**
`delegate_id, conference_id, delegation_id, school, first_name, last_name, email, experience_level, checked_in_at`

**`payments.csv` columns (v1):**
`payment_id, conference_id, delegation_id, school, recorded_at, amount_currency, amount_units, amount_cents, kind, method, reference, notes, recorded_by`

Adding columns is a non-breaking change; removing or renaming is breaking and bumps `v2`.

---

## 13. Versioning & deprecation

- Path version is in the Protobuf package: `numun.v1`.
- **Breaking changes** require a new package (`numun.v2`) running side-by-side with `v1` for at least one full conference cycle.
- **Non-breaking changes** (adding optional fields, adding new RPCs) ship in `v1` without ceremony.
- Buf's `breaking` check in CI prevents accidental breakage of `v1`.
- Deprecation policy: an RPC marked `[deprecated = true]` in the proto stays live for one full conference cycle before removal in the next major version.

---

## 14. Observability

- Every request gets a `X-Request-Id` (UUIDv7) generated by API Gateway, propagated through Lambda logs and Sentry context.
- Every RPC handler logs an `slog` record with: RPC name, caller user id, request id, duration, response code.
- Response includes `X-Request-Id` header for client-side log correlation.
- No `/v1/ready` endpoint — Lambda has no "warm vs. cold" readiness state worth exposing.

---

## 15. Decisions log

| Decision | Choice | Reason |
|---|---|---|
| Service framework | Connect over HTTP/1.1 | gRPC contract benefits without HTTP/2 infra cost |
| Contract format | Protobuf, source-of-truth in `/api/proto/numun/v1/` | Single contract feeds Go + TS code-gen |
| Tooling | Buf (lint, breaking, gen) | Industry standard for Protobuf workflow |
| Generated code | Committed to repo | Easier review and IDE support; no runtime generation |
| URL scheme | Connect default `/numun.v1.<Service>/<Method>` | Generated clients hide URLs; no resource-style routing needed |
| IDs | UUIDv7 everywhere, including conferences | Per interview |
| Pagination | Cursor-based, default 100 / max 500, plus `:all` variants | Per interview |
| Sorting | Client-side only | DDB doesn't cheaply support arbitrary server-side sorts; sets are small |
| Error model | Connect status codes + `google.rpc.BadRequest` for validation + `numun.v1.ErrorInfo` for domain errors | Standard, debuggable |
| Validation | `protovalidate` annotations on `.proto` messages | Single source of truth |
| Optimistic locking | `expected_version` field in request body | Easier in TS than custom headers |
| Idempotency | `Idempotency-Key` header on mutating RPCs, 24h dedupe in DDB | Optional; covers retries |
| Auth | HttpOnly cookie scoped to `.numun.org`, SameSite=Strict + double-submit CSRF | Per APPLICATION.md §5 |
| Bulk ops | All-or-nothing, max 500 rows | Predictable semantics; matches DDB transaction batching |
| CSV exports | Parallel non-Connect HTTP routes (`GET /v1/exports/*.csv`) | CSV is not a Protobuf-friendly wire format |
| Versioning | Package-suffixed (`numun.v1`), no breaking changes within a major | Industry norm |
| Public surface | Dedicated `PublicService` (no auth, CORS-open, rate-limited) for build-time landing-page reads | Cleanly separates the public read surface from the authenticated portal API |

---

## 16. Open items

- **`ExportService.Export*Csv` RPC vs. direct HTTP route** — §12.1 expresses both. If the portal team finds the RPC shim adds friction without benefit, we can drop the RPC and keep only the HTTP routes. Revisit after the first portal screen ships.
- **Idempotency key storage TTL** — 24 hours is a guess. Tune after first month of usage.
- **`SearchDelegates` ranking** — current spec is prefix-only via GSI2. If real users want fuzzy search, plan the OpenSearch upgrade path (DATA_MODEL.md §9 open item).
- **OpenAPI / Swagger surface** — Buf can emit OpenAPI documents from Protobuf. Useful for non-TS consumers (e.g., curl/Postman exploration). Decide whether to generate and publish in a later iteration.
- **WebSocket / streaming surfaces** — none in v1. If a "live dashboard" need emerges later, Connect's streaming RPCs over HTTP/1.1 SSE are an option, but they require Lambda response streaming (now supported but adds complexity).
- **`PublicService.GetActiveConference` rate-limit mechanism** — resolved in M12: in-memory `golang.org/x/time/rate` bucket per Lambda env, keyed by client IP, 60 req/min. At expected traffic (one build per CMS commit) the approximate per-env model is fine. See SECURITY.md §2.10.
- **Future `PublicService` RPCs** — likely candidates if the landing page ever wants live, non-rebuild-triggered data: `ListPastConferences`, `GetConferenceStats` (counts of registered delegations, etc.). None in v1.
