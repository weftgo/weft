#!/usr/bin/env sh
# Exercises apidiff.sh's own failure modes, so the gate cannot rot green:
# a deliberately incompatible tree must fail it, and a non-compiling
# tree must fail it before apidiff ever runs (the fail-closed path).
# Four gates are exercised — the root module's, two enforced
# sub-modules' (thread, against the newest thread/v* tag, and
# thread/sqlite, against the newest thread/sqlite/v* tag and loaded
# through go.work) and a reported one's (obsdb) — plus the workspace
# fallback and the unknown-module refusal.
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
# takes — so the sub-modules' clean-tree steps also prove the committed
# .apidiff-allow files cover what the tree changed since those tags.
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

echo "selftest 1/12: a clean tree reads green"
out="$(run_gate "$base")"
echo "$out" | tail -1
echo "$out" | grep -q "no incompatible changes"

echo "selftest 2/12: an incompatible change fails the gate"
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

echo "selftest 3/12: a non-compiling tree fails the gate before apidiff"
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

# sub_gate exercises one enforced sub-module's gate: green on the
# clean tree, red on a renamed exported function, red on a tree that
# does not compile. $1 first step number, $2 module dir, $3 file, $4
# the sed expression that renames an exported function in it.
sub_gate() {
  n="$1"; mod="$2"; file="$3"; rename="$4"

  echo "selftest $n/12: the $mod gate reads green on a clean tree"
  out="$(run_gate "" "$mod")" || {
    echo "FAIL: the $mod gate is red on a clean tree:" >&2
    echo "$out" >&2
    exit 1
  }
  echo "$out" | tail -1
  echo "$out" | grep -q "no incompatible changes\|allowed (listed in" || {
    echo "FAIL: the $mod gate did not report its comparison:" >&2
    echo "$out" >&2
    exit 1
  }

  echo "selftest $((n + 1))/12: an incompatible $mod change fails the $mod gate"
  mutate "$file" "$rename"
  if out="$(run_gate "" "$mod")"; then
    echo "FAIL: a renamed exported function did not fail the $mod gate" >&2
    echo "$out" >&2
    exit 1
  fi
  echo "$out" | grep -q "INCOMPATIBLE" || {
    echo "FAIL: the $mod gate failed without naming the incompatible change:" >&2
    echo "$out" >&2
    exit 1
  }
  git -C "$tmp/tree" checkout -- "$file"

  echo "selftest $((n + 2))/12: a non-compiling $mod fails the $mod gate before apidiff"
  printf '\nfunc broken( {\n' >> "$tmp/tree/$file"
  if out="$(run_gate "" "$mod")"; then
    echo "FAIL: a non-compiling $mod read green" >&2
    echo "$out" >&2
    exit 1
  fi
  echo "$out" | grep -q "does not compile" || {
    echo "FAIL: the $mod gate failed for the wrong reason:" >&2
    echo "$out" >&2
    exit 1
  }
  git -C "$tmp/tree" checkout -- "$file"
}

sub_gate 4 thread thread/session.go 's/^func PublicID(/func PublicIDRenamed(/'
sub_gate 7 thread/sqlite thread/sqlite/sqlite.go 's/^func Open(/func OpenRenamed(/'

echo "selftest 10/12: a reported module names an incompatible change without failing"
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

echo "selftest 11/12: a module that needs an untagged sibling loads through the workspace, and says so"
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

echo "selftest 12/12: a module without a policy is refused"
status=0
out="$(run_gate "" no-such-module)" || status=$?
if [ "$status" -ne 2 ]; then
  echo "FAIL: an unknown module exited $status, want 2:" >&2
  echo "$out" >&2
  exit 1
fi

echo "apidiff selftest: all twelve failure modes behave"
