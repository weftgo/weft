#!/usr/bin/env sh
# The selvedge (TODO §1.7): fail on any incompatible change to a
# module's public Go API since that module's last tag. The root module
# gates against the newest v* tag; the thread module against the
# newest thread/v* tag; thread/sqlite against the newest
# thread/sqlite/v* tag. The other sub-modules get their own gate when
# they tag: add a case beside these and an apidiff-* target then. (The
# store module's gate died with the module, step 5 of ADR 0024.)
#
# Usage: scripts/apidiff.sh [base-ref] [module-dir]
#   module-dir "." (the default) is the root module; "thread" is the
#   thread module, "thread/sqlite" the SQLite backend. base-ref
#   defaults to the module's newest matching tag reachable from HEAD;
#   an explicit empty string means the same. A module with no matching
#   tag yet skips with a note — nothing to diff against, nothing
#   vouched for; the gate starts at the release after the first tag.
#
# How each side is loaded. The root and thread modules load with
# GOWORK=off: their requirements resolve from the module graph, so the
# gate never leans on the working tree's go.work. thread/sqlite cannot:
# it is developed against the in-tree thread module and released
# second (thread is tagged first, then sqlite's go.mod is moved to
# that tag), so between two thread tags its working tree only compiles
# against the thread beside it. Both of its sides therefore load
# through their own tree's go.work — the base at the tag's commit, the
# head in the working tree — which is how CI builds and tests it too.
#
# Pre-1.0 evolutions that are source-compatible but flagged by apidiff
# (widening a return type to a superset interface, adding a trailing
# variadic) can be acknowledged by listing the exact apidiff line in
# the module's .apidiff-allow (root: ./.apidiff-allow). Anything not
# listed fails the build. Post-1.0
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
work=off
case "$mod" in
  .) tagpat='v*' ;;
  thread) tagpat='thread/v*' ;;
  thread/sqlite) tagpat='thread/sqlite/v*'; work=tree ;;
  *) echo "apidiff: unknown module dir '$mod' (want ., thread or thread/sqlite)" >&2; exit 2 ;;
esac

# load runs a go or apidiff command the way the module is loaded (see
# the header): outside any workspace, or through the tree's go.work.
load() {
  if [ "$work" = off ]; then
    GOWORK=off "$@"
  else
    "$@"
  fi
}
base="${1:-$(git describe --tags --abbrev=0 --match "$tagpat" HEAD 2>/dev/null || true)}"
if [ -z "$base" ]; then
  base="$(git describe --tags --abbrev=0 --match "$tagpat" HEAD 2>/dev/null || true)"
fi
if [ -z "$base" ]; then
  echo "apidiff: $mod has no $tagpat tag reachable from HEAD — the gate starts at the release after the module's first tag; skipping"
  exit 0
fi
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if ! command -v apidiff >/dev/null 2>&1; then
  echo "apidiff not found: go install golang.org/x/exp/cmd/apidiff@latest" >&2
  exit 2
fi

# Fail closed on a non-compiling working tree: apidiff would emit an
# empty report, and without this build the gate would read it as green.
if ! (cd "$mod" && load go build ./...); then
  echo "apidiff: $mod does not compile — the gate cannot vouch for it" >&2
  exit 1
fi

git worktree add --detach --force "$tmp/base" "$base" >/dev/null 2>&1
trap 'git worktree remove --force "$tmp/base" >/dev/null 2>&1 || true; rm -rf "$tmp"' EXIT

# -m: whole module. The base export is written from the tagged tree,
# then compared against the working tree. The base must build too: a
# base that cannot load leaves an empty export and an empty report —
# the same fail-closed hole as above, on the other side.
if ! (cd "$tmp/base/$mod" && load go build ./...); then
  echo "apidiff: the base ref $base ($mod) does not compile — the gate cannot vouch for it" >&2
  exit 1
fi
(cd "$tmp/base/$mod" && load apidiff -m -w "$tmp/base.export" .)

# apidiff exits non-zero when it finds incompatible changes; capture the
# status so the report is still parsed below. A non-zero status with an
# empty report is an execution failure (a load error apidiff printed to
# stderr), not a clean diff — fail closed on that combination.
status=0
(cd "$mod" && load apidiff -m "$tmp/base.export" . > "$tmp/report") || status=$?
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
