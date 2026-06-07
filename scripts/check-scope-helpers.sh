#!/usr/bin/env bash
# Lint gate: every handler that takes a path-bound entity id MUST call
# the matching scope helper at the top of the method body. Per AUTH.md §7.2
# and IMPLEMENTATION_PLAN.md M2 (lint rule "blocks handlers from touching
# repositories without a preceding mustHaveScopeOn* call").
#
# Approach (intentionally simple; revisit if it gets too noisy):
#
#   1. Find every Go file under /api/internal/handlers/.
#   2. For each handler function whose first parameter looks like a request
#      containing a *_id field for a scoped entity (delegation_id,
#      delegate_id, committee_id, assignment_id, payment_id), verify the
#      function body contains a call to the matching MustHaveScopeOn<Entity>.
#   3. Whitelist files marked with the comment `// scope-check: skip` on
#      line 1 so we can intentionally bypass when needed (rare).
#
# Exits 0 when all handlers pass, 1 otherwise. Run from repo root.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HANDLERS_DIR="$ROOT/api/internal/handlers"

if [[ ! -d "$HANDLERS_DIR" ]]; then
  echo "scope-check: no $HANDLERS_DIR yet (pre-M2 state); skipping."
  exit 0
fi

shopt -s nullglob globstar

fail=0

declare -A ID_TO_HELPER=(
  [delegation_id]=MustHaveScopeOnDelegation
  [delegate_id]=MustHaveScopeOnDelegate
  [committee_id]=MustHaveScopeOnCommittee
  [assignment_id]=MustHaveScopeOnAssignment
  [payment_id]=MustHaveScopeOnPayment
  [conference_id]=MustHaveScopeOnConference
)

for f in "$HANDLERS_DIR"/**/*.go; do
  # Honor opt-out marker on line 1.
  if head -n1 "$f" | grep -q "scope-check: skip"; then
    continue
  fi
  # Iterate field name → helper. If the file mentions a scoped id field at
  # all, demand the matching helper invocation somewhere in the file. This
  # is coarser than per-function analysis but cheap and good enough for v1;
  # tighten when handler count grows.
  for field in "${!ID_TO_HELPER[@]}"; do
    helper="${ID_TO_HELPER[$field]}"
    # The field name appears in request struct accessors as Go's PascalCase
    # form (e.g., GetDelegationId, msg.DelegationId). Build a regex that
    # matches the PascalCase suffix.
    pascal_suffix="$(awk -v s="$field" 'BEGIN{
      n=split(s,a,"_"); out=""
      for(i=1;i<=n;i++){ p=a[i]; out = out toupper(substr(p,1,1)) substr(p,2) }
      print out
    }')"
    if grep -qE "\\.${pascal_suffix}\\b|\\bGet${pascal_suffix}\\b" "$f"; then
      if ! grep -q "auth\\.${helper}\\b\\|${helper}(" "$f"; then
        echo "scope-check: $f mentions ${pascal_suffix} but does not call auth.${helper}"
        fail=1
      fi
    fi
  done
done

if [[ $fail -ne 0 ]]; then
  echo ""
  echo "Add a scope helper call (e.g., auth.MustHaveScopeOnDelegation(ctx, req.GetDelegationId()))"
  echo "or, if intentional, mark the file's first line with '// scope-check: skip'."
  exit 1
fi
