# Runbook — DDoS or API abuse

**Status:** stub. Filled out in M12.

How to tighten rate limits, optionally enable AWS WAF on the CloudFront distribution (escalation path; ~$5/mo base), and apply IP blocklists. Detection sources: CloudWatch `5xx` spikes, `AuthAuditEvent.sign_in_failed` clusters, Sentry error rate. See SECURITY.md §2.10.
