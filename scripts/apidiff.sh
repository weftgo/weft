#!/usr/bin/env sh
# The selvedge (TODO §1.7): compare a module's public Go API against
# that module's last tag. Every module in go.work has a policy here —
# a module added to the workspace without one fails the gate (exit 2)
# until it gets a case in policy_of below.
#
#   enforce  an incompatible change fails the build unless its exact
#            apidiff line is listed in the module's .apidiff-allow:
#            the core module (newest core/v* tag; the loop stays
#            additive and source-compatible).
#   report   incompatible changes are printed, never fatal: the root
#            module (newest v* tag) holds the pre-freeze layers —
#            thread, obsdb, otel, runtime, studio (ADR 0024's programme
#            allows breaking them) — beside the adapters and the
#            generated facades. The report is what the release reads:
#            an incompatible line means the next tag is a minor bump
#            with a "breaking" CHANGELOG entry, not a patch. Move the
#            root to enforce when its layers freeze.
#   skip     nothing to gate: examples/* are never tagged.
#
# (The store module's gate died with the module, step 5 of ADR 0024.)
#
# Usage: scripts/apidiff.sh [base-ref] [module-dir|all]
#   module-dir "." (the default) is the root (framework) module; "core"
#   is the loop module; "all" runs every module go.work lists, prints
#   a summary, and fails if any gate failed. base-ref defaults to the module's newest matching
#   tag reachable from HEAD; an explicit empty string means the same.
#   A module with no matching tag yet skips with a note — nothing to
#   diff against, nothing vouched for; the gate starts at the release
#   after the first tag.
#
# Pre-1.0 evolutions that are source-compatible but flagged by apidiff
# (widening a return type to a superset interface, adding a trailing
# variadic) can be acknowledged by listing the exact apidiff line in
# the module's .apidiff-allow (core/.apidiff-allow; the root would be
# ./.apidiff-allow). Anything not listed fails an enforced module.
# Post-1.0 the allowlist is emptied and stays empty.
#
# The gate fails closed: a tree that does not compile, or an apidiff
# run that dies before writing a report, must never reach the "no
# incompatible changes" path. An empty report means "nothing to parse",
# which is green only when apidiff itself succeeded.
# scripts/apidiff-selftest.sh exercises the failure modes.
#
# How a module is loaded. Each side (the tag's tree, the working tree)
# is built with GOWORK=off first: its requirements then resolve from
# the module graph (the repo is public; the proxy serves the tags), so
# the gate does not lean on go.work. The root on main may
# legitimately not build that way between releases — it uses a core
# change that is not tagged yet (the two-phase release, ADR 0027), or
# `go work sync` moved a requirement its go.sum has not followed. The
# gate then says so and loads that side through the workspace instead;
# a side that compiles in neither mode fails the gate. The note is the
# release order: a module that prints it needs its dependency tagged
# (and its go.mod/go.sum tidied against that tag) before its own tag.
# APIDIFF_STRICT=1 turns the note into a failure — the release-time
# check that every module builds from published tags alone.
#
# (Before 0.9.0 thread/sqlite loaded through the workspace first, on
# both sides; it is a package of the root now.)
set -eu

cd "$(dirname "$0")/.."
self="$(pwd)/scripts/apidiff.sh"
mod="${2:-.}"

# policy_of <module-dir> sets tagpat, policy and load ("graph": the
# module graph first, the workspace as a noted fallback; "workspace":
# the workspace first, see the header), or returns 1 for a directory
# the gate does not know.
policy_of() {
  load=graph
  case "$1" in
    core) tagpat='core/v*'; policy=enforce ;;
    .) tagpat='v*'; policy=report ;;
    examples/*) tagpat=''; policy=skip ;;
    *) return 1 ;;
  esac
}

if [ "$mod" = all ]; then
  root="$(pwd)"
  dirs="$(go list -m -f '{{.Dir}}')" || {
    echo "apidiff: cannot list the workspace modules (go list -m)" >&2
    exit 2
  }
  failed=""
  for dir in $dirs; do
    rel="${dir#"$root"}"
    rel="${rel#/}"
    [ -n "$rel" ] || rel=.
    if ! sh "$self" "${1:-}" "$rel"; then
      failed="$failed $rel"
    fi
  done
  if [ -n "$failed" ]; then
    echo "apidiff: FAILED for:$failed" >&2
    exit 1
  fi
  echo "apidiff: every workspace module passed its gate"
  exit 0
fi

if ! policy_of "$mod"; then
  echo "apidiff: unknown module dir '$mod' — add its policy (enforce, report or skip) to policy_of in scripts/apidiff.sh" >&2
  exit 2
fi
if [ "$policy" = skip ]; then
  echo "apidiff: $mod is a command or an example — no importable API to gate; skipping"
  exit 0
fi

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

# load_mode <dir> <what> picks how one side is loaded and leaves the
# answer in $gowork: "off" (the module graph alone) or "" (the
# workspace found from <dir>). Returns 1 when the side compiles in
# neither mode — or, under APIDIFF_STRICT=1, when it needs the
# workspace. core never needs the workspace: its requirements are
# published; the root may, between a core change and its tag.
load_mode() {
  : > "$tmp/build-ws.err"
  if [ "$load" = workspace ] && [ "${APIDIFF_STRICT:-}" != 1 ]; then
    if (cd "$1" && GOWORK='' go build ./...) 2>"$tmp/build-ws.err"; then
      gowork=''
      return 0
    fi
    cat "$tmp/build-ws.err" >&2
    return 1
  fi
  if (cd "$1" && GOWORK=off go build ./...) 2>"$tmp/build.err"; then
    gowork=off
    return 0
  fi
  if [ "${APIDIFF_STRICT:-}" != 1 ] && [ "$mod" != core ] && (cd "$1" && GOWORK='' go build ./...) 2>"$tmp/build-ws.err"; then
    echo "apidiff: note: $2 does not build from its published requirements (GOWORK=off):"
    sed 's/^/    /' "$tmp/build.err" | head -5
    echo "apidiff: note: loading it through the workspace instead — tag its dependencies (and tidy its go.mod/go.sum against them) before tagging $mod"
    gowork=''
    return 0
  fi
  cat "$tmp/build.err" >&2
  if [ -s "$tmp/build-ws.err" ]; then
    echo "apidiff: through the workspace:" >&2
    cat "$tmp/build-ws.err" >&2
  fi
  return 1
}

# Fail closed on a non-compiling working tree: apidiff would emit an
# empty report, and without this build the gate would read it as green.
if ! load_mode "$mod" "$mod"; then
  echo "apidiff: $mod does not compile — the gate cannot vouch for it" >&2
  exit 1
fi
head_gowork="$gowork"

git worktree add --detach --force "$tmp/base" "$base" >/dev/null 2>&1
trap 'git worktree remove --force "$tmp/base" >/dev/null 2>&1 || true; rm -rf "$tmp"' EXIT

# -m: whole module. The base export is written from the tagged tree,
# then compared against the working tree. The base must build too: a
# base that cannot load leaves an empty export and an empty report —
# the same fail-closed hole as above, on the other side.
if ! load_mode "$tmp/base/$mod" "the base ref $base ($mod)"; then
  echo "apidiff: the base ref $base ($mod) does not compile — the gate cannot vouch for it" >&2
  exit 1
fi
(cd "$tmp/base/$mod" && GOWORK="$gowork" apidiff -m -w "$tmp/base.export" .)

# apidiff exits non-zero when it finds incompatible changes; capture the
# status so the report is still parsed below. A non-zero status with an
# empty report is an execution failure (a load error apidiff printed to
# stderr), not a clean diff — fail closed on that combination.
status=0
(cd "$mod" && GOWORK="$head_gowork" apidiff -m "$tmp/base.export" . > "$tmp/report") || status=$?
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
  elif [ "$policy" = report ]; then
    echo "apidiff: REPORTED (not fatal, $mod is pre-freeze): $line"
    status=3
  else
    echo "apidiff: INCOMPATIBLE: $line" >&2
    status=1
  fi
done < "$tmp/incompatible"

if [ "$status" -eq 3 ]; then
  echo "apidiff: $mod changed incompatibly since $base — allowed for a pre-freeze module; its next tag is a minor bump with a breaking CHANGELOG entry, not a patch"
  exit 0
fi
if [ "$status" -ne 0 ]; then
  echo "apidiff: incompatible API change(s) in $mod since $base. Revert the change, or — pre-1.0 only — add the exact line above to $allow with a reviewed justification." >&2
fi
exit "$status"
