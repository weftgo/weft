#!/usr/bin/env sh
# Exercises apidiff.sh's own failure modes, so the gate cannot rot green:
# a deliberately incompatible tree must fail it, and a non-compiling
# tree must fail it before apidiff ever runs (the fail-closed path).
# Both gates are exercised — the root module's and the store module's
# (which compares against the newest store/v* tag, plan §3.1).
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
# tree its own path lives in). The store gate's base-ref argument is
# empty: it resolves the newest store/v* tag itself, the default path
# CI takes.
run_gate() {
  (cd "$tmp/tree" && sh ./scripts/apidiff.sh "$1" "${2:-.}" 2>&1)
}

echo "selftest 1/6: a clean tree reads green"
out="$(run_gate "$base")"
echo "$out" | tail -1
echo "$out" | grep -q "no incompatible changes"

echo "selftest 2/6: an incompatible change fails the gate"
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

echo "selftest 3/6: a non-compiling tree fails the gate before apidiff"
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

echo "selftest 4/6: the store gate reads green on a clean tree"
out="$(run_gate "" store)"
echo "$out" | tail -1
echo "$out" | grep -q "no incompatible changes"

echo "selftest 5/6: an incompatible store change fails the store gate"
sed -i.bak 's/^func Memory() Store {/func MemoryRenamed() Store {/' "$tmp/tree/store/memory.go"
rm -f "$tmp/tree/store/memory.go.bak"
if out="$(run_gate "" store)"; then
  echo "FAIL: a renamed store constructor did not fail the store gate" >&2
  echo "$out" >&2
  exit 1
fi
echo "$out" | grep -q "INCOMPATIBLE" || {
  echo "FAIL: store gate failed without naming the incompatible change" >&2
  exit 1
}
git -C "$tmp/tree" checkout -- store/memory.go

echo "selftest 6/6: a non-compiling store fails the store gate before apidiff"
printf '\nfunc broken( {\n' >> "$tmp/tree/store/memory.go"
if out="$(run_gate "" store)"; then
  echo "FAIL: a non-compiling store read green" >&2
  echo "$out" >&2
  exit 1
fi
echo "$out" | grep -q "does not compile" || {
  echo "FAIL: store gate failed for the wrong reason:" >&2
  echo "$out" >&2
  exit 1
}

echo "apidiff selftest: all six failure modes behave"
