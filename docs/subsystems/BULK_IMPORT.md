# BULK_IMPORT.md

This document specifies the **bulk delegate import** flow: how an advisor uploads a roster as CSV, XLSX, or a Google Sheets link, previews the parsed result, fixes problems inline, and commits the rows into their delegation.

It builds on [PROJECT.md](../PROJECT.md) (requirement origin), [API.md](../API.md) §11 (bulk-op envelope and all-or-nothing semantics), [APPLICATION.md](../APPLICATION.md) (Go Lambdalith stack), [DATA_MODEL.md](../DATA_MODEL.md) (Delegate entity), [INFRASTRUCTURE.md](../INFRASTRUCTURE.md) §3.6 (S3 upload bucket), and [AUTH.md](../AUTH.md) §11 (audit log).

A schema addition is required — `BulkImportPreview` cache entity — listed as a proposed amendment in §11.

---

## 1. Overview

```
┌──────────────┐     1. presign PUT          ┌────────┐
│   advisor    │ ───────────────────────────▶│ /api   │
│  (portal)    │ ◀───── presigned URL ────── └────┬───┘
│              │                                  │
│              │     2. PUT file directly         │
│              │ ───────────────────────────▶ ┌───▼──────┐
│              │                              │  S3      │
│              │                              │ uploads  │
│              │                              └──────────┘
│              │     3. preview (uploadKey)
│              │ ───────────────────────────▶ ┌───┐ parse + validate
│              │ ◀── parsed preview + ───── │API├──────────────────┐
│              │      uploadId               └─┬─┘                  │
│              │                               │              ┌────▼────┐
│              │                               │              │BulkImpor│
│              │     4. inline edits           │              │tPreview │
│              │        (local)                │              │ (30-min │
│              │                               │              │  cache) │
│              │     5. commit (uploadId,      │              └─────────┘
│              │        edited rows, mode)     │
│              │ ───────────────────────────▶  │   TransactWriteItems
│              │ ◀── created/updated/         │   into Delegate rows
│              │      deleted counts          │
└──────────────┘                              └────────────┘
```

Google Sheets path is identical except steps 1–2 are replaced by a URL field in step 3; no S3 round-trip.

---

## 2. Inputs

### 2.1 Supported formats

| Format | Extension | Source |
|---|---|---|
| CSV | `.csv` | File upload (S3 presigned PUT) |
| XLSX | `.xlsx` | File upload (S3 presigned PUT). Legacy `.xls` is **rejected** with `invalid_argument`. |
| Google Sheets | n/a | Shareable link (`https://docs.google.com/spreadsheets/d/<id>/edit...`). The sheet must be set to "Anyone with the link can view" — the portal explains this on the form. |

### 2.2 Size limits

- File size: **5 MB max**. Larger files are rejected at presign time (presigned URL is generated with a `Content-Length-Range` policy capping at 5 MB).
- Row count: **2,000 max** parsed rows per import. At NUMUN scale, the largest single delegation is ~50 delegates, so 2,000 is comfortable headroom; the cap exists to prevent accidental abuse.

### 2.3 Encoding & dialect detection (CSV)

- Encoding auto-detected; UTF-8 / UTF-8-BOM / Windows-1252 are all accepted. The parser strips a leading BOM if present and re-encodes to UTF-8 internally.
- Delimiter auto-detected from the first 4 KB; `,`, `;`, and `\t` are accepted. Detection picks the delimiter that maximizes consistent column counts across the first 10 rows.
- Quote character is `"` (RFC 4180). Embedded `"` are doubled (`""`).
- Line endings: `\n`, `\r\n`, or `\r` — all accepted.

### 2.4 XLSX & Google Sheets workbooks with multiple tabs

When the workbook has more than one tab:

- The `Preview` RPC returns the list of available tab names without parsing rows.
- The portal displays a tab picker.
- The advisor selects a tab; the portal re-invokes `Preview` with the chosen `tab_name`.

If exactly one tab exists, it's used implicitly and no picker is shown.

### 2.5 Empty / header-only / unparseable files

- Empty file → `invalid_argument`: `"file is empty"`.
- Header-only (0 data rows) → `invalid_argument`: `"file has no data rows"`.
- Cannot parse (corrupt XLSX, malformed Google Sheets URL) → `invalid_argument` with a `BadRequest` field violation identifying the problem.

---

## 3. Column schema

The bulk import accepts the following Delegate fields. Headers are normalized (lowercased, non-alphanumeric stripped) before matching, so `"First Name"`, `"first_name"`, `"firstName"`, and `"FIRST NAME"` all match the same canonical column.

| Canonical column | Required? | Accepted header aliases (normalized) |
|---|---|---|
| `firstName` | yes (or via `fullName`) | `firstname`, `givenname`, `first` |
| `lastName` | yes (or via `fullName`) | `lastname`, `surname`, `familyname`, `last` |
| `fullName` | alternative to first+last | `fullname`, `name` |
| `email` | optional | `email`, `emailaddress`, `mail` |
| `experienceLevel` | optional | `experiencelevel`, `experience`, `level`, `tier` |

### 3.1 `fullName` fallback

If neither a `firstName` column nor a `lastName` column is recognized but `fullName` is, the parser splits each value on the **last whitespace**:

- `"Mary Jane Smith"` → `firstName = "Mary Jane"`, `lastName = "Smith"`.
- `"Madonna"` → `lastName = "Madonna"`, `firstName = ""` → row error (`firstName must not be empty`).

`firstName`/`lastName` and `fullName` columns **may not both appear** in the same file; doing so triggers an `invalid_argument` ("conflicting columns: provide either fullName, or firstName+lastName, not both").

### 3.2 `experienceLevel` aliases

Case-insensitive value mapping:

| Input value | Mapped to |
|---|---|
| `novice`, `beginner`, `n` | `novice` |
| `intermediate`, `mid`, `medium`, `i` | `intermediate` |
| `advanced`, `experienced`, `senior`, `a` | `advanced` |
| (empty / missing) | `intermediate` (default) |
| anything else | row error |

### 3.3 Email normalization

- Trimmed, lowercased.
- Validated against an RFC 5322 lite regex (`^[^@\s]+@[^@\s]+\.[^@\s]+$`). Invalid → row error.
- Empty / missing → null on the Delegate row (allowed by the schema).

### 3.4 Unrecognized columns

- Silently ignored at parse time.
- The preview surfaces a count: *"3 unrecognized columns will be ignored: 'grade', 'dietary', 'tshirt'"*. This is informational, not blocking.

---

## 4. Two-step flow

### 4.1 Step 1 — Preview

`POST /v1.DelegateService/PreviewUpsertDelegatesBulk`

Request body:

```
{
  "delegation_id": "<uuid>",
  "source": one_of {
    "upload":      { "upload_key": "<s3-key>", "format": "csv" | "xlsx", "tab_name": "" },
    "google_sheet": { "url": "...", "tab_name": "" }
  }
}
```

Response body:

```
{
  "upload_id": "<uuid>",
  "available_tabs": ["Roster", "Advisors"],  // populated only when source had >1 tab and tab_name was empty
  "rows":         [PreviewRow, ...],
  "summary": {
    "parsed_count":     50,
    "valid_count":      48,
    "error_count":      2,
    "create_count":     45,
    "update_count":     3,
    "match_by_email":   25,
    "match_by_name":    23,
    "ignored_columns":  ["grade", "dietary"]
  }
}
```

`PreviewRow` shape:

```
{
  "row_number": 12,                       // 1-indexed source row, header excluded
  "input":      { "first_name": "Jane", "last_name": "Doe", "email": "jane@x.com", "experience_level": "advanced" },
  "errors":     [{ "field": "email", "message": "invalid format" }],
  "match":      one_of {
    "create":   {},
    "update":   { "existing_delegate_id": "<uuid>", "diff": { "email": { "old": "old@x.com", "new": "new@x.com" } } },
    "conflict": { "with_row_number": 7, "reason": "same email as row 7" }
  }
}
```

### 4.2 Step 2 — Commit

After the advisor reviews the preview and (optionally) inline-edits rows, the portal sends:

`POST /v1.DelegateService/UpsertDelegatesBulk`

```
{
  "upload_id":     "<uuid>",         // from Preview response
  "delegation_id": "<uuid>",
  "mode":          "additive" | "full_sync",
  "rows":          [DelegateInput, ...]   // final values, including any inline edits
}
```

Server-side commit logic:

1. **Resolve** the `BulkImportPreview` cache row by `upload_id`. Fail with `not_found` if missing/expired (advisor must re-upload).
2. **Authorize** the caller against `delegation_id` per AUTH.md §7 (`mustHaveScopeOnDelegation`).
3. **Re-validate** every `DelegateInput` against the same rules as preview. Validation failure → `invalid_argument` with row-indexed `BadRequest` violations. (Inline edits could introduce new errors.)
4. **Re-compute** matches against the current delegation roster (the cache might be stale if another advisor edited rows in the meantime).
5. **Cross-row conflict check** — same-upload duplicates → `invalid_argument`.
6. **Apply** in TransactWriteItems batches (see §6.2).
7. **Audit** — write `AuthAuditEvent` of kind `bulk_import_committed` with metadata `{ delegation_id, mode, parsed_count, create_count, update_count, soft_delete_count }`.
8. **Return** the final summary counts.

### 4.3 Preview cache TTL

The `BulkImportPreview` row carries a DDB TTL of **30 minutes** from creation. Past that, the commit RPC returns `not_found` and the advisor must re-upload. This is long enough for a careful review; short enough to keep parsed PII from lingering.

The cache row is also **deleted on successful commit** to avoid double-commits with the same `upload_id`.

### 4.4 Why server-side parsing?

Parsing CSV in the browser is trivial, but XLSX parsing requires ~500 KB of JS (e.g., SheetJS), and Google Sheets fetching needs a CORS proxy. Server-side parsing keeps the portal bundle small, centralizes validation rules, and lets auditing capture the canonical row data the server actually saw — not a possibly-different client-side parse.

---

## 5. Preview UX

The portal renders the `PreviewUpsertDelegatesBulk` response as a paginated table on `portal.numun.org/delegations/{id}/delegates/import/preview`. Sketch:

- **Summary bar:** `48 rows valid · 2 with errors · 45 new · 3 updates · matched 25 by email, 23 by name`.
- **Mode picker (radio):** `Additive (default)` / `Full sync (destructive — see below)`. Full sync displays a warning ribbon describing soft-deletion impact.
- **Table** (50 rows/page):
  - Columns: row number, first name, last name, email, experience level, match status (`new` / `update — diff` / `conflict — see row N`), errors.
  - Rows with errors are highlighted in red.
  - Each cell is inline-editable.
  - Updates show before/after for changed fields.
- **Footer:** "Confirm import" button. Disabled if any row has errors; enabled only when all rows are valid.
- **Cancel** button discards the preview (and triggers a `DeletePreview` cleanup RPC, best-effort).

Inline edits update the portal's local state only; the server is not consulted again until "Confirm import" is clicked. The commit endpoint re-validates the final state, so client-side checks are advisory.

---

## 6. Upsert semantics

### 6.1 Dedupe key

Within a single delegation, two delegates "match" if:

1. Both have a non-empty email, and the lowercased trimmed values are equal, **or**
2. Neither has an email and the normalized `firstName + " " + lastName` (lowercased, trimmed, internal whitespace collapsed to single spaces) are equal.

If one row in the upload has an email matching an existing delegate, but `firstName/lastName` differ, this is treated as **update** (the email is the dedupe key). The preview displays the diff so the advisor can spot accidental data corruption.

### 6.2 Modes

#### Additive (default)

- Rows matching existing delegates → update.
- Rows not matching any existing delegate → create.
- Existing delegates not present in the upload → **untouched**.

#### Full sync

- Same matching as additive, plus:
- Existing delegates not present in the upload → **soft-deleted** (`isDeleted = true`).

The mode is communicated to the user with a destructive-action warning whenever Full sync is selected. The summary preview surfaces the count of delegates that will be soft-deleted before commit.

### 6.3 Same-upload conflicts

If two rows in the upload match each other (e.g., both have `jane@x.com`), the preview flags both with `conflict.with_row_number` pointing to the other. The advisor must resolve before confirming (delete one, edit the email, or fix the source data).

### 6.4 Atomicity

API.md §11 says bulk endpoints are all-or-nothing. In practice:

- **Small imports** (creates + updates + soft-deletes ≤ 100 ops): a single `TransactWriteItems` call. True atomicity.
- **Large imports** (> 100 ops): the operation is split into sequential `TransactWriteItems` batches of 100 ops each. A top-level `BulkImportJob` cache row tracks `{ uploadId, totalBatches, completedBatches, status: applying|complete|failed }`. If a batch fails:
  - Status flips to `failed`.
  - The handler returns `aborted` with the batch index.
  - Already-applied changes remain in DDB (partial state). The portal surfaces this and offers a "resume" affordance that re-runs the remaining batches with the same `uploadId`.
  - True rollback of already-applied changes is **not implemented in v1** — it would require a compensating-write log and is out of scope.

At NUMUN scale (typical delegation: ≤ 50 delegates → ≤ 150 ops max for full sync), large-import path is unlikely to be exercised. Documented for honesty.

### 6.5 Hard delete vs. soft delete (Full sync)

Per DATA_MODEL.md, all entities soft-delete by setting `isDeleted = true`. Bulk import follows this rule — full sync **never** hard-deletes. A staff-admin can later hard-delete soft-deleted delegates via a separate (not bulk) admin RPC.

### 6.6 Optimistic locking interaction

Update rows produced by bulk import use the existing Delegate row's current `version` (which the server reads as part of the match computation immediately before the TransactWrite). If a concurrent edit changes the row between read and TransactWrite, the conditional write fails and the batch returns `aborted`. The portal surfaces the conflict and the advisor re-previews.

---

## 7. Sources of inputs in detail

### 7.1 CSV / XLSX file upload

1. Portal calls `POST /v1.UploadService/Presign` with `{ purpose: "bulk_delegates", filename, content_type, size }`.
2. Backend validates extension + size, generates an S3 presigned PUT URL into `numun-org-uploads` under a key like `bulk-delegates/<user_id>/<uuid>.csv`. Returns `{ url, upload_key, headers, expires_at }`. URL TTL: 10 minutes.
3. Portal `PUT`s the file directly to S3.
4. Portal calls `PreviewUpsertDelegatesBulk` with `source.upload.upload_key`.
5. Backend reads the object, parses, populates the preview cache, and returns.
6. The file stays in S3 for the 30-day lifecycle defined in INFRASTRUCTURE.md §3.6, then is auto-deleted.

### 7.2 Google Sheets

The portal accepts a `https://docs.google.com/spreadsheets/d/<sheetId>/edit...` URL. Backend:

1. Validates the URL syntactically.
2. Extracts the `sheetId` (and optional `gid` for the tab).
3. Fetches `https://docs.google.com/spreadsheets/d/<sheetId>/export?format=csv&gid=<gid>` (no auth).
4. If the response is HTTP 403 or 404 → `invalid_argument`: `"sheet is not publicly viewable — set sharing to 'Anyone with the link can view'"`.
5. If success: parse as CSV using the same parser as §2.3 and proceed.

A `gid=0` default is used if no `gid` is in the URL; the tab listing for multi-tab workbooks is fetched via the **HTML** export (`?format=html`) and parsed for `<sheet>` titles — cheap, no API key needed.

The Sheets path **never writes the file to S3.** Parsed rows go straight into the preview cache.

---

## 8. Audit & rate limiting

### 8.1 Audit events

- `bulk_import_previewed` — written on every successful preview. Metadata: `{ delegation_id, source_type, parsed_count, error_count }`. No file content captured.
- `bulk_import_committed` — written on every successful commit. Metadata: `{ delegation_id, mode, create_count, update_count, soft_delete_count, upload_id }`.

### 8.2 Rate limits

- **Per advisor:** 10 successful previews per hour, 10 successful commits per hour. Excess → `resource_exhausted`.
- Implemented with a fixed-window counter in DDB keyed by `USER#<userId>#BULK_IMPORT_HOUR#<floor(unix/3600)>` with a 1-hour TTL. One conditional `UpdateItem` per attempt; rejects when the post-increment count exceeds 10. Persistent across Lambda envs because the other rate-limit layers (per-user 300/min, per-IP) are in-memory and cannot enforce an *hourly* budget across cold starts. See SECURITY.md §2.10 for the broader rate-limit posture.

### 8.3 Abuse considerations

- File size and row count caps (§2.2) bound parse cost.
- Google Sheets fetch uses a strict 5 s HTTP timeout to prevent slow-loris on the Sheets fetcher.
- The S3 upload key is scoped to the caller's `userId`; the preview RPC validates the key prefix matches the caller before reading.
- Email content is parsed but **not logged** (PII).

---

## 9. Downloadable templates

The portal's bulk import screen shows two "Download template" links:

- `/templates/delegate-import-template.csv`
- `/templates/delegate-import-template.xlsx`

Both files are static assets served from `numun-org-assets` via `assets.numun.org`. Each contains:

- Header row with the canonical column names.
- 2 example rows showing realistic values (including one with `experienceLevel = "advanced"` and one omitted, demonstrating the default).
- A second sheet (XLSX only) with a brief "How to use this template" guide.

Templates are versioned by file content hash in the URL (`delegate-import-template.<hash>.csv`); cache headers set far-future expiration. Template changes ship through the normal landing-site deploy pipeline.

---

## 10. API surface summary

Two new RPCs on `DelegateService` (in addition to the existing `UpsertDelegatesBulk` referenced in API.md §10.6):

| RPC | Purpose |
|---|---|
| `PreviewUpsertDelegatesBulk` | Parse + validate. Returns `upload_id` + preview. No DDB writes other than the cache row + audit event. |
| `UpsertDelegatesBulk` | Commit (existing RPC, now taking `upload_id` + `rows` + `mode`). |
| `DeleteBulkImportPreview` *(housekeeping)* | Cancels a pending preview. Best-effort; the TTL handles cleanup if missed. |

And one RPC on a new `UploadService`:

| RPC | Purpose |
|---|---|
| `Presign` | Returns an S3 presigned PUT URL for the bulk-delegates upload key. |

These are reflected in `bulk_import.proto` (`numun.v1.DelegateService` + `numun.v1.UploadService`).

---

## 11. Proposed amendments to DATA_MODEL.md

### 11.1 BulkImportPreview

A short-lived cache row holding parsed rows between Preview and Commit.

| Attribute | Notes |
|---|---|
| `id` | UUIDv7 — same as `upload_id` |
| `userId` | Cognito `sub` of the advisor who created it |
| `delegationId` | Target delegation |
| `conferenceId` | (denormalized) |
| `sourceType` | `"csv"` \| `"xlsx"` \| `"google_sheet"` |
| `sourceRef` | S3 upload key (for csv/xlsx) or Sheets URL (for google_sheet) |
| `tabName` | Optional |
| `parsedRows` | JSON array of `PreviewRow` |
| `summary` | JSON summary stats |
| `createdAt` | |
| `expiresAt` | epoch-seconds; DDB TTL drops the row 30 minutes after `createdAt` |

Keys:
- PK: `BULK_IMPORT#<id>`
- SK: `META`
- TTL attribute: `expiresAt`

This entity:
- Is **not** soft-deleted (TTL handles cleanup).
- Is **not** `version`-locked (single writer; the same advisor edits between preview and commit).
- Authorization is verified at read time by comparing the row's `userId` to the caller; no GSI needed.

### 11.2 BulkImportJob (only for > 100-op imports)

When a commit splits into multiple TransactWrite batches, a tracking row is created so a partial failure can be resumed.

| Attribute | Notes |
|---|---|
| `id` | UUIDv7 |
| `uploadId` | Reference back to the originating `BulkImportPreview` |
| `userId`, `delegationId`, `conferenceId` | |
| `totalBatches` | int |
| `completedBatches` | int |
| `status` | `"applying"` \| `"complete"` \| `"failed"` |
| `lastError` | nullable string |
| `createdAt`, `updatedAt`, `expiresAt` | TTL: 7 days |

Keys:
- PK: `BULK_IMPORT_JOB#<id>`
- SK: `META`

Optional GSI lookup (not in v1): by `userId` to list a user's recent imports.

---

## 12. Decisions log

| Decision | Choice | Reason |
|---|---|---|
| Formats | CSV (UTF-8), XLSX, Google Sheets via public link | Per PROJECT.md |
| Reject `.xls` | yes | Legacy binary format; XLSX is the modern standard |
| Google Sheets access | Public-link CSV export (no Google OAuth) | Simpler; matches use case |
| Max file size | 5 MB | Headroom over expected payloads |
| Max rows per import | 2,000 | Abuse cap |
| Encoding / delimiter detection | Auto-detect (UTF-8/Win-1252; `,`/`;`/`\t`) | Excel exports are inconsistent |
| Header alias matching | Normalize + alias table | Tolerant of advisor templates |
| `fullName` fallback | Split on last whitespace | Pragmatic |
| Experience level aliases | Small fixed alias set | Tolerant of common synonyms |
| Unrecognized columns | Silently ignored, counted in summary | Don't fail on harmless extras |
| Two-step flow | Preview → Commit with server-side `upload_id` | Server is the source of truth |
| Preview cache TTL | 30 minutes | Long enough to review |
| Inline edit | Yes, in the preview | Big UX win |
| Confirm-blocked while errors exist | Yes | No partial commits |
| Dedupe key | Email if present; else first+last normalized | Pragmatic |
| Same-upload conflicts | Error | Forces human resolution |
| Modes | Additive default, Full sync opt-in destructive | Don't surprise users |
| Atomicity | TransactWriteItems; chunked beyond 100 ops with a `BulkImportJob` recovery row | Honest about DDB limits |
| Soft delete on Full sync | Yes | Matches global soft-delete rule |
| Audit | `bulk_import_previewed` + `bulk_import_committed` | Anti-abuse + forensics |
| Rate limit | 10 previews + 10 commits per hour per advisor | Modest cap |
| Templates | Downloadable CSV + XLSX from `assets.numun.org` | Self-service onboarding |
| Server-side parsing | All formats parsed in Go | Bundle size + auth consistency |
| New cache entity | `BulkImportPreview` (DDB TTL'd) | Bridges preview and commit |

---

## 13. Open items

- **Library choice for XLSX parsing** — `github.com/xuri/excelize/v2` is the leading Go library; final pick deferred to implementation.
- **`BulkImportJob` resume UX in the portal** — the data model supports resume, but the portal screen for "your last import partially failed — resume?" is unspecified.
- **Compensating rollback for partial large-import failures** — not in v1. If real failure rates make this a problem, add a write-log + reverse-apply pass.
- **Future formats** — Numbers (Apple), ODS (LibreOffice) — out of scope.
- **Future "Sheets via OAuth"** — would unlock private sheets and avoid the "make it public" friction. Adds Google Cloud project + OAuth consent. Future enhancement.
- **Future per-import diff history** — keeping a permanent record of "what was in this import" beyond audit metadata would let staff investigate "where did this delegate come from?" Likely a `BulkImportArchive` entity later.
- **Conflict resolution helpers** — when full-sync would soft-delete a checked-in delegate, the system should refuse rather than silently delete day-of attendance. Not modeled in v1; add a validation rule later.
