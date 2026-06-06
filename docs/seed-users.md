# Seed users for local development

**Status:** stub. Filled out in M2.

Because there is no real Cognito locally, `make dev` runs the API with `DEV_BYPASS_AUTH=true` and accepts an `X-Dev-User-Id` header to synthesize a session. This document will enumerate the three seed users (one advisor, one `staff-staffer` with scope on a seed delegation + a seed committee, one `staff-admin`), their seed delegation, and the exact `X-Dev-User-Id` values to use. Populated by `make seed`.
