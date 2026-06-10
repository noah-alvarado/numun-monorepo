# NUMUN

This repository holds the design, code, and operational docs for the **Northwestern University Model United Nations (NUMUN)** public website and conference-management portal.

## What this is

Two products in one codebase:

1. **The landing site** at `numun.org` — public-facing, content-managed by NUMUN leadership, replaces the current operational site at https://numun.org.
2. **The conference portal** at `portal.numun.org` — used by faculty advisors to register and manage school delegations, and by NUMUN staff to run the annual conference end-to-end.

Plus the supporting surfaces: an editorial CMS at `cms.numun.org` (Decap CMS), a backend API at `api.numun.org`, and CloudFront-served public assets at `assets.numun.org`.

## Project status

**Design phase complete. Implementation has not yet begun.**

This repo currently contains a comprehensive set of design documents specifying every aspect of the system. They are intended to be read as a coherent plan, and to drive implementation. Code, infrastructure templates, and CMS configuration will be added incrementally.

## Where to start

| You are… | Start here |
|---|---|
| A new developer joining the project | [DEVELOPERS.md](./DEVELOPERS.md) |
| A NUMUN **admin** running the conference | [PROCEDURES_ADMIN.md](./PROCEDURES_ADMIN.md) |
| A NUMUN **staffer** (limited scope) | [PROCEDURES.md](./PROCEDURES.md) |
| A non-technical content editor (board changes, news, photos) | `/docs/cms-editor-onboarding.md` (forthcoming) |
| Trying to understand the architecture | The design docs below in order |

## Design documents

Read in this order to build a full picture of the system:

1. **[PROJECT.md](./docs/PROJECT.md)** — the product requirements: who the audiences are, what NUMUN does, what gets built.
2. **[INFRASTRUCTURE.md](./docs/INFRASTRUCTURE.md)** — AWS services, DNS, deployment topology, cost ceiling.
3. **[APPLICATION.md](./docs/APPLICATION.md)** — frameworks, repo layout, build/deploy pipelines.
4. **[DATA_MODEL.md](./docs/DATA_MODEL.md)** — the DynamoDB single-table schema and access patterns.
5. **[API.md](./docs/API.md)** — the Connect/Protobuf API contract between portal and backend.
6. **[AUTH.md](./docs/AUTH.md)** — authentication, sessions, authorization enforcement.
7. **[SECURITY.md](./docs/SECURITY.md)** — integrative threat model, controls, incident response.

Subsystem deep-dives (read on demand):

- **[ASSIGNMENT_ALGORITHM.md](./docs/subsystems/ASSIGNMENT_ALGORITHM.md)** — how the delegate-to-committee algorithm works.
- **[BULK_IMPORT.md](./docs/subsystems/BULK_IMPORT.md)** — CSV/XLSX/Google Sheets advisor uploads.
- **[EMAIL.md](./docs/subsystems/EMAIL.md)** — outbound mail pipeline, templates, deliverability.
- **[CMS_CONTENT_MODEL.md](./docs/subsystems/CMS_CONTENT_MODEL.md)** — Decap CMS schema and editorial workflow.

Operational docs:

- **[PROCEDURES.md](./PROCEDURES.md)** — staff-staffer user guide.
- **[PROCEDURES_ADMIN.md](./PROCEDURES_ADMIN.md)** — staff-admin user guide.
- **[DEVELOPERS.md](./DEVELOPERS.md)** — developer onboarding, local dev, testing, deployment, VS Code setup.

## Repository layout (planned)

```
/                        — design docs, README
/site/                   — Astro landing site (numun.org)
/portal/                 — SolidJS portal (portal.numun.org)
/api/                    — Go backend (api.numun.org)
  /proto/numun/v1/       — Protobuf service definitions
  /cmd/api/              — Lambdalith entrypoint
  /cmd/email-worker/     — SQS consumer entrypoint
  /cmd/email-feedback/   — SES feedback handler
  /internal/             — handlers, middleware, store, domain, email
  /templates/email/      — email HTML + plaintext templates
/cms/                    — Decap CMS static admin bundle
/content/                — CMS-managed content (markdown, images, uploads)
/infra/                  — AWS SAM templates
/.github/workflows/      — GitHub Actions deploy and CI workflows
/docs/                   — runbooks, onboarding guides
```

See [APPLICATION.md §1](./docs/APPLICATION.md) for the canonical layout spec.

## Cost target

Under **$100/year** total AWS spend (see [INFRASTRUCTURE.md §6](./docs/INFRASTRUCTURE.md)). Achieved via a serverless-first stack (S3 + CloudFront, API Gateway HTTP API, Lambda, DynamoDB on-demand) that costs essentially nothing at idle.

## License

License not yet selected. To be decided before the first non-NUMUN contributor lands a PR.

## Maintainers

NUMUN's web/tech committee. Contact via the relevant staff-admin distribution list (operational; not yet defined in this repo).
