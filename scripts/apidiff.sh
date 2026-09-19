#!/usr/bin/env sh
# The selvedge (TODO §1.7): fail on any incompatible change to the root
# module's public Go API since the last root tag. Adapters are their own
# modules and get their own gate when they tag.
#
# Usage: scripts/apidiff.sh [base-ref]
#   base-ref defaults to the newest v* tag reachable from HEAD.
#
# Pre-1.0 evolutions that are source-compatible but flagged by apidiff
# (widening a return type to a superset interface, adding a trailing
# variadic) can be acknowledged by listing the exact apidiff line in
# .apidiff-allow. Anything not listed fails the build. Post-1.0 the
# allowlist is emptied and stays empty.
#
# The gate fails closed: a tree that does not compile, or an apidiff
# run that dies before writing a report, must never reach the "no
# incompatible changes" path. An empty report means "nothing to parse",
# which is green only when apidiff itself succeeded.
# scripts/apidiff-selftest.sh exercises both failure modes.
set -eu

cd "$(dirname "$0")/.."
base="${1:-$(git describe --tags --abbrev=0 --match 'v*' HEAD)}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if ! command -v apidiff >/dev/null 2>&1; then
  echo "apidiff not found: go install golang.org/x/exp/cmd/apidiff@latest" >&2
  exit 2
fi

# Fail closed on a non-compiling working tree: apidiff would emit an
# empty report, and without this build the gate would read it as green.
if ! GOWORK=off go build ./...; then
  echo "apidiff: the working tree does not compile — the gate cannot vouch for it" >&2
  exit 1
fi

git worktree add --detach --force "$tmp/base" "$base" >/dev/null 2>&1
trap 'git worktree remove --force "$tmp/base" >/dev/null 2>&1 || true; rm -rf "$tmp"' EXIT

# -m: whole module. The base export is written from the tagged tree,
# then compared against the working tree. The base must build too: a
# base that cannot load leaves an empty export and an empty report —
# the same fail-closed hole as above, on the other side.
if ! (cd "$tmp/base" && GOWORK=off go build ./...); then
  echo "apidiff: the base ref $base does not compile — the gate cannot vouch for it" >&2
  exit 1
fi
(cd "$tmp/base" && GOWORK=off apidiff -m -w "$tmp/base.export" .)

# apidiff exits non-zero when it finds incompatible changes; capture the
# status so the report is still parsed below. A non-zero status with an
# empty report is an execution failure (a load error apidiff printed to
# stderr), not a clean diff — fail closed on that combination.
status=0
GOWORK=off apidiff -m "$tmp/base.export" . > "$tmp/report" || status=$?
if [ "$status" -ne 0 ] && [ ! -s "$tmp/report" ]; then
  echo "apidiff: exited $status without a report — execution failure, not a clean diff" >&2
  exit 1
fi

echo "apidiff: $base → HEAD"
cat "$tmp/report"

# Extract the incompatible lines: everything between the "Incompatible
# changes:" header and the next header (or EOF).
awk '/^Incompatible changes:/{f=1;next} /^Compatible changes:/{f=0} f && /^- /{print}' "$tmp/report" \
  | sed 's/^- //' > "$tmp/incompatible"

if [ ! -s "$tmp/incompatible" ]; then
  echo "apidiff: no incompatible changes"
  exit 0
fi

status=0
while IFS= read -r line; do
  if [ -f .apidiff-allow ] && grep -qxF -- "$line" .apidiff-allow; then
    echo "apidiff: allowed (listed in .apidiff-allow): $line"
  else
    echo "apidiff: INCOMPATIBLE: $line" >&2
    status=1
  fi
done < "$tmp/incompatible"

if [ "$status" -ne 0 ]; then
  echo "apidiff: incompatible API change(s) since $base. Revert the change, or — pre-1.0 only — add the exact line above to .apidiff-allow with a reviewed justification." >&2
fi
exit "$status"
