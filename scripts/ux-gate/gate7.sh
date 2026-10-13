#!/usr/bin/env bash
# gate7.sh — the phase 7 gate (audit §9, gate 7), end to end: a fresh
# Studio from $REPO, the arena's offline seed into it (under strace when
# available: every connect() it made), the spans-only contract fixture
# ingested, the journeys (gate7.js: API, Studio UI, the devtools panel),
# then the CLI clause and the principle gate. One line per clause:
# PASS / FAIL / RECORDED with its evidence; the lot in $RUN/gate7.txt.
#
#   scripts/ux-gate/gate7.sh            # defaults below
#   REPO=… ARENA=… RUN=… PLAYWRIGHT_DIR=… scripts/ux-gate/gate7.sh
#
# REPO     the weft tree under test (default: the arena's go.work sibling,
#          $ARENA/../weft — the tree the arena compiles against)
# ARENA    the weft-arena checkout (default: $REPO/../weft-arena); built
#          as it is, never modified
# RUN      scratch dir for the DB, logs, binaries and screenshots
# GO_TEST_DIR  where the principle gate's go tests run (default $REPO)
# PLAYWRIGHT_DIR  a dir whose node_modules has playwright (pw.js)
# AXE_DIR  axe-core for the panel's a11y line (default $REPO/studio/web/node_modules/axe-core)
# TODO     the plan's TODO (default $REPO/../WEFT-UX-IMPL-TODO.md)
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARENA="${ARENA:-$(cd "$HERE/../.." && pwd)/../weft-arena}"
[ -d "$ARENA" ] || { echo "no weft-arena checkout at $ARENA: set ARENA=" >&2; exit 2; }
ARENA="$(cd "$ARENA" && pwd)"
REPO="${REPO:-$(cd "$ARENA/../weft" && pwd)}"
RUN="${RUN:-$PWD/gate7-run}"
ADDR="${ADDR:-127.0.0.1:7341}"
TOKEN="${TOKEN:-gate7tok}"
GO_TEST_DIR="${GO_TEST_DIR:-$REPO}"
AXE_DIR="${AXE_DIR:-$REPO/studio/web/node_modules/axe-core}"
TODO="${TODO:-$REPO/../WEFT-UX-IMPL-TODO.md}"
STUDIO="http://$ADDR"
export PLAYWRIGHT_DIR="${PLAYWRIGHT_DIR:-}" AXE_DIR GATE_SHOTS="$RUN/shots"
mkdir -p "$RUN/shots"
OUT="$RUN/gate7.txt"; : > "$OUT"
say() { echo "$*" | tee -a "$OUT"; }
api() { curl -sf -H "Authorization: Bearer $TOKEN" "$STUDIO$1"; }
FAILS=0
clause() { # id status title evidence…
  local id="$1" st="$2" title="$3"; shift 3
  [ "$st" = FAIL ] && FAILS=$((FAILS+1))
  say "($id) $st — $title"; for e in "$@"; do say "      $e"; done
}

say "gate 7 — $(date -u +%FT%TZ)"
say "repo   $REPO @ $(git -C "$REPO" log -1 --format='%h %s' | cut -c1-100)$( [ -n "$(git -C "$REPO" status --porcelain)" ] && echo ' (DIRTY)')"
say "arena  $ARENA ($(git -C "$ARENA" status --porcelain | wc -l) uncommitted paths, built as is)"
say "run    $RUN"

# ---- 1. Studio from $REPO, a fresh DB ---------------------------------
( cd "$REPO" && go build -o "$RUN/weft" ./cmd/weft ) || { say "weft build failed"; exit 2; }
rm -f "$RUN"/studio.db*
"$RUN/weft" studio --addr "$ADDR" --db "sqlite://$RUN/studio.db" --token "$TOKEN" --open=false > "$RUN/studio.log" 2>&1 &
SPID=$!; echo "$SPID" > "$RUN/studio.pid"
trap 'kill $SPID 2>/dev/null; wait $SPID 2>/dev/null' EXIT
for i in $(seq 1 60); do api /api/meta >/dev/null && break; sleep 0.5; done
api /api/meta >/dev/null || { say "Studio did not answer /api/meta"; cat "$RUN/studio.log"; exit 2; }
say "studio $STUDIO pid $SPID: $(api /api/meta | jq -c '{weft_version, auth_required, pricing: .pricing.source}')"

# ---- 2. the arena, built against $REPO (go.work), seeded offline ------
( cd "$ARENA" && go build -o "$RUN/weft-arena" ./cmd/weft-arena ) > "$RUN/arena-build.log" 2>&1
if [ $? -ne 0 ]; then say "arena build FAILED against $REPO:"; tail -30 "$RUN/arena-build.log" | tee -a "$OUT"; exit 3; fi
( cd "$ARENA" && "$RUN/weft-arena" version ) | sed 's/^/arena  /' | tee -a "$OUT"
SEED=( "$RUN/weft-arena" run --offline --studio --no-save --jobs 8 )
if command -v strace >/dev/null && [ "${STRACE:-1}" != 0 ]; then
  SEED=( strace -f -qq -e trace=connect -o "$RUN/seed.strace" "${SEED[@]}" )
fi
( cd "$ARENA" && WEFT_STUDIO_URL="$STUDIO" WEFT_STUDIO_TOKEN="$TOKEN" "${SEED[@]}" ) > "$RUN/seed.log" 2>&1
SEEDRC=$?
say "seed   exit $SEEDRC: $(grep -E '^weft-arena — engine=' "$RUN/seed.log" | tail -1)"
sleep 3 # the exporter's last batch
say "seed   landed: $(api '/api/runs?limit=1' | jq .total) top-level runs, $(api '/api/runs?limit=1&all=1' | jq .total) with subagent children"
say "seed   WARN/ERROR lines: $(grep -cE ' (WARN|ERROR) ' "$RUN/seed.log")"
grep -E ' (WARN|ERROR) ' "$RUN/seed.log" | sed -E 's/^[0-9/]+ [0-9:]+ //' | sort | uniq -c | sed 's/^/         /' | tee -a "$OUT"

# the spans-only contract fixture (an app with a tracer and no logger)
curl -sf -o /dev/null -X POST -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" \
  --data-binary @"$REPO/obsdb/testdata/contract/tracer_only/traces.json" "$STUDIO/v1/traces" \
  && say "ingest obsdb/testdata/contract/tracer_only/traces.json → /v1/traces ok" || say "ingest of the tracer_only fixture FAILED"
sleep 1

# ---- 3. the journeys: API + Studio UI + the panel (a)–(h) -------------
( cd "$HERE" && STUDIO="$STUDIO" TOKEN="$TOKEN" RUN="$RUN" node gate7.js ) 2>&1 | grep -v -E '^(zoxide|Please ensure|If the issue|https://github.com/ajeetdsouza|Disable this message|$)' | tee -a "$OUT"
[ "${PIPESTATUS[0]}" -ne 0 ] && FAILS=$((FAILS+1))

# ---- (i) weft runs --json ---------------------------------------------
J="$RUN/runs.json"
"$RUN/weft" runs --url "$STUDIO" --token "$TOKEN" --json --limit 0 > "$J" 2>/dev/null
"$RUN/weft" runs --url "$STUDIO" --token "$TOKEN" --json --limit 5 > "$RUN/runs-5.json" 2>/dev/null
tally=$(jq -c '{rows: length,
  stopped_by: map(select(.stopped_by)) | length, error_type: map(select(.error_type)) | length,
  events_emitted: map(select(.events_emitted != null)) | length, messages_emitted: map(select(.messages_emitted != null)) | length,
  tool_errors: map(select(.tool_errors)) | length, attempts_retried: map(select(.attempts_retried)) | length,
  fallbacks: map(select(.fallbacks)) | length, denied: map(select(.denied)) | length, truncated: map(select(.truncated)) | length,
  tap_panics: map(select(.tap_panics)) | length, prompt_preview: map(select(.prompt_preview)) | length}' "$J")
ul=$(jq -c '[.[] | select(.meta["arena.scenario"]=="opt.usage-limit")][0] | {stopped_by, error_type, events_emitted, messages_emitted, prompt_preview}' "$J")
first=$(jq -c '.[0] | {id, stopped_by, error_type, events_emitted, prompt_preview}' "$RUN/runs-5.json")
ok=$(jq '(map(select(.stopped_by))|length) > 0 and (map(select(.error_type))|length) > 0 and (map(select(.tool_errors))|length) > 0 and (map(select(.prompt_preview))|length) > 0' "$J")
clause i "$([ "$ok" = true ] && echo PASS || echo FAIL)" "weft runs --json carries stopped_by, error_type, the counters, prompt_preview" \
  "weft runs --json --limit 0 — rows carrying each field: $tally" "opt.usage-limit: $ul" "--limit 5, first row: $first"

# ---- (j) stress.event-storm --------------------------------------------
sid=$(jq -r '[.[] | select(.meta["arena.scenario"]=="stress.event-storm")][0].id' "$J")
doc=$(api "/api/runs/$sid" | jq -c '{status, events_emitted, event_count, delta_count, holes: [.holes[]?.hole]}')
warn=$(grep -E ' (WARN|ERROR) .*(queue full|dropped|lost|overflow)' "$RUN/seed.log" | head -3)
clause j RECORDED "stress.event-storm: holes on its run doc + any overflow WARN in seed.log" \
  "run $sid: $doc" "seed.log overflow/drop lines: ${warn:-none}" \
  "$(echo "$doc" | jq -r 'if (.holes|index("gap")) then "events_lost gap present" else "no gap: events_emitted == event_count, the Studio destination kept every record" end')"

# ---- (k) the principle gate -------------------------------------------
n=$(cd "$REPO" && go list -deps ./core | grep -c weft/)
clause k1 "$([ "$n" = 2 ] && echo PASS || echo FAIL)" "core's imports unchanged: go list -deps ./core | grep -c weft/ = 2" \
  "$n — $(cd "$REPO" && go list -deps ./core | grep weft/ | tr '\n' ' ')"

T='TestReportingHookChangesNothingModelVisible|TestRequestRecordsChangeNothingModelVisible|TestRetryOverFallbackReportsEveryAttempt'
res=$(cd "$GO_TEST_DIR/core" && go test -count=1 -run "^($T)\$" -v . 2>&1 | grep -E '^(--- |ok|FAIL|PASS)' | tr '\n' ' ')
clause k2 "$(echo "$res" | grep -q 'ok ' && ! echo "$res" | grep -q FAIL && echo PASS || echo FAIL)" \
  "observability on/off golden identical (core/contract_test.go + core/request_test.go: the transcript, events, requests byte-identical with the reporting/records on and off, with and without a tracer)" \
  "cd core && go test -count=1 -run '^($T)\$' . → $res"

honesty="$REPO/studio/web/src/lib/honesty.ts"
golden="$REPO/studio/testdata/holes.golden.json"
causes=$(jq -r '.[] | .hole as $h | (.causes // {}) | keys[] | "\($h):\(.)"' "$golden" | tr '\n' ' ')
missing=""
for c in no_logger tail_lost events_lost metadata_cap result_cap $(jq -r '.[] | (.causes // {}) | keys[]' "$golden"); do
  grep -qE "^\s+$c: \{" "$honesty" || missing="$missing $c"
done
clause k3 "$([ -z "$missing" ] && echo PASS || echo FAIL)" "every new hole a badge from the A3 table (lib/honesty.ts's closed HOLES + CAUSES)" \
  "this phase's causes no_logger (not_recorded) · tail_lost, events_lost (gap) · metadata_cap, result_cap (truncated): each a key of honesty.ts CAUSES${missing:+ — MISSING:$missing}" \
  "holes.golden.json's causes (honesty.test.ts pins the table to it): $causes"

mfst=$(api /api/meta | jq -r '.capabilities | index("manifests") != null')
c404=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$STUDIO/api/manifests/$(printf '0%.0s' $(seq 64))")
c401=$(curl -s -o /dev/null -w '%{http_code}' "$STUDIO/api/manifests/$(printf '0%.0s' $(seq 64))")
row=$(grep -n 'name: "GET /api/manifests/{hash}"' "$REPO/studio/auth_matrix_test.go" | cut -d: -f1)
am=$(cd "$GO_TEST_DIR" && go test -count=1 -run '^TestAuthMatrix$' ./studio 2>&1 | tail -1)
clause k4 "$([ "$mfst" = true ] && [ -n "$row" ] && echo "$am" | grep -q '^ok' && echo PASS || echo FAIL)" \
  "every new route in the auth matrix and /api/meta (GET /api/manifests/{hash})" \
  "studio/auth_matrix_test.go:$row {name: \"GET /api/manifests/{hash}\" …} — read token 403, unknown hash 404, else 200; go test -run TestAuthMatrix ./studio → $am" \
  "/api/meta capabilities has \"manifests\": $mfst; live: unknown hash with the token → $c404, without → $c401"

attrs="weft.run.stopped_by weft.cancel.cause weft.run.events weft.run.messages weft.records.dropped weft.tool.truncated_bytes weft.tool.input_error weft.panic.layer weft.run.denied weft.attempt.layer weft.tool.retry weft.tool.retry_budget weft.tap.panics weft.resume.calls weft.resumed_from weft.approval.actor weft.tool.denied weft.tool.denied_kind weft.session.title"
lines=(); bad=""; rec=""
for a in $attrs; do
  d=$(grep -c -F "\`$a\`" "$REPO/docs/record-contract.md")
  f=$(grep -l -F "\"$a\"" "$REPO"/obsdb/testdata/contract/*/*.json 2>/dev/null | xargs -r -n1 dirname | xargs -r -n1 basename | sort -u | tr '\n' ',' | sed 's/,$//')
  if [ -z "$f" ]; then
    t=$(grep -rl -F "$a" "$REPO" --include='*_test.go' 2>/dev/null | sed "s|$REPO/||" | tr '\n' ',' | sed 's/,$//')
    f="(no contract fixture; tests: ${t:-none})"; rec="$rec $a"
  fi
  [ "$d" -gt 0 ] || bad="$bad $a"
  lines+=("$a — record-contract.md ×$d · fixtures: $f")
done
ct=$(cd "$GO_TEST_DIR" && go test -count=1 -run '^TestContract$' ./obsdb 2>&1 | tail -1)
st=PASS; [ -n "$rec" ] && st=RECORDED; [ -n "$bad" ] && st=FAIL; echo "$ct" | grep -q '^ok' || st=FAIL
clause k5 "$st" "every new attribute in the record contract with a fixture (obsdb/testdata/contract/*)" "${lines[@]}" \
  "go test -run TestContract ./obsdb → $ct" ${bad:+"not in docs/record-contract.md:$bad"} ${rec:+"documented, tested, but no obsdb/testdata/contract fixture:$rec"}

hosts='api\.openai\.com|api\.anthropic\.com|generativelanguage\.googleapis\.com|aiplatform\.googleapis\.com|api\.z\.ai|open\.bigmodel\.cn|api\.moonshot|openrouter\.ai|api\.deepseek'
hl=$(for f in seed.log studio.log; do echo -n "$f:$(grep -cE "$hosts" "$RUN/$f") "; done)
if [ -f "$RUN/seed.strace" ]; then
  dests=$(perl -ne 'next unless /connect\(.*sa_family=AF_INET6?/; my ($port) = /sin6?_port=htons\((\d+)\)/; my ($addr) = /inet_addr\("([^"]+)"\)/; ($addr) = /inet_pton\(AF_INET6, "([^"]+)"/ unless $addr; print "$addr:$port\n"' "$RUN/seed.strace" | sort | uniq -c | sort -rn)
  inet=$(echo "$dests" | awk 'NF{print $2" ×"$1}' | tr '\n' ' ')
  outside=$(echo "$dests" | awk 'NF{print $2}' | grep -vE '^(127\.|::1:|::ffff:127\.)' | wc -l)
  net="strace -f -e connect over the seed: AF_INET/6 destinations $inet — $outside outside loopback"
else outside=0; net="strace not available: the provider-host grep alone"; fi
clause k6 "$([ "$outside" = 0 ] && ! echo "$hl" | grep -qE ':[1-9]' && echo PASS || echo FAIL)" "no outbound network call (the seed runs --offline)" \
  "$net" "provider hosts named in the logs (seed.log, studio.log): $hl" \
  "engine: $(grep -oE 'engine=[a-z]+' "$RUN/seed.log" | sort -u | tr '\n' ' ')"

ps=$(cd "$HERE" && bun panel-size.ts "$REPO" 2>&1 | tail -1)
pok=$(echo "$ps" | jq -r '(.measured == .last_gzip) and (.over | not)' 2>/dev/null)
clause k7 "$([ "$pok" = true ] && echo PASS || echo FAIL)" "the panel under the cap with its ledger row (panel-budget.json's last row = the measured bundle)" \
  "$(echo "$ps" | jq -r '"measured \(.measured) B gzip (studio/dist/panel/panel.js, the build'"'"'s gzipSize over the stamp-normalized bundle) · ledger last row \(.last_label) = \(.last_gzip) B · cap \(.cap) B"' 2>/dev/null || echo "$ps")" \
  "the build's line: $(echo "$ps" | jq -r .line 2>/dev/null)" \
  "(make studio-check rebuilds studio/dist in place; the gate measures the committed bundle with the same functions instead)"

if [ -f "$TODO" ]; then
  mapfile -t pol < <(awk '/^## Phase 7/{on=1;next} /^## /{on=0} on' "$TODO" | grep -E '^\| (U32(\.[0-9]+)?|U13\.1)[ |(]' | awk -F'|' '{gsub(/^ +| +$/,"",$2); gsub(/^ +| +$/,"",$4); print $2": "substr($4,1,90)}')
  open=$(printf '%s\n' "${pol[@]}" | grep -vcE ': (tested|done)')
  clause k8 "$([ "$open" = 0 ] && echo PASS || echo RECORDED)" "the phase 7 polish batch is closed (the TODO's U32 / U13.1 rows)" "${pol[@]}"
else clause k8 RECORDED "the phase 7 polish batch is closed" "TODO not found at $TODO"; fi

# ---- the end ------------------------------------------------------------
kill $SPID 2>/dev/null; wait $SPID 2>/dev/null; trap - EXIT
say "studio stopped; DB $RUN/studio.db, logs $RUN/{studio,seed}.log, shots $RUN/shots"
say "gate 7: $([ $FAILS = 0 ] && echo 'no clause FAILed' || echo "$FAILS clause(s) FAILed")"
exit $([ $FAILS = 0 ] && echo 0 || echo 1)
