#!/usr/bin/env sh
# Exercises apidiff.sh's own failure modes, so the gate cannot rot green:
# a deliberately incompatible tree must fail it, and a non-compiling
# tree must fail it before apidiff ever runs (the fail-closed path).
# Three gates are exercised — the root module's, an enforced
# sub-module's (thread, which compares against the newest thread/v*
# tag) and a reported one's (obsdb) — plus the workspace fallback and
# the unknown-module refusal.
# Run by CI's apidiff job and `make apidiff-selftest`.
set -eu

cd "$(dirname "$0")/.."
base="apidiff-selftest-base"
unset APIDIFF_STRICT

tmp="$(mktemp -d)"
cleanup() {
  git worktree remove --force "$tmp/tree" >/dev/null 2>&1 || true
  git tag -d "$base" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

git worktree add --detach --force "$tmp/tree" HEAD >/dev/null 2>&1
git -C "$tmp/tree" tag "$base"
# The gate under test is the working tree's copy, not HEAD's: the tree
# is a throwaway checkout, and testing the committed script would lag
# the change being reviewed by one commit.
cp scripts/apidiff.sh "$tmp/tree/scripts/apidiff.sh"

# Run a gate inside the throwaway tree (apidiff.sh operates on the
# tree its own path lives in). An empty base-ref argument makes the
# gate resolve the module's newest tag itself, the default path CI
# takes.
run_gate() {
  (cd "$tmp/tree" && sh ./scripts/apidiff.sh "$1" "${2:-.}" 2>&1)
}

# Rename an exported symbol in the throwaway tree; a symbol that is
# gone from the source means this script is stale, not that the gate
# passed.
mutate() {
  cp "$tmp/tree/$1" "$tmp/mutate.orig"
  sed "$2" "$tmp/mutate.orig" > "$tmp/tree/$1"
  if cmp -s "$tmp/mutate.orig" "$tmp/tree/$1"; then
    echo "FAIL: the selftest's mutation no longer matches $1 — update scripts/apidiff-selftest.sh" >&2
    exit 1
  fi
}

echo "selftest 1/9: a clean tree reads green"
out="$(run_gate "$base")"
echo "$out" | tail -1
echo "$out" | grep -q "no incompatible changes"

echo "selftest 2/9: an incompatible change fails the gate"
mutate agent.go 's/func (a \*Agent) TapPanics()/func (a *Agent) TapPanicsRenamed()/'
if out="$(run_gate "$base")"; then
  echo "FAIL: a renamed exported method did not fail the gate" >&2
  echo "$out" >&2
  exit 1
fi
echo "$out" | grep -q "INCOMPATIBLE" || {
  echo "FAIL: gate failed without naming the incompatible change" >&2
  exit 1
}
git -C "$tmp/tree" checkout -- agent.go

echo "selftest 3/9: a non-compiling tree fails the gate before apidiff"
printf '\nfunc broken( {\n' >> "$tmp/tree/agent.go"
if out="$(run_gate "$base")"; then
  echo "FAIL: a non-compiling tree read green" >&2
  echo "$out" >&2
  exit 1
fi
echo "$out" | grep -q "does not compile" || {
  echo "FAIL: gate failed for the wrong reason:" >&2
  echo "$out" >&2
  exit 1
}
git -C "$tmp/tree" checkout -- agent.go

echo "selftest 4/9: the thread gate reads green on a clean tree"
out="$(run_gate "" thread)"
echo "$out" | tail -1
echo "$out" | grep -q "no incompatible changes"

echo "selftest 5/9: an incompatible thread change fails the thread gate"
mutate thread/session.go 's/^func PublicID(/func PublicIDRenamed(/'
if out="$(run_gate "" thread)"; then
  echo "FAIL: a renamed thread option did not fail the thread gate" >&2
  echo "$out" >&2
  exit 1
fi
echo "$out" | grep -q "INCOMPATIBLE" || {
  echo "FAIL: thread gate failed without naming the incompatible change:" >&2
  echo "$out" >&2
  exit 1
}
git -C "$tmp/tree" checkout -- thread/session.go

echo "selftest 6/9: a non-compiling thread fails the thread gate before apidiff"
printf '\nfunc broken( {\n' >> "$tmp/tree/thread/session.go"
if out="$(run_gate "" thread)"; then
  echo "FAIL: a non-compiling thread read green" >&2
  echo "$out" >&2
  exit 1
fi
echo "$out" | grep -q "does not compile" || {
  echo "FAIL: thread gate failed for the wrong reason:" >&2
  echo "$out" >&2
  exit 1
}
git -C "$tmp/tree" checkout -- thread/session.go

echo "selftest 7/9: a reported module names an incompatible change without failing"
mutate obsdb/hub.go 's/^func QueueSize(/func QueueSizeRenamed(/'
if ! out="$(run_gate "$base" obsdb)"; then
  echo "FAIL: an incompatible change failed a report-only gate:" >&2
  echo "$out" >&2
  exit 1
fi
echo "$out" | grep -q "REPORTED.*QueueSize" || {
  echo "FAIL: the report-only gate did not name the incompatible change:" >&2
  echo "$out" >&2
  exit 1
}
if echo "$out" | grep -q "no incompatible changes"; then
  echo "FAIL: the report-only gate read an incompatible change as clean:" >&2
  echo "$out" >&2
  exit 1
fi
git -C "$tmp/tree" checkout -- obsdb/hub.go

echo "selftest 8/9: a module that needs an untagged sibling loads through the workspace, and says so"
printf 'package obsdb\n\n// SelftestOnly exists only in the selftest tree.\nfunc SelftestOnly() {}\n' > "$tmp/tree/obsdb/zz_selftest.go"
printf 'package otel\n\nimport "github.com/weftgo/weft/obsdb"\n\nvar _ = obsdb.SelftestOnly\n' > "$tmp/tree/otel/zz_selftest.go"
out="$(run_gate "$base" otel)" || {
  echo "FAIL: a tree that builds in the workspace failed the gate:" >&2
  echo "$out" >&2
  exit 1
}
echo "$out" | grep -q "does not build from its published requirements" || {
  echo "FAIL: the workspace fallback was silent:" >&2
  echo "$out" >&2
  exit 1
}
echo "$out" | grep -q "no incompatible changes"
if out="$(cd "$tmp/tree" && APIDIFF_STRICT=1 sh ./scripts/apidiff.sh "$base" otel 2>&1)"; then
  echo "FAIL: APIDIFF_STRICT=1 accepted a module that needs the workspace" >&2
  echo "$out" >&2
  exit 1
fi
echo "$out" | grep -q "does not compile" || {
  echo "FAIL: the strict gate failed for the wrong reason:" >&2
  echo "$out" >&2
  exit 1
}
rm -f "$tmp/tree/obsdb/zz_selftest.go" "$tmp/tree/otel/zz_selftest.go"

echo "selftest 9/9: a module without a policy is refused"
status=0
out="$(run_gate "" no-such-module)" || status=$?
if [ "$status" -ne 2 ]; then
  echo "FAIL: an unknown module exited $status, want 2:" >&2
  echo "$out" >&2
  exit 1
fi

echo "apidiff selftest: all nine failure modes behave"
