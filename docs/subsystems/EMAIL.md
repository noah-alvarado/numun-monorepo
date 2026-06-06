# EMAIL.md

This document is the source of truth for **how NUMUN sends email**: which messages are sent, who composes them, where templates live, the synchronous-vs-queued send pipeline, deliverability posture (SPF/DKIM/DMARC), bounce and complaint handling, the new-registration 15-minute debounce, and audit logging.

It builds on:
- [INFRASTRUCTURE.md §3.7](../INFRASTRUCTURE.md) — Amazon SES selected as the mailer.
- [AUTH.md](../AUTH.md) — Cognito-mediated emails (verification, password reset, staff invite).
- [API.md §10.12](../API.md) — `AnnouncementService.Send`.
- [DATA_MODEL.md §6](../DATA_MODEL.md) — the 15-min debounce mention for new-registration alerts.

Schema additions required by this document — `EmailEvent` entity, `NotificationDedupe` entity, and a new `emailStatus` field on `User` — are listed in §10 as proposed amendments to DATA_MODEL.md.

---

## 1. Catalog of outbound mail

Two origins: **Cognito** (auth flows, sent on our behalf via SES) and **the application backend** (everything else).

### 1.1 Cognito-originated (auth flows)

| ID | Trigger | Recipient | Sender |
|---|---|---|---|
| **C1** | Sign-up verification code | New advisor | `cognito@mail.numun.org` |
| **C2** | Password reset code | Any user | `cognito@mail.numun.org` |
| **C3** | Staff invite (temp password) | New staff member | `cognito@mail.numun.org` |
| **C4** | Email change verification | Any user | `cognito@mail.numun.org` |

Cognito is configured to send via SES (§4.1), not via Cognito's default low-rate sender. Templates use Cognito's defaults in v1; custom-message Lambda trigger for branding is deferred to v1.1.

### 1.2 Backend-originated transactional (system events)

Single recipient, sent synchronously inline with the triggering request handler.

| ID | Trigger | Recipient | Sender |
|---|---|---|---|
| **T1** | Delegation approved | Lead advisor + all secondary advisors of the delegation | `noreply@mail.numun.org` |
| **T2** | Delegation rejected | Same as T1; includes reason if provided | `noreply@mail.numun.org` |
| **T3** | Payment recorded | Same as T1; includes amount, kind, balance | `noreply@mail.numun.org` |
| **T4** | Bulk-import committed | The advisor who committed | `noreply@mail.numun.org` |
| **T5** | Assignment-run completed | The staffer who triggered the run | `noreply@mail.numun.org` |
| **T6** | Scope/role change | Affected user | `noreply@mail.numun.org` |

### 1.3 Backend-originated transactional (debounced staff alert)

Many possible triggers, exactly one email per 15-min window. See §7.

| ID | Trigger | Recipient | Sender |
|---|---|---|---|
| **T7** | "New registrations received" summary | All `staff-admin` users | `noreply@mail.numun.org` |

### 1.4 Human-composed (announcements)

| ID | Trigger | Recipient | Sender |
|---|---|---|---|
| **A1** | `AnnouncementService.Send` | Filterable audience (advisors of approved delegations, etc.) | `announcements@mail.numun.org` |

Announcements are the only path that goes through the **queued send pipeline** (§5).

### 1.5 Explicit v1 exclusions

Deferred to v1.1:
- Day-of conference logistics emails (check-in, badges).
- Award notifications post-conference.
- "BG guide now available" notifications.
- Pre-conference reminder emails (1 week, day-of).
- Payment receipts as a separate stream (T3 covers the in-flow case).

---

## 2. Sending domain

| Setting | Value |
|---|---|
| Sending subdomain | `mail.numun.org` (isolated from any future `@numun.org` human mailboxes) |
| Primary sender (transactional) | `noreply@mail.numun.org` |
| Announcements sender | `announcements@mail.numun.org` |
| Cognito sender | `cognito@mail.numun.org` |
| Reply-To (transactional + announcements) | `contact@numun.org` if such a mailbox exists; otherwise omitted (recipients reply at their own risk to the no-reply address — clearly stated in the email footer). |
| Bounce / complaint feedback loop | SES → SNS topic `numun-prod-email-feedback` → handler Lambda (§6). |

### 2.1 DNS records (in Route 53)

| Record | Purpose |
|---|---|
| 3× `_amazonses` CNAMEs on `mail.numun.org` | SES domain verification + DKIM (SES generates the values). |
| `mail.numun.org` TXT `"v=spf1 include:amazonses.com ~all"` | SPF authorization. |
| `_dmarc.mail.numun.org` TXT `"v=DMARC1; p=quarantine; pct=10; rua=mailto:dmarc-reports@numun.org; ruf=mailto:dmarc-forensic@numun.org; adkim=s; aspf=s"` | DMARC. Starts at 10% quarantine; tighten to `p=reject; pct=100` after watching reports for 2–4 weeks. |
| `mail.numun.org` MX (optional, only if we want to receive bounces directly) | Skipped — SES handles bounces internally via SNS. |

### 2.2 List-Unsubscribe

Announcements (A1) **only**:

```
List-Unsubscribe: <https://api.numun.org/v1/email/unsubscribe?token=...>
List-Unsubscribe-Post: List-Unsubscribe=One-Click
```

Token is a stateless HMAC-signed payload containing `{ userId, kind: "announcements" }`. Hitting the URL flips the User's `announcements_opt_in = false`. The portal exposes an admin-visible re-opt-in path.

Transactional emails (T1–T7) do **not** carry an unsubscribe header — advisors cannot opt out of "your delegation was approved."

### 2.3 SES sandbox

SES accounts begin in sandbox (can only send to verified addresses). Exiting sandbox is a **launch prerequisite** tracked in INFRASTRUCTURE.md open items.

---

## 3. Templates

### 3.1 Where templates live

`/api/templates/email/`:

```
/api/templates/email/
  _layout.html.tmpl           — shared HTML shell (Northwestern purple header, footer, inline CSS)
  _layout.txt.tmpl            — shared plaintext shell
  delegation_approved.html.tmpl    + .txt.tmpl
  delegation_rejected.html.tmpl    + .txt.tmpl
  payment_recorded.html.tmpl       + .txt.tmpl
  bulk_import_committed.html.tmpl  + .txt.tmpl
  assignment_run_completed.html.tmpl + .txt.tmpl
  scope_role_changed.html.tmpl     + .txt.tmpl
  new_registration_summary.html.tmpl + .txt.tmpl
  announcement.html.tmpl           + .txt.tmpl
```

Rendered server-side via Go's `html/template` and `text/template`. Each kind has both an HTML and a plaintext variant; SES is invoked with the `multipart/alternative` shape so clients pick the format they prefer.

### 3.2 Layout

`_layout.html.tmpl` provides:

- Inline-CSS-only Northwestern brand header (purple `#4E2A84` band; logo image hosted at `assets.numun.org`).
- Body slot.
- Inline-CSS footer with sender name (`NUMUN`), the relevant `Reply-To` address, and (for announcements only) the unsubscribe link.
- All sizing in absolute units; no media queries (most email clients ignore them).

The shared layout is included via `{{template "_layout" .}}` blocks; per-kind templates supply the `{{define "body"}}` block.

### 3.3 Substitutions

Every template receives a struct with at minimum:

```go
type TemplateData struct {
    RecipientName  string  // "Dr. Jane Smith" or just first name
    Subject        string
    NowFormatted   string  // pre-formatted timestamp in §3.5's timezone
    BrandColor     string  // "#4E2A84"
    AssetsBaseURL  string  // "https://assets.numun.org"
    PortalBaseURL  string  // "https://portal.numun.org"
    UnsubscribeURL string  // empty unless this kind supports it
    Kind           string  // identifier (matches the `kind` enum on EmailEvent)
    Vars           map[string]any // kind-specific variables (see §3.4)
}
```

### 3.4 Per-kind variables (illustrative)

| Kind | Required `Vars` keys |
|---|---|
| `delegation_approved` | `delegationName`, `conferenceName`, `portalLink` |
| `delegation_rejected` | `delegationName`, `conferenceName`, `reason` (optional) |
| `payment_recorded` | `delegationName`, `amountFormatted`, `kind`, `newBalanceFormatted`, `notes` (optional) |
| `bulk_import_committed` | `delegationName`, `createCount`, `updateCount`, `softDeleteCount`, `mode` |
| `assignment_run_completed` | `conferenceName`, `assignmentCount`, `objective`, `runLink` |
| `scope_role_changed` | `actorName`, `changeSummary` |
| `new_registration_summary` | `conferenceName`, `delegations: []{ name, school, advisorEmail, createdAt }`, `additionalCount` |
| `announcement` | `subject`, `bodyHTML`, `bodyText`, `delegationName` (optional) |

Templates that reference an undefined variable raise a render-time error → the send fails fast (better than silently sending broken mail). Tests cover every template against representative `Vars`.

### 3.5 Time formatting

All timestamps in emails render in **Central Time** with explicit `(CT)` label (e.g., `"Feb 15, 2026 at 9:42 AM CT"`). Recipient timezone is not collected in v1.

### 3.6 Localization

English only in v1.

### 3.7 Branding alignment with the landing page

Tokens used in `_layout.html.tmpl` (brand color, font stack, asset URLs) are derived from the same Northwestern brand source as the landing page (APPLICATION.md §2). When the landing page's brand tokens change, the email layout is updated in lockstep — code review enforces this until a shared module factors them out (open item §13).

---

## 4. Cognito + SES integration

### 4.1 Configure Cognito to send via SES

- Cognito user pool's email configuration is set to **`Send email with Amazon SES`**, not the default Cognito-managed sender.
- `FROM` address: `cognito@mail.numun.org` (verified in SES).
- `SourceArn`: the SES verified identity ARN for the address.
- `ReplyToEmailAddress`: `contact@numun.org` (or unset).

This raises the Cognito email send cap from 50/day (default) to the full SES throughput.

### 4.2 Cognito email content in v1

Cognito's default English templates are used unchanged. The verification email contains the 6-digit code; the password reset email contains the code; the invite email contains the temporary password.

Customizing these emails requires a **`CustomMessage` Cognito Lambda trigger**, which inserts our own templated bodies. Considered out of scope for v1 (deferred to §13).

---

## 5. Sending pipeline

Two paths converge on a shared `email.Send(ctx, req)` helper inside `/api/internal/email/`.

```
                ┌──────────────────────────┐
                │  Triggering Go handler   │
                └──────────────┬───────────┘
                               │
              ┌────────────────┴────────────────┐
              │                                 │
       Transactional                      Announcement
       (single recipient,                 (many recipients,
        sync)                              SQS-delayed)
              │                                 │
              ▼                                 ▼
     ┌──────────────────┐         ┌────────────────────────────┐
     │ email.Send(req)  │         │ AnnouncementService.Send   │
     │  • render        │         │  • enqueue 1 SQS message   │
     │  • SES SendEmail │         │    per recipient           │
     │  • write Email-  │         └─────────────┬──────────────┘
     │    Event row     │                       │
     └──────────────────┘             ┌─────────▼──────────┐
                                      │ SQS queue          │
                                      │ numun-prod-email-  │
                                      │ send (delay/retry) │
                                      └─────────┬──────────┘
                                                │
                                      ┌─────────▼──────────────┐
                                      │ EmailWorker Lambda     │
                                      │ (same Go module, diff  │
                                      │  Lambda function)      │
                                      │  • render              │
                                      │  • SES SendEmail       │
                                      │  • write EmailEvent    │
                                      └────────────────────────┘
```

### 5.1 Transactional path (T1–T6, sync)

The handler that drives the state change (e.g., `DelegationService.Approve`) calls `email.Send(ctx, req)` after the DDB write succeeds. The send writes an `EmailEvent` row regardless of outcome. If SES returns a 5xx, the handler treats the send as best-effort and does **not** roll back the state change — the DDB write is the source of truth; the email is a notification, not the state itself.

Failures produce:
- An `EmailEvent` row with `status = "failed"` and the SES error message in `failureReason`.
- A CloudWatch metric increment.
- No retry from the handler (handler is sync; SQS-driven retries would defeat sync semantics). Operators investigate via the email-health admin page (§9).

### 5.2 Debounced path (T7)

See §7 in full. Briefly: on Delegation create, the handler attempts a conditional `PutItem` on a `NotificationDedupe` row; on success it enqueues a 15-minute-delayed SQS message; the worker Lambda fires after the delay and sends the summary.

### 5.3 Announcement path (A1)

`AnnouncementService.Send`:

1. Validates the audience filter (e.g., "all advisors of approved delegations in conference X").
2. Computes the recipient list server-side (never trusts the client to specify recipients).
3. Filters out users with `emailStatus ≠ "ok"` (suppressed bounced/complained addresses) and users with `announcements_opt_in = false`.
4. Writes the `Announcement` DDB row (DATA_MODEL.md §2.13).
5. Enqueues one SQS message **per surviving recipient** to `numun-prod-email-send`. Each message carries `{ kind: "announcement", recipientUserId, announcementId, vars }`. No delay.
6. Returns to the caller. The actual send happens asynchronously in the worker.

The portal supports a **preview-before-send** flow: a separate RPC `AnnouncementService.PreviewSendAudience` returns the count of resolved recipients (after suppression filters) and a sample rendering for one recipient. Confirms staff intent before committing.

### 5.4 EmailWorker Lambda

A second Lambda function, built from the same `/api` Go module. Entry point in `/api/cmd/email-worker/main.go`. Triggered by SQS event source on `numun-prod-email-send`.

- Each invocation processes a batch (max 10 messages per SQS receive).
- For each message: render the template, call SES, write the `EmailEvent` row.
- On individual message failure: rely on SQS's built-in retry. Visibility timeout `60s`, `maxReceiveCount = 3`. After 3 attempts, message moves to the DLQ `numun-prod-email-send-dlq`.
- DLQ depth > 0 fires a CloudWatch alarm.

### 5.5 Retry policy

| Path | Retries |
|---|---|
| Transactional (sync) | None. Failures surface to operators. |
| Worker (SQS-driven) | SQS automatic redelivery up to 3 times with default visibility-timeout backoff. After 3, DLQ. |

(The earlier suggested "10s, 60s, 5min" custom backoff is replaced by SQS's standard behavior — simpler, fewer moving parts.)

### 5.6 Idempotency

Each SQS message carries a `clientToken` UUIDv7. The worker checks for an existing `EmailEvent` with that `clientToken` before sending; if present, it skips. This makes redeliveries safe.

---

## 6. Bounce & complaint handling

### 6.1 Wiring

- SES is configured to publish **Bounce**, **Complaint**, and **Delivery** notifications to SNS topic `numun-prod-email-feedback`.
- A small Lambda (`/api/cmd/email-feedback/main.go`, third entry point in the same Go module) subscribes to the topic.

### 6.2 Handling

| Notification | Action |
|---|---|
| **Hard bounce** | Set the recipient user's `emailStatus = "bounced"`. Subsequent backend sends to that user are suppressed (we never call SES for them). Portal admin page (§9) flags the user. |
| **Soft bounce** | Increment a per-user soft-bounce counter. If > 5 in 30 days, treat as hard bounce. No suppression on first occurrence. |
| **Complaint** | Set `emailStatus = "complained"`. Suppress. CloudWatch alarm fires; staff is notified. |
| **Delivery** | No action other than optional `EmailEvent.deliveredAt` update. |

Each notification writes an audit-line `EmailEvent` of kind `bounce_received` / `complaint_received` / `delivery_confirmed` for forensics. These rows are keyed by the affected `userId`; if the email was sent to an address with no corresponding User row (rare — only Cognito-originated mail to invited-but-never-confirmed accounts), the event is logged keyed by raw email address with PK `EMAIL_FEEDBACK#<emailLowercase>`.

### 6.3 Unsuppression

A `staff-admin` can unsuppress via the portal (set `emailStatus` back to `"ok"`). This is an explicit action with confirmation; we don't auto-unsuppress, even after a delivery confirmation arrives later.

---

## 7. New-registration debounce (T7)

Per DATA_MODEL.md §6: at most one "new registrations received" email per 15-minute window per conference, summarizing all delegations created in that window.

### 7.1 Mechanism

On every `Delegation` create (status defaults to `pending`):

1. The handler attempts a conditional `PutItem` on a `NotificationDedupe` row:
   - PK: `NOTIFY_DEDUPE#new-registration#<conferenceId>`
   - SK: `META`
   - `windowStartedAt`: now
   - `expiresAt`: now + 15 minutes (DDB TTL)
   - Condition: `attribute_not_exists(PK)` — only the first writer in the window succeeds.
2. If the conditional `PutItem` **succeeds**, the handler enqueues an SQS message to `numun-prod-email-send` with `DelaySeconds = 900` and payload `{ kind: "new_registration_summary", conferenceId, windowStartedAt }`.
3. If the conditional `PutItem` **fails** (row exists), the handler does nothing — another delegation already triggered the window.

### 7.2 Summary email composition

When the EmailWorker receives the delayed message after 15 minutes:

1. Queries Delegation rows created in `[windowStartedAt, windowStartedAt + 15min]` for the conference.
2. Renders `new_registration_summary` with up to 20 delegations listed; if more exist, includes `additionalCount`.
3. Resolves recipients: all `staff-admin` users (not staffers).
4. Sends one personalized email per admin (single SQS message per recipient via the worker's normal path, plus the immediate inline send shape — they converge in the same `email.Send` helper).
5. Writes `EmailEvent` rows.

The `NotificationDedupe` row expires naturally via TTL; subsequent registrations after the 15-min window will start a fresh notification cycle.

### 7.3 Edge cases

- **A delegation is created at minute 14:59** — falls into the current window, included in the summary.
- **A delegation is created at minute 15:01** — starts a new window; new summary scheduled for 15 minutes later.
- **No admins exist** — extremely unlikely. The summary is rendered but sent to nobody; an `EmailEvent.skipped` row records the no-op.
- **All admins have `emailStatus ≠ "ok"`** — same as above; logged.

---

## 8. Email audit log

A new `EmailEvent` entity (DDB) records every backend-initiated send and every feedback notification.

| Attribute | Notes |
|---|---|
| `id` | UUIDv7 |
| `userId` | Recipient (or null for `EMAIL_FEEDBACK#<email>` rows) |
| `recipientEmail` | Denormalized at send time (so historical events survive email changes) |
| `kind` | Enum (`delegation_approved`, `announcement`, `bounce_received`, etc.) |
| `subject` | Denormalized |
| `senderAddress` | Which of the three identities |
| `sesMessageId` | Returned by SES, nullable for `failed` or `skipped` |
| `status` | `sent` \| `failed` \| `skipped` \| `bounce_received` \| `complaint_received` \| `delivery_confirmed` |
| `failureReason` | Nullable string |
| `clientToken` | The SQS-message idempotency token (worker path only) |
| `sentAt` | |
| `expiresAt` | TTL: 1 year |
| `metadata` | Free-form JSON, kind-specific |

Keys:
- PK: `USER#<userId>` (matches the `AuthAuditEvent` layout) for normal sends.
- SK: `EMAIL_EVENT#<sentAt>#<id>`.
- For `EMAIL_FEEDBACK#<emailLowercase>` rows (no resolvable user), PK is the email-lowercase key with the same SK pattern.
- DDB TTL on `expiresAt`.

### 8.1 What we don't log

- Email body content (PII / scale).
- Recipient name (the recipient email is the audit anchor).
- SES open tracking pixels (not enabled in v1 — privacy-leaning + adds an external image fetch).

---

## 9. Portal admin surface

Path: `portal.numun.org/admin/email-health`. Visible to `staff-admin` only.

The page lists, as a single table:

- Every User row where `emailStatus ≠ "ok"`.
- Sorted by most-recent feedback first.
- Columns: name, email, status (`bounced` / `complained`), last feedback received, latest event detail.
- Action button per row: **Unsuppress** (with confirmation). Flips `emailStatus = "ok"` and writes an `AuthAuditEvent` of kind `email_unsuppressed`.

Backed by `EmailHealthService.ListSuppressed` (a small new RPC on the public catalog — added in passing, not a separate doc).

Read access only in v1. Auto-unsuppression on subsequent delivery confirmations is **not** implemented (per §6.3 — explicit decision).

---

## 10. Proposed amendments to DATA_MODEL.md

### 10.1 EmailEvent

New entity. Append-only audit row, TTL'd at 1 year. Schema and keys per §8.

### 10.2 NotificationDedupe

New entity. Short-lived dedupe row used by the 15-minute debounce and any future similar pattern.

| Attribute | Notes |
|---|---|
| `kind` | `"new-registration"` (extensible to other future patterns) |
| `scopeId` | The id of whatever the dedupe is scoped to (e.g., `conferenceId`) |
| `windowStartedAt` | When this window opened |
| `expiresAt` | epoch-seconds; DDB TTL drops the row when the window closes |

Keys:
- PK: `NOTIFY_DEDUPE#<kind>#<scopeId>`
- SK: `META`

Not `version`-locked (single-writer pattern via conditional `attribute_not_exists`); not soft-deleted (TTL handles).

### 10.3 User.emailStatus + User.announcementsOptIn

New fields on the existing `User` entity (DATA_MODEL.md §2.2):

- `emailStatus` — `"ok"` | `"bounced"` | `"complained"`. Default `"ok"`. Mutated only by §6 handlers or by `staff-admin` via §9.
- `announcementsOptIn` — boolean, default `true`. Mutated by the List-Unsubscribe handler (§2.2) and by an explicit `staff-admin` action.

These fields participate in optimistic locking (`version` increments on update).

### 10.4 No GSI changes

The admin "list suppressed users" query at NUMUN scale (~100 users total) is acceptable as a Scan with a `FilterExpression: emailStatus <> "ok"`. No GSI needed in v1. Revisit if user counts grow.

---

## 11. Cost & scale

| Volume estimate (annual) | Reasoning |
|---|---|
| Transactional (T1–T6): ~3,000 | Approval, payment, bulk-import, assignment-run events at NUMUN scale |
| Debounced staff alerts (T7): ~100 | At most 4/hour × peak registration days |
| Announcements (A1): ~1,500 | ~12 sends/year × ~100 recipients |
| Cognito (C1–C4): ~500 | Sign-up, password resets, staff invites |
| **Total per year** | **~5,000 — far under SES's 62,000/month free tier** |

SQS at this scale is well under the 1M-request/month free tier. SNS bounce/complaint notifications are similarly trivial.

---

## 12. Decisions log

| Decision | Choice | Reason |
|---|---|---|
| Mailer | Amazon SES | Cheapest AWS-native option |
| Sending domain | Dedicated `mail.numun.org` subdomain | Isolates app mail from any apex-domain human mail |
| Sender addresses | Three purpose-scoped no-reply addresses | Helps users & spam filters distinguish kinds |
| DKIM/SPF/DMARC | All three; DMARC starts `p=quarantine; pct=10` | Modern deliverability baseline |
| List-Unsubscribe | Announcements only | Required by 2024 bulk-sender rules; transactional mail not eligible to be opted out of |
| Templates | Go `html/template` + `text/template` in `/api/templates/email/` | Lowest setup cost; reviewable in PRs |
| Multipart | HTML + plaintext both required | Compatibility floor |
| Localization | English only in v1 | No multilingual demand expressed |
| Time format | Central Time (CT), explicit label | NUMUN is at Northwestern; no recipient TZ collected in v1 |
| Cognito mail backend | Cognito → SES (not Cognito-default) | Raises throughput cap; consistent identity & deliverability |
| Cognito template customization | Default templates in v1; `CustomMessage` Lambda deferred | Out of scope |
| Pipeline | Synchronous for transactional single-recipient; SQS for announcements + debounced staff alert | Best of both: fast UX + decoupled bulk send |
| Worker entry point | Separate `email-worker` Lambda, same Go module | Avoids mixing concerns in the API Lambdalith entrypoint |
| Retries | SQS native (3 attempts, then DLQ) | Standard, fewer custom pieces |
| Idempotency | `clientToken` on each SQS message | Safe redeliveries |
| Bounce / complaint | SES → SNS → handler Lambda → `User.emailStatus` | Standard pattern; suppression is explicit |
| Email audit | `EmailEvent` entity, 1-year TTL | Per-user searchable history |
| 15-min debounce | Conditional `PutItem` on `NotificationDedupe` + 15-min-delay SQS | At-most-one summary per window with full batch contents |
| Debounced recipients | `staff-admin` only | Per interview default |
| Admin email-health surface | `/admin/email-health`, read-only, manual unsuppress | Lightweight v1 |
| Open tracking | Not enabled | Privacy-leaning, no engagement need in v1 |
| Sandbox exit | Launch prerequisite | Standard SES onboarding step |

---

## 13. Open items

- **Custom Cognito message templates** via `CustomMessage` Lambda trigger — gives advisor-friendly branded verification/reset emails. v1.1 candidate.
- **Shared brand-tokens module** between landing-page Tailwind config and email layout — currently hand-synced. Factor out once a third surface (e.g., portal emails) needs the same tokens.
- **DMARC progression** — watch reports for 2–4 weeks at `p=quarantine; pct=10`, then move stepwise to `p=quarantine; pct=100` and eventually `p=reject; pct=100`.
- **Reply-To target mailbox** (`contact@numun.org`) — exists only if NUMUN operates a real inbox there. If not, the `Reply-To` is omitted and the email footer states explicitly that replies are not monitored.
- **Soft-bounce threshold tuning** — 5-in-30-days is a guess; tune from real bounce-rate data.
- **Open tracking** — privacy tradeoff; revisit if announcement engagement becomes a KPI.
- **Email preferences UI** — beyond the single global "announcements opt-in," advisors may eventually want per-conference or per-topic filters. Out of scope in v1.
- **`CustomMessage` trigger as the route for branded Cognito emails** — implementation work, not a design choice. Listed here because it's the natural next step.
- **Future emails (day-of logistics, awards, BG-guide release, conference reminders)** — listed in §1.5; reintroduce in v1.1 with corresponding templates.
- **Receipt PDFs attached to T3 payment-recorded mail** — currently text-only; PDF attachment would require receipt-generation infra. Future.
- **Compliance attestation** — if NUMUN ever processes data under CAN-SPAM-equivalent rules requiring physical postal address in the footer, add it to the layout. Currently the footer references `numun.org` only.
