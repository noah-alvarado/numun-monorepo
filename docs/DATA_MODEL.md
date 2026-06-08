# DATA_MODEL.md

This document defines the **DynamoDB data model** for the NUMUN portal: entities, key design, indexes, and how each access pattern is served. Application architecture is in [APPLICATION.md](./APPLICATION.md); infrastructure in [INFRASTRUCTURE.md](./INFRASTRUCTURE.md); product requirements in [PROJECT.md](./PROJECT.md); the assignment algorithm that consumes and produces several of these entities is in [ASSIGNMENT_ALGORITHM.md](./subsystems/ASSIGNMENT_ALGORITHM.md); auth/session entities are detailed further in [AUTH.md](./AUTH.md); bulk import cache entities are detailed in [BULK_IMPORT.md](./subsystems/BULK_IMPORT.md); email audit and debounce entities are detailed in [EMAIL.md](./subsystems/EMAIL.md).

---

## 1. Approach

- **Single-table design.** One DynamoDB table — `numun-prod` — holds every conference-scoped entity. Rationale: the entities are deeply interconnected, transactional writes across entities benefit from one table, and a single table simplifies IAM, metrics, and IaC. The cost of single-table design is upfront schema discipline; the cost of multi-table at NUMUN's scale would be operational overhead with no offsetting benefit.
- **Multi-year scoping.** Every conference-scoped entity carries a `conferenceId` (UUIDv7) referencing one `Conference` row. Human-readable identification (e.g., `"NUMUN XXIII"`) lives on the `name` attribute of the Conference, not in the ID.
- **Globally unique IDs.** All entity IDs are UUIDv7 (sortable, time-prefixed) — Conference IDs included.
- **Soft delete.** Every entity carries `isDeleted: bool`. Application code filters it out on read.
- **Optimistic locking.** Every entity carries `version: number`. All writes use a `ConditionExpression: version = :expected` and increment.
- **Indexes:** **two** global secondary indexes, `GSI1` and `GSI2`. See §5.

### A note on GSI cost

NUMUN's scale (~1,000 items per conference, ~5,000 items across a decade of history, ~860 writes/year for delegate creation) keeps both base table and GSIs comfortably inside the DynamoDB always-free tier. Two GSIs is a readability/maintenance choice, not a cost-driven one.

---

## 2. Entities

| Entity | Conference-scoped? | Soft-delete | Versioned |
|---|---|---|---|
| Conference | n/a (it *is* the scope) | yes | yes |
| User | no (global) | yes | yes |
| DelegationAdvisor (link) | implicit via parent Delegation | yes | yes |
| StaffDelegationAssignment (link) | implicit | yes | yes |
| StaffCommitteeAssignment (link) | implicit | yes | yes |
| Delegation | yes | yes | yes |
| Delegate | yes | yes | yes |
| Committee | yes | yes | yes |
| Position | yes (via parent Committee) | yes | yes |
| Assignment (Delegate ↔ Position) | yes | yes | yes |
| AssignmentRun | yes | yes | yes |
| PaymentRecord (ledger entry) | yes (via parent Delegation) | yes | yes |
| Announcement | optional (may be global) | yes | yes |
| Award | yes | yes | yes |
| Session | no (global; scoped to one User) | n/a (TTL'd) | n/a |
| AuthAuditEvent | no (global; scoped to one User) | n/a (TTL'd, append-only) | n/a |
| BulkImportPreview | implicit (via target Delegation) | n/a (TTL'd, 30 min) | n/a |
| BulkImportJob | implicit (via target Delegation) | n/a (TTL'd, 7 days) | n/a |
| EmailEvent | no (global; scoped to recipient User or raw email) | n/a (TTL'd, append-only) | n/a |
| NotificationDedupe | implicit (via scope id, e.g., conference) | n/a (TTL'd, single-writer) | n/a |

### 2.1 Conference

Top-level entity. All others (except User and optionally Announcement) reference one Conference.

- `id` — UUIDv7
- `name` — human-readable display name, e.g., `NUMUN XXIII`
- `editionNumber` — integer; the conference's edition (e.g., `23` for NUMUN XXIII)
- `year` — integer; the calendar year the conference is held in
- `startsAt`, `endsAt` — ISO timestamps
- `status` — `draft` | `open-for-registration` | `closed` | `in-progress` | `archived`
- `metadata` — free-form (theme, location, etc.)

`name`, `editionNumber`, and `year` are display/sort fields. The ID is UUIDv7 and is the only thing referenced from foreign keys.

### 2.2 User

Profile data for both advisors and staff. The Cognito user pool holds credentials and roles (`advisor`, `staff-admin`, `staff-staffer`). This entity holds app-level profile fields. Identified by the Cognito `sub` claim.

- `id` — Cognito `sub` (UUID)
- `role` — `advisor` | `staff-admin` | `staff-staffer` (mirrored from Cognito for queryability)
- `email` — string (required for all roles)
- `name` — string (required for advisors, optional for staff)
- `phone` — string (required for advisors, optional for staff)
- `emailStatus` — `"ok"` | `"bounced"` | `"complained"`; default `"ok"`. Mutated by the SES feedback handler (EMAIL.md §6) or by a `staff-admin` via the email-health admin page. Sends to users where `emailStatus ≠ "ok"` are suppressed.
- `announcementsOptIn` — boolean; default `true`. Toggled by the List-Unsubscribe handler (EMAIL.md §2.2) or by `staff-admin`. Transactional mail ignores this flag; only announcements honor it.

Validation of "required for advisors / optional for staff" lives in the application layer, not the table. `emailStatus` and `announcementsOptIn` participate in the standard `version` optimistic-lock pattern.

### 2.3 Delegation

A school's registration for one Conference. May have **multiple advisors**; **at least one** must hold `lead` status (multiple leads are allowed); **at least one** advisor is required (enforced at application layer).

- `id` — UUIDv7
- `conferenceId`
- `school` — string
- `address` — `{ street, city, state, postalCode, country }`
- `status` — `pending` | `approved` | `rejected`
- `estimatedDelegates` — `{ total: number, financiallyQualifying: number }`
- `committeePreferences` — **trinary** per axis; consumed by the assignment algorithm (see ASSIGNMENT_ALGORITHM.md §4.1):
  ```
  {
    type: {
      crisis:    "positive" | "negative" | "neutral",
      nonCrisis: "positive" | "negative" | "neutral",
    },
    size: {
      small:  "positive" | "negative" | "neutral",
      medium: "positive" | "negative" | "neutral",
      large:  "positive" | "negative" | "neutral",
    },
  }
  ```
- `balanceDue` — number (denormalized convenience; authoritative ledger is in PaymentRecord items)
- `paidInFull` — bool (denormalized; computed from ledger by application)
- `approvedAt`, `approvedBy` — timestamps + userId

### 2.4 Delegate

A student in a Delegation. **No login** — pure data record.

- `id` — UUIDv7
- `conferenceId`
- `delegationId`
- `firstName`, `lastName`
- `email` (optional)
- `experienceLevel` — `"novice" | "intermediate" | "advanced"`; default `"intermediate"`. Consumed by the assignment algorithm.
- `checkedInAt` (optional) — set day-of

### 2.5 DelegationAdvisor (link User ↔ Delegation)

Many-to-many between Users (advisor role) and Delegations.

- `userId`, `delegationId`, `conferenceId`
- `role` — `lead` | `secondary`

### 2.6 StaffDelegationAssignment (link User ↔ Delegation)

Per PROJECT.md staffer-scope case (a): staffers explicitly assigned to oversee specific delegations.

- `userId`, `delegationId`, `conferenceId`

### 2.7 StaffCommitteeAssignment (link User ↔ Committee)

Per PROJECT.md staffer-scope case (c): staffers tied to specific committees. Their effective delegation visibility is *delegations whose delegates are assigned to those committees* — computed at query time in v1.

- `userId`, `committeeId`, `conferenceId`

### 2.8 Committee

- `id` — UUIDv7
- `conferenceId`
- `name` — e.g., `UNSC`
- `type` — `crisis` | `non-crisis`
- `size` — `small` | `medium` | `large`
- `backgroundGuideRef` — **path** to a CMS-managed file under `/content/background-guides/...` (no S3 key stored here; the URL is constructed by the site/portal from the content path).

Background guides themselves are **CMS-managed files**, not DynamoDB rows — see §7.1.

### 2.9 Position

A country/role within a Committee.

- `id` — UUIDv7
- `conferenceId`
- `committeeId`
- `name` — e.g., `France`
- `maxDelegates` — number, default `1` (per PROJECT.md, multiple delegates may share a position; default is one).
- `dualDelegation` — bool, default `false`. When `true`, both seats must be filled by delegates from the **same school** (enforced by the algorithm — see ASSIGNMENT_ALGORITHM.md H4). Application also enforces: if `dualDelegation == true`, `maxDelegates` must equal `2`.
- `prestigeTier` — `"standard" | "elevated" | "reserved"`, default `"standard"`. `reserved` positions are skipped by the algorithm and must be assigned manually.

### 2.10 Assignment (Delegate ↔ Position)

A delegate's placement on a position. Multiple Assignments may point at one Position (when `maxDelegates > 1`).

- `id` — UUIDv7
- `conferenceId`
- `delegateId`
- `positionId`
- `committeeId` (denormalized for filter)
- `delegationId` (denormalized for filter)
- `status` — `proposed` | `approved`
- `proposedAt`, `approvedAt`, `approvedBy`
- `runId` — UUIDv7 reference to the `AssignmentRun` that produced this proposal (null only for assignments created manually outside an algorithm run)
- `score` — float; per-assignment objective score at proposal time (see ASSIGNMENT_ALGORITHM.md §4.2)
- `reason` — short human-readable explanation of why this delegate landed on this position

### 2.11 AssignmentRun

A record of one execution of the assignment algorithm. Inputs hash, seed, status, summary stats, and outcome — see ASSIGNMENT_ALGORITHM.md §5–6 for behavior.

- `id` — UUIDv7
- `conferenceId`
- `seed` — uint64; the RNG seed used for this run
- `runOrdinal` — sequence within the conference (1, 2, 3, ...)
- `isCanonical` — bool; `true` for the first run with the canonical seed (`hash(conferenceId)`)
- `triggeredBy` — userId
- `triggeredAt`, `completedAt` — timestamps
- `status` — `running` | `done` | `failed`
- `objective` — float; final `O` value if `done`
- `assignmentCount` — number of Assignment items produced
- `inputsHash` — sha256 of normalized inputs (delegations, delegates, committees, positions, pinned set)
- `diagnostics` — nullable string; populated if `status = failed`

### 2.12 PaymentRecord (ledger entry)

Append-only ledger of payments against a Delegation. Balance is derived by summing the ledger; `Delegation.balanceDue` / `paidInFull` are denormalized caches updated transactionally on insert.

- `id` — UUIDv7
- `conferenceId`
- `delegationId`
- `amount` — number (positive = credit; negative = invoice/charge entries also allowed)
- `kind` — `charge` | `payment` | `adjustment`
- `method` — `check` | `wire` | `cash` | `other`
- `reference` — string (check #, etc.)
- `notes`
- `recordedBy` — userId
- `recordedAt` — timestamp

### 2.13 Announcement

- `id` — UUIDv7
- `conferenceId` (nullable — set for conference-scoped announcements, null for global)
- `subject`, `body`
- `audienceFilter` — JSON describing recipients (e.g., `{ scope: "advisors", status: "approved" }`)
- `sentAt`, `sentBy`

No per-recipient delivery tracking in v1.

### 2.14 Award

A recognition that may be applied to one or more delegates and/or delegations.

- `id` — UUIDv7
- `conferenceId`
- `name` — e.g., `Best Delegate`, `Outstanding Delegation`
- `category` — free-form
- `recipients` — array of `{ kind: "delegate" | "delegation", id: string }`
- `awardedAt`, `awardedBy`

### 2.15 Session

Server-side session row. The opaque session ID is the value placed in the `numun_session` browser cookie. See AUTH.md §5 for the request-time lifecycle and §13.1 for the full attribute list.

- `id` — UUIDv7; the opaque session id in the cookie
- `userId` — Cognito `sub`
- `refreshToken`, `cachedAccessToken` — Cognito tokens (encrypted at rest)
- `cachedAccessTokenExpiresAt` — when to refresh against Cognito
- `csrfToken` — the current CSRF token for this session
- `ip`, `userAgent` — first-seen client info
- `createdAt`, `lastUsedAt` — `lastUsedAt` is best-effort updated on each request
- `expiresAt` — epoch-seconds; DDB TTL drops the row automatically

This entity is not `version`-locked and is not soft-deleted — concurrent updates from the same browser are not a realistic threat, and TTL handles cleanup.

### 2.16 AuthAuditEvent

Append-only auth audit log. See AUTH.md §11 for the full kind enum and the retention policy.

- `id` — UUIDv7
- `userId` — subject of the event
- `actorUserId` — performer (often equal to `userId`)
- `kind` — e.g., `sign_in_succeeded`, `sign_in_failed`, `password_reset_completed`, `role_changed`, `scope_granted`, `account_deleted` (full list in AUTH.md §11)
- `ip`, `userAgent`
- `occurredAt`
- `metadata` — free-form JSON for kind-specific fields
- `expiresAt` — epoch-seconds; DDB TTL drops the row after **1 year**

Append-only; no update path. Not `version`-locked, not soft-deleted.

### 2.17 BulkImportPreview

Short-lived cache row that bridges the two-step bulk-import flow. See BULK_IMPORT.md §4 and §11.1 for the full lifecycle.

- `id` — UUIDv7; the same value is returned to the client as `upload_id`
- `userId` — Cognito `sub` of the advisor who created the preview
- `delegationId` — target delegation
- `conferenceId` — denormalized
- `sourceType` — `"csv"` | `"xlsx"` | `"google_sheet"`
- `sourceRef` — S3 upload key (`csv`/`xlsx`) or Sheets URL (`google_sheet`)
- `tabName` — optional, for multi-tab workbooks
- `parsedRows` — JSON array of `PreviewRow` (see BULK_IMPORT.md §4.1)
- `summary` — JSON summary stats
- `createdAt`
- `expiresAt` — epoch-seconds; DDB TTL drops the row 30 minutes after `createdAt`. Also explicitly deleted on successful commit.

Not `version`-locked (single writer) and not soft-deleted (TTL handles cleanup). Authorization at read time compares `userId` to the caller.

### 2.18 BulkImportJob

Recovery / progress tracker created only when a single bulk import exceeds DDB's 100-op transaction limit and must split across batches. See BULK_IMPORT.md §6.4 and §11.2.

- `id` — UUIDv7
- `uploadId` — reference back to the originating `BulkImportPreview`
- `userId`, `delegationId`, `conferenceId`
- `totalBatches` — int
- `completedBatches` — int
- `status` — `"applying"` | `"complete"` | `"failed"`
- `lastError` — nullable string
- `createdAt`, `updatedAt`
- `expiresAt` — epoch-seconds; 7-day TTL

Not `version`-locked (single writer is the commit handler) and not soft-deleted.

### 2.19 EmailEvent

Append-only audit row for backend-initiated email sends and SES feedback notifications. See EMAIL.md §8 for full attribute list and §6 for feedback-event semantics.

- `id` — UUIDv7
- `userId` — recipient User id (may be null for `EMAIL_FEEDBACK#<emailLowercase>` rows where no User exists)
- `recipientEmail` — denormalized at send time so historical events survive email changes
- `kind` — enum (`delegation_approved`, `payment_recorded`, `announcement`, `new_registration_summary`, `bounce_received`, `complaint_received`, `delivery_confirmed`, etc.)
- `subject` — denormalized
- `senderAddress` — which of the three sender identities
- `sesMessageId` — returned by SES; nullable for `failed` / `skipped`
- `status` — `sent` | `failed` | `skipped` | `bounce_received` | `complaint_received` | `delivery_confirmed`
- `failureReason` — nullable string
- `clientToken` — UUIDv7 idempotency token (worker path only)
- `sentAt`
- `expiresAt` — epoch-seconds; DDB TTL drops the row after **1 year**
- `metadata` — kind-specific free-form JSON

Append-only; not `version`-locked; not soft-deleted.

### 2.20 NotificationDedupe

Short-lived dedupe row used by the 15-minute new-registration debounce (EMAIL.md §7) and extensible to any future similar pattern.

- `kind` — string, e.g., `"new-registration"`
- `scopeId` — id of whatever the dedupe is scoped to (e.g., `conferenceId`)
- `windowStartedAt` — when this window opened
- `expiresAt` — epoch-seconds; DDB TTL drops the row when the window closes

Single-writer pattern (the conditional `PutItem` with `attribute_not_exists` ensures at-most-one creator per window); not `version`-locked; not soft-deleted.

---

## 3. Common attributes (every item)

| Attribute | Type | Purpose |
|---|---|---|
| `PK`, `SK` | string | Primary key (see §4) |
| `entity` | string | Entity discriminator, e.g., `"Delegation"` |
| `id` | string | Entity ID (UUIDv7 or conference key) |
| `conferenceId` | string \| null | Multi-year scope |
| `isDeleted` | bool | Soft-delete flag (default `false`) |
| `version` | number | Optimistic-lock counter (starts at 1) |
| `createdAt`, `updatedAt` | string (ISO 8601) | Audit timestamps |
| `createdBy`, `updatedBy` | string | userId references |
| `GSI1PK`, `GSI1SK`, `GSI2PK`, `GSI2SK` | string (sparse) | Index keys, set only on items that need them |

All writes use:
```
ConditionExpression: attribute_not_exists(PK) OR (version = :expectedVersion AND isDeleted = :false)
UpdateExpression: ... SET version = :newVersion, updatedAt = :now
```

---

## 4. Primary key design (PK / SK)

| Entity | PK | SK |
|---|---|---|
| Conference | `CONF#<conferenceId>` | `META` |
| User | `USER#<userId>` | `PROFILE` |
| Delegation | `CONF#<conferenceId>` | `DELEGATION#<delegationId>` |
| Delegate | `DELEGATION#<delegationId>` | `DELEGATE#<delegateId>` |
| DelegationAdvisor | `DELEGATION#<delegationId>` | `ADVISOR#<userId>` |
| StaffDelegationAssignment | `DELEGATION#<delegationId>` | `STAFF#<userId>` |
| StaffCommitteeAssignment | `COMMITTEE#<committeeId>` | `STAFF#<userId>` |
| Committee | `CONF#<conferenceId>` | `COMMITTEE#<committeeId>` |
| Position | `COMMITTEE#<committeeId>` | `POSITION#<positionId>` |
| Assignment | `POSITION#<positionId>` | `DELEGATE#<delegateId>` |
| AssignmentRun | `CONF#<conferenceId>` | `ASSIGNMENT_RUN#<triggeredAt>#<id>` |
| PaymentRecord | `DELEGATION#<delegationId>` | `PAYMENT#<recordedAt>#<id>` |
| Announcement | `CONF#<conferenceId>` *or* `GLOBAL` | `ANNOUNCEMENT#<sentAt>#<id>` |
| Award | `CONF#<conferenceId>` | `AWARD#<id>` |
| Session | `SESSION#<sessionId>` | `META` |
| AuthAuditEvent | `USER#<userId>` | `AUTH_EVENT#<occurredAt>#<id>` |
| BulkImportPreview | `BULK_IMPORT#<id>` | `META` |
| BulkImportJob | `BULK_IMPORT_JOB#<id>` | `META` |
| EmailEvent (user-anchored) | `USER#<userId>` | `EMAIL_EVENT#<sentAt>#<id>` |
| EmailEvent (orphaned recipient) | `EMAIL_FEEDBACK#<emailLowercase>` | `EMAIL_EVENT#<sentAt>#<id>` |
| NotificationDedupe | `NOTIFY_DEDUPE#<kind>#<scopeId>` | `META` |

### Why these choices

- **`CONF#<conferenceId>` as PK for conference-scoped top-level entities** lets a single Query retrieve *all* delegations, committees, awards, or announcements for one conference using `SK begins_with` filters. Partition size at NUMUN scale (~1,000 items) is far below the 10 GB / 3,000 RCU limit.
- **`DELEGATION#<delegationId>` as PK for items "under" a delegation** (delegates, advisors, staff assignments, payment ledger entries) lets a single Query retrieve a delegation's full child set in one call.
- **`POSITION#<positionId>` as PK for Assignment** because the dominant query is "who is on this position?" and positions have low fanout (typically 1 delegate per position).
- **`SESSION#<sessionId>` as its own partition** isolates session lookups and lets DDB TTL drop expired rows without disturbing user data.
- **`USER#<userId>` as PK for `AuthAuditEvent`** co-locates a user's audit history under the same partition as their `User` profile, so "show me a user's recent auth activity" is a single Query.
- **`BULK_IMPORT#<id>` and `BULK_IMPORT_JOB#<id>` as their own partitions** keep short-lived import cache rows from cluttering long-lived entity partitions; both rely on DDB TTL for cleanup.
- **`USER#<userId>` as PK for `EmailEvent`** co-locates a user's email history under their `User` partition, mirroring the `AuthAuditEvent` layout, so "show me every email sent to this user" is a single Query. The `EMAIL_FEEDBACK#<emailLowercase>` variant handles the rare case where a bounce arrives for an address that has no resolvable User (mostly Cognito-originated mail to invited-but-not-yet-confirmed accounts).
- **`NOTIFY_DEDUPE#<kind>#<scopeId>` as its own partition** isolates the at-most-one-writer conditional `PutItem` from any other write pattern; DDB TTL handles cleanup when the dedupe window closes.

---

## 5. Global secondary indexes

### GSI1 — "by user / by conference status / by delegate"

A general-purpose reverse-lookup index. Used for several patterns; each item that needs alternate access populates `GSI1PK` / `GSI1SK`.

| Use case | Item type | GSI1PK | GSI1SK |
|---|---|---|---|
| List a user's delegations (as advisor) | DelegationAdvisor | `USER#<userId>` | `ADVISES#<conferenceId>#<delegationId>` |
| List a staffer's assigned delegations | StaffDelegationAssignment | `USER#<userId>` | `OVERSEES#<conferenceId>#<delegationId>` |
| List a staffer's assigned committees | StaffCommitteeAssignment | `USER#<userId>` | `CHAIRS#<conferenceId>#<committeeId>` |
| Find a delegate's assignment | Assignment | `DELEGATE#<delegateId>` | `ASSIGNED_TO#<positionId>` |
| Find a delegation's assignments | Assignment | `DELEGATION#<delegationId>` | `ASSIGNED#<positionId>#<delegateId>` |

A single Query on `GSI1PK = USER#<userId>` returns all of a user's role-bindings in one call; the SK prefix (`ADVISES` / `OVERSEES` / `CHAIRS`) lets the application route appropriately.

### GSI2 — "by status / by name search"

Status-filtered listings and bounded prefix search.

| Use case | Item type | GSI2PK | GSI2SK |
|---|---|---|---|
| List delegations by status (staff dashboard) | Delegation | `CONF#<conferenceId>#DELEGATION_STATUS#<status>` | `<delegationId>` |
| List all delegates across delegations in a conference, by name | Delegate | `CONF#<conferenceId>#DELEGATE_NAME` | `<lastNameLower>#<firstNameLower>#<delegateId>` |
| List announcements (newest first) | Announcement | `CONF#<conferenceId>#ANNOUNCEMENT` *or* `GLOBAL#ANNOUNCEMENT` | `<sentAt>#<id>` |
| Find an assignment run by status (e.g., the in-flight `running` run) | AssignmentRun | `CONF#<conferenceId>#ASSIGNMENT_RUN_STATUS#<status>` | `<triggeredAt>#<id>` |

`GSI2SK` for delegate name uses lowercased last+first names. Supports `begins_with` prefix search; full fuzzy search is **not in scope** (Scan-with-filter is acceptable fallback at NUMUN scale).

### Index attribute projections

- `GSI1`: **KEYS_ONLY** — application re-fetches items from the base table when full attributes are needed. Keeps GSI storage minimal.
- `GSI2`: **INCLUDE** a small attribute set (`entity`, `status`, `school` or `firstName`+`lastName`, `updatedAt`) — supports listing UIs without follow-up reads.

---

## 6. Access pattern mapping

| # | Pattern | Operation |
|---|---|---|
| A1 | Get my delegation by my user id | Query `GSI1` PK=`USER#<userId>` SK begins_with `ADVISES#` → resolve `delegationId`s → BatchGetItem on `Delegation` rows |
| A2 | List delegates in my delegation | Query base PK=`DELEGATION#<id>` SK begins_with `DELEGATE#` |
| A3 | Bulk upsert delegates | `BatchWriteItem` of Delegate rows under PK=`DELEGATION#<id>` (in chunks of 25). Each write conditional on `attribute_not_exists` or matching `version` |
| A4 | View my delegation's assignments | Query `GSI1` PK=`DELEGATION#<id>` SK begins_with `ASSIGNED#` |
| A5 | View announcements | Query `GSI2` PK=`CONF#<id>#ANNOUNCEMENT` (newest first via `ScanIndexForward: false`) |
| S1 | List all delegations by status | Query `GSI2` PK=`CONF#<id>#DELEGATION_STATUS#<status>` |
| S2 | Get delegation + its delegates | Query base PK=`DELEGATION#<id>` (returns delegation if cached separately, plus all children); OR GetItem on Delegation + Query on PK=`DELEGATION#<id>` SK begins_with `DELEGATE#` |
| S3 | Approve / reject a delegation | UpdateItem on Delegation (conditional on version); if approving, also TransactWrite to update `GSI2PK` status entry |
| S4 | Update payment / append ledger entry | TransactWrite: PutItem PaymentRecord + UpdateItem Delegation (`balanceDue`, `paidInFull`, version) |
| S5 | List committees + positions | Query base PK=`CONF#<id>` SK begins_with `COMMITTEE#` → for each, Query base PK=`COMMITTEE#<id>` SK begins_with `POSITION#` (or a single conference-scoped scan in the algorithm's hot path) |
| S6 | Create / edit committee or position | PutItem / UpdateItem |
| S7 | Run the assignment algorithm | Read-side load: Query base PK=`CONF#<id>` for delegations + committees; Query each `DELEGATION#<id>` for delegates and preferences; check `GSI2` for any `running` `AssignmentRun` (reject duplicate). Algorithm runs in-memory in the Lambda. See ASSIGNMENT_ALGORITHM.md. |
| S8 | Persist proposed assignments | TransactWriteItems (up to 100 items per transaction) — for larger batches, chunk across multiple transactions. Each Assignment item populates `GSI1` for delegate- and delegation-side reverse lookup. AssignmentRun record written alongside. |
| S9 | Edit a proposed assignment | UpdateItem (conditional on version) |
| S10 | Approve final assignments | UpdateItem in bulk; sets `status = approved` and `approvedAt`/`approvedBy`. Unmark-approval is the reverse: UpdateItem conditional on `status = approved`, flips back to `proposed`. |
| S11 | Send announcement | PutItem Announcement; trigger SES send asynchronously via a separate handler (no per-recipient row written in v1) |
| S12 | Day-of check-in | UpdateItem on Delegate setting `checkedInAt` |
| S13 | Record award | PutItem Award |
| AU1 | Get user profile by Cognito sub | GetItem PK=`USER#<sub>` SK=`PROFILE` |
| Staff scope (a) | Staffer's overseen delegations | Query `GSI1` PK=`USER#<userId>` SK begins_with `OVERSEES#` |
| Staff scope (c) | Staffer's committees → delegations through committee | Query `GSI1` PK=`USER#<userId>` SK begins_with `CHAIRS#` → for each committee, Query `COMMITTEE#<id>` for positions → for each position, Query base PK=`POSITION#<id>` for Assignments → resolve `delegationId` (denormalized on Assignment) → BatchGetItem on Delegations. Acceptable at NUMUN scale; revisit if it becomes hot. |
| New-registration notification | Email staff about new pending Delegation | Application logic: on Delegation create, write an item to a notification dedupe key with 15-min TTL and only send SES email if no recent dedupe entry exists. Out-of-band of the DDB schema; mentioned here for completeness. |

---

## 7. Cross-cutting concerns

### 7.1 Files (not in DynamoDB)

- **Background guides** — CMS-managed files. Path stored on Committee as `backgroundGuideRef`. Public via CloudFront at `assets.numun.org`.
- **Advisor CSV/XLSX uploads** — Private S3 (`numun-org-uploads`); presigned URL for upload; Lambda parses; rows become Delegate items via A3; original file deleted via S3 lifecycle (30 days).
- **Generated certificates** — Future. Stored in `numun-org-artifacts`; metadata referenced on Award or as a new entity later.

### 7.2 Payment ledger consistency

A PaymentRecord insert and its Delegation balance update happen in a single **TransactWriteItems** call so the two cannot diverge. If `balanceDue` ever needs reconciliation, recompute by summing the ledger and overwrite.

### 7.3 Soft delete

`isDeleted = true` removes an item from application-visible queries but preserves history. All Queries include a `FilterExpression: isDeleted = :false`. Note that DynamoDB applies filters **after** the read, so RCU is unaffected — acceptable at our scale, but worth noting if read volume ever climbs.

### 7.4 Optimistic locking

`version` starts at 1 on insert. Every UpdateItem / TransactWrite increments and asserts the prior value via `ConditionExpression`. Failed conditional writes surface as `ConditionalCheckFailedException` and the application returns HTTP 409 to the client.

### 7.5 ID strategy

- All entity IDs (Conference included) are **UUIDv7** — sortable by creation time and collision-free across distributed writers. Generated server-side in Go via a UUIDv7 library; clients never assign IDs.
- Human-readable conference identification (e.g., `"NUMUN XXIII"`, `2025`, edition `23`) lives in display fields on the Conference row (`name`, `editionNumber`, `year`), not in the ID.

### 7.6 Streams (forward-looking)

DynamoDB Streams **not enabled in v1**. Easy to enable later for triggers (e.g., post-write announcement dispatch, search indexing). Mentioned to signal it's a deliberate "off" rather than missed.

---

## 8. Decisions log

| Decision | Choice | Reason |
|---|---|---|
| Table topology | Single table `numun-prod` | Interconnected entities; simpler ops |
| Multi-year scope | `conferenceId` on every conference-scoped item; `CONF#<id>` in PK where it's the natural parent | Supports history; partitions stay small |
| Indexes | 2 GSIs (`GSI1` reverse-lookup, `GSI2` status/search) | Sufficient; cost negligible at scale |
| IDs | UUIDv7 (server-generated) | Sortable, time-prefixed, distributed-safe |
| Soft delete | `isDeleted` flag, filtered on read | Cheap, reversible |
| Optimistic lock | `version` attribute on every entity | User-requested; collisions plausible during assignment edits |
| Payment ledger | Append-only PaymentRecord + denormalized cache on Delegation | Flexibility (history) + fast reads (cache) |
| Background guides | CMS-managed, referenced by path | Aligns with PROJECT.md |
| Committees / positions | Portal-managed (DDB entities) | Algorithm needs direct access |
| Awards | Single Award entity with recipients array | Recipients may be delegates and/or delegations |
| Staffer scope | Both (a) explicit delegation assignment and (c) committee assignment supported via two link entities | Per requirements |
| Streams | Off in v1 | Not needed yet |
| Delegation committee preferences | Trinary (`positive`/`negative`/`neutral`) per type and size axis | Required by the assignment algorithm |
| Delegate experience | New field `experienceLevel` | Algorithm input |
| Position prestige & duals | New `prestigeTier` and `dualDelegation` fields | Algorithm constraints |
| AssignmentRun entity | Captures seed, status, inputs hash, outcome | Reproducibility and concurrency control for the algorithm |
| Assignment enrichment | Added `score`, `reason`, `runId` | Explainability of algorithm output |
| Session entity | Server-side opaque-id sessions with TTL | Browser never sees Cognito refresh tokens; bounded XSS blast radius |
| AuthAuditEvent entity | Append-only auth audit log with 1-year TTL | Investigations + future compliance posture |
| Common attributes exceptions | `Session`, `AuthAuditEvent`, `BulkImportPreview`, `BulkImportJob` skip `version` and `isDeleted` | TTL + append-only / single-writer patterns make those fields unneeded |
| BulkImportPreview entity | 30-min TTL cache bridging Preview→Commit in the bulk-import flow | Server is source of truth for parsed rows; avoids re-parsing on commit |
| BulkImportJob entity | 7-day TTL recovery row when an import exceeds DDB's 100-op transaction limit | Honest handling of large-import partial failure |
| EmailEvent entity | Append-only 1-year TTL audit row co-located under `USER#<userId>` (with `EMAIL_FEEDBACK#<email>` variant for orphan bounces) | Per-recipient history + forensics |
| NotificationDedupe entity | Short-lived single-writer row underlying the 15-min new-registration debounce | At-most-one notification per window via conditional `PutItem` + TTL |
| User additions | `emailStatus` and `announcementsOptIn` fields on existing User entity | Suppress sends to bounced/complained recipients; honor List-Unsubscribe on announcements |
| Common-attributes exceptions (continued) | `EmailEvent` and `NotificationDedupe` also skip `version` and `isDeleted` | Append-only and TTL'd-single-writer patterns make those fields unneeded |

---

## 9. Open items

- **Search beyond `begins_with`** — full-text fuzzy search on delegate names is not designed; Scan-with-filter is acceptable at current scale. If volumes grow, an external index (OpenSearch / Algolia / Meilisearch) would be the upgrade path.
- **GSI2 INCLUDE projection contents** — final attribute list per use case to be tuned once the UI is built.
- **Notification dedupe** — the 15-min debounce mechanism is sketched in §6; the implementation choice (a separate `Notification` item type with TTL vs. an SSM parameter vs. an external store) is deferred to the application code design.
- **Account / data retention** — no archival policy defined. Conferences are stored indefinitely; revisit if storage projections change.
