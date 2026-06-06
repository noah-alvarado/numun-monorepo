# Runbook — DNS cutover from GoDaddy to Route 53

**Status:** stub. Filled out and executed once in M1.

The `numun.org` domain stays registered at GoDaddy; DNS authority moves to Route 53. This runbook captures the exact procedure: create the Route 53 hosted zone, mirror existing GoDaddy records, point GoDaddy nameservers at the four Route 53 NS records, verify resolution with `dig`. See INFRASTRUCTURE.md §1.
