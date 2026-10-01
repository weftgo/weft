#!/usr/bin/env sh
# Exercises apidiff.sh's own failure modes, so the gate cannot rot green:
# a deliberately incompatible tree must fail it, and a non-compiling
# tree must fail it before apidiff ever runs (the fail-closed path).
# All three gates are exercised — the root module's, the thread
# module's (against the newest thread/v* tag) and thread/sqlite's
# (against the newest thread/sqlite/v* tag, loaded through go.work).
# Run by CI's apidiff job and `make apidiff-selftest`.
set -eu

cd "$(dirname "$0")/.."
base="apidiff-selftest-base"

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
# tree its own path lives in). The sub-module gates' base-ref argument
# is empty: each resolves its newest tag itself, the default path CI
# takes — so their clean-tree steps also prove the committed
# .apidiff-allow files cover what the tree changed since those tags.
run_gate() {
  (cd "$tmp/tree" && sh ./scripts/apidiff.sh "$1" "${2:-.}" 2>&1)
}

echo "selftest 1/9: a clean tree reads green"
out="$(run_gate "$base")"
echo "$out" | tail -1
echo "$out" | grep -q "no incompatible changes"

echo "selftest 2/9: an incompatible change fails the gate"
sed -i.bak 's/func (a \*Agent) TapPanics()/func (a *Agent) TapPanicsRenamed()/' "$tmp/tree/agent.go"
rm -f "$tmp/tree/agent.go.bak"
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

# sub_gate exercises one sub-module's gate: green on the clean tree,
# red on a renamed exported function, red on a tree that does not
# compile. $1 first step number, $2 module dir, $3 file, $4 the sed
# expression that renames an exported function in it.
sub_gate() {
  n="$1"; mod="$2"; file="$3"; rename="$4"

  echo "selftest $n/9: the $mod gate reads green on a clean tree"
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

  echo "selftest $((n + 1))/9: an incompatible $mod change fails the $mod gate"
  sed -i.bak "$rename" "$tmp/tree/$file"
  rm -f "$tmp/tree/$file.bak"
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

  echo "selftest $((n + 2))/9: a non-compiling $mod fails the $mod gate before apidiff"
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

sub_gate 4 thread thread/memory.go 's/^func Memory(/func MemoryRenamed(/'
sub_gate 7 thread/sqlite thread/sqlite/sqlite.go 's/^func Open(/func OpenRenamed(/'

echo "apidiff selftest: all nine failure modes behave"
