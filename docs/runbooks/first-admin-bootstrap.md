# Runbook — First-admin bootstrap

**Status:** stub. Filled out and executed once in M2.

When no `staff-admin` exists, a new portal cannot function — there is no one to invite staff or approve delegations. This runbook documents the one-time procedure to create the first `staff-admin` Cognito user via `aws cognito-idp admin-create-user`, performed under the break-glass IAM user. See SECURITY.md §7.1 and PROCEDURES_ADMIN.md §1.1.
