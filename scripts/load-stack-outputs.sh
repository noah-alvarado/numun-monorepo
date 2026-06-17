#!/usr/bin/env bash
# load-stack-outputs.sh — export CloudFormation stack outputs as env vars.
#
# Usage:
#   scripts/load-stack-outputs.sh <stack-name> [prefix]
#
# Writes one line per output to stdout as `KEY=value`, and (when running
# inside GitHub Actions, i.e., $GITHUB_ENV is set) appends each line to
# $GITHUB_ENV so the following job steps see the values as env vars.
#
# An optional prefix is added to every output key — useful when loading
# from multiple stacks that share key names. For example,
#   scripts/load-stack-outputs.sh numun-test-base-data DATA_
# turns an output named `CognitoUserPoolId` into `DATA_CognitoUserPoolId`.
#
# Designed for the M14 env-config migration so deploy workflows can read
# stack outputs live instead of mirroring them into GitHub variables.
# See IMPLEMENTATION_PLAN.md §M14.

set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <stack-name> [prefix]" >&2
  exit 2
fi

stack="$1"
prefix="${2:-}"

aws cloudformation describe-stacks \
  --stack-name "$stack" \
  --query 'Stacks[0].Outputs' \
  --output json \
  | jq -r --arg p "$prefix" '.[]? | "\($p)\(.OutputKey)=\(.OutputValue)"' \
  | while IFS= read -r line; do
      printf '%s\n' "$line"
      if [[ -n "${GITHUB_ENV:-}" ]]; then
        printf '%s\n' "$line" >> "$GITHUB_ENV"
      fi
    done
