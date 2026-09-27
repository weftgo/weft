#!/usr/bin/env sh
# The selvedge (TODO §1.7): fail on any incompatible change to a
# module's public Go API since that module's last tag. The root module
# gates against the newest v* tag; the store module gates against the
# newest store/v* tag, from its second tag on (plan §3.1). The other
# sub-modules get their own gate when they tag.
#
# Usage: scripts/apidiff.sh [base-ref] [module-dir]
#   module-dir "." (the default) is the root module; "store" is the
#   store module. base-ref defaults to the module's newest matching
#   tag reachable from HEAD; an explicit empty string means the same.
#
# Pre-1.0 evolutions that are source-compatible but flagged by apidiff
# (widening a return type to a superset interface, adding a trailing
# variadic) can be acknowledged by listing the exact apidiff line in
# the module's .apidiff-allow (root: ./.apidiff-allow; store:
# store/.apidiff-allow). Anything not listed fails the build. Post-1.0
# the allowlist is emptied and stays empty.
#
# The gate fails closed: a tree that does not compile, or an apidiff
# run that dies before writing a report, must never reach the "no
# incompatible changes" path. An empty report means "nothing to parse",
# which is green only when apidiff itself succeeded.
# scripts/apidiff-selftest.sh exercises the failure modes of both
# gates.
set -eu

cd "$(dirname "$0")/.."
mod="${2:-.}"
case "$mod" in
  .) tagpat='v*' ;;
  store) tagpat='store/v*' ;;
  *) echo "apidiff: unknown module dir '$mod' (want . or store)" >&2; exit 2 ;;
esac
base="${1:-$(git describe --tags --abbrev=0 --match "$tagpat" HEAD)}"
if [ -z "$base" ]; then
  base="$(git describe --tags --abbrev=0 --match "$tagpat" HEAD)"
fi
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if ! command -v apidiff >/dev/null 2>&1; then
  echo "apidiff not found: go install golang.org/x/exp/cmd/apidiff@latest" >&2
  exit 2
fi

# Fail closed on a non-compiling working tree: apidiff would emit an
# empty report, and without this build the gate would read it as green.
if ! (cd "$mod" && GOWORK=off go build ./...); then
  echo "apidiff: $mod does not compile — the gate cannot vouch for it" >&2
  exit 1
fi

git worktree add --detach --force "$tmp/base" "$base" >/dev/null 2>&1
trap 'git worktree remove --force "$tmp/base" >/dev/null 2>&1 || true; rm -rf "$tmp"' EXIT

# -m: whole module. The base export is written from the tagged tree,
# then compared against the working tree. The base must build too: a
# base that cannot load leaves an empty export and an empty report —
# the same fail-closed hole as above, on the other side. GOWORK=off
# holds for the store module too: its root requirement resolves from
# the module graph (the repo is public; the proxy serves the tags), so
# the gate never leans on the working tree's go.work.
if ! (cd "$tmp/base/$mod" && GOWORK=off go build ./...); then
  echo "apidiff: the base ref $base ($mod) does not compile — the gate cannot vouch for it" >&2
  exit 1
fi
(cd "$tmp/base/$mod" && GOWORK=off apidiff -m -w "$tmp/base.export" .)

# apidiff exits non-zero when it finds incompatible changes; capture the
# status so the report is still parsed below. A non-zero status with an
# empty report is an execution failure (a load error apidiff printed to
# stderr), not a clean diff — fail closed on that combination.
status=0
(cd "$mod" && GOWORK=off apidiff -m "$tmp/base.export" . > "$tmp/report") || status=$?
if [ "$status" -ne 0 ] && [ ! -s "$tmp/report" ]; then
  echo "apidiff: exited $status without a report — execution failure, not a clean diff" >&2
  exit 1
fi

echo "apidiff: $mod: $base → HEAD"
cat "$tmp/report"

# Extract the incompatible lines: everything between the "Incompatible
# changes:" header and the next header (or EOF).
awk '/^Incompatible changes:/{f=1;next} /^Compatible changes:/{f=0} f && /^- /{print}' "$tmp/report" \
  | sed 's/^- //' > "$tmp/incompatible"

if [ ! -s "$tmp/incompatible" ]; then
  echo "apidiff: no incompatible changes"
  exit 0
fi

allow="$mod/.apidiff-allow"
status=0
while IFS= read -r line; do
  if [ -f "$allow" ] && grep -qxF -- "$line" "$allow"; then
    echo "apidiff: allowed (listed in $allow): $line"
  else
    echo "apidiff: INCOMPATIBLE: $line" >&2
    status=1
  fi
done < "$tmp/incompatible"

if [ "$status" -ne 0 ]; then
  echo "apidiff: incompatible API change(s) in $mod since $base. Revert the change, or — pre-1.0 only — add the exact line above to $allow with a reviewed justification." >&2
fi
exit "$status"
