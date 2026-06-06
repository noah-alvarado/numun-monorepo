# Runbook — SES domain identity setup

**Status:** stub. Filled out and executed once in M1.

How to verify the `mail.numun.org` SES domain identity: create the three sender identities (`noreply@`, `announcements@`, `cognito@`), add the SES-generated DKIM CNAMEs to Route 53, publish SPF (`v=spf1 include:amazonses.com ~all`) and DMARC (`p=quarantine; pct=10; …`) records, and submit the SES sandbox-exit request. See EMAIL.md §2.
