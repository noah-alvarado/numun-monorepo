#!/usr/bin/env bash
# Lint gate: every handler that takes a path-bound entity id MUST call
# the matching scope helper at the top of the method body. Per AUTH.md §7.2
# and IMPLEMENTATION_PLAN.md M2 (lint rule "blocks handlers from touching
# repositories without a preceding mustHaveScopeOn* call").
#
# Approach (intentionally simple; revisit if it gets too noisy):
#
#   1. Find every Go file under /api/internal/handlers/.
#   2. If the file references the PascalCase form of a scoped *_id field
#      (DelegationId, DelegateId, CommitteeId, AssignmentId, PaymentId,
#      ConferenceId), demand the matching MustHaveScopeOn<Entity> call.
#   3. Whitelist files marked with the comment `// scope-check: skip` on
#      line 1 (or anywhere in the first 2 lines).
#
# Exits 0 when all handlers pass, 1 otherwise. Run from repo root.

set -eo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HANDLERS_DIR="$ROOT/api/internal/handlers"

if [[ ! -d "$HANDLERS_DIR" ]]; then
  echo "scope-check: no $HANDLERS_DIR yet (pre-M2 state); skipping."
  exit 0
fi

# Parallel arrays: PascalCase field suffix → expected helper.
FIELDS=(DelegationId DelegateId CommitteeId AssignmentId PaymentId ConferenceId)
HELPERS=(MustHaveScopeOnDelegation MustHaveScopeOnDelegate MustHaveScopeOnCommittee MustHaveScopeOnAssignment MustHaveScopeOnPayment MustHaveScopeOnConference)

fail=0

while IFS= read -r -d '' f; do
  if head -n2 "$f" | grep -q "scope-check: skip"; then
    continue
  fi
  for i in "${!FIELDS[@]}"; do
    field="${FIELDS[$i]}"
    helper="${HELPERS[$i]}"
    if grep -qE "\\.${field}\\b|\\bGet${field}\\b" "$f"; then
      if ! grep -q "${helper}(" "$f"; then
        echo "scope-check: $f references ${field} but does not call ${helper}"
        fail=1
      fi
    fi
  done
done < <(find "$HANDLERS_DIR" -type f -name '*.go' -print0)

if [[ $fail -ne 0 ]]; then
  echo ""
  echo "Add a scope helper call (e.g., auth.MustHaveScopeOnDelegation(ctx, req.GetDelegationId()))"
  echo "or, if intentional, mark the file's first line with '// scope-check: skip'."
  exit 1
fi
