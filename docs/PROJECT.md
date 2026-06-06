# PROJECT.md

## Overview

Northwestern University Model United Nations (**NUMUN**) needs an online portal and public website that serves two purposes:

1. **Landing page** — communicate who NUMUN is and what it does to a mixed audience (prospective Northwestern students, high school students and faculty advisors evaluating the conference, alumni, and donors).
2. **Conference portal** — handle registration for and ongoing management of NUMUN's annual hosted conference.

This document captures **requirements only**. Technology choices (framework, database, specific CMS, auth provider, mailer) are deliberately deferred to a later technical design document, except where the requirement names a constraint (e.g., "deployed on AWS free tier").

The project replaces/improves on the current operational site at https://numun.org. There is **no deadline** — quality over speed.

---

## Audiences

| Audience | Primary need |
|---|---|
| Prospective Northwestern students | Learn what NUMUN is and how to join |
| High school students | Discover the conference NUMUN hosts |
| Faculty advisors | Register and manage a school delegation |
| Alumni & donors | Stay connected, see club activity |
| NUMUN staff (Secretariat) | Operate the conference end-to-end |

A first-time visitor should immediately recognize the site's purpose, see an overview of what's available, and reach the content relevant to them in **one click** from the landing page.

---

## Part 1: Landing Page (Public Site)

### Content
The structure and content types should mirror the current site at https://numun.org. That site is the source of truth for what sections exist and what kind of content lives in each. We are not redesigning the information architecture in this document.

### Content management
- All landing-page content must be editable by club leadership **without developer involvement**.
- Content is served and controlled via a **CMS** (not hard-coded in the application repository).
- The CMS must be **free and open source** and deployable within the **AWS free tier**.
- Specific CMS selection is deferred to the technical design doc.
- **Documentation requirement:** Because club leadership turns over annually and content updates happen multiple times per year, the CMS workflow must be **well documented** for non-technical handoff to new teams.

### Calls to action
The landing page does not have a single fixed CTA. Different visitors have different next steps; the landing page must make those next steps **visually obvious within one click**.

---

## Part 2: Conference Portal

### Conference profile
- **Frequency:** one annual conference.
- **Reference scale (most recent year):**
  - Delegations: 66
  - Advisers: 96
  - Delegates: 846

### Registration model
Delegates are **never** self-registered. The flow is:

1. **Advisor registers a delegation** on behalf of a school, including:
   - School
   - Approximate/estimated delegate count
     - Total count
     - "Financially qualifying" count (subset of total)
   - Committee preferences at the **delegation level**:
     - Crisis vs. non-crisis
     - Small / medium / large
2. **NUMUN staff reviews and approves** the delegation registration.
3. **Advisor finalizes the delegate list.** The portal must support **bulk delegate add** via:
   - CSV upload
   - XLSX upload
   - Google Sheets link
   - In all three cases, the advisor sees a **preview** of the parsed rows and confirms an **upsert** into the delegation's delegate list.

> Per-delegate preferences (individual delegate position rankings) are **out of scope**. Preferences are collected only at the delegation level.

### Payments
- The conference charges fees.
- **Payment is collected outside the platform** (no integrated payment processor).
- The platform **must track paid / unpaid status** per delegation (and balances where applicable).

### Authentication & roles

| Role | Auth | Capabilities |
|---|---|---|
| Public visitor | none | Browse landing page; download background guides |
| Advisor | email + password | Register a delegation; manage their own delegation (delegate list, view assignments, see payment status, see announcements/materials) |
| NUMUN staff (Secretariat) | email + password | View all registrations; approve/edit delegation status; review and adjust committee assignments; track payments; send communications; manage day-of logistics; issue post-conference artifacts |

- Advisors **self-register**; their delegation is then gated behind staff approval before progressing.
- **Planned future upgrade (not v1):** SSO via Google, Microsoft, and Apple.

### Conference management features

All six features below are **in scope for v1**, listed in priority order:

1. **Payment tracking** — paid/unpaid status and balances per delegation. No online collection.
2. **Committee & position assignment** — an **algorithm proposes** delegate-to-committee/country/role assignments based on delegation-level preferences and roster; Secretariat may **edit before approving**.
3. **Background guide distribution** — guides are **publicly downloadable** from the site (no per-delegation access control).
4. **Communications** — announcements to registered schools via **email**. No specific mailing tool is selected yet; that decision is deferred.
5. **Day-of-conference logistics** — check-in, attendance, awards tracking.
6. **Post-conference** — certificates, feedback surveys.

---

## Constraints

- **Cost:** run as cheaply as possible. Target the **AWS free tier** wherever feasible.
- **Domain:** already owned.
- **Hosting:** open to AWS; specifics deferred.
- **No payment processing integration** in v1 (intentional — fees handled off-platform).
- **No deadline.** This is an improvement effort over an existing operational site, not a launch under time pressure.

---

## Out of scope (v1)

- Online payment collection (Stripe, etc.).
- Per-delegate (individual) position preferences.
- Access-controlled background guides.
- SSO (Google / Microsoft / Apple) — planned as a later upgrade.
- Integrations with external tools beyond email (TBD).

---

## Decisions captured in later docs

The following items were left open in PROJECT.md and have since been resolved by the subsequent design docs:

- **CMS choice** → **Decap CMS** (git-backed, GitHub OAuth, no ongoing infra cost beyond S3+CloudFront). See [INFRASTRUCTURE.md §3.3](./INFRASTRUCTURE.md) and [CMS_CONTENT_MODEL.md](./subsystems/CMS_CONTENT_MODEL.md).
- **Email-sending mechanism** → **Amazon SES** with a dedicated `mail.numun.org` subdomain and three purpose-scoped sender addresses. See [EMAIL.md](./subsystems/EMAIL.md).
- **AWS service shape** → **Serverless** (S3 + CloudFront for static surfaces; API Gateway HTTP API + Lambda for the backend; DynamoDB on-demand). See [INFRASTRUCTURE.md §2](./INFRASTRUCTURE.md).
- **Assignment algorithm definition** → Deterministic greedy + 2-opt local search in Go, running inside the Lambdalith. See [ASSIGNMENT_ALGORITHM.md](./subsystems/ASSIGNMENT_ALGORITHM.md).
