# CMS_CONTENT_MODEL.md

This document specifies **what the landing page lets editors edit** through Decap CMS: the directory layout of `/content`, the collections and singleton files, per-field schemas and validation, media handling, the GitHub OAuth proxy required to log editors in, and how CMS content meets dynamic data (the current Conference row from DynamoDB) at build time.

It builds on:
- [PROJECT.md](../PROJECT.md) — CMS-driven landing page is a hard requirement; ongoing maintenance must be performable by non-developer editors.
- [INFRASTRUCTURE.md](../INFRASTRUCTURE.md) §3.1, §3.3, §3.6 — landing site bucket, Decap CMS bundle at `cms.numun.org`, asset bucket for files.
- [APPLICATION.md](../APPLICATION.md) §2, §6 — Astro for the site, Decap CMS as the editorial UI.
- [DATA_MODEL.md](../DATA_MODEL.md) — Conference entity (whose live data feeds the landing page at build time).

This doc is **schema and process**. The literal site copy (mission statement wording, board member bios, news posts) is not in this repo at design time — that's what the CMS exists for.

---

## 1. Scope & non-goals

### Scope
- The Decap CMS configuration (`config.yml`) shape, conceptually.
- The `/content` directory layout the CMS writes into.
- Per-collection field schemas + validation.
- Editor authentication, onboarding, and the OAuth proxy that backs it.
- How the build pipeline merges CMS content with DDB-sourced "current conference" data.

### Non-goals
- The actual site copy (a CMS exists precisely so this doc doesn't need to specify it).
- Landing-page visual design (covered by Northwestern brand tokens — see APPLICATION.md §2).
- Internationalization (English-only in v1).
- Per-section editor permissions (flat permissions in v1).

---

## 2. Repository layout for content

```
/content/
  /pages/                       — single-instance pages (one file each)
    home.md
    about.md
    hosted-conference.md
    travel-team.md
    resources.md
    contact.md
  /leadership/                  — collection: executive board members
    <year>-<role-slug>.md
  /news/                        — collection: blog-style posts (NewsPost)
    YYYY-MM-DD-<slug>.md
  /past-conferences/            — collection: completed-conference summaries
    numun-XX-<slug>.md
  /awards-archive/              — collection: curated awards highlights (v1)
    YYYY-<slug>.md
  /background-guides/           — collection: BG-guide metadata + PDF reference
    <conference-slug>/<committee-slug>.md
  /faq/                         — collection: FAQ entries
    <slug>.md
  /config/                      — single-file site-wide settings
    seo-defaults.md
    footer.md
    contact-links.md
  /uploads/                     — media (Git-committed) — see §6
  /_generated/                  — build-time-only files; never edited by humans
    active-conference.json
```

- Collection entries are individual files; Decap lists them in its admin UI.
- Single-instance pages live in `/content/pages/` and are listed in Decap's "Files" section.
- Site-wide singletons (`seo-defaults.md`, `footer.md`, `contact-links.md`) live in `/content/config/`.
- `/content/_generated/` is **never edited via the CMS or by humans**. The build pipeline writes it. It is checked into Git only for the duration of a build (or alternatively `.gitignore`d and regenerated each build — see §9).

Decap CMS distinguishes:
- **Collection (folder)** — many files under one directory, one per entry, sharing a schema.
- **Collection (files)** — a fixed set of named files (e.g., `home.md`, `about.md`), each with its own schema.

The `/content/pages/` directory is configured as a Decap "files" collection (fixed list, mixed schemas). The other directories are "folder" collections (many entries, single schema).

---

## 3. Decap CMS configuration

Conceptually (`/cms/config.yml`):

```yaml
backend:
  name: github
  repo: <github-org>/<repo-name>
  branch: main
  base_url: https://api.numun.org      # OAuth proxy host
  auth_endpoint: /cms-oauth/auth

publish_mode: simple                    # not editorial_workflow

site_url: https://numun.org
display_url: https://numun.org
logo_url: /uploads/og-default.jpg

media_folder: content/uploads
public_folder: /uploads

slug:
  encoding: ascii
  clean_accents: true
  sanitize_replacement: '-'

collections:
  # ... defined per §4 below
```

- `publish_mode: simple` means every save commits directly to `main` — **no PR-based editorial workflow** in v1.
- The GitHub backend writes commits as the authenticated editor (their own GitHub user); attribution is intact in `git log`.
- `media_folder` is in-repo; uploads land in `/content/uploads` (see §6).

---

## 4. Content types

Each subsection below lists the canonical field schema for one collection or single-page file. Field types map to Decap's widget catalog (`string`, `text`, `markdown`, `image`, `file`, `list`, `object`, `select`, `boolean`, `date`, `relation`, `number`).

Validation conventions used throughout:
- **`required: true`** unless explicitly marked optional.
- Headlines / short labels: max **100 chars**.
- Descriptions / summaries: max **200 chars**.
- Body Markdown: no hard cap (editorial responsibility).
- All images and files use the schema defined in §6.

### 4.1 Pages — single-instance files

#### 4.1.1 `home.md`

| Field | Widget | Notes |
|---|---|---|
| `hero` | object | `{ headline (string ≤100), subheadline (string ≤200), image (image), primaryCta: { label (string ≤30), href (string) }, secondaryCta (optional same shape) }` |
| `intro` | object | `{ headline (string ≤100), body (markdown) }` |
| `featuredSections` | list of object | each: `{ label (string ≤80), summary (string ≤200), image (image), ctaLabel (string ≤30), ctaHref (string) }` — typically 3–6 entries |
| `currentConferenceTeaser` | object (optional) | `{ overrideHeadline (string ≤100, optional), overrideCtaLabel (string ≤30, optional) }` — overrides defaults baked from DDB (see §9) |
| `seo` | object | per §7 |

#### 4.1.2 `about.md`

| Field | Widget | Notes |
|---|---|---|
| `title` | string | |
| `heroImage` | image | optional |
| `mission` | string (≤300) | short statement |
| `vision` | string (≤300) | short statement |
| `body` | markdown | long-form |
| `seo` | object | per §7 |

#### 4.1.3 `hosted-conference.md`

The static, editorial copy *about* NUMUN's hosted conference. Live conference data (name, dates, registration deadline) comes from DDB at build time (§9) and is rendered alongside this content.

| Field | Widget | Notes |
|---|---|---|
| `title` | string | e.g., `"The NUMUN Conference"` |
| `tagline` | string (≤200) | |
| `overviewBody` | markdown | |
| `forAdvisorsBody` | markdown | optional, audience-targeted section |
| `forDelegatesBody` | markdown | optional |
| `registrationPortalUrl` | string | default `https://portal.numun.org` |
| `seo` | object | per §7 |

#### 4.1.4 `travel-team.md`

| Field | Widget | Notes |
|---|---|---|
| `title` | string | |
| `body` | markdown | |
| `conferencesAttended` | list of object | each: `{ conferenceName, location, date (date), summary (string ≤200), highlights (list of string, optional) }` |
| `seo` | object | per §7 |

#### 4.1.5 `resources.md`

| Field | Widget | Notes |
|---|---|---|
| `title` | string | |
| `intro` | markdown | optional |
| `linkGroups` | list of object | each: `{ groupTitle (string ≤80), links: list of { label (≤80), href, description (≤200, optional) } }` |
| `seo` | object | per §7 |

#### 4.1.6 `contact.md`

| Field | Widget | Notes |
|---|---|---|
| `title` | string | |
| `body` | markdown | optional |
| `generalEmail` | string | RFC 5322 lite |
| `inquiryRouting` | list of object | each: `{ label (string ≤80), email }` — optional per-topic emails |
| `seo` | object | per §7 |

### 4.2 Leadership (folder collection)

One file per board member per academic year, under `/content/leadership/<year>-<role-slug>.md`.

| Field | Widget | Notes |
|---|---|---|
| `name` | string (≤80) | |
| `role` | string (≤80) | e.g., `"Secretary-General"` |
| `roleCategory` | select | `secretariat` / `executive-board` / `staff` |
| `year` | number | academic year start (e.g., `2025`) |
| `order` | number | for sorting within a role category |
| `headshot` | image | required, alt text required |
| `bio` | markdown | optional |
| `email` | string | optional |
| `linkedIn` | string | optional |

Default sort: `(year DESC, roleCategory, order ASC, name ASC)`.

### 4.3 News (folder collection)

One file per news post under `/content/news/YYYY-MM-DD-<slug>.md`.

| Field | Widget | Notes |
|---|---|---|
| `title` | string (≤120) | |
| `slug` | string | auto-generated from title; editable |
| `date` | datetime | required; used in URL `/news/YYYY/slug` |
| `author` | string (≤80) | optional |
| `summary` | string (≤200) | shown in lists |
| `heroImage` | image | optional |
| `body` | markdown | |
| `tags` | list of string | optional |
| `seo` | object | per §7 |

Default sort: `date DESC`.

### 4.4 Past conferences (folder collection)

One file per completed conference. NOT auto-generated from DDB Conference rows in v1 (curated narrative).

| Field | Widget | Notes |
|---|---|---|
| `conferenceName` | string (≤80) | e.g., `"NUMUN XXII"` |
| `editionNumber` | number | e.g., `22` |
| `year` | number | |
| `dateRange` | string (≤80) | e.g., `"February 15–17, 2025"` |
| `heroImage` | image | optional |
| `body` | markdown | narrative recap |
| `photoGallery` | list of object | each: `{ image, caption (string ≤200, optional) }` |
| `notableAwards` | list of object | each: `{ awardName (≤80), recipient (≤120), kind: delegate \| delegation }` |
| `seo` | object | per §7 |

Default sort: `editionNumber DESC`.

### 4.5 Awards archive (folder collection, API-synced)

As of M11 this collection is **API-synced from DDB**. The Lambdalith
authenticates as a GitHub App (`numun-cms-bot`) with `contents: write` and
commits one markdown file per Award on every `AwardService.CreateAward` /
`UpdateAward` / `DeleteAward` call. Files are named `<awardId>.md` (immutable
DDB UUID) so renames don't lose history. Manual edits via Decap are
permissible but will be overwritten on the next API write — treat the CMS UI
as a forensic / read view for awards rather than the authoritative editor.

Recipient kinds were expanded in M11 beyond `delegate` / `delegation` to also
include committees, users (covers both staff-staffer and advisor), and
conferences. A delegate-pair (two delegates sharing a dual-delegation
position) is modeled as two recipients with `kind: delegate` on the same
award.

| Field | Widget | Notes |
|---|---|---|
| `awardId` | string | DDB UUIDv7. API-managed; do not change. |
| `conferenceId` | string | API-managed. |
| `year` | number | derived from conference end-year |
| `awardName` | string (≤200) | |
| `category` | string (≤200) | optional, free-form |
| `recipients` | list of object | each: `{ kind: delegate \| delegation \| committee \| user \| conference, id (string), displayName (string, optional) }` |
| `awardedAt` | datetime | optional |
| `awardedBy` | string | optional, user id of the staffer/admin who awarded it |

Default sort on the public `/awards` page: `year DESC`, then `awardName ASC`.

### 4.6 Background guides (folder collection)

One file per `(conference, committee)` pair. Stored under `/content/background-guides/<conference-slug>/<committee-slug>.md`.

| Field | Widget | Notes |
|---|---|---|
| `committeeName` | string (≤80) | e.g., `"UNSC"` |
| `committeeType` | select | `crisis` / `non-crisis` |
| `committeeSize` | select | `small` / `medium` / `large` |
| `conferenceName` | string (≤80) | e.g., `"NUMUN XXIII"` — display label |
| `pdfFile` | file | required; the actual BG guide PDF |
| `summary` | markdown | optional, shown on the listing page |
| `updatedAt` | datetime | auto-set on save |

Background guides are **publicly readable** per PROJECT.md. The PDF lives in `/content/uploads/background-guides/<conference-slug>/<committee-slug>.pdf` and is served publicly via CloudFront (`assets.numun.org`).

The DDB `Committee.backgroundGuideRef` (DATA_MODEL.md §2.8) references the canonical content path; the relationship between CMS BG-guide entries and DDB Committee rows is **by convention, not enforced**. Staff are responsible for keeping the slug consistent.

### 4.7 FAQ (folder collection)

| Field | Widget | Notes |
|---|---|---|
| `question` | string (≤200) | |
| `answer` | markdown | |
| `category` | select | `general` / `registration` / `payment` / `day-of` / `delegates` / `advisors` |
| `order` | number | within category |

Default sort: `(category ASC, order ASC)`.

### 4.8 Site config — singleton files

#### `config/seo-defaults.md`

| Field | Widget | Notes |
|---|---|---|
| `siteTitle` | string | |
| `siteDescription` | string (≤200) | |
| `defaultOgImage` | image | |
| `twitterHandle` | string | optional, e.g., `"@numun"` |
| `themeColor` | string | hex color, used for `<meta name="theme-color">` |

#### `config/footer.md`

| Field | Widget | Notes |
|---|---|---|
| `copyrightText` | string (≤200) | e.g., `"© 2026 NUMUN"` |
| `legalLinks` | list of object | each: `{ label (≤40), href }` |
| `sponsorLogos` | list of object | each: `{ name (≤80), image, href (optional) }` |
| `acknowledgments` | markdown | optional |

#### `config/contact-links.md`

| Field | Widget | Notes |
|---|---|---|
| `socialLinks` | list of object | each: `{ platform: select(instagram\|twitter\|linkedin\|facebook\|youtube\|other), url, label (≤40, optional) }` |
| `primaryEmail` | string | |
| `mailingAddress` | string (≤300) | optional |

---

## 5. Markdown vs. structured fields

- Long-form prose (page bodies, news posts, BG guide summaries) is **Markdown**. Editors get a built-in Markdown editor with live preview.
- Repeating structured items (`featuredSections` on the home page, `linkGroups`, `photoGallery`, `legalLinks`) are **lists of objects** — fixed shape, editor adds/removes rows.
- Headlines, labels, and short summary text are **plain strings** (not Markdown). The site renderer does not parse them for Markdown syntax.

Decap renders Markdown editing with formatting toolbar, embedded images, and live preview. Editors paste images by drag-drop; Decap uploads them to `/content/uploads/` and inserts the Markdown image reference.

---

## 6. Media

### 6.1 Storage

All uploaded media is committed to the Git repo under `/content/uploads/`. PDFs (background guides) live under `/content/uploads/background-guides/<conference-slug>/`. Images live under `/content/uploads/images/` (Decap creates this on first upload).

Rationale: simplicity and a single source of truth. Migration to an S3-backed media library is a documented future option (§13).

### 6.2 Images

- **Accepted formats:** JPEG, PNG, WebP, GIF, SVG.
- **Maximum file size:** 5 MB per file (enforced by Decap config `media_library.config.max_file_size`).
- **Required alt text:** every image widget has an `alt` sub-field marked `required: true`. The site renderer fails the build if an alt is missing.
- **Recommended source size:** ≥ 1600 px wide for hero/photography. Smaller images degrade after Astro's responsive resizing.
- **Optimization:** performed at build time by Astro's `<Image>` component. Editors upload originals; the site serves optimized WebP/AVIF at responsive breakpoints. Originals remain in `/content/uploads/` for editorial reference.

### 6.3 Files (non-image)

Background guide PDFs use the same `/content/uploads/` mechanism with a `file` widget instead of `image`. Maximum size: 25 MB.

### 6.4 Asset URLs at runtime

CMS-uploaded files at `/content/uploads/<...>` are deployed to the `numun-org-site` bucket under `/uploads/<...>` (or alternatively to `numun-org-assets` for files behind `assets.numun.org`). The Astro build configures the public URL prefix; editors see `/uploads/...` in the CMS and that path resolves correctly at the deployed site.

---

## 7. SEO conventions

Every editable page or post (`home.md`, `about.md`, news posts, past-conference entries, etc.) carries an `seo` sub-object:

```
seo:
  title: string ≤ 70           # optional; falls back to page title
  description: string ≤ 200    # optional; falls back to siteDescription
  ogImage: image               # optional; falls back to defaultOgImage
  noindex: boolean             # default false
```

The Astro layout renders `<title>`, `<meta name="description">`, `<meta property="og:*">`, `<meta name="twitter:*">`, and (when `noindex: true`) `<meta name="robots" content="noindex">`.

A site-wide sitemap.xml and `/news/feed.xml` (RSS) are generated at build time from the CMS-managed collections.

---

## 8. Editor authentication & onboarding

### 8.1 Authentication

Editors log into Decap CMS at `cms.numun.org` using their **GitHub** identity. Decap's `github` backend handles the OAuth flow; the OAuth proxy described in §8.3 is required to complete it.

Editor access is granted by adding their GitHub user as a **collaborator** on the content repo. Permissions are flat (everyone can edit everything in v1).

### 8.2 Onboarding runbook (docs/cms-editor-onboarding.md — to be authored)

PROJECT.md requires this be documented for non-technical leadership turnover. The runbook covers:

1. New editor sends their GitHub username to a current admin.
2. Admin adds the GitHub user as a repo collaborator with `write` permission.
3. Editor visits `cms.numun.org`, clicks "Login with GitHub", authorizes the OAuth app on first visit.
4. Editor can immediately see + edit all collections.

The runbook also documents how to recover from common mistakes (revert a bad commit via GitHub's UI; how to undo an accidental delete; who to contact if Decap fails to load).

### 8.3 OAuth proxy (required infrastructure)

Decap CMS, when configured with the `github` backend, needs an **OAuth proxy** to complete the GitHub OAuth flow. GitHub doesn't allow the OAuth code-for-token exchange directly from the browser (the `client_secret` would be exposed). The proxy lives server-side, holds the secret, and returns the access token to the Decap browser session.

**Where it runs:** as a small set of routes on the existing Lambdalith at `api.numun.org`, **outside** the Connect router (see API.md §1). Two endpoints:

| Endpoint | Purpose |
|---|---|
| `GET /cms-oauth/auth` | Generates a random `state`, redirects the browser to `https://github.com/login/oauth/authorize?client_id=<id>&scope=repo&state=<state>&redirect_uri=https://api.numun.org/cms-oauth/callback`. Stores `state` in a short-lived signed cookie. |
| `GET /cms-oauth/callback` | Verifies `state`. Exchanges `?code=...` for an access token via `POST https://github.com/login/oauth/access_token`. Returns a small HTML page that posts the token back to the opener window via `window.opener.postMessage('authorization:github:success:{"token":"..."}', '*')` and then closes itself. |

**Secrets:**
- `GITHUB_CMS_OAUTH_CLIENT_ID` — SSM SecureString.
- `GITHUB_CMS_OAUTH_CLIENT_SECRET` — SSM SecureString.

**GitHub OAuth App setup** (one-time, documented in `docs/cms-oauth-app-setup.md`):
- Authorization callback URL: `https://api.numun.org/cms-oauth/callback`
- Homepage URL: `https://cms.numun.org`
- Requested scope: `repo` (needed for Decap to commit to the repo)

**postMessage origin lock:** the callback HTML targets `https://cms.numun.org` explicitly as the `postMessage` recipient. The `state` parameter is HMAC-signed with a server-side secret to prevent CSRF.

---

## 9. Build pipeline integration

CMS content alone produces 95% of the landing page. The remaining 5% is **live conference data** — the current active Conference row from DDB — which Astro reads at build time.

### 9.1 Active conference data flow

```
GitHub Actions ──▶ public RPC: PublicService.GetActiveConference
                       │
                       ▼
              api.numun.org (Lambdalith → DDB)
                       │
                       ▼
   /content/_generated/active-conference.json
                       │
                       ▼
              Astro build reads this file
                       │
                       ▼
       Pages reference `import data from '/content/_generated/active-conference.json'`
```

**Public RPC `PublicService.GetActiveConference`** (specified in API.md §10.1b):

- **Auth:** none. Public read.
- **CORS:** open (`Access-Control-Allow-Origin: *`).
- **Rate limit:** 60 req/min per IP. Far below any real-world build cadence.
- **Logic:** scan the Conference partition (small, ~one row per year) and return the unique row where `status ∈ {open-for-registration, in-progress}`. Returns null if none, error if more than one.
- **Response:** `{ conferenceId, name, editionNumber, year, startsAt, endsAt, registrationStatus, themeMetadata }` — a subset of the full Conference entity safe for public consumption.

### 9.2 Site build trigger conditions

The `.github/workflows/site.yml` from APPLICATION.md §8 runs when:

1. Any file under `/site/**` changes.
2. Any file under `/content/**` changes (covers all CMS commits).
3. **A manual `workflow_dispatch` button is clicked** by an admin. This is the path for "the active conference status just changed in DDB and we want the site re-rendered."

The workflow:

1. Checks out the repo.
2. Calls the active-conference RPC, writes the response to `/content/_generated/active-conference.json`. If the call fails or returns malformed data, writes a fallback file (`{ "conferenceId": null }`) so the build can still produce a "between conferences" placeholder page.
3. `pnpm install` + `pnpm --filter site build`.
4. `aws s3 sync` to `numun-org-site`.
5. CloudFront invalidation.

`/content/_generated/` is `.gitignore`d. Each build regenerates it fresh.

### 9.3 What if there's no active conference?

When the RPC returns null, the landing page renders a "between conferences — check back soon" state on pages that would otherwise show conference details. Specifically:

- `hosted-conference.md` page renders without the live data section.
- `home.md` `currentConferenceTeaser` is hidden.
- Footer and other static content render unaffected.

This is the steady state between annual conferences and must not error or break the build.

---

## 10. Validation & build-time checks

Beyond Decap's per-field validation:

- The site build script verifies that every image referenced from Markdown content has corresponding alt text (parses MDX/Markdown image syntax, errors on missing alt).
- Required SEO defaults must be present in `/content/config/seo-defaults.md` — build fails on missing.
- The footer must have at least one legal link (build fails otherwise).
- All `href` strings undergo basic URL validation; build emits warnings (not errors) for malformed URLs.

These checks live in the Astro build pipeline, not in Decap, because Decap's runtime validation can't enforce cross-file relationships.

---

## 11. Sitemap, RSS, robots

- **Sitemap:** `sitemap.xml` at site root, generated at build time by Astro's `@astrojs/sitemap` integration. Covers every Markdown file in `/content/pages/` plus every news post, past conference, awards archive entry, and BG guide listing page.
- **RSS:** `/news/feed.xml`, generated from the News collection sorted by `date DESC`, capped at the most recent 50 entries.
- **robots.txt:** generated at site root, default-allow, references the sitemap. No per-page `Disallow` rules in v1.
- **No analytics scripts** in v1 (per interview default).

---

## 12. History, rollback, versioning

- CMS content lives in Git. Every save is a commit attributable to the editor's GitHub user.
- **Rollback:** GitHub's web UI `git revert` (or a maintainer's `git revert` from the CLI), followed by an automatic re-deploy via the site workflow.
- **Diffs between versions:** GitHub's built-in compare UI is sufficient. No custom version-comparison UI in v1.
- **Branch protection:** none in v1 (`publish_mode: simple` requires direct commits to `main`). If incidents make this insufficient, switch to `editorial_workflow` (Decap supports it) at the cost of editor onboarding complexity.

---

## 13. Decisions log

| Decision | Choice | Reason |
|---|---|---|
| Content directory | `/content/` at repo root | Standard Decap convention; clear separation from `/site/` (the renderer) |
| Page architecture | Single-file pages + folder collections | Matches the natural shape of NUMUN's content |
| News naming | `NewsPost` (CMS) ≠ `Announcement` (DDB) | Different audiences; different lifecycles |
| Background guides | CMS folder collection with metadata | Richer than loose file listing |
| Past conferences / Awards archive | CMS-curated narrative entries (not DDB-derived) in v1 | Editorial voice matters; revisit when years of DDB data accrue |
| Current Conference info | DDB-sourced at build time via public RPC | Single source of truth; no editor sync drift |
| Editorial workflow | Simple mode (direct commits) | Low editor count; PR overhead unwarranted |
| Permissions | Flat (all editors equal) | Decap limitation + small team |
| Media storage | In-repo at `/content/uploads/` | Simplicity; small volume; clean URLs |
| Image optimization | Astro `<Image>` at build time | Built-in; no extra service |
| Alt text | Required, enforced at build | Accessibility floor |
| Markdown for body content | Yes | Editor-friendly + Git-clean |
| Structured fields where shape is fixed | Yes | List of objects pattern via Decap |
| SEO fields | Required per page (with site-wide defaults) | Search visibility from day one |
| OAuth proxy | Routes on existing `api.numun.org` Lambdalith (`/cms-oauth/auth`, `/cms-oauth/callback`) | One fewer Lambda to deploy; shared infra |
| Site rebuild trigger on DDB change | Manual `workflow_dispatch` button | Avoids tight coupling between DDB writes and site builds |
| Sitemap + RSS | Yes, build-time | Free SEO + subscriber value |
| Analytics | None in v1 | Privacy + simplicity |
| Localization | English only in v1 | No demand expressed |
| History UI | GitHub's built-in diff | Sufficient |

---

## 14. Open items

- **Editor onboarding runbook** (`docs/cms-editor-onboarding.md`) — PROJECT.md requirement; to be authored before the first non-developer editor is onboarded.
- **OAuth app registration runbook** (`docs/cms-oauth-app-setup.md`) — one-time GitHub setup steps for new deployments / regeneration.
- **Migrating media to S3** — if `/content/uploads/` ever bloats Git history meaningfully (likely after several years of news posts), shift to a Decap S3 media library backend. Schema unchanged; deployment + a proxy Lambda required.
- **Awards archive as DDB-derived render** — defer until enough DDB-tracked Award data exists across multiple years to justify the change.
- **Editorial workflow upgrade path** — if an unreviewed bad edit ever ships, switch `publish_mode` to `editorial_workflow` and document the PR-review flow.
- **Preview deploys** — full-component preview (a temporary CloudFront for each in-progress draft) would help editors see their changes before commit. Not in v1; revisit when content volume warrants it.
- **Per-section editor permissions** — Decap can be extended with custom plugins or hosted on top of a CMS that supports finer-grained roles. Not v1.
- **i18n** — Decap supports it; data model would need locale prefixes on every collection. Not v1.
- **Site search** — Pagefind or Algolia DocSearch can be wired in at build time when news/archive volume justifies it.
