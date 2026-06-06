# PROCEDURES.md — Staffer User Guide

This guide is written for **staff-staffer** users of the NUMUN portal. If you are a NUMUN admin (full access), read [PROCEDURES_ADMIN.md](./PROCEDURES_ADMIN.md) instead.

A **staffer** is a NUMUN staff member with a defined, limited scope:

- **By delegation** — you've been assigned to oversee specific school delegations as their primary contact and support.
- **By committee** — you've been assigned to chair or co-chair specific committees, and can see the delegations whose delegates are in those committees.

You will only see and act on the delegations and committees in your scope. If something you expect to see isn't there, contact a NUMUN admin to update your assignments.

---

## 1. Getting set up

### 1.1 Your account

A NUMUN admin creates your account. You'll receive an email from `cognito@mail.numun.org` titled something like *"Welcome to NUMUN"* with a temporary password.

1. Visit https://portal.numun.org/sign-in.
2. Enter your email and the temporary password.
3. You'll be prompted to set a new password. Choose one with at least 12 characters, including a lowercase letter and a digit.
4. After you set the password, you're logged in.

If you don't receive the invite within a few minutes, check spam, then ask the admin who invited you to resend.

### 1.2 Optional: Multi-factor auth

For v1, the portal does not have an MFA enrollment screen. If you want MFA on your account (recommended for any staff role), ask an admin — they can guide you through a one-time setup via the AWS CLI.

### 1.3 Bookmarks

Bookmark:

- https://portal.numun.org — your everyday entry point
- https://numun.org — the public site (helpful when checking what advisors see)

---

## 2. The dashboard

When you sign in, you land on the dashboard. As a staffer, you see:

- **Your delegations** — the schools assigned directly to you (oversight assignment).
- **Your committees** — the committees you've been assigned to chair or co-chair.
- **Delegations in your committees** — derived view: schools that have delegates currently assigned to one of your committees.
- **Announcements** — the most recent broadcasts NUMUN staff have sent to advisors and staff.

If your dashboard is empty, you have no scope yet. Contact a NUMUN admin to assign you.

---

## 3. Working with your assigned delegations

For each delegation in your scope, you can:

- **View the delegation profile** — school, address, advisors (lead + secondary), estimated delegate counts, committee preferences, registration status, payment status.
- **View the delegate roster** — full list of confirmed delegates with their experience level.
- **View payment history** — the ledger of payments recorded against the delegation (read-only for you; only admins can record payments).
- **View assignment status** — which positions the delegation's delegates have been assigned to (read-only).

### 3.1 What you cannot do

As a staffer, you **cannot**:

- Approve or reject a delegation registration (admin only).
- Edit a delegation's profile fields, preferences, or status.
- Modify the delegate roster (advisors and admins do this).
- Record or edit payments.
- Add or remove advisors on a delegation.
- Send announcements.

If any of those is needed, contact an admin.

### 3.2 When to take action

You are typically the point of contact when an advisor has a question, runs into a problem with the portal, or needs guidance on the registration flow. Common situations:

- **An advisor can't log in** — verify the email matches the one on the delegation; if needed, ask an admin to reset the password.
- **An advisor uploaded a CSV that failed parsing** — open the delegate roster to see what successfully imported, then advise them on fixing the file (see [BULK_IMPORT.md §3](./docs/subsystems/BULK_IMPORT.md) for column rules).
- **A delegation's payment status is wrong** — flag to an admin, since you can't edit it yourself.

---

## 4. Working with your assigned committees

For each committee in your scope, you can:

- **View the committee profile** — name, type (crisis / non-crisis), size, list of positions.
- **View position assignments** — for each position in the committee, see who's currently assigned.
- **Approve a proposed assignment** (within your committee's scope) — flips an assignment's status from `proposed` to `approved`, pinning it.
- **Unapprove an assignment** — reverses an approval, returning it to `proposed`.
- **Manually edit a proposed assignment** — change which delegate is in which position (only for assignments in your committee's positions, only while status is `proposed`).

Approval flow in detail in §5.

### 4.1 What you cannot do

- Create new committees or positions (admin only).
- Edit committee or position properties (name, type, size, prestige tier, dual-delegation flag).
- Run the assignment algorithm itself (admin only).

---

## 5. The assignment workflow (your part)

NUMUN's assignment algorithm proposes a placement of every delegate into a position. The algorithm runs once or several times before the conference. As a committee staffer, your role is:

### 5.1 After the algorithm runs

1. An admin runs the assignment algorithm. You'll receive an email from `noreply@mail.numun.org` titled "Assignment proposal ready" (if you're scoped to a committee that received proposed assignments).
2. Open the **Assignments view** on your dashboard, filtered to your committees.
3. Review each proposed assignment. Each row shows:
   - The delegate (name, school, experience level)
   - The position (country/role)
   - The committee (which of yours)
   - A **score** indicating how well the match aligns with the delegation's preferences (higher is better)
   - A **reason** explaining the algorithm's choice in plain language

### 5.2 Editing a proposed assignment

If you want to swap a delegate into a different position within your committees:

1. Click the assignment row.
2. Choose **Edit** — a position-picker appears showing available positions in your committees with capacity.
3. Pick the new position. If the swap would violate a hard constraint (max 2 delegates per school per committee, dual-delegation school match, etc.), the portal explains why and refuses.
4. Confirm. The status remains `proposed`.

### 5.3 Approving an assignment

When you're satisfied with a proposed assignment:

1. Click the assignment row.
2. Choose **Approve**.
3. The status flips to `approved`, and the assignment is **pinned** — future runs of the assignment algorithm will not change it.

### 5.4 Unapproving an assignment

If you approve in error or change your mind:

1. Click the assignment row.
2. Choose **Unapprove**.
3. The status flips back to `proposed`. Future algorithm runs may move this delegate.

### 5.5 Best practices

- Approve in batches. Don't approve one-by-one as proposals come in; let the admin re-run the algorithm if many edits are needed, then approve the batch.
- If you want a particular delegate locked into a particular position, edit *and* approve in sequence. Approve alone keeps the current proposed pair pinned.
- Communicate with the admin about your committee's needs before they run the algorithm — they can adjust position preferences or even the prestige tier of specific positions ahead of time.

---

## 6. Day-of conference

When the conference is in progress, your day-of duties may include:

### 6.1 Check-in

Each delegate has a `checkedInAt` field. When a delegate physically arrives, check them in:

1. Open the **Delegate roster** for the delegation (or use the search to find a delegate by name).
2. Click the delegate.
3. Choose **Check in**. The current timestamp is recorded.

Once checked in, the field cannot be undone via the portal in v1; contact an admin if a mistake was made.

### 6.2 Attendance throughout the conference

The portal does not track per-session attendance in v1. Attendance is the responsibility of in-room committee staff using paper or whatever tools your committee chooses.

### 6.3 Awards

Awards are recorded by admins post-conference. As a staffer, you may submit award recommendations via your usual NUMUN communication channels; the portal does not have a "submit recommendation" workflow in v1.

---

## 7. Reading announcements

NUMUN admins send announcements to advisors (e.g., "Registration deadline reminder," "Background guides are now available"). You see these on your dashboard under **Announcements**.

You cannot reply through the portal. If an advisor responds to an announcement and copies you, handle the response through your usual email.

You cannot send announcements yourself; this is an admin-only action.

---

## 8. Your account

### 8.1 Update your profile

- **Email** — change via the **Account** menu. You'll be sent a verification code to the new address; enter it to confirm.
- **Phone** — optional, edit in the **Account** menu.
- **Password** — change via **Account → Change password**. Same 12-character minimum policy applies.

### 8.2 Sign out

Top-right menu → **Sign out**. The session is invalidated immediately, and your refresh token is revoked.

If you suspect your account has been compromised (someone else has your password, or you see activity you didn't perform), sign out and immediately notify a NUMUN admin so they can force-reset your password and revoke all sessions.

### 8.3 Self-service deletion

Self-service account deletion is **not available** in v1. Contact a NUMUN admin if you need your account removed.

---

## 9. Getting help

| Question | Where to go |
|---|---|
| "I can't sign in" | Confirm the email; if still stuck, ask an admin to reset your password. |
| "I expected to see a delegation, but it's missing" | Your scope may not include it. Ask an admin to check your `StaffDelegationAssignment` / `StaffCommitteeAssignment` records. |
| "An advisor is asking about X" | Refer to the relevant section here or to the public site at numun.org. For payment, registration approval, or account questions, route to an admin. |
| "Something looks broken" | Take a screenshot of the URL and any error message, then send to a NUMUN admin. They have the audit log access needed to investigate. |
| "How does the assignment algorithm decide?" | See [ASSIGNMENT_ALGORITHM.md §4](./docs/subsystems/ASSIGNMENT_ALGORITHM.md) for the scoring rules. The short version: fairness across delegations first, then spreading delegations across committees, then type preference, then size preference, then committee balance. |

---

## 10. What's coming later (v1.1+)

These are noted so you know what's *not* in the current release:

- "Log out everywhere" — currently sign-out only affects the current browser.
- Self-service MFA enrollment in the portal UI.
- Per-session attendance tracking.
- Awards-recommendation submission.
- Inactivity-based auto-logout.
- A staffer notifications inbox (currently announcements appear inline on the dashboard only).

If any of these become urgent for your team, raise it with the admins so it can be scoped.
