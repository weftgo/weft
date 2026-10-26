#!/usr/bin/env bash
# gate10a.sh — the phase 10a gate (experiments you can trust, runtimes
# you can see), end to end: a fresh Studio from $REPO, the arena built
# against this tree (a scratch go.work, never edited) and seeded offline
# into it, then `weft-arena serve --offline` linked to it (its three
# runtime links: the fleet, the rw.budget-guards batch it runs, the
# thread lab's), the journeys (gate10a.js: (a) the experiment journey on
# rw.budget-guards in Studio and the panel, (b) /runtimes with a Studio
# restart over the same db, (c) /agents, (d) the error-class facet → the
# matrix form), (e) `weft dev -- studio-local` driven from the page's
# panel, (f) `make mcp-check`, (g) `make studio-check` and the panel
# ledger, (h1–h7) the principle gate. Every process the gate starts runs
# under `strace -e connect` when strace is there (h4). One line per
# clause: PASS / FAIL / RECORDED with its evidence; the lot in
# $RUN/gate10a.txt; the last line `GATE 10a: PASSED` or
# `GATE 10a: FAILED (<clauses>)`. No live model call: the seed and serve
# run --offline, every Run pressed reads data-run-engine="scripted", the
# matrix is never run, studio-local's model is its echo.
#
#   ARENA=… RUN=… PLAYWRIGHT_DIR=… scripts/ux-gate/gate10a.sh
#
# REPO     the weft tree under test (default: this script's own tree)
# ARENA    the weft-arena checkout (default: $REPO/../weft-arena); never
#          modified — built against $REPO through $RUN/arena.work
# RUN      scratch dir for the DBs, logs, binaries and screenshots
#          (default ./gate10a-run); emptied of the gate's own files first
# PLAYWRIGHT_DIR  a dir whose node_modules has playwright (pw.js)
# STUDIO_CHECK=0  skip `make studio-check` (g then reads the committed
#          bundle only)
# KNOWN_STALE   the arena scenarios that fail by design against this
#          weft (the seed line is RECORDED when the failures are a
#          subset; default: the five phase 10a changed words / shapes)
# LAST_ROW      when set, the ledger's last row must carry this label
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="${REPO:-$(cd "$HERE/../.." && pwd)}"
ARENA="${ARENA:-$REPO/../weft-arena}"
[ -d "$ARENA" ] || { echo "no weft-arena checkout at $ARENA: set ARENA=" >&2; exit 2; }
ARENA="$(cd "$ARENA" && pwd)"
RUN="${RUN:-$PWD/gate10a-run}"
ADDR="${ADDR:-127.0.0.1:7341}"
SERVE_ADDR="${SERVE_ADDR:-127.0.0.1:7789}"
DEV_ADDR="${DEV_ADDR:-127.0.0.1:7397}"
APP_ADDR="${APP_ADDR:-127.0.0.1:8093}"
TOKEN="${TOKEN:-gate10atok}"
DEV_TOKEN="${DEV_TOKEN:-gate10adevtok}"
STUDIO="http://$ADDR"
CAP_FLOOR="${CAP_FLOOR:-4096}"
KNOWN_STALE="${KNOWN_STALE:-obs18.env-destinations runtime18.sources runtimelab.transcript-edits rw.ops-console rw.records-forensics}"
export PLAYWRIGHT_DIR="${PLAYWRIGHT_DIR:-}" GATE_SHOTS="$RUN/shots"
mkdir -p "$RUN"
rm -rf "$RUN/shots" "$RUN"/*.strace "$RUN"/*.log "$RUN"/gate10a-*.json "$RUN"/studio.db* "$RUN"/dev.db* "$RUN/devapp"
mkdir -p "$RUN/shots" "$RUN/devapp"
OUT="$RUN/gate10a.txt"; : > "$OUT"
say() { echo "$*" | tee -a "$OUT"; }
api() { curl -sf -H "Authorization: Bearer $TOKEN" "$STUDIO$1"; }
noise='^(zoxide|Please ensure|If the issue|https://github.com/ajeetdsouza|Disable this message|$)'
FAILED=()
clause() { # id status title evidence…
  local id="$1" st="$2" title="$3"; shift 3
  [ "$st" = FAIL ] && FAILED+=("$id")
  say "($id) $st — $title"; for e in "$@"; do say "      $e"; done
}
T0=$(date +%s)

# bg <strace file> <log> <cmd…>: starts cmd in the background, under
# strace -f -e connect when available; BGPID is the command's own pid,
# BGWRAP the process to wait for.
STRACE=0; command -v strace >/dev/null && [ "${STRACE_ON:-1}" != 0 ] && STRACE=1
PIDS=()
bg() {
  local f="$1" log="$2"; shift 2
  if [ $STRACE = 1 ]; then
    strace -f -qq -e trace=connect -o "$f" "$@" > "$log" 2>&1 &
    BGWRAP=$!; BGPID=""
    for _ in $(seq 1 100); do BGPID=$(pgrep -P "$BGWRAP" | head -1); [ -n "$BGPID" ] && break; sleep 0.05; done
  else
    "$@" > "$log" 2>&1 &
    BGWRAP=$!; BGPID=$BGWRAP
  fi
  PIDS+=("$BGPID" "$BGWRAP")
}
stop() { # pid wrap
  [ -n "$1" ] && kill "$1" 2>/dev/null
  for _ in $(seq 1 100); do kill -0 "$1" 2>/dev/null || break; sleep 0.1; done
  kill -9 "$1" 2>/dev/null; wait "$2" 2>/dev/null
}
cleanup() { for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null; done; wait 2>/dev/null; }
trap cleanup EXIT
waitfor() { # seconds cmd…
  local n=$(( $1 * 4 )); shift
  for _ in $(seq 1 "$n"); do "$@" >/dev/null 2>&1 && return 0; sleep 0.25; done; return 1
}
rt_has() { api /api/runtimes | jq -e --arg a "$1" '[.runtimes[] | select(.connected != false) | select(any(.agents[]?; .name == $a))] | length > 0'; }

say "gate 10a — $(date -u +%FT%TZ)"
say "repo   $REPO @ $(git -C "$REPO" log -1 --format='%h %s' | cut -c1-100)$( [ -n "$(git -C "$REPO" status --porcelain -- . ':!scripts/ux-gate')" ] && echo ' (DIRTY)')"
say "arena  $ARENA @ $(git -C "$ARENA" rev-parse --short HEAD) ($(git -C "$ARENA" status --porcelain | wc -l) uncommitted paths, built as is, never edited) against $REPO through $RUN/arena.work"
say "run    $RUN (strace: $([ $STRACE = 1 ] && echo on || echo 'not available'))"
for a in "$ADDR" "$SERVE_ADDR" "$DEV_ADDR" "$APP_ADDR"; do
  if ss -ltn 2>/dev/null | grep -q " $a "; then say "$a is taken: $(ss -ltnp 2>/dev/null | grep " $a ")"; exit 2; fi
done

# ---- 1. builds: weft and studio-local from $REPO, the arena against it ----
( cd "$REPO" && go build -o "$RUN/weft" ./cmd/weft && go build -o "$RUN/studio-local" ./examples/studio-local ) > "$RUN/build.log" 2>&1 \
  || { say "weft / studio-local build failed: $(tail -5 "$RUN/build.log")"; exit 2; }
printf 'go 1.26.0\n\nuse (\n\t%s\n\t%s\n\t%s/core\n)\n' "$ARENA" "$REPO" "$REPO" > "$RUN/arena.work"
( cd "$ARENA" && GOWORK="$RUN/arena.work" go build -o "$RUN/weft-arena" ./cmd/weft-arena ) > "$RUN/arena-build.log" 2>&1 \
  || { say "arena build FAILED:"; tail -30 "$RUN/arena-build.log" | tee -a "$OUT"; exit 3; }

# ---- 2. Studio, a fresh DB -------------------------------------------------
start_studio() { # strace-file log
  bg "$1" "$2" "$RUN/weft" studio --addr "$ADDR" --db "sqlite://$RUN/studio.db" --token "$TOKEN" --open=false
  SPID=$BGPID; SWRAP=$BGWRAP
  waitfor 30 api /api/meta || { say "Studio did not answer /api/meta"; cat "$2"; exit 2; }
}
start_studio "$RUN/studio.strace" "$RUN/studio.log"
say "studio $STUDIO pid $SPID: $(api /api/meta | jq -c '{weft_version, auth_required, pricing: .pricing.source}')"

# ---- 3. the arena's offline seed ------------------------------------------
( cd "$ARENA" && WEFT_STUDIO_URL="$STUDIO" WEFT_STUDIO_TOKEN="$TOKEN" \
  $([ $STRACE = 1 ] && echo strace -f -qq -e trace=connect -o "$RUN/seed.strace") \
  "$RUN/weft-arena" run --offline --studio --no-save --jobs 8 ) > "$RUN/seed.log" 2>&1
SEEDRC=$?
seedline=$(grep -E '^weft-arena — engine=' "$RUN/seed.log" | tail -1)
failed=$(grep -E '^❌' "$RUN/seed.log" | awk '{print $2}' | sort | tr '\n' ' ' | sed 's/ $//')
sleep 3 # the exporter's last batch
say "seed   exit $SEEDRC: $seedline"
say "seed   landed: $(api '/api/runs?limit=1' | jq .total) top-level runs, $(api '/api/runs?limit=1&all=1' | jq .total) with subagent children, $(api '/api/sessions?limit=1' | jq .total) sessions"
unknown=""; for f in $failed; do [[ " $KNOWN_STALE " == *" $f "* ]] || unknown="$unknown $f"; done
note() { case "$1" in
  rw.records-forensics) echo "expects the store at schema 22 (\"migrated to schema 22\"); weft 0.19 adds migrations 0023 span_stats, 0024 pass_predicate, 0025 spans.attempt (U24.1) — the check should read the current schema (or ≥ 22)";;
  runtimelab.transcript-edits) echo "expects \"transcript edits need engine live\"; U17 rewrote the scripted refusal: \"your edit changes step N's request, and scripted holds only the recorded answer to the original — run on live, or drop the edit\"";;
  rw.ops-console) echo "runs \`weft export --wefttest --test …\` expecting the fixtures-only tree; U19 (Breaking) made --wefttest write the save-as-test tree — \`--test goes with --fixtures-only\` (exit 2): add --fixtures-only or follow the new tree (TestReplay_<run>)";;
  runtime18.sources) echo "expects the 0.18 scripted-miss and refusal words (\"no recorded turn for this request (scripted engine\", \"scripted engine with a model override would silently replay…\", \"…a source run is required\"); U18.1/U18.2 rewrote them (\"no recorded turn for step N (scripted engine, key …): … [component]\", \"scripted cannot try another model: …\", \"scripted replays a recorded run's answers: pick a source run, or run on live\")";;
  obs18.env-destinations) echo "expects \"otel: Blocking ignored: WEFT_ENV is not dev\" under otel.NoEnv(); I2b names the real cause there: \"otel: Blocking ignored: otel.NoEnv reads no WEFT_ENV\" (the old text stays for the env case)";;
  *) echo "not a known stale check: look at $RUN/seed.log";; esac; }
sev=(); for f in $failed; do sev+=("arena note · $f: $(note "$f")"); done
while read -r l; do sev+=("    $l"); done < <(grep -E '^❌' -A6 "$RUN/seed.log" | grep -E '^ +❌' | cut -c1-200)
if [ -z "$failed" ]; then sst=PASS; elif [ -z "$unknown" ]; then sst=RECORDED; else sst=FAIL; fi
clause seed "$sst" "the arena's offline seed: every failure a known stale arena check (KNOWN_STALE: $KNOWN_STALE)" \
  "$seedline" "failed: ${failed:-none}${unknown:+ — NOT KNOWN:$unknown}" "${sev[@]}"

# ---- 4. weft-arena serve: its three runtime links ----------------------------
cd "$ARENA" || exit 2
bg "$RUN/serve.strace" "$RUN/serve.log" env WEFT_STUDIO_URL="$STUDIO" WEFT_STUDIO_TOKEN="$TOKEN" "$RUN/weft-arena" serve --offline --addr "$SERVE_ADDR"
APID=$BGPID; AWRAP=$BGWRAP
cd - >/dev/null || exit 2
waitfor 60 rt_has writer || say "serve: no fleet runtime registered ($(tail -3 "$RUN/serve.log"))"
batch=$(curl -sf -X POST "http://$SERVE_ADDR/api/runs" -d '{"scenarios":["rw.budget-guards"]}')
waitfor 60 rt_has poller || say "serve: no rw.budget-guards batch runtime registered"
curl -sf -X POST "http://$SERVE_ADDR/api/thread/sessions" -d '{"scenario":"support","settings":{"engine":"offline","storage":"memory","title":"gate10a"}}' > "$RUN/thread-session.json"
waitfor 30 rt_has acme-support || say "serve: no thread runtime registered"
sleep 3
say "serve  pid $APID ($SERVE_ADDR, engine offline): batch $(echo "$batch" | jq -c .run_ids) · thread session $(jq -r .id "$RUN/thread-session.json") · runtimes: $(api /api/runtimes | jq -r '[.runtimes[] | "\(.id) pid \(.pid) \(.agents | length) agents"] | join(" · ")')"

# ---- 5. the journeys (a)–(d) -------------------------------------------------
js() { ( cd "$HERE" && STUDIO="$STUDIO" TOKEN="$TOKEN" RUN="$RUN" "$@" ) 2>&1 | grep -v -E "$noise"; return "${PIPESTATUS[0]}"; }
ui() { # phase [env…]
  local ph="$1"; shift
  js env "$@" node gate10a.js "$ph" > "$RUN/gate10a-$ph.txt"; local rc=$?
  cat "$RUN/gate10a-$ph.txt" | tee -a "$OUT"
  for id in $(grep -oE '^\([a-z0-9]+\) FAIL' "$RUN/gate10a-$ph.txt" | tr -d '()' | awk '{print $1}'); do FAILED+=("$id"); done
  [ "$rc" -ge 2 ] && FAILED+=("js-$ph")
}
ui main

# ---- 6. (b) the arena stopped, Studio restarted over the same db ------------
stop "$APID" "$AWRAP"
sleep 2
ui gone
stop "$SPID" "$SWRAP"
start_studio "$RUN/studio2.strace" "$RUN/studio2.log"
say "studio restarted over $RUN/studio.db: pid $SPID — $(grep -oE 'restored from the store.*' "$RUN/studio2.log" | head -1 | cut -c1-120)"
ui restart

# ---- 7. (e) weft dev -- studio-local -----------------------------------------
cd "$RUN/devapp" || exit 2
bg "$RUN/dev.strace" "$RUN/dev.log" "$RUN/weft" dev --addr "$DEV_ADDR" --db "sqlite://$RUN/dev.db" --token "$DEV_TOKEN" --open=false -- "$RUN/studio-local" -addr "$APP_ADDR"
DPID=$BGPID; DWRAP=$BGWRAP
cd - >/dev/null || exit 2
waitfor 60 grep -q 'runtime .* registered' "$RUN/dev.log"
APP_PID=$(grep -oE 'app pid [0-9]+' "$RUN/dev.log" | tail -1 | awk '{print $3}')
say "dev    $(grep -E '^studio http' "$RUN/dev.log" | tail -1)"
if [ -n "$APP_PID" ]; then
  ui dev DEV_STUDIO="http://$DEV_ADDR" DEV_TOKEN="$DEV_TOKEN" APP="http://$APP_ADDR" APP_PID="$APP_PID"
  devmode=real
else
  devmode=fallback
fi
say "dev    weft dev after the kill: $(grep -E 'exited|restarts' "$RUN/dev.log" | tail -1)"
stop "$DPID" "$DWRAP"
dt=$(cd "$REPO" && go test -count=1 -run '^(TestWeftDevApprovesFromThePagePanel)$' ./examples/studio-local 2>&1 | tail -1)
dt2=$(cd "$REPO" && go test -count=1 -run '^(TestStoppedAppTurnReadsDisconnected)$' ./runtime 2>&1 | tail -1)
if [ "$devmode" = fallback ]; then
  st=$(echo "$dt" | grep -q '^ok' && echo PASS || echo FAIL)
  clause e "$st" "weft dev hosts studio-local (FALLBACK: the real command did not come up — the test instead)" "$(tail -5 "$RUN/dev.log" | tr '\n' ' ')" "go test -run TestWeftDevApprovesFromThePagePanel ./examples/studio-local → $dt"
else
  say "      supporting: go test -run TestWeftDevApprovesFromThePagePanel ./examples/studio-local → $dt · go test -run TestStoppedAppTurnReadsDisconnected ./runtime → $dt2"
fi

# ---- 8. (f) make mcp-check ------------------------------------------------------
( cd "$REPO" && make mcp-check ) > "$RUN/mcp-check.log" 2>&1; MRC=$?
mev=(); while read -r l; do mev+=("$l"); done < <(grep -E '^mcp-check: seeded sub|first error: step 0 shipping_quote|^run c5b0+c405 .*first error|denied by alice: change freeze until Monday → resumed|bytes compact|MCP CHECK|^--- (PASS|FAIL)' "$RUN/mcp-check.log" | cut -c1-230)
clause f "$([ $MRC = 0 ] && grep -q 'MCP CHECK PASS' "$RUN/mcp-check.log" && echo PASS || echo FAIL)" "make mcp-check passes (I5's three answers: sub.failed's child error, agentic.chaos's shapes, a denied thread turn; a nine-turn session under 4 KB)" \
  "make mcp-check → exit $MRC" "${mev[@]}"

# ---- 9. (g) make studio-check + the ledger ----------------------------------------
ps=$(cd "$HERE" && bun panel-size.ts "$REPO" 2>&1 | tail -1)
if [ "${STUDIO_CHECK:-1}" != 0 ]; then
  ( cd "$REPO" && make studio-check ) > "$RUN/studio-check.log" 2>&1; SCRC=$?
  ps=$(cd "$HERE" && bun panel-size.ts "$REPO" 2>&1 | tail -1)
  sc="make studio-check → exit $SCRC: $(grep -E '^studio dist: ' "$RUN/studio-check.log" | tail -1); $(grep -E '^ +Tests +[0-9]' "$RUN/studio-check.log" | head -1 | sed -E 's/ +/ /g')"
  hl=$(grep -E '^total .* headroom' "$RUN/studio-check.log" | tail -1)
  kib=$(grep -oE '^studio dist: [0-9]+' "$RUN/studio-check.log" | tail -1 | grep -oE '[0-9]+$')
  dirty=$(git -C "$REPO" status --porcelain -- studio/dist studio/web/npm | wc -l)
else SCRC=0; sc="skipped (STUDIO_CHECK=0)"; hl=$(echo "$ps" | jq -r .line); kib=""; dirty=0; fi
measured=$(echo "$ps" | jq -r .measured); cap=$(echo "$ps" | jq -r .cap); last=$(echo "$ps" | jq -r .last_gzip); label=$(echo "$ps" | jq -r .last_label)
room=$((cap - measured))
[ -z "$kib" ] && kib=$(( $(find "$REPO/studio/dist" -type f -exec sh -c 'gzip -c "$1" | wc -c' _ {} \; | paste -sd+ | bc) / 1024 ))
labok=$([ -z "${LAST_ROW:-}" ] || [ "$label" = "$LAST_ROW" ] && echo yes || echo no)
gok=$([ "$SCRC" = 0 ] && [ "$measured" = "$last" ] && [ "$labok" = yes ] && [ "$cap" = 106496 ] && [ "$room" -ge "$CAP_FLOOR" ] && [ "$kib" -le 800 ] && [ "$dirty" = 0 ] && echo PASS || echo FAIL)
clause g "$gok" "make studio-check passes; the ledger's last row equals the built panel.js; headroom ≥ $CAP_FLOOR B under 106,496 B; Studio dist ≤ 800 KiB" \
  "$sc" "the build's ledger line: ${hl:-?}" \
  "panel measured $measured B gzip (studio/dist/panel/panel.js, stamp-normalized) = the ledger's last row $label $last B: $([ "$measured" = "$last" ] && echo yes || echo NO)${LAST_ROW:+ (want $LAST_ROW: $labok)}; cap $cap B → headroom $room B ($([ "$room" -ge "$CAP_FLOOR" ] && echo "≥ $CAP_FLOOR" || echo "UNDER $CAP_FLOOR"))" \
  "Studio dist $kib KiB gzipped (≤ 800: $([ "$kib" -le 800 ] && echo yes || echo NO)); studio/dist + npm left clean by the rebuild: $([ "$dirty" = 0 ] && echo yes || echo "NO ($dirty paths)")"

# ---- 10. (h) the principle gate ------------------------------------------------
n=$(cd "$REPO/core" && go list -deps . | grep -c weftgo)
clause h1 "$([ "$n" = 3 ] && echo PASS || echo FAIL)" "core deps: cd core && go list -deps . | grep -c weftgo = 3 (core, contentflag, jsonclose)" \
  "$n — $(cd "$REPO/core" && go list -deps . | grep weftgo | tr '\n' ' ')"

T='TestReportingHookChangesNothingModelVisible|TestRequestRecordsChangeNothingModelVisible|TestCompactionViewChangesNothingModelVisible|TestRequestFactsOffComputeNothing'
res=$(cd "$REPO/core" && go test -count=1 -run "^($T)\$" -v . 2>&1 | grep -E '^(--- |ok|FAIL)' | tr '\n' ' ')
TT='TestObservabilityNeverChangesTheSession|TestThreadRecordsRespectContentOption'
rest=$(cd "$REPO" && go test -count=1 -run "^($TT)\$" -v ./thread 2>&1 | grep -E '^(--- |ok|FAIL)' | tr '\n' ' ')
TO='TestThreadRecordContentOff|TestThreadRecordContentOn'
reso=$(cd "$REPO" && go test -count=1 -run "^($TO)\$" -v ./otel 2>&1 | grep -E '^(--- |ok|FAIL)' | tr '\n' ' ')
cnt() { echo "$1" | grep -oE -- '--- PASS' | wc -l; }
h2=$(echo "$res$rest$reso" | grep -q 'FAIL' && echo FAIL || ([ "$(cnt "$res")" -ge 4 ] && [ "$(cnt "$rest")" -ge 2 ] && [ "$(cnt "$reso")" -ge 2 ] && echo PASS || echo FAIL))
clause h2 "$h2" "the observability on/off goldens identical: core's four (no hook / reporting hook, request records, compaction view, request facts off), thread's session golden and content option, otel's thread records off/on" \
  "cd core && go test -run '^($T)\$' . → $(cnt "$res") PASS: $res" "go test -run '^($TT)\$' ./thread → $(cnt "$rest") PASS: $rest" "go test -run '^($TO)\$' ./otel → $(cnt "$reso") PASS: $reso"

honesty="$REPO/studio/web/src/lib/honesty.ts"; golden="$REPO/studio/testdata/holes.golden.json"
missing=""; for c in $(jq -r '.[] | (.causes // {}) | keys[]' "$golden"); do grep -qE "^\s+$c: \{" "$honesty" || missing="$missing $c"; done
mj() { jq -r "$1" "$RUN/gate10a-$2.json" 2>/dev/null; }
unp=$(mj '.facts.unpriced // ""' main); gone=$(mj '.facts.gone.words // [] | join(" · ")' gone); goneok=$(mj '.facts.goneOK // false' gone)
capped=$(mj '.facts.capped | "capped=\(.capped) over \(.classes) classes"' main)
capw=$(grep -A1 -E '^\s+capped:' "$REPO/studio/web/src/components/studio/error-class-facet.tsx" | tail -1 | sed -E 's/^ +//; s/,$//')
capt=$(grep -rlE 'error_class_capped' "$REPO"/studio/*_test.go "$REPO"/studio/web/src --include='*.test.ts*' 2>/dev/null | sed "s|$REPO/||" | head -3 | tr '\n' ' ')
nj=$(grep -nE 'unknown: "\? not judged"' "$REPO/studio/web/src/lib/predicate.ts" | head -1)
njt=$(grep -rlE 'not judged|PREDICATE_WORDS' "$REPO/studio/web/src" --include='*.test.ts' --include='*.test.tsx' 2>/dev/null | xargs -r grep -li predicate | sed "s|$REPO/||" | tr '\n' ' ')
h3=$([ -z "$missing" ] && [ -n "$unp" ] && [ "$goneok" = true ] && [ -n "$nj" ] && [ -n "$capw" ] && echo PASS || echo FAIL)
clause h3 "$h3" "every new hole a badge: the A3 table (holes.golden.json causes ⊆ lib/honesty.ts CAUSES) and the samples — an unpriced cost, a capped facet, \"? not judged\", a gone runtime" \
  "holes golden: $(jq -r '[.[].hole] | unique | join(" ")' "$golden"); causes in CAUSES: ${missing:+MISSING$missing}${missing:-all}" \
  "unpriced cost (Studio /agents strip): ${unp:-NONE SEEN}" \
  "gone runtime (/runtimes after the arena stopped): ${gone:-NONE SEEN}" \
  "capped facet: this seed's facet $capped (not reached: ≤ 20 classes); its word ${capw:-MISSING} — tests: ${capt:-none}" \
  "\"? not judged\": lib/predicate.ts ${nj:-MISSING} — tests: ${njt:-none} (the matrix's cells are never run here: its column is live)"

hosts='api\.openai\.com|api\.anthropic\.com|generativelanguage\.googleapis\.com|aiplatform\.googleapis\.com|api\.z\.ai|open\.bigmodel\.cn|api\.moonshot|openrouter\.ai|api\.deepseek'
hl=$(for f in seed.log serve.log studio.log studio2.log dev.log; do [ -f "$RUN/$f" ] && echo -n "$f:$(grep -cE "$hosts" "$RUN/$f") "; done)
traces=$(ls "$RUN"/*.strace 2>/dev/null)
if [ -n "$traces" ]; then
  dests=$(perl -ne 'next unless /connect\(.*sa_family=AF_INET6?/; my ($port) = /sin6?_port=htons\((\d+)\)/; my ($addr) = /inet_addr\("([^"]+)"\)/; ($addr) = /inet_pton\(AF_INET6, "([^"]+)"/ unless $addr; print "$addr:$port\n"' $traces | sort | uniq -c | sort -rn)
  inet=$(echo "$dests" | awk 'NF{print $2" ×"$1}' | tr '\n' ' ')
  outside=$(echo "$dests" | awk 'NF{print $2}' | grep -vE '^(127\.|::1:|::ffff:127\.)' | wc -l)
  net="strace -f -e connect over $(echo $traces | xargs -n1 basename | tr '\n' ' '): AF_INET/6 destinations $inet — $outside outside loopback"
else outside=0; net="strace not available: the provider-host grep alone"; fi
clause h4 "$([ "$outside" = 0 ] && ! echo "$hl" | grep -qE ':[1-9]' && echo PASS || echo FAIL)" "no outbound network call: the gate's Studio (both starts), the seed, serve and weft dev + studio-local make no request off 127.0.0.1" \
  "$net" "provider hosts named in the logs: $hl" "engines: $(grep -ohE 'engine=[a-z]+' "$RUN/seed.log" "$RUN/serve.log" | sort -u | tr '\n' ' ')"

M="$REPO/studio/auth_matrix_test.go"
caps=$(api /api/meta | jq -r '.capabilities | join(",")')
row() { grep -nF "$1" "$M" | head -1 | cut -d: -f1; }
h5l=(); h5bad=""
for spec in "runtime_forget|runtime_forget|name: \"DELETE /api/runtimes/{id}\"|runtimes.golden.json" \
            "run_commands|run_commands|name: \"GET /api/runs/{id}/commands\"|run-commands.golden.json" \
            "stats|stats|name: \"GET /api/stats\"|stats-agent.golden.json" \
            "target_check|target_check|name: \"POST /api/playground/runs (source run)\"|playground-cross-agent.golden.json" \
            "runtime_link|-|name: \"GET /api/meta\"|meta-runtime-link.golden.json"; do
  IFS='|' read -r what cap re gold <<< "$spec"
  r=$(row "$re")
  capok=$([ "$cap" = - ] && echo "a field of /api/meta (not a capability)" || (echo ",$caps," | grep -q ",$cap," && echo "capability \"$cap\" ✓" || echo "capability \"$cap\" MISSING"))
  [ -z "$r" ] && h5bad="$h5bad $what(matrix)"
  echo "$capok" | grep -q MISSING && h5bad="$h5bad $what(meta)"
  [ -f "$REPO/studio/testdata/api/$gold" ] || h5bad="$h5bad $what(golden)"
  h5l+=("$what — auth_matrix_test.go $([ -n "$r" ] && echo "line $r: ${re#name: }" || echo "NO ROW") · /api/meta $capok · golden studio/testdata/api/$gold$([ -f "$REPO/studio/testdata/api/$gold" ] || echo ' MISSING')")
done
rl=$(mj '.facts.runtime_link // null | tostring' dev)
am=$(cd "$REPO" && go test -count=1 -run '^(TestAuthMatrix|TestAuthMatrixCompletenessCheck|TestMetaRuntimeLink|TestRunCommandsRoute|TestStatsGoldens|TestRuntimesToldApartRestoredAndForgotten)$' ./studio 2>&1 | tail -1)
echo "$am" | grep -q '^ok' || h5bad="$h5bad tests"
clause h5 "$([ -z "$h5bad" ] && echo PASS || echo FAIL)" "every new route in the auth matrix and /api/meta (runtime_forget, run_commands, stats, target_check, runtime_link)" \
  "${h5l[@]}" "runtime_link live: studio-local's embedded /studio/api/meta under weft dev → $rl" \
  "go test -run '^(TestAuthMatrix|TestAuthMatrixCompletenessCheck|TestMetaRuntimeLink|TestRunCommandsRoute|TestStatsGoldens|TestRuntimesToldApartRestoredAndForgotten)\$' ./studio → $am" ${h5bad:+"missing:$h5bad"}

C="$REPO/docs/record-contract.md"
h6l=(); h6bad=""
for spec in "from_step|studio/testdata/api/experiment.golden.json" "pass_predicate|studio/testdata/api/experiment.golden.json" "span_stats|studio/testdata/api/stats-agent.golden.json"; do
  IFS='|' read -r f fx <<< "$spec"
  d=$(grep -c -F "\`$f\`" "$C"); dl=$(grep -n -F "\`$f\`" "$C" | head -1 | cut -d: -f1)
  if [ "$f" = span_stats ]; then hit=$(grep -c '"runs"' "$REPO/$fx"); else hit=$(grep -c "\"$f\"" "$REPO/$fx"); fi
  [ "$d" -gt 0 ] && [ "$hit" -gt 0 ] || h6bad="$h6bad $f"
  h6l+=("$f — record-contract.md ×$d (line ${dl:-—}) · fixture $fx ($([ "$f" = span_stats ] && echo "the derived index's read: $hit groups" || echo "×$hit"))")
done
t6=$(cd "$REPO" && go test -count=1 -run '^(TestExperimentsGolden|TestStatsGoldens)$' ./studio 2>&1 | tail -1)
t6b=$(cd "$REPO" && go test -count=1 -run '^(TestSpanStatsAtIngest|TestUpgradeTo0023BackfillsSpanStats|TestSpanStatsFollowDeletes)$' ./obsdb/sqlite 2>&1 | tail -1)
t6c=$(cd "$REPO" && go test -count=1 -run '^TestContract$' ./obsdb 2>&1 | tail -1)
for t in "$t6" "$t6b" "$t6c"; do echo "$t" | grep -q '^ok' || h6bad="$h6bad tests"; done
clause h6 "$([ -z "$h6bad" ] && echo PASS || echo FAIL)" "every new stored field in the record contract with a fixture (from_step, pass_predicate, the span_stats derived index)" \
  "${h6l[@]}" "go test -run '^(TestExperimentsGolden|TestStatsGoldens)\$' ./studio → $t6" \
  "go test -run '^(TestSpanStatsAtIngest|TestUpgradeTo0023BackfillsSpanStats|TestSpanStatsFollowDeletes)\$' ./obsdb/sqlite → $t6b" "go test -run TestContract ./obsdb → $t6c" ${h6bad:+"missing:$h6bad"}

pok=$(echo "$ps" | jq -r '(.measured == .last_gzip) and (.over | not)' 2>/dev/null)
clause h7 "$([ "$pok" = true ] && [ "$room" -ge "$CAP_FLOOR" ] && echo PASS || echo FAIL)" "the panel under the cap with its ledger row (= g)" \
  "$(echo "$ps" | jq -r '"measured \(.measured) B gzip · ledger last row \(.last_label) = \(.last_gzip) B · cap \(.cap) B"' 2>/dev/null || echo "$ps") · headroom $room B"

# ---- the end ---------------------------------------------------------------------
stop "$SPID" "$SWRAP"; trap - EXIT; cleanup
say "studio stopped; DBs $RUN/{studio,dev}.db, logs $RUN/*.log, straces $RUN/*.strace, shots $RUN/shots ($(ls "$RUN/shots" | wc -l) files) — wall $(( $(date +%s) - T0 )) s"
if [ ${#FAILED[@]} = 0 ]; then say "GATE 10a: PASSED"; exit 0; fi
say "GATE 10a: FAILED ($(printf '%s\n' "${FAILED[@]}" | awk '!s[$0]++' | paste -sd, | sed 's/,/, /g'))"
exit 1
