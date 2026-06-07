# Runbook — Break-glass IAM access

The `numun-break-glass` IAM user is the single named human path for emergency AWS console + CLI access outside the normal OIDC-federated CI flow. Use it sparingly; every action under it should be reflected in CloudTrail.

References: [SECURITY.md](../SECURITY.md) §3.2 and §6.5.

## When to use

- One-time bootstrap deploys: the OIDC bootstrap stack, the base stack, the api stack (very first deploy), ACM cert creation, Route 53 hosted-zone creation.
- Incident response when CI is unavailable (rare).
- Manual Cognito admin operations: first-admin bootstrap (M2), forced password resets, global sign-outs.
- DDB administrative reads outside the normal application path (audit-log investigation).

## When NOT to use

- Routine deploys → use GitHub Actions OIDC workflows.
- Local development → use `make dev` against the local prod-mirror.
- Anything that can be automated → automate it instead.

## Setup (one-time)

### 1. Create the IAM user

```bash
aws iam create-user --user-name numun-break-glass
```

### 2. Attach `AdministratorAccess`

```bash
aws iam attach-user-policy \
  --user-name numun-break-glass \
  --policy-arn arn:aws:iam::aws:policy/AdministratorAccess
```

### 3. Enable virtual MFA

Console → IAM → Users → `numun-break-glass` → Security credentials → "Assign MFA device". Use a hardware key (YubiKey) where available; otherwise authenticator app (TOTP).

### 4. **Do not create long-lived access keys**

Access is via console + STS temporary credentials only. If you must use the CLI, generate session creds via console "Command line or programmatic access" (issues a 12-hour STS token).

### 5. Enforce MFA at the policy level

Attach this inline policy so the user can't perform any action without MFA:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "DenyAllExceptListedIfNoMFA",
      "Effect": "Deny",
      "NotAction": [
        "iam:CreateVirtualMFADevice",
        "iam:EnableMFADevice",
        "iam:GetUser",
        "iam:ListMFADevices",
        "iam:ListVirtualMFADevices",
        "iam:ResyncMFADevice",
        "sts:GetSessionToken"
      ],
      "Resource": "*",
      "Condition": {
        "BoolIfExists": {
          "aws:MultiFactorAuthPresent": "false"
        }
      }
    }
  ]
}
```

## Using the user

### Console

1. Sign in to `https://signin.aws.amazon.com/console` with username `numun-break-glass`.
2. Provide TOTP code.
3. Switch to `us-east-2` region for app resources, `us-east-1` for CloudFront / billing.
4. Perform the action.
5. Sign out immediately when done.

### CLI (short session)

```bash
# Get a 12-hour MFA-bearing session.
aws sts get-session-token \
  --serial-number arn:aws:iam::ACCOUNT_ID:mfa/numun-break-glass \
  --token-code 123456 \
  --duration-seconds 43200
```

Export the returned credentials into the shell, do the work, then `unset` them.

## Audit

Every break-glass session writes CloudTrail events. After any non-routine session, file a one-line entry in `/docs/runbooks/breakglass-log.md` (not committed; kept locally by the admin who used it): date, reason, what was done. Review quarterly.

## Recovery

If MFA device is lost: AWS account root holder (separate from this IAM user) can detach and re-add MFA. The root account itself is sealed with a hardware MFA — see [SECURITY.md](../SECURITY.md) §6.5.

## Future path

Replace break-glass with AWS IAM Identity Center (SSO) once an organizational identity provider is available. The break-glass user remains as a last-resort fallback.
