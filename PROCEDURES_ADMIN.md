# PROCEDURES_ADMIN.md — Admin User Guide

This guide is written for **staff-admin** users of the NUMUN portal — the people who run NUMUN's annual conference end-to-end. Admins have full access to every delegation, committee, payment, assignment, and user account.

If you are a staffer with a limited scope, read [PROCEDURES.md](./PROCEDURES.md) instead.

---

## 1. Getting set up

### 1.1 Becoming an admin

Admin accounts are created by another admin (no self-sign-up). You'll receive an invite email from `cognito@mail.numun.org` with a temporary password. Follow the same first-login flow described in [PROCEDURES.md §1.1](./PROCEDURES.md).

If you are the **first** admin (bootstrapping NUMUN's portal), an engineer with AWS access creates your account directly in Cognito via the AWS CLI. This is a one-time procedure documented in `/docs/runbooks/first-admin-bootstrap.md`.

### 1.2 Multi-factor auth (strongly recommended for admins)

Admin accounts have access to all NUMUN data. Enable TOTP MFA via the runbook at `/docs/runbooks/mfa-enrollment.md`. The portal UI for MFA enrollment is planned for v1.1; until then, enrollment is via AWS CLI with assistance from an engineer.

### 1.3 Bookmarks

- https://portal.numun.org — your primary tool
- https://numun.org — public site
- https://cms.numun.org — content management (separate auth via GitHub)
- AWS Console — for emergencies via the break-glass IAM user; see `/docs/runbooks/breakglass-access.md`

---

## 2. The dashboard

The admin dashboard at https://portal.numun.org shows:

- **At-a-glance counts** — active conference's total delegations, advisors, delegates, registration status breakdown.
- **Pending approvals** — delegations awaiting your review (filterable list).
- **Recent activity** — last 30 minutes of changes (new registrations, status changes, payments).
- **Health indicators** — outstanding email bounces/complaints, in-progress assignment run if any, queued announcements.
- **Quick actions** — buttons to invite staff, send announcement, propose assignments, record payment.

---

## 3. Conference lifecycle

A Conference (the entity defined in [DATA_MODEL.md §2.1](./docs/DATA_MODEL.md)) progresses through a state machine.

### 3.1 Create a new conference

1. Navigate to **Conferences** → **Create**.
2. Provide:
   - **Name** (e.g., "NUMUN XXIV")
   - **Edition number** (e.g., 24)
   - **Year** (e.g., 2027)
   - **Start date** and **end date** (the conference event itself, not registration)
   - Optional **metadata** (theme, location notes)
3. Save. The conference is created in `draft` status — nothing public, no registrations accepted.

### 3.2 Open the conference for registration

When you're ready to accept advisor registrations:

1. Open the conference profile.
2. Change status from `draft` to `open-for-registration`.
3. **Important:** once status is `open-for-registration` (or `in-progress`), this conference becomes the **active conference**. The landing site's "current conference" panel will display its information after the next site rebuild ([CMS_CONTENT_MODEL.md §9](./docs/subsystems/CMS_CONTENT_MODEL.md)).
4. Trigger a manual site rebuild from GitHub Actions (or wait for the next CMS commit, which triggers one anyway).

### 3.3 Close registration

When the registration deadline passes:

1. Open the conference profile.
2. Change status from `open-for-registration` to `closed`.
3. Advisors can no longer create new delegations; existing pending delegations are still reviewable.

### 3.4 Conference in progress

Set status to `in-progress` on the conference's start day. This is informational; the portal does not gate behavior on this status. Check-in (§9.1) is the primary day-of action.

### 3.5 Archive a conference

After the conference ends and all post-conference work is done:

1. Open the conference profile.
2. Change status from `in-progress` to `archived`.
3. The active-conference banner on the landing site reverts to "no active conference" until the next year's conference is opened.

---

## 4. Delegation review

This is your highest-volume task during the weeks before a conference.

### 4.1 Receiving notifications

When advisors create new delegations, you receive a debounced summary email titled "New registrations received" no more than once every 15 minutes (see [EMAIL.md §7](./docs/subsystems/EMAIL.md)). The email lists up to 20 new delegations in the window.

### 4.2 Reviewing a pending delegation

1. From the dashboard, click **Pending approvals** → select a delegation.
2. Verify:
   - **School name** is recognizable and not obvious abuse.
   - **Address** is reasonable.
   - **Estimated delegate count** is sane (no extreme outliers without context).
   - **Committee preferences** are filled in (trinary positive/negative/neutral per axis).
   - **Lead advisor** is identified.
3. If everything looks good, click **Approve**. The delegation status moves from `pending` to `approved`, and an email is sent to the lead advisor and all secondary advisors.
4. If something looks off (suspicious entry, missing info), you can:
   - **Reject** with an optional reason; the advisor is emailed and may resubmit.
   - **Contact the advisor** through your usual email to clarify before deciding.

### 4.3 Managing advisors on a delegation

A delegation may have multiple advisors, with exactly one marked `lead`.

- **Add an advisor:** in the delegation profile, choose **Add advisor**. Enter their email. If they already have an advisor account, they're added directly. If not, they receive a sign-up invite.
- **Remove an advisor:** click the advisor → **Remove**. You cannot remove the only advisor; you must add a replacement first.
- **Change lead:** click the advisor you want to make lead → **Set as lead**. The previous lead becomes a secondary.

### 4.4 Assigning staffers to a delegation

To assign a staff-staffer for oversight of a specific delegation:

1. Open the delegation profile.
2. **Staffer assignments** → **Add staffer**.
3. Pick the staffer from the list of existing staff users.

You can also remove a staffer assignment from the same page. This affects what that staffer can see (PROCEDURES.md §3).

### 4.5 Soft-deleting a delegation

If a delegation is no longer valid (school withdrew, duplicate, etc.):

1. Open the delegation profile.
2. **More actions** → **Delete delegation**.
3. Confirm. The delegation is soft-deleted (`isDeleted = true`). It disappears from normal views but is recoverable.

To recover, contact an engineer — there's no admin UI for un-soft-delete in v1.

---

## 5. Committee and position setup

Committees and positions are the structure into which delegates are assigned. You configure these per conference, typically well before registration opens.

### 5.1 Creating a committee

1. **Conferences** → select the conference → **Committees** → **Create committee**.
2. Provide:
   - **Name** (e.g., "UNSC")
   - **Type** — `crisis` or `non-crisis`
   - **Size** — `small`, `medium`, or `large`
3. Save. The committee is now visible to advisors (in their preference picker) and to the assignment algorithm.

### 5.2 Creating positions in a committee

1. Open the committee profile → **Positions** → **Add position**.
2. Provide:
   - **Name** (e.g., "France")
   - **Max delegates** — usually 1; set to 2 for dual delegations.
   - **Dual delegation** — boolean. If true, **both** seats must be filled by delegates from the same school (enforced by the algorithm). `Max delegates` must be 2 if this is true.
   - **Prestige tier** — `standard` (default), `elevated` (prefer experienced delegates), or `reserved` (algorithm skips this position; you assign manually).
3. Save.

You can bulk-add positions by entering names line by line on the **Add positions** screen.

### 5.3 Editing committees and positions

Same screens, click **Edit**. Changes are subject to optimistic locking — if someone edited the same record while you were viewing, you'll see a conflict message and need to refresh.

You can soft-delete a committee or position; the algorithm ignores soft-deleted records.

### 5.4 Background guides

Background guides are **managed via the CMS** (not the portal). To upload a BG guide for a committee:

1. Sign in to the CMS at https://cms.numun.org.
2. Open **Background Guides** → **New entry**.
3. Fill in:
   - Conference name (e.g., "NUMUN XXIV")
   - Committee name (must match the committee in the portal)
   - Committee type, size
   - PDF file (upload)
   - Optional summary (Markdown)
4. Save. Within a few minutes, the BG guide is publicly available on numun.org.

In the portal, you may want to copy/paste the BG guide's path into the committee's `backgroundGuideRef` field so the portal can link to it from the committee profile.

---

## 6. Staff onboarding

### 6.1 Inviting a new staffer

1. Navigate to **Admin** → **Staff** → **Invite**.
2. Provide name, email, and role (`staff-admin` or `staff-staffer`).
3. Submit. Cognito sends an invite email with a temporary password.
4. The new staff member follows the first-login flow (set a new password).

A newly invited `staff-staffer` has no scope — they can log in but won't see any delegations or committees. You must assign them after they accept the invite (§4.4, §6.3).

### 6.2 Changing a staffer's role

To promote a staffer to admin (or demote an admin):

1. Open the user's profile.
2. **Change role** → pick the new role → confirm.
3. The change writes an `AuthAuditEvent` of kind `role_changed`. The affected user receives an email notifying them.

### 6.3 Assigning a staffer to committees

For case (c) staffer scope (oversight of a committee's assignment workflow):

1. Open the committee profile (not the staffer's profile).
2. **Staffer assignments** → **Add staffer**.
3. Pick the staffer.

Now that staffer sees the committee on their dashboard and can approve/edit proposed assignments in it.

### 6.4 Deleting a staff account

1. Open the user profile.
2. **More actions** → **Delete account**.
3. Confirm.

If the deleted user is the lead advisor on any active delegation, the system refuses the delete and prompts you to reassign first. Hard delete is permanent in Cognito; the DDB row is soft-deleted (audit trail preserved).

---

## 7. The assignment algorithm

See [ASSIGNMENT_ALGORITHM.md](./docs/subsystems/ASSIGNMENT_ALGORITHM.md) for the full design. The admin's interaction model:

### 7.1 Preparing inputs

Before running the algorithm, ensure:

- All approved delegations have a final delegate list (advisors have uploaded their rosters and confirmed).
- All committees and positions for the conference are set up (§5).
- Position `prestige tier` and `dual delegation` flags are correct.
- No active `AssignmentRun` is in progress.

### 7.2 Running the algorithm

1. **Conferences** → active conference → **Assignments** → **Propose assignments**.
2. The dialog shows summary inputs (delegation count, delegate count, position count, capacity headroom).
3. Choose:
   - **Dry run** — algorithm runs but writes nothing. You see the proposed output as JSON / table preview.
   - **Commit** — algorithm runs and persists proposed `Assignment` rows.
4. Optionally enter a **seed** (uint64). Leave blank to use the canonical seed (hash of the conference id). Use a custom seed to produce a different deterministic proposal — see [ASSIGNMENT_ALGORITHM.md §6](./docs/subsystems/ASSIGNMENT_ALGORITHM.md) ("shuffle mode").
5. Submit. The algorithm runs synchronously (typically 1–5 seconds for NUMUN's scale).
6. If it fails:
   - The error explains why (e.g., over-subscribed conference, infeasible same-school clustering).
   - You'll need to adjust inputs (add capacity, edit preferences) and retry.

### 7.3 Reviewing the proposal

After a successful commit run:

1. **Assignments** view shows every proposed assignment with a score and a reason.
2. Filter by committee, delegation, or status to triage.
3. Sort by score ascending to see the worst matches first — these are usually candidates for manual edits.

### 7.4 Editing proposed assignments

1. Click an assignment.
2. **Edit** → pick a new position from the dropdown.
3. The system enforces all hard constraints (capacity, same-school clustering cap of 2, dual-delegation school matching) and refuses moves that violate them.

### 7.5 Approving assignments

Approval **pins** an assignment so future re-runs won't modify it. There are two patterns:

- **Per-row approve** — click an assignment → **Approve**.
- **Bulk approve** — select rows via checkbox → **Approve selected**.

After approval:

- Status changes from `proposed` to `approved`.
- Future calls to the algorithm preserve the approved assignment (it's part of the pinned set).

### 7.6 Unapproving

If you need to undo an approval (perhaps to re-run the algorithm with different inputs):

1. Click the assignment.
2. **Unapprove**. Status returns to `proposed`.

### 7.7 Re-running the algorithm after edits

If you've approved some assignments and want to re-propose the rest:

1. **Propose assignments** again.
2. The new run respects all approved assignments and only re-proposes the rest.
3. You can iteratively run, edit, and approve until the entire proposal is approved.

---

## 8. Payment ledger

Payments are tracked but not collected by the platform.

### 8.1 Recording a payment

1. Open the delegation profile.
2. **Payments** → **Record payment**.
3. Provide:
   - **Amount** (USD; dollars + cents).
   - **Kind** — `payment` (incoming), `charge` (invoice), `adjustment` (correction).
   - **Method** — `check`, `wire`, `cash`, `other`.
   - **Reference** — check number, wire confirmation, etc.
   - **Notes** — free-form text.
4. Save. The ledger entry is appended, and the delegation's `balanceDue` and `paidInFull` denormalized fields update in the same transaction.
5. The lead advisor receives an automatic email confirmation.

### 8.2 Editing a payment record

You can update `notes` or `reference` after the fact, but **not** the amount or kind. To correct an amount, record an `adjustment` entry that nets the difference.

### 8.3 Deleting a payment record

Soft-delete a payment entry from the payment row's menu. The delegation's `balanceDue` recomputes in the same transaction.

### 8.4 Reviewing a delegation's balance

The delegation profile shows the current `balanceDue` and `paidInFull` flag. The Payments tab shows the full ledger sorted newest first.

---

## 9. Day-of conference

### 9.1 Check-in

1. Use **Search delegates** in the top bar to find a delegate by name.
2. Open the delegate's profile.
3. **Check in** — current timestamp is recorded.

If you check in someone in error, contact an engineer to clear the field via the AWS CLI (no admin UI to clear in v1).

### 9.2 Logistics

Day-of badge printing, room assignments, paper materials, etc. are out of scope for the portal. The portal is the canonical source for committee/position assignments — print whatever lists you need ahead of time using the export feature (§13).

---

## 10. Announcements

Email broadcasts to advisors (and optionally other audiences) are sent through the **AnnouncementService**.

### 10.1 Composing

1. **Communications** → **New announcement**.
2. Provide:
   - **Subject** (≤ 100 chars).
   - **Body** — plaintext only (no HTML). Substitutions `{{advisorName}}` and `{{delegationName}}` are available.
   - **Audience filter** — pick from preset filters (e.g., "All advisors of approved delegations in active conference", "All advisors regardless of status", etc.). Custom filters are not in v1.
3. Save as draft if you want to come back to it.

### 10.2 Preview

Before sending, click **Preview**. You'll see:

- The fully resolved recipient count (after suppression of bounced and opted-out addresses).
- A sample rendering for one recipient with substitutions applied.
- A warning if the recipient count is unusually low or zero.

### 10.3 Sending

1. Click **Send**.
2. Confirm the recipient count.
3. The announcement is persisted and one SQS message is enqueued per recipient. The actual sends happen asynchronously via the email-worker Lambda; most arrive within seconds.

Once sent, an announcement cannot be unsent. You can compose a follow-up correction.

### 10.4 Reviewing past announcements

**Communications** → **Announcements** lists all past announcements (newest first). You can open one to see its body, audience filter, and the resolved recipient list.

The portal does NOT show per-recipient delivery status (open/click tracking is not enabled in v1; bounce information shows up in the email-health admin page §11).

---

## 11. Email health

The admin email-health page surfaces users whose emails have hard-bounced or been flagged as complaints.

1. **Admin** → **Email health**.
2. View the list of suppressed users with status (`bounced` / `complained`), last feedback received, and event detail.
3. To unsuppress a user (after they've confirmed they fixed their email), click **Unsuppress**. The user can receive backend emails again. An `AuthAuditEvent` of kind `email_unsuppressed` is written.

Suppressed users still appear in the regular portal views; they just don't receive backend-initiated emails.

---

## 12. Awards (post-conference)

After the conference, you record awards in the portal.

### 12.1 Creating an award

1. **Conferences** → conference → **Awards** → **Create award**.
2. Provide:
   - **Name** (e.g., "Best Delegate").
   - **Category** — free-form (e.g., "Crisis Committee Awards").
   - **Recipients** — add one or more rows, each with kind (`delegate` or `delegation`) and the ID.
3. Save.

### 12.2 Editing an award

Open the award → **Edit**. Same field set, optimistic locking applies.

### 12.3 Awards on the public site

For v1, **public display of awards is CMS-curated**, not auto-generated from the portal. To feature a notable award on the past-conferences page:

1. Sign in to https://cms.numun.org.
2. Open **Awards Archive** → **New entry**.
3. Fill in the year, award name, recipient name, optional description.
4. Save. The award appears on the public site after the next site rebuild.

---

## 13. Data exports (CSV)

Exports are available for assignments, delegates, and payments, scoped automatically to your access level. As an admin, you see everything in the conference.

### 13.1 Export

1. Navigate to the relevant page (Assignments, Delegates, Payments).
2. **Export → CSV**.
3. The browser downloads a file like `assignments-<conferenceId>.csv`.

Column schemas are documented in [API.md §12.3](./docs/API.md). UTF-8 with BOM, CRLF line endings, Excel-compatible.

---

## 14. Audit log

Most administrative actions write an `AuthAuditEvent` row capturing who did what, when, and from where (IP + User-Agent).

### 14.1 Reviewing in v1

The portal does **not** have an audit log viewer in v1. To investigate an incident, contact an engineer who can query DynamoDB directly. Future versions may expose an admin viewer.

### 14.2 What's logged

- All sign-in successes and failures.
- All role changes, scope grants, and revocations.
- All delegation approvals, rejections, and rejections.
- All payment record additions, edits, and deletes.
- All assignment approvals, unapprovals, and manual edits.
- All bulk imports (committed counts only, never row content).
- All announcement sends.
- All award creates / modifies / deletes.
- All email unsuppressions.
- All account deletions.

Retention: 1 year (then auto-pruned via DDB TTL).

---

## 15. Emergency procedures

For specific incident classes, runbooks live under `/docs/runbooks/`:

| Incident | Runbook |
|---|---|
| Suspected account takeover | `/docs/runbooks/account-takeover.md` |
| Suspected data breach | `/docs/runbooks/data-breach-suspected.md` |
| Site defacement / bad deploy | `/docs/runbooks/site-defacement.md` |
| Email reputation collapse (mass bounces/complaints) | `/docs/runbooks/email-reputation-collapse.md` |
| DDoS / sustained API abuse | `/docs/runbooks/ddos-or-api-abuse.md` |
| Spam sign-up wave | `/docs/runbooks/sign-up-abuse.md` |
| AWS-account-level emergency | `/docs/runbooks/breakglass-access.md` |

Most runbooks call for an engineer in some step. NUMUN should have at least one engineer reachable during the registration period and the conference itself.

---

## 16. Your account

### 16.1 Your profile

**Account** menu → edit name, email, phone, password. Same flows as any user.

### 16.2 Sign out

Top-right menu → **Sign out**. The session is invalidated; refresh token is revoked.

### 16.3 If you suspect compromise

Sign out immediately. Then ask another admin to:

1. Force-reset your password via Cognito `AdminResetUserPassword`.
2. Revoke all sessions via Cognito `AdminUserGlobalSignOut`.
3. Review your `AuthAuditEvent` history for anomalies.
4. If anomalies are found, follow `/docs/runbooks/account-takeover.md`.

---

## 17. What's coming later (v1.1+)

Things admins might expect that **aren't in v1**:

- Audit-log viewer in the portal.
- Bulk approve / reject pending delegations.
- "Log out everywhere" for any user.
- Self-service MFA enrollment UI.
- Per-recipient announcement delivery status (open, click, bounce per row).
- Custom announcement audience filters.
- Award auto-generation on the public site from DDB Award entities.
- Inline conflict-resolution UI for failed bulk-imports.
- Email templates editable through the CMS instead of through code deploys.
- Bulk position-creation by CSV.
- Day-of conference room/badge/printout helpers.
- Hard-delete UI (admin currently soft-deletes; hard-delete requires an engineer).

If any of these would unblock you, flag it to the engineering team early so it can be scoped.

---

## 18. Quick reference

| Action | Where |
|---|---|
| Approve a pending delegation | Dashboard → Pending approvals |
| Record a payment | Delegation → Payments → Record payment |
| Run the assignment algorithm | Conference → Assignments → Propose assignments |
| Approve an assignment | Assignment row → Approve |
| Send an announcement | Communications → New announcement |
| Invite a new staff member | Admin → Staff → Invite |
| Create a committee | Conference → Committees → Create committee |
| Upload a background guide | https://cms.numun.org → Background Guides |
| Unsuppress a bounced email | Admin → Email health → Unsuppress |
| Export assignments to CSV | Assignments → Export |
| Check in a delegate | Search delegates → delegate → Check in |
| Manage user roles | Admin → Staff → user profile → Change role |
