#!/usr/bin/env bash
# `gh api` with retries, for workflow steps that must survive GitHub's
# occasional 5xx responses. Same arguments as `gh api`.
set -uo pipefail

for attempt in 1 2 3 4 5; do
  if gh api "$@"; then
    exit 0
  fi
  [ "$attempt" -lt 5 ] && sleep $((attempt * 5))
done
echo "gh api $* failed after 5 attempts" >&2
exit 1
