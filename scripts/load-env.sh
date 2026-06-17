#!/usr/bin/env bash
# load-env.sh — export `infra/envs/<env>.yaml` keys as env vars.
#
# Usage:
#   scripts/load-env.sh <env-name>      # e.g., test, prod
#
# Writes one line per top-level key to stdout as `KEY=value`, and (when
# running inside GitHub Actions) appends each line to $GITHUB_ENV so the
# following job steps see the values as env vars.
#
# See IMPLEMENTATION_PLAN.md §M14.
#
# Empty values are allowed (e.g., prod's ENV_SUBDOMAIN is intentionally
# blank since the apex IS the root domain). Downstream tooling
# (`sam deploy`, `aws cloudformation`) catches a missing-required-field
# with a clearer error than this script could.

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <env-name>" >&2
  exit 2
fi

env_name="$1"
yaml_path="infra/envs/${env_name}.yaml"

if [[ ! -f "$yaml_path" ]]; then
  echo "error: $yaml_path not found" >&2
  exit 1
fi

# yq → json → jq → KEY=value lines. Using JSON as the intermediate
# format avoids the `KEY = value` (with spaces) shape that `yq -o=props`
# produces, which $GITHUB_ENV doesn't accept.
while IFS= read -r line; do
  printf '%s\n' "$line"
  if [[ -n "${GITHUB_ENV:-}" ]]; then
    printf '%s\n' "$line" >> "$GITHUB_ENV"
  fi
done < <(yq -o=json "$yaml_path" | jq -r 'to_entries | .[] | "\(.key)=\(.value)"')
