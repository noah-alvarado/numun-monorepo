# Runbook — DDoS or API abuse

Operator playbook for when traffic against `api.numun.org` or the CloudFront-fronted static surfaces looks abusive. The layers already in place (CloudFront edge cache, API Gateway account-level throttle, in-memory rate limits in `api/internal/middleware/ratelimit`, the bulk-import DDB counter, the $10/mo billing alarm) absorb the vast majority of bad traffic. This runbook is the escalation path when they don't.

**Read first:** [`../SECURITY.md`](../SECURITY.md) §2.10 (rate limits), §3.7 (alarms), §6 (abuse threats). Related: [`./sign-up-abuse.md`](./sign-up-abuse.md), [`./email-reputation-collapse.md`](./email-reputation-collapse.md).

> **Most important guidance — read before doing anything.**
> The annual conference happens at a single physical venue. Hundreds of advisors, delegates, and staff egress through one or two NATted IPs simultaneously during registration day, day-of check-in, and swap windows. **A spike of authenticated traffic from one IP during a known conference window is expected and is NOT abuse.** The per-IP authenticated cap is intentionally loose (600/min) to avoid false-tripping at the venue; the per-user 300/min cap constrains individual abusers. Do not tighten per-IP limits in response to volume from a known venue IP. Confirm the calendar before you escalate.

---

## 1. Detection signals

Any of these can be the first warning. None on its own confirms abuse — correlate before acting.

- **CloudWatch billing alarm at $10/mo fires** — early signal of cost runaway (abuse, or a runaway loop in our own code).
- **Lambda error-rate alarm on `api`** — distinguish a deploy regression from external pressure.
- **DDB throttle alarm on the main table** — usually a hot partition, but sustained throttling under heavy traffic can be both.
- **API Gateway 4xx / 5xx spike** in CloudWatch (`AWS/ApiGateway`). 4xx without 5xx → script-kiddie probing or rate-limit pushback; 5xx → real handler failures.
- **Rate-limit 429s spiking** — Lambda log entries from `api/internal/middleware/ratelimit` (`"ip rate limit exceeded"` / `"user rate limit exceeded"`). Connect maps these to `resource_exhausted`.
- **CloudFront request counts above baseline** — per-distribution `Requests` metric. CloudFront access logs are **not enabled in v1** (SECURITY.md §3.7) — see §4.
- **Sentry** secondary signal: a sudden burst of exceptions from one handler often precedes an alarm.

---

## 2. Immediate triage — is this real abuse, or a legitimate spike?

Before any mitigation, answer four questions.

**Is today a known event window?** Check the conference calendar. Registration deadline, conference day(s), or a scheduled swap window all produce large, legitimate bursts. Hundreds of users from one NATted venue IP during these windows is expected. If yes → **do not mitigate**. Monitor. The per-user 300/min cap handles any individual abuser without touching collective venue traffic.

**4xx or 5xx?**

- **4xx-dominant** (400/401/404): script-kiddie probing or scanner. Stage B if it concentrates on a few IPs; usually no WAF needed.
- **5xx-dominant**: real handler failures. **Before assuming attack, check the most recent deploy** and roll back if a regression matches the timing. See [`./first-prod-deploy.md`](./first-prod-deploy.md).
- **429s climbing**: rate limits are doing their job. Decide whether to tighten (Stage A) or escalate (Stage C).

**IP distribution?** Query CloudWatch Logs Insights (see §4):

- **One unfamiliar IP, thousands of requests** → single abuser. Stage B.
- **Many residential IPs, modest counts each** → likely botnet. Stage C.
- **One known venue IP** → **legitimate conference traffic, not abuse.** Re-read §2.1.

**What surface?**

- Anonymous surfaces (`PublicService.*`, `AuthService.Exchange`, `/cms-oauth/*`, `/v1/health`, `/v1/email/unsubscribe`) — capped at 60/min/IP. Pressure here = scrapers, OAuth abuse, or sign-up flooding (see [`./sign-up-abuse.md`](./sign-up-abuse.md)).
- Authenticated RPCs — capped at 300/min/user and 600/min/IP. Pressure here = compromised account or malicious authenticated user.
- Static CloudFront surfaces — absorbed by edge cache; usually a non-event for the wallet.

---

## 3. Immediate containment — staged escalation

Apply the **lowest stage that works**. Document timestamps and actions in the incident log as you go.

### Stage A — tighten in-app rate-limit config

Drop the per-IP anonymous cap from 60/min to 20/min via an emergency deploy of `api/internal/middleware/ratelimit/ratelimit.go::DefaultLimits()`:

```go
func DefaultLimits() Limits {
    return Limits{
        PerUserPerMin:        300,
        PerIPAuthedPerMin:    600, // leave alone — see venue NAT warning
        PerIPAnonymousPerMin: 20,  // tightened from 60 for incident <id>
    }
}
```

Deploy via the standard `api-deploy` workflow. **Future improvement:** expose limits as Lambda env vars so this is config, not code. Not implemented today.

> Do **not** tighten `PerIPAuthedPerMin` during a conference window. Authenticated venue traffic must not be IP-throttled; `PerUserPerMin` constrains individual misuse.

### Stage B — CloudFront IP blocklist

If a small set of source IPs is the source, block at the edge rather than paying Lambda invocation cost. Identify the offending IPs from §4, then update (or create) a CloudFront function on the relevant distribution that returns 403 for the listed IPs:

```bash
aws cloudfront update-function \
  --name numun-ip-blocklist \
  --function-config Comment="incident <id>",Runtime=cloudfront-js-2.0 \
  --function-code fileb://blocklist.js \
  --if-match <ETAG>

aws cloudfront publish-function --name numun-ip-blocklist --if-match <ETAG>
```

For API Gateway (which sits in front of Lambda separately from CloudFront), use Stage C instead.

### Stage C — enable AWS WAF on the API Gateway custom domain

When abuse is distributed (botnet) or persistent, turn on WAF. **Not enabled in v1** (SECURITY.md §3.7) — this is the documented escalation. Cost: ~$5/mo base + ~$1/managed rule + traffic. Acceptable for an active incident.

```bash
# 1. Create the WebACL with two AWS-managed rule groups.
aws wafv2 create-web-acl \
  --name numun-api-waf --scope REGIONAL --region us-east-2 \
  --default-action Allow={} \
  --visibility-config SampledRequestsEnabled=true,CloudWatchMetricsEnabled=true,MetricName=numun-api-waf \
  --rules '[
    {"Name":"AWSManagedRulesCommonRuleSet","Priority":0,"OverrideAction":{"None":{}},
     "Statement":{"ManagedRuleGroupStatement":{"VendorName":"AWS","Name":"AWSManagedRulesCommonRuleSet"}},
     "VisibilityConfig":{"SampledRequestsEnabled":true,"CloudWatchMetricsEnabled":true,"MetricName":"common"}},
    {"Name":"AWSManagedRulesAmazonIpReputationList","Priority":1,"OverrideAction":{"None":{}},
     "Statement":{"ManagedRuleGroupStatement":{"VendorName":"AWS","Name":"AWSManagedRulesAmazonIpReputationList"}},
     "VisibilityConfig":{"SampledRequestsEnabled":true,"CloudWatchMetricsEnabled":true,"MetricName":"ipreputation"}}
  ]'

# 2. Associate it with the API Gateway stage.
aws wafv2 associate-web-acl \
  --web-acl-arn arn:aws:wafv2:us-east-2:<ACCOUNT_ID>:regional/webacl/numun-api-waf/<UUID> \
  --resource-arn arn:aws:apigateway:us-east-2::/restapis/<API_ID>/stages/prod \
  --region us-east-2
```

**Verify** in CloudWatch (`AWS/WAFV2`) that `BlockedRequests` is non-zero and `AllowedRequests` still includes the venue IP's legitimate traffic. The reputation list catches known-bad IPs; the common rule set catches OWASP-shaped probes. Watch the sampled-requests dashboard for the first hour to confirm no legitimate user (especially venue IP) is blocked.

If WAF stays on after the incident, raise the billing-alarm threshold to absorb the recurring ~$5–10/mo cost.

### Stage D — temporarily disable an abused public surface

Last resort. If a single endpoint is being abused (e.g. `AuthService.Exchange` flooded with garbage codes), 503 that path via a feature-flagged Lambda env var:

```go
if os.Getenv("DISABLE_EXCHANGE") == "1" {
    return nil, connect.NewError(connect.CodeUnavailable, errors.New("temporarily disabled — incident <id>"))
}
```

Deploy with `DISABLE_EXCHANGE=1`. Re-enable as soon as Stages A–C contain the abuse. Communicate to staff — users on the affected flow will see failures.

---

## 4. Investigation

Once contained, identify the attack signature.

**CloudWatch Logs Insights — top source IPs over the window:**

```
fields @timestamp, @message
| parse @message /"ip":"(?<ip>[^"]+)"/
| filter ispresent(ip)
| stats count() as reqs by ip
| sort reqs desc
| limit 50
```

**Top paths by 4xx rate:**

```
fields @timestamp, @message
| parse @message /"path":"(?<path>[^"]+)".*"status":(?<status>\d+)/
| filter status >= 400 and status < 500
| stats count() as four_xx by path
| sort four_xx desc
| limit 20
```

**Rate-limit rejections by key:**

```
fields @timestamp, @message
| filter @message like /rate limit exceeded/
| stats count() as rejects by @message
| sort rejects desc
```

**Were real users affected?** Query `AuthAuditEvent` for the period — successful sign-ins from the venue IP confirm legitimate users could still authenticate during the incident. A cluster of `sign_in_failed` from one IP across many usernames is credential stuffing (SECURITY.md §4.4; Cognito's built-in limits should already bite).

**Forensic gap:** CloudFront access logs are not enabled in v1 (SECURITY.md §3.7). For static-surface abuse you have only aggregate CloudWatch metrics, no per-request detail. If this incident needs per-request CloudFront forensics, enable access logs to a dedicated S3 bucket as part of remediation and flag it in the post-mortem.

---

## 5. Remediation

- If WAF (Stage C) was added and the attack pattern is recurring, **keep it on permanently** and update SECURITY.md §2.10 / §3.7 / §10 to reflect the new posture. Raise the billing-alarm threshold to absorb the ~$5–10/mo cost.
- If the sign-up flow was the target, follow [`./sign-up-abuse.md`](./sign-up-abuse.md) and enable Cloudflare Turnstile (SECURITY.md §6.1).
- If the email flow was the target (bounce/complaint flood or unsubscribe abuse), see [`./email-reputation-collapse.md`](./email-reputation-collapse.md).
- Restore Stage A rate limits to defaults post-incident, unless analysis shows the tighter values are sustainable without false positives.
- **AWS Shield Advanced** ($3,000/mo) — not justified at NUMUN scale. Do not request without exec sign-off.
- If Stage D was used, confirm `DISABLE_*` env vars are removed and the next deploy restores the surface.

---

## 6. Post-mortem

Write up within 48 hours of containment. Capture:

- **Attack signature:** source IPs / ASNs, target paths, request shape, peak rate, duration.
- **Detection latency:** time from first malicious request to first operator awareness. Right alarm in place?
- **Response time:** time from awareness to containment, per stage.
- **What worked / what didn't:** which protective layer carried which load; false negatives and false positives (especially any venue traffic blocked).
- **Cost impact:** AWS bill spike; whether new permanent controls (WAF, access logs) change the steady-state ceiling.
- **Action items:** missing alarms, missing logs (CloudFront access logs are the standing candidate), config that should become env vars, runbook gaps.

Update this runbook with what you learned and cross-link the postmortem from any stage that needed clarification.
