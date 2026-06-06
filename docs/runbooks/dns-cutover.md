# Runbook — DNS cutover from GoDaddy to Route 53

One-time procedure. The `numun.org` domain stays registered at GoDaddy; DNS authority moves to Route 53.

## Prerequisites
- AWS console access (break-glass IAM user is fine).
- GoDaddy account access for the `numun.org` domain.
- A current snapshot of every DNS record on GoDaddy (`dig` or screenshot the DNS Management page).

## Procedure

### 1. Create the Route 53 hosted zone

```bash
aws route53 create-hosted-zone \
  --name numun.org \
  --caller-reference "numun-$(date +%s)" \
  --hosted-zone-config Comment="NUMUN production zone"
```

Capture the four NS records returned (e.g., `ns-123.awsdns-12.com.`). These are the values you'll set at GoDaddy.

### 2. Mirror every existing GoDaddy record into Route 53

Before flipping nameservers, recreate every non-NS, non-SOA record from GoDaddy into Route 53 so the cutover is zero-downtime. Use the AWS console (Hosted zones → Create record) or `aws route53 change-resource-record-sets`. At minimum:

- MX records (if any — e.g., Google Workspace, Outlook 365)
- TXT records (SPF for the apex if email is hosted elsewhere, domain verification entries, etc.)
- Any A/AAAA/CNAME used by current operational tooling

The base SAM stack will create the apex A-alias and subdomain aliases at deploy time. Don't pre-create those.

### 3. Verify Route 53 resolves correctly (before flipping nameservers)

```bash
# Query Route 53 directly — bypasses any cached GoDaddy resolution.
for ns in ns-XXX.awsdns-XX.com ns-XXX.awsdns-XX.net ns-XXX.awsdns-XX.org ns-XXX.awsdns-XX.co.uk; do
  dig @"$ns" numun.org MX +short
  dig @"$ns" numun.org TXT +short
done
```

Every record from GoDaddy should appear in the Route 53 responses. If any are missing, fix Route 53 first.

### 4. Update nameservers at GoDaddy

GoDaddy → Domain Settings → Nameservers → "Enter my own nameservers (advanced)". Replace the GoDaddy defaults with the four Route 53 NS values from step 1. Save.

### 5. Wait for propagation

```bash
# Until this returns Route 53 NS values, propagation is still in flight.
dig +short NS numun.org
```

Typical propagation: 1–4 hours. Some recursive resolvers cache for up to 48 hours per the SOA TTL.

### 6. Verify end-to-end

```bash
# Should resolve to a Route 53-owned nameserver.
dig +trace numun.org NS | tail -10

# If the base stack is already deployed: should resolve to a CloudFront IP.
dig +short numun.org
```

### 7. Capture the Route 53 hosted zone id

Set this as a GitHub repository variable (`vars.HOSTED_ZONE_ID`) and as the `HostedZoneId` parameter when deploying the base SAM stack.

```bash
aws route53 list-hosted-zones-by-name --dns-name numun.org \
  --query "HostedZones[0].Id" --output text
```

## Rollback

If something resolves incorrectly after the GoDaddy flip:

1. Revert GoDaddy → Domain Settings → Nameservers → "Default (GoDaddy nameservers)".
2. Wait for caches to expire.
3. Diagnose the Route 53 record set.
4. Re-attempt.

The Route 53 hosted zone itself is `DeletionPolicy: Retain` on the parameter level — it's not deleted by stack tear-downs.

## Cost notes

- Hosted zone: $0.50/mo.
- Queries: $0.40 per million standard queries; NUMUN will not approach this.
