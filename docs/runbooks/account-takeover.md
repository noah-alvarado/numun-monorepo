# Runbook — Account takeover (suspected)

**Status:** stub. Filled out in M12.

When a `staff-admin` suspects a user's credentials are compromised, this runbook documents how to force a password reset (`AdminResetUserPassword`), revoke active refresh tokens (`AdminUserGlobalSignOut`), and purge the user's `Session` rows from DDB. See AUTH.md §3 / §5 and SECURITY.md §4.1.
