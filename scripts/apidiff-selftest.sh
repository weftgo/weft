#!/usr/bin/env sh
# Exercises apidiff.sh's own failure modes, so the gate cannot rot green:
# a deliberately incompatible tree must fail it, and a non-compiling
# tree must fail it before apidiff ever runs (the fail-closed path).
# Both modules' gates are exercised — core's (enforced, against the
# newest core/v* tag) and the root's (reported: the framework module
# holds the pre-freeze layers) — plus the root's workspace fallback
# onto an untagged core change and the unknown-module refusal.
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

echo "selftest 1/8: a clean core reads green"
out="$(run_gate "$base" core)"
echo "$out" | tail -1
echo "$out" | grep -q "no incompatible changes"

echo "selftest 2/8: an incompatible core change fails the core gate"
mutate core/agent.go 's/func (a \*Agent) TapPanics()/func (a *Agent) TapPanicsRenamed()/'
if out="$(run_gate "$base" core)"; then
  echo "FAIL: a renamed exported method did not fail the gate" >&2
  echo "$out" >&2
  exit 1
fi
echo "$out" | grep -q "INCOMPATIBLE" || {
  echo "FAIL: gate failed without naming the incompatible change" >&2
  exit 1
}
git -C "$tmp/tree" checkout -- core/agent.go

echo "selftest 3/8: a non-compiling core fails the core gate before apidiff"
printf '\nfunc broken( {\n' >> "$tmp/tree/core/agent.go"
if out="$(run_gate "$base" core)"; then
  echo "FAIL: a non-compiling tree read green" >&2
  echo "$out" >&2
  exit 1
fi
echo "$out" | grep -q "does not compile" || {
  echo "FAIL: gate failed for the wrong reason:" >&2
  echo "$out" >&2
  exit 1
}
git -C "$tmp/tree" checkout -- core/agent.go

echo "selftest 4/8: a clean root reads green"
out="$(run_gate "$base")"
echo "$out" | tail -1
echo "$out" | grep -q "no incompatible changes"

echo "selftest 5/8: the root (reported) names an incompatible change without failing"
mutate obsdb/hub.go 's/^func QueueSize(/func QueueSizeRenamed(/'
if ! out="$(run_gate "$base")"; then
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

echo "selftest 6/8: a non-compiling root fails the root gate even though it only reports"
printf '\nfunc broken( {\n' >> "$tmp/tree/thread/session.go"
if out="$(run_gate "$base")"; then
  echo "FAIL: a non-compiling root read green" >&2
  echo "$out" >&2
  exit 1
fi
echo "$out" | grep -q "does not compile" || {
  echo "FAIL: the root gate failed for the wrong reason:" >&2
  echo "$out" >&2
  exit 1
}
git -C "$tmp/tree" checkout -- thread/session.go

echo "selftest 7/8: a root that needs an untagged core change loads through the workspace, and says so"
# The throwaway tree must resolve core from its published requirements,
# as a consumer would: drop any development replace first.
(cd "$tmp/tree" && go mod edit -dropreplace=github.com/weftgo/weft/core)
printf 'package core\n\n// SelftestOnly exists only in the selftest tree.\nfunc SelftestOnly() {}\n' > "$tmp/tree/core/zz_selftest.go"
printf 'package weft\n\nimport "github.com/weftgo/weft/core"\n\nvar _ = core.SelftestOnly\n' > "$tmp/tree/zz_selftest.go"
out="$(run_gate "$base")" || {
  echo "FAIL: a tree that builds in the workspace failed the gate:" >&2
  echo "$out" >&2
  exit 1
}
echo "$out" | grep -q "does not build from its published requirements" || {
  echo "FAIL: the workspace fallback was silent:" >&2
  echo "$out" >&2
  exit 1
}
if out="$(cd "$tmp/tree" && APIDIFF_STRICT=1 sh ./scripts/apidiff.sh "$base" . 2>&1)"; then
  echo "FAIL: APIDIFF_STRICT=1 accepted a module that needs the workspace" >&2
  echo "$out" >&2
  exit 1
fi
echo "$out" | grep -q "does not compile" || {
  echo "FAIL: the strict gate failed for the wrong reason:" >&2
  echo "$out" >&2
  exit 1
}
rm -f "$tmp/tree/core/zz_selftest.go" "$tmp/tree/zz_selftest.go"
git -C "$tmp/tree" checkout -- go.mod

echo "selftest 8/8: a module without a policy is refused"
status=0
out="$(run_gate "" no-such-module)" || status=$?
if [ "$status" -ne 2 ]; then
  echo "FAIL: an unknown module exited $status, want 2:" >&2
  echo "$out" >&2
  exit 1
fi

echo "apidiff selftest: all eight failure modes behave"
