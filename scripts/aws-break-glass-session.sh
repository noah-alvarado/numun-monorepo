#!/usr/bin/env bash
# Fallback access path. The primary CLI auth flow is:
#     aws login --profile numun-prod
# which uses the AWS CLI 2.32+ browser-based sign-in (no access keys) and
# satisfies the deny-without-MFA policy on the break-glass user via the
# console MFA prompt.
#
# Use this script only if `aws login` is unavailable (e.g., CLI version too
# old, browser sign-in flow broken). It issues a 12-hour MFA-bearing STS
# session for the numun-break-glass user using a long-lived access key
# stored under profile `numun-bg-static`, and writes the temp credentials
# to profile `numun-prod` in ~/.aws/credentials.
#
# Prerequisites:
#   - Profile `numun-bg-static` configured with the break-glass user's
#     long-lived access key (`aws configure --profile numun-bg-static`).
#   - Env var MFA_SERIAL set to the user's MFA device ARN
#     (e.g., arn:aws:iam::123456789012:mfa/<device-name>) — OR exported
#     once into the script via NUMUN_MFA_SERIAL.
#
# Usage:
#   scripts/aws-break-glass-session.sh
#
# It will prompt for the 6-digit TOTP code.

set -euo pipefail

MFA_SERIAL="${NUMUN_MFA_SERIAL:-${MFA_SERIAL:-}}"
if [ -z "$MFA_SERIAL" ]; then
  echo "Set NUMUN_MFA_SERIAL=arn:aws:iam::<acct>:mfa/<device-name> in your shell rc," >&2
  echo "or export MFA_SERIAL before running this script." >&2
  exit 1
fi

read -r -p "MFA code (6 digits): " CODE
if [[ ! "$CODE" =~ ^[0-9]{6}$ ]]; then
  echo "MFA code must be 6 digits." >&2
  exit 1
fi

echo "Requesting 12-hour session token..."
CREDS=$(aws sts get-session-token \
  --profile numun-bg-static \
  --serial-number "$MFA_SERIAL" \
  --token-code "$CODE" \
  --duration-seconds 43200 \
  --output json)

ACCESS_KEY=$(echo "$CREDS" | jq -r '.Credentials.AccessKeyId')
SECRET_KEY=$(echo "$CREDS" | jq -r '.Credentials.SecretAccessKey')
SESSION=$(echo   "$CREDS" | jq -r '.Credentials.SessionToken')
EXPIRES=$(echo   "$CREDS" | jq -r '.Credentials.Expiration')

# Write to ~/.aws/credentials profile [numun-prod]. Idempotent — replaces
# the existing block if present.
PROFILE="numun-prod"
CRED_FILE="${HOME}/.aws/credentials"
mkdir -p "$(dirname "$CRED_FILE")"
touch "$CRED_FILE"

python3 - "$PROFILE" "$ACCESS_KEY" "$SECRET_KEY" "$SESSION" "$EXPIRES" "$CRED_FILE" <<'PY'
import configparser, sys
profile, ak, sk, st, exp, path = sys.argv[1:7]
cp = configparser.ConfigParser()
cp.read(path)
if not cp.has_section(profile):
    cp.add_section(profile)
cp.set(profile, "aws_access_key_id", ak)
cp.set(profile, "aws_secret_access_key", sk)
cp.set(profile, "aws_session_token", st)
cp.set(profile, "x_expiration", exp)  # for human reference
with open(path, "w") as f:
    cp.write(f)
PY

echo "Wrote MFA-bearing creds to profile [${PROFILE}] in ${CRED_FILE}."
echo "Session expires at: ${EXPIRES}"
echo "Usage: aws --profile ${PROFILE} ..."
