#!/usr/bin/env bash
# Verify that the local dev toolchain is installed and matches the pinned versions.
set -u

# Include Go's default install location so `go install`-managed tools
# (govulncheck, etc.) are discoverable even if the user hasn't added it
# to their shell PATH.
export PATH="$PATH:${GOBIN:-$HOME/go/bin}"

OK="\033[32m✓\033[0m"
FAIL="\033[31m✗\033[0m"
WARN="\033[33m!\033[0m"

errors=0

# check NAME BIN VERSION_CMD
#   NAME         human label
#   BIN          binary to look up via `command -v`
#   VERSION_CMD  shell snippet that prints a version string (run only if BIN exists)
check() {
  local label="$1"
  local bin="$2"
  local version_cmd="$3"

  if ! command -v "$bin" >/dev/null 2>&1; then
    printf "  ${FAIL} %-22s missing\n" "$label"
    errors=$((errors+1))
    return 1
  fi

  local got
  got=$(eval "$version_cmd" 2>/dev/null | head -n1)
  printf "  ${OK} %-22s %s\n" "$label" "${got:-installed}"
}

echo "Toolchain check:"
check "node"          "node"          "node --version"
check "pnpm"          "pnpm"          "pnpm --version"
check "go"            "go"            "go version | awk '{print \$3}'"
check "docker"        "docker"        "docker --version | awk '{print \$3}' | tr -d ','"

# `docker compose` is a subcommand, not a separate binary.
if docker compose version --short >/dev/null 2>&1; then
  printf "  ${OK} %-22s %s\n" "docker compose" "$(docker compose version --short)"
else
  printf "  ${FAIL} %-22s missing\n" "docker compose"
  errors=$((errors+1))
fi

check "aws"           "aws"           "aws --version 2>&1 | awk '{print \$1}'"
check "sam"           "sam"           "sam --version 2>&1 | awk '{print \$NF}'"
check "buf"           "buf"           "buf --version"
check "golangci-lint" "golangci-lint" "golangci-lint --version | head -n1"
check "govulncheck"   "govulncheck"   "govulncheck -version 2>&1 | head -n1"

# Pinned-version checks. .nvmrc names the minimum supported Node;
# newer minor/patch releases are accepted.
if command -v node >/dev/null 2>&1; then
  expected=$(cat .nvmrc)
  actual=$(node --version | sed 's/^v//')
  lowest=$(printf '%s\n%s\n' "$expected" "$actual" | sort -V | head -n1)
  if [ "$lowest" != "$expected" ]; then
    printf "  ${WARN} node too old: need >=%s, got %s (see .nvmrc)\n" "$expected" "$actual"
    errors=$((errors+1))
  fi
fi

# api/go.mod's `go` directive names the minimum supported toolchain;
# newer releases are accepted.
if command -v go >/dev/null 2>&1; then
  expected=$(awk '/^go / {print $2; exit}' api/go.mod)
  actual=$(go version | awk '{print $3}' | sed 's/^go//')
  lowest=$(printf '%s\n%s\n' "$expected" "$actual" | sort -V | head -n1)
  if [ "$lowest" != "$expected" ]; then
    printf "  ${WARN} go too old: need >=%s, got %s (see api/go.mod)\n" "$expected" "$actual"
    errors=$((errors+1))
  fi
fi

# package.json's `packageManager` field names the pinned pnpm version
# (corepack reads this). Newer releases are accepted.
if command -v pnpm >/dev/null 2>&1; then
  expected=$(sed -nE 's/.*"packageManager": *"pnpm@([^"]+)".*/\1/p' package.json | head -n1)
  actual=$(pnpm --version)
  lowest=$(printf '%s\n%s\n' "$expected" "$actual" | sort -V | head -n1)
  if [ "$lowest" != "$expected" ]; then
    printf "  ${WARN} pnpm too old: need >=%s, got %s (see package.json \"packageManager\")\n" "$expected" "$actual"
    errors=$((errors+1))
  fi
fi

if [ "$errors" -gt 0 ]; then
  echo
  echo "Missing $errors tool(s). See /DEVELOPERS.md for install instructions."
  exit 1
fi

echo
echo "All required tools present."
