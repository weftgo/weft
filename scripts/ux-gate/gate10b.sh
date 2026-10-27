#!/usr/bin/env bash
# gate10b.sh — the phase 10b gate (one system: shared words, scope, the
# onion, the CLI), end to end: a fresh Studio from $REPO, the arena built
# against this tree (a scratch go.work, never edited) and seeded offline
# into it, an in-gate Go program (g10bdemo.go, built under $RUN, never in
# the repo) for what the arena does not record — runs stamped with a
# flow, an MCP server named mcp.Server("billing"), a registered
# governance-shaped agent whose manifest Studio stores, a run in flight
# for /live — the stock_genai contract fixtures through /v1/traces, then
# the clauses: (a) the words, (b) the scope, (c) the onion, (d) MCP
# identity, (e) the CLI, (f) derived runs, (g) the panel and dist,
# (h) panel contrast in a real browser, (i) make mcp-check, (j1–j7) the
# principle gate, (k) the Coverage ledger. Every process the gate starts
# runs under `strace -e connect` when strace is there (j4). One line per
# clause: PASS / FAIL / RECORDED with its evidence; the lot in
# $RUN/gate10b.txt; the last line `GATE 10b: PASSED` or
# `GATE 10b: FAILED (<clauses>)`. No live model call: the seed runs
# --offline, every model in the Go program is a wefttest script, no Run
# is pressed.
#
# The gate never touches the user's discovery file: every weft process
# runs with XDG_RUNTIME_DIR / XDG_CACHE_HOME under $RUN, from a cwd with
# no ./.weft, with WEFT_STUDIO_URL / WEFT_STUDIO_TOKEN / WEFT_DISCOVERY
# unset.
#
#   ARENA=… RUN=… PLAYWRIGHT_DIR=… scripts/ux-gate/gate10b.sh
#
# REPO     the weft tree under test (default: this script's own tree)
# ARENA    the weft-arena checkout (default: $REPO/../weft-arena); never
#          modified — built against $REPO through $RUN/arena.work
# RUN      scratch dir for the DBs, logs, binaries and screenshots
#          (default ./gate10b-run); emptied of the gate's own files first
# ADDR / ADDR2  the gate's Studio and the fresh one (127.0.0.1:7351 / :7352)
# PLAYWRIGHT_DIR  a dir whose node_modules has playwright (pw.js)
# STUDIO_CHECK=0  skip `make studio-check` (g then reads the committed bundle)
# KNOWN_STALE   the arena scenarios that fail by design against this weft
#          (the seed line is RECORDED when the failures are a subset)
# TODO     the plan's TODO (default: the main checkout's ../WEFT-UX-IMPL-TODO.md)
# IN_FLIGHT     ledger items whose open rows are in flight (RECORDED in k,
#          not FAIL; default none)
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="${REPO:-$(cd "$HERE/../.." && pwd)}"
ARENA="${ARENA:-$REPO/../weft-arena}"
[ -d "$ARENA" ] || { echo "no weft-arena checkout at $ARENA: set ARENA=" >&2; exit 2; }
ARENA="$(cd "$ARENA" && pwd)"
RUN="${RUN:-$PWD/gate10b-run}"
ADDR="${ADDR:-127.0.0.1:7351}"
ADDR2="${ADDR2:-127.0.0.1:7352}"
TOKEN="${TOKEN:-gate10btok}"
STUDIO="http://$ADDR"
STUDIO2="http://$ADDR2"
CAP_FLOOR="${CAP_FLOOR:-7500}"
KNOWN_STALE="${KNOWN_STALE:-obs18.env-destinations runtime18.sources runtimelab.transcript-edits rw.ops-console rw.records-forensics mcp.policy-and-failures rw.invoice-extraction rw.provider-outage rw.finance-cost-review runtime16.sessions panel18.routes}"
IN_FLIGHT="${IN_FLIGHT:-}"
TODO="${TODO:-$(dirname "$(git -C "$REPO" rev-parse --path-format=absolute --git-common-dir)")/../WEFT-UX-IMPL-TODO.md}"
export PLAYWRIGHT_DIR="${PLAYWRIGHT_DIR:-}" GATE_SHOTS="$RUN/shots"
export PLAYWRIGHT_BROWSERS_PATH="${PLAYWRIGHT_BROWSERS_PATH:-$HOME/.cache/ms-playwright}"
mkdir -p "$RUN"
RUN="$(cd "$RUN" && pwd)"
rm -rf "$RUN/shots" "$RUN"/*.strace "$RUN"/*.log "$RUN"/gate10b-*.json "$RUN"/gate10b-*.txt "$RUN"/studio.db* "$RUN"/fresh.db* "$RUN/demo" "$RUN/xdg" "$RUN/cwd"
mkdir -p "$RUN/shots" "$RUN/demo" "$RUN/xdg/run" "$RUN/xdg/cache" "$RUN/cwd"
chmod 700 "$RUN/xdg/run"
unset WEFT_STUDIO_URL WEFT_STUDIO_TOKEN WEFT_DISCOVERY
# W runs a weft process in the gate's private discovery world.
# (Go's and Playwright's caches stay where they were.)
W=(env XDG_RUNTIME_DIR="$RUN/xdg/run" XDG_CACHE_HOME="$RUN/xdg/cache" GOCACHE="$(go env GOCACHE)" GOMODCACHE="$(go env GOMODCACHE)")
DISC="$RUN/xdg/run/weft/studio.json"
OUT="$RUN/gate10b.txt"; : > "$OUT"
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

STRACE=0; command -v strace >/dev/null && [ "${STRACE_ON:-1}" != 0 ] && STRACE=1
ST() { [ $STRACE = 1 ] && echo strace -f -qq -e trace=connect -o "$1"; }
PIDS=()
bg() { # <strace file> <log> <cmd…>: BGPID the command, BGWRAP what to wait for
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

say "gate 10b — $(date -u +%FT%TZ)"
say "repo   $REPO @ $(git -C "$REPO" log -1 --format='%h %s' | cut -c1-100)$( [ -n "$(git -C "$REPO" status --porcelain -- . ':!scripts/ux-gate')" ] && echo ' (DIRTY)')"
say "arena  $ARENA ($(git -C "$ARENA" status --porcelain -- . | wc -l) uncommitted paths, built as is, never edited) against $REPO through $RUN/arena.work"
say "run    $RUN (strace: $([ $STRACE = 1 ] && echo on || echo 'not available')); discovery dir $RUN/xdg/run/weft (private)"
for a in "$ADDR" "$ADDR2"; do
  if ss -ltn 2>/dev/null | grep -q " $a "; then say "$a is taken: $(ss -ltnp 2>/dev/null | grep " $a ")"; exit 2; fi
done

# ---- 1. builds: weft from $REPO, the arena and the demo against it ------
( cd "$REPO" && go build -o "$RUN/weft" ./cmd/weft ) > "$RUN/build.log" 2>&1 \
  || { say "weft build failed: $(tail -5 "$RUN/build.log")"; exit 2; }
printf 'go 1.26.0\n\nuse (\n\t%s\n\t%s\n\t%s/core\n)\n' "$ARENA" "$REPO" "$REPO" > "$RUN/arena.work"
( cd "$ARENA" && GOWORK="$RUN/arena.work" go build -o "$RUN/weft-arena" ./cmd/weft-arena ) > "$RUN/arena-build.log" 2>&1 \
  || { say "arena build FAILED:"; tail -30 "$RUN/arena-build.log" | tee -a "$OUT"; exit 3; }
sed '/^\/\/go:build ignore$/d' "$HERE/g10bdemo.go" > "$RUN/demo/main.go"
sdkv=$(grep -oE 'modelcontextprotocol/go-sdk v[0-9][^ ]*' "$REPO/go.mod" | awk '{print $2}')
printf 'module g10bdemo\n\ngo 1.26.0\n\nrequire (\n\tgithub.com/weftgo/weft v0.19.0\n\tgithub.com/modelcontextprotocol/go-sdk %s\n)\n' "$sdkv" > "$RUN/demo/go.mod"
printf 'go 1.26.0\n\nuse (\n\t.\n\t%s\n\t%s/core\n)\n' "$REPO" "$REPO" > "$RUN/demo/go.work"
( cd "$RUN/demo" && GOPROXY=off go build -o "$RUN/g10bdemo" . ) > "$RUN/demo-build.log" 2>&1 \
  || { say "g10bdemo build FAILED: $(tail -10 "$RUN/demo-build.log")"; exit 3; }
( cd "$REPO/studio/web" && bun -e 'const {TERMS} = await import("./src/lib/glossary.ts"); console.log(JSON.stringify(TERMS))' ) > "$RUN/terms.json" 2>/dev/null
demo() { # strace-name mode args…: the demo, its JSON lines on stdout
  local n="$1"; shift
  ( cd "$RUN/cwd" && "${W[@]}" $(ST "$RUN/$n.strace") "$RUN/g10bdemo" "$@" ) 2> "$RUN/$n.log"
}

# ---- 2. Studio, a fresh DB, from a cwd with no ./.weft ---------------------
cd "$RUN/cwd" || exit 2
bg "$RUN/studio.strace" "$RUN/studio.log" "${W[@]}" "$RUN/weft" studio --addr "$ADDR" --db "sqlite://$RUN/studio.db" --token "$TOKEN" --open=false
SPID=$BGPID; SWRAP=$BGWRAP
waitfor 30 api /api/meta || { say "Studio did not answer /api/meta"; cat "$RUN/studio.log"; exit 2; }
say "studio $STUDIO pid $SPID: $(api /api/meta | jq -c '{weft_version, auth_required, discovery_file}')"

# ---- 3. (e) the CLI, first half: bare weft doctor, a second studio, open ---
dout=$( cd "$RUN/cwd" && "${W[@]}" "$RUN/weft" doctor 2> "$RUN/doctor.err" ); drc=$?
derr=$(grep -v -E "$noise" "$RUN/doctor.err" | head -3 | tr '\n' ' ')
e1=$(echo "$derr" | grep -qF "weft: Studio $STUDIO (discovery file $DISC)" && echo yes || echo no)
( cd "$RUN/cwd" && "${W[@]}" "$RUN/weft" studio --addr "$ADDR2" --db "sqlite://$RUN/studio.db" --token other --open=false > "$RUN/second.log" 2>&1 ); s2rc=$?
s2=$(grep -v -E "$noise" "$RUN/second.log" | head -2 | tr '\n' ' ')
e2=$([ "$s2rc" != 0 ] && echo "$s2" | grep -qF "$STUDIO" && echo yes || echo no)
oout=$( cd "$RUN/cwd" && "${W[@]}" "$RUN/weft" open --with-token 2> "$RUN/open.err" ); orc=$?
e3=$([ "$orc" = 0 ] && [ "$oout" = "$STUDIO/#token=$TOKEN" ] && echo yes || echo no)
say "cli    weft doctor (no flags, no env; XDG_RUNTIME_DIR=$RUN/xdg/run) exit $drc"

# ---- 4. the arena's offline seed --------------------------------------------
( cd "$ARENA" && "${W[@]}" WEFT_STUDIO_URL="$STUDIO" WEFT_STUDIO_TOKEN="$TOKEN" TMPDIR="$RUN/cwd" \
  $(ST "$RUN/seed.strace") "$RUN/weft-arena" run --offline --studio --no-save --jobs 8 ) > "$RUN/seed.log" 2>&1
SEEDRC=$?
seedline=$(grep -E '^weft-arena — engine=' "$RUN/seed.log" | tail -1)
failed=$(grep -E '^❌' "$RUN/seed.log" | awk '{print $2}' | sort | tr '\n' ' ' | sed 's/ $//')
sleep 3 # the exporter's last batch
BATCH=$(api '/api/runs?limit=1' | jq -r '.runs[0].meta["arena.batch"] // empty')
say "seed   exit $SEEDRC: $seedline"
say "seed   landed: batch $BATCH — $(api '/api/runs?limit=1' | jq .total) top-level runs, $(api '/api/runs?limit=1&all=1' | jq .total) with child runs, $(api '/api/sessions?limit=1' | jq .total) sessions"
unknown=""; for f in $failed; do [[ " $KNOWN_STALE " == *" $f "* ]] || unknown="$unknown $f"; done
note() { case "$1" in
  rw.records-forensics) echo "expects the store at schema 22 (\"migrated to schema 22\"); weft adds migrations since (now $(grep -oE 'migrated to schema 22: got "[0-9]+"' "$RUN/seed.log" | grep -oE '[0-9]+"$' | tr -d '"')) — the check should read the current schema (or ≥ 22)";;
  runtimelab.transcript-edits) echo "expects \"transcript edits need engine live\"; U17 rewrote the scripted refusal: \"your edit changes step N's request, and scripted holds only the recorded answer to the original — run on live, or drop the edit\"";;
  rw.ops-console) echo "runs \`weft export --wefttest --test …\` expecting the fixtures-only tree; U19 (Breaking) made --wefttest write the save-as-test tree — \`--test goes with --fixtures-only\` (exit 2): add --fixtures-only or follow the new tree (TestReplay_<run>); also \"13 tools\" now 22 (weft mcp grew stats, U33.1) and \"1 calls\" now \"1 call\" (U33.1's plurals)";;
  runtime18.sources) echo "expects the 0.18 scripted-miss and refusal words; U18.1/U18.2 rewrote them (\"no recorded turn for step N (scripted engine, key …): … [component]\", \"scripted cannot try another model: …\", \"scripted replays a recorded run's answers: pick a source run, or run on live\")";;
  obs18.env-destinations) echo "expects \"otel: Blocking ignored: WEFT_ENV is not dev\" under otel.NoEnv(); I2b names the real cause there: \"otel: Blocking ignored: otel.NoEnv reads no WEFT_ENV\"";;
  mcp.policy-and-failures) echo "expects error.type mcp_transport for a JSON-RPC error answer; U23.2-fix (finding 7) types a server's JSON-RPC error answer mcp_rpc_error (\"MCP refused the call\"), keeping mcp_transport for no answer — the check should want mcp_rpc_error (or a closed session for mcp_transport)";;
  rw.invoice-extraction) echo "pins \"repaired stays 0 (no args_repaired report in 0.18)\"; U23.1 records weft.tool.args_repaired / the repaired counter, so a RepairJSON repair now counts 1 — flip the check to 1";;
  rw.provider-outage) echo "pins \"step 1 attempt 2: no layer\" (\"\"); U23.1 marks a Retry attempt weft.attempt.layer=retry — want \"retry\"";;
  rw.finance-cost-review) echo "wants \"N of M subagent runs unpriced\" / \"subagent runs' cost not known yet\"; U21.1's one-word rule made the noun \"child run\" (studio/pricing.go tally(…, \"child run\", true)) — update the needles; ALSO delegated_unpriced_models came back [llama-b, llama-a] under the full --jobs 8 seed but [llama-a, llama-b] when the scenario runs alone (×5): the order is the children's read order (studio/pricing.go addModels appends, never sorts) — compare as a set, or weft sorts the list (a weft LOW, see the report)";;
  runtime16.sessions) echo "expects cancel on an app turn / a late steer to be accepted (202) and then rejected on the command row; U33.1 refuses both 409 at once (\"app-turn cancel and finished-turn steer refused 409 at once\") with the same words — read the 409 body instead of the command row";;
  panel18.routes) echo "expects the server token's hand-off to carry no fragment (\"/studio/runs\"); U22 made the setup B hand-off carry the dev token into the opened tab like setup C (\"/studio/runs#token=…\") — flip the expectation";;
  *) echo "not a known stale check: look at $RUN/seed.log";; esac; }
sev=(); for f in $failed; do sev+=("arena note · $f: $(note "$f")"); done
while read -r l; do sev+=("    $l"); done < <(grep -E '^❌' -A8 "$RUN/seed.log" | grep -E '^ +❌' | cut -c1-200)
if [ -z "$failed" ]; then sst=PASS; elif [ -z "$unknown" ]; then sst=RECORDED; else sst=FAIL; fi
clause seed "$sst" "the arena's offline seed: every failure a known stale arena check (KNOWN_STALE: $KNOWN_STALE)" \
  "$seedline" "failed: ${failed:-none}${unknown:+ — NOT KNOWN:$unknown}" "${sev[@]}"

# ---- 5. the in-gate program and the derived fixtures ----------------------
FLOW_BATCH="g10b-flow-$(date +%s)"
demo demo-flow flow "$STUDIO" "$TOKEN" "$FLOW_BATCH" > "$RUN/demo-flow.json"
MCP_RUN=$(demo demo-mcp mcp "$STUDIO" "$TOKEN" | jq -r .run)
ONION_RUN=$(demo demo-onion onion "$STUDIO" "$TOKEN" | jq -r .run)
say "demo   flow: $(jq -r '"\(.run[0:8]) \(.flow)"' "$RUN/demo-flow.json" | tr '\n' ' ')· mcp: ${MCP_RUN:-FAILED} · onion: ${ONION_RUN:-FAILED} ($(cat "$RUN"/demo-*.log | grep -v -E "$noise" | head -3 | tr '\n' ' '))"
for c in stock_genai stock_genai_noconv; do
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" \
    --data-binary @"$REPO/obsdb/testdata/contract/$c/traces.json" "$STUDIO/v1/traces")
  say "ingest obsdb/testdata/contract/$c/traces.json → /v1/traces $code"
done
TAGTOK=$(curl -sf -X POST -H "Authorization: Bearer $TOKEN" "$STUDIO/api/panel-tokens" -d "{\"tags\":{\"arena.batch\":\"$BATCH\"}}" | jq -r .token)
sleep 3

# ---- 6. (a) the vocabulary audit -------------------------------------------
( cd "$REPO/studio/web" && bun run scripts/vocab-audit.ts ) > "$RUN/vocab.log" 2>&1; VRC=$?
clause a1 "$([ $VRC = 0 ] && echo PASS || echo FAIL)" "the words audit: cd studio/web && bun run scripts/vocab-audit.ts — no retired word over components / routes / hooks / router / lib / panel" \
  "exit $VRC: $(grep -v -E "$noise" "$RUN/vocab.log" | tail -3 | tr '\n' ' ' | cut -c1-300)"

# ---- 7. the browser: (a) (b) (c) (d) (f) (h) --------------------------------
js() { ( cd "$HERE" && "${W[@]}" STUDIO="$STUDIO" TOKEN="$TOKEN" RUN="$RUN" "$@" ) 2>&1 | grep -v -E "$noise"; return "${PIPESTATUS[0]}"; }
ui() { # phase [env…]
  local ph="$1"; shift
  js env "$@" node gate10b.js "$ph" > "$RUN/gate10b-$ph.txt"; local rc=$?
  tee -a "$OUT" < "$RUN/gate10b-$ph.txt"
  for id in $(grep -oE '^\([a-z0-9-]+\) FAIL' "$RUN/gate10b-$ph.txt" | tr -d '()' | awk '{print $1}'); do FAILED+=("$id"); done
  [ "$rc" -ge 2 ] && FAILED+=("js-$ph")
}
ui main BATCH="$BATCH" TAGTOK="$TAGTOK" FLOW_BATCH="$FLOW_BATCH" MCP_RUN="$MCP_RUN" ONION_RUN="$ONION_RUN" \
  DEMO="$RUN/g10bdemo" TERMS_JSON="$RUN/terms.json"

# ---- 8. (c) the manifest's chain, (d) the arena's MCP gap ------------------
gp=$(api "/api/runs?limit=5&tag.arena.batch=$BATCH&tag.arena.scenario=mw.governance-patterns" | jq -r '[.runs[] | "\(.id[0:8]) manifest \(.manifest_hash[0:12])"] | join(", ")')
gh=$(api "/api/runs?limit=1&tag.arena.batch=$BATCH&tag.arena.scenario=mw.governance-patterns" | jq -r '.runs[0].manifest_hash // empty')
gcode=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$STUDIO/api/manifests/$gh")
gavail=$(api "/api/runs?limit=1&tag.arena.batch=$BATCH&tag.arena.scenario=mw.governance-patterns" | jq -r '.runs[0].id' | xargs -I{} curl -sf -H "Authorization: Bearer $TOKEN" "$STUDIO/api/runs/{}" | jq -r '.manifest_available')
oh=$(api "/api/runs/$ONION_RUN" | jq -r '.manifest_hash // empty')
chain=$(api "/api/manifests/$oh" | jq -c '.manifest.agents[] | select(.name=="gov-desk") | {model_middleware: [.model_middleware[]?.name], tool_middleware: [.tool_middleware[]? | (.name + (if .source then " (" + .source + ")" else "" end))]}')
cok=$(echo "$chain" | jq -e '.model_middleware == ["mw.Retry","mw.RepairJSON"] and (.tool_middleware | length) == 2 and (.tool_middleware[0] | test("^func@demo/main\\.go:[0-9]+ \\(demo/main\\.go:[0-9]+\\)$")) and .tool_middleware[1] == "mw.Allow"' >/dev/null && echo yes || echo no)
gt=$(cd "$REPO/core" && go test -count=1 -run '^TestOnionGovernancePatterns$' -v . 2>&1 | grep -E '^(--- |ok|FAIL)' | tr '\n' ' ')
clause c1 "$([ "$cok" = yes ] && echo "$gt" | grep -q -- '--- PASS' && echo PASS || echo FAIL)" "the onion recorded: a governance-patterns manifest lists its middleware chain outer-to-inner" \
  "the in-gate gov-desk (the arena's governance closures + mw.Allow, mw.Retry + mw.RepairJSON; registered through runtime.Install): run $ONION_RUN → GET /api/manifests/${oh:0:12}… → $chain ($cok: model Retry › RepairJSON, tools the scrub closure by name and file:line › mw.Allow)" \
  "cd core && go test -run '^TestOnionGovernancePatterns\$' . → $gt (the arena scenario's two agents: manifest chain, default spans unchanged, DevDetail's layer spans)" \
  "RECORDED (arena gap): the arena's own mw.governance-patterns runs ($gp) — GET /api/manifests/${gh:0:12}… → $gcode, run doc manifest_available=$gavail: the arena registers no runtime for that scenario, so Studio never stores its manifest (the serve batch registers replay twins of other scenarios only, under other hashes). Arena note: register mw.governance-patterns' agents (runtime.Agents) — or ship a weft.json for them — so its manifest reaches Studio"
mm=$(api "/api/runs?limit=5&tag.arena.batch=$BATCH&tag.arena.scenario=rw.mcp-mesh" | jq -r '.runs[0].id // empty')
mo=$( [ -n "$mm" ] && api "/api/runs/$mm/spans" | jq -r '[.spans[] | select(.attrs["gen_ai.operation.name"]=="execute_tool") | "\(.attrs["gen_ai.tool.name"]) origin=\(.attrs["weft.tool.origin"] // "—")"] | unique | join(", ")')
clause d2 RECORDED "the arena's rw.mcp-mesh names no server (a known arena gap, TODO Blockers)" \
  "rw.mcp-mesh run ${mm:0:8}: execute_tool $mo — the arena imports with mcp.Tools(…) and no mcp.Server(name), so its tools read origin \"mcp\" and the graph has no mcp:<server> node; (d) proves O8 with the in-gate mcp.Server(\"billing\") run instead. Arena note: pass mcp.Server(\"billing\") (and a second server for the transport failure) in rw.mcp-mesh"

# ---- 9. (e) the CLI, second half: a fresh Studio with no --token -----------
bg "$RUN/fresh.strace" "$RUN/fresh.log" "${W[@]}" "$RUN/weft" studio --addr "$ADDR2" --db "sqlite://$RUN/fresh.db" --open=false
FPID=$BGPID; FWRAP=$BGWRAP
waitfor 30 curl -sf "$STUDIO2/api/meta" -o /dev/null
waitfor 10 test -s "$RUN/fresh.db.token"
TOKEN2=$(cat "$RUN/fresh.db.token" 2>/dev/null)
banner=$(grep -v -E "$noise" "$RUN/fresh.log" | grep -iE 'token' | head -2 | tr '\n' ' ')
ui fresh STUDIO2="$STUDIO2" TOKEN2="$TOKEN2" TOKEN_FILE="$RUN/fresh.db.token"
stop "$FPID" "$FWRAP"
sleep 1
discback=$(jq -r .url "$DISC" 2>/dev/null)
clause e1 "$([ "$e1" = yes ] && [ "$drc" = 0 ] && echo PASS || echo FAIL)" "bare \`weft doctor\` (no flags, no env; a private discovery dir) names the Studio started with --addr and its source on stderr" \
  "stderr: $derr" "stdout: $(echo "$dout" | grep -E '^(ok|warn|FAIL|fail) +(studio|discovery|token)' | tr '\n' ' ' | sed -E 's/ +/ /g' | cut -c1-300)"
clause e2 "$([ "$e2" = yes ] && echo PASS || echo FAIL)" "a second \`weft studio\` on the same db exits non-zero naming the first's URL" "exit $s2rc: $s2"
clause e3 "$([ "$e3" = yes ] && echo PASS || echo FAIL)" "\`weft open --with-token\` with no id prints the root with #token=" \
  "stdout: $oout (stderr: $(grep -v -E "$noise" "$RUN/open.err" | head -1))"
say "      the fresh Studio's banner: $banner; token file $RUN/fresh.db.token $([ -n "$TOKEN2" ] && echo written || echo MISSING); discovery file after it stopped names $discback ($([ "$discback" = "$STUDIO" ] && echo restored || echo NOT RESTORED))"

# ---- 10. (g) make studio-check + the ledger --------------------------------
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
B="$REPO/studio/web/panel-budget.json"
rows10b=$(jq -r '.rows | (map(.label) | index("P10a-pre-web-fix")) as $i | .[($i+1):][] | "\(.label) \(.gzip)"' "$B" | paste -sd'·' | sed 's/·/ · /g')
# every commit since v0.19.0 that changed the built panel.js appended a ledger row
miss=""; ncommits=0
for c in $(git -C "$REPO" rev-list v0.19.0..HEAD -- studio/dist/panel/panel.js); do
  ncommits=$((ncommits+1))
  a=$(git -C "$REPO" show "$c:studio/web/panel-budget.json" 2>/dev/null | jq '.rows | length'); p=$(git -C "$REPO" show "$c^:studio/web/panel-budget.json" 2>/dev/null | jq '.rows | length')
  [ "${a:-0}" -gt "${p:-0}" ] || miss="$miss $(git -C "$REPO" log -1 --format=%h "$c")"
done
gok=$([ "$SCRC" = 0 ] && [ "$measured" = "$last" ] && [ "$cap" = 106496 ] && [ "$room" -ge "$CAP_FLOOR" ] && [ "$kib" -le 800 ] && [ "$dirty" = 0 ] && [ -z "$miss" ] && echo PASS || echo FAIL)
clause g "$gok" "make studio-check passes; the ledger's last row equals the built panel.js; headroom ≥ $CAP_FLOOR B under 106,496 B; every phase 10b panel delta has its row; Studio dist ≤ 800 KiB" \
  "$sc" "the build's ledger line: ${hl:-?}" \
  "panel measured $measured B gzip (studio/dist/panel/panel.js, stamp-normalized) = the ledger's last row $label $last B: $([ "$measured" = "$last" ] && echo yes || echo NO); cap $cap B → headroom $room B ($([ "$room" -ge "$CAP_FLOOR" ] && echo "≥ $CAP_FLOOR" || echo "UNDER $CAP_FLOOR"))" \
  "rows since P10a-pre-web-fix: $rows10b" \
  "the $ncommits commits since v0.19.0 that changed panel.js each appended a row: $([ -z "$miss" ] && echo yes || echo "NO —$miss")" \
  "Studio dist $kib KiB gzipped (≤ 800: $([ "$kib" -le 800 ] && echo yes || echo NO)); studio/dist + npm left clean by the rebuild: $([ "$dirty" = 0 ] && echo yes || echo "NO ($dirty paths)")"

# ---- 11. (i) make mcp-check -------------------------------------------------
( cd "$REPO" && "${W[@]}" make mcp-check ) > "$RUN/mcp-check.log" 2>&1; MRC=$?
mev=(); while read -r l; do mev+=("$l"); done < <(grep -E 'MCP CHECK|^--- (PASS|FAIL)|^(ok|FAIL)\s' "$RUN/mcp-check.log" | cut -c1-200)
clause i "$([ $MRC = 0 ] && grep -q 'MCP CHECK PASS' "$RUN/mcp-check.log" && echo PASS || echo FAIL)" "make mcp-check passes" "make mcp-check → exit $MRC" "${mev[@]}"

# ---- 12. (j) the principle gate ----------------------------------------------
n=$(cd "$REPO" && go list -deps ./core | grep -c weft/)
clause j1 "$([ "$n" = 3 ] && echo PASS || echo FAIL)" "core deps: go list -deps ./core | grep -c weft/ = 3" \
  "$n — $(cd "$REPO" && go list -deps ./core | grep weft/ | tr '\n' ' ')"

T='TestReportingHookChangesNothingModelVisible|TestRequestRecordsChangeNothingModelVisible|TestCompactionViewChangesNothingModelVisible|TestRequestFactsOffComputeNothing|TestOnionRecordChangesNothing'
res=$(cd "$REPO/core" && go test -count=1 -run "^($T)\$" -v . 2>&1 | grep -E '^(--- |ok|FAIL)' | tr '\n' ' ')
TT='TestObservabilityNeverChangesTheSession|TestThreadRecordsRespectContentOption'
rest=$(cd "$REPO" && go test -count=1 -run "^($TT)\$" -v ./thread 2>&1 | grep -E '^(--- |ok|FAIL)' | tr '\n' ' ')
TO='TestThreadRecordContentOff|TestThreadRecordContentOn'
reso=$(cd "$REPO" && go test -count=1 -run "^($TO)\$" -v ./otel 2>&1 | grep -E '^(--- |ok|FAIL)' | tr '\n' ' ')
cnt() { echo "$1" | grep -oE -- '--- PASS' | wc -l; }
j2=$(echo "$res$rest$reso" | grep -q 'FAIL' && echo FAIL || ([ "$(cnt "$res")" -ge 5 ] && [ "$(cnt "$rest")" -ge 2 ] && [ "$(cnt "$reso")" -ge 2 ] && echo PASS || echo FAIL))
clause j2 "$j2" "the observability on/off goldens identical: core's (no hook / reporting hook, request records, compaction view, request facts off, the onion record), thread's session golden and content option, otel's thread records off/on" \
  "cd core && go test -run '^($T)\$' . → $(cnt "$res") PASS: $res" "go test -run '^($TT)\$' ./thread → $(cnt "$rest") PASS: $rest" "go test -run '^($TO)\$' ./otel → $(cnt "$reso") PASS: $reso"

honesty="$REPO/studio/web/src/lib/honesty.ts"; golden="$REPO/studio/testdata/holes.golden.json"
missing=""; for c in $(jq -r '.[] | (.causes // {}) | keys[]' "$golden"); do grep -qE "^\s+$c: \{" "$honesty" || missing="$missing $c"; done
mj() { jq -r "$1" "$RUN/gate10b-$2.json" 2>/dev/null; }
derived=$(mj '.facts.derived // ""' main); unp=$(mj '.facts.unpriced // ""' main)
hid=$(curl -s -H "Authorization: Bearer $TAGTOK" "$STUDIO/api/runs/$(api "/api/runs?limit=1&tag.arena.batch=$BATCH" | jq -r '.runs[0].id')/requests" | jq -c '{badge, reason}')
rtw=$(mj '[.facts.runtimes[]? | select(.state != "connected") | "\(.id) \(.state) “\(.word)”"] | join(" · ")' main)
rapi=$(api /api/runtimes | jq -r '[.runtimes[] | select(.agents | any(.name=="gov-desk")) | "\(.id) connected=\(.connected)"] | join(", ")')
j3=$([ -z "$missing" ] && [ -n "$derived" ] && [ -n "$unp" ] && echo "$hid" | grep -q '"badge":"hidden"' && [ -n "$rtw" ] && echo PASS || echo FAIL)
clause j3 "$j3" "every new hole a badge: the A3 table (holes.golden.json causes ⊆ lib/honesty.ts) and the samples — derived, hidden by token access, a disconnected runtime, unpriced" \
  "holes golden causes in CAUSES: ${missing:+MISSING$missing}${missing:-all}" \
  "derived (the stock_genai run page): ${derived:-NONE SEEN}" \
  "hidden by token access (the batch's tag token, GET /api/runs/{id}/requests): $hid" \
  "a gone / disconnected runtime (the in-gate gov-desk link after the program exited; API $rapi): /runtimes ${rtw:-NONE SEEN} [$(mj '.facts.runtimesShot // "—"' main)]" \
  "unpriced (the batch-scoped panel): ${unp:-NONE SEEN}"

hosts='api\.openai\.com|api\.anthropic\.com|generativelanguage\.googleapis\.com|aiplatform\.googleapis\.com|api\.z\.ai|open\.bigmodel\.cn|api\.moonshot|openrouter\.ai|api\.deepseek'
hl4=$(for f in seed.log studio.log fresh.log demo-flow.log demo-mcp.log demo-onion.log; do [ -f "$RUN/$f" ] && echo -n "$f:$(grep -cE "$hosts" "$RUN/$f") "; done)
traces=$(ls "$RUN"/*.strace 2>/dev/null)
if [ -n "$traces" ]; then
  dests=$(perl -ne 'next unless /connect\(.*sa_family=AF_INET6?/; my ($port) = /sin6?_port=htons\((\d+)\)/; my ($addr) = /inet_addr\("([^"]+)"\)/; ($addr) = /inet_pton\(AF_INET6, "([^"]+)"/ unless $addr; print "$addr:$port\n"' $traces | sort | uniq -c | sort -rn)
  inet=$(echo "$dests" | awk 'NF{print $2" ×"$1}' | tr '\n' ' ')
  outside=$(echo "$dests" | awk 'NF{print $2}' | grep -vE '^(127\.|::1:|::ffff:127\.)' | wc -l)
  net="strace -f -e connect over $(echo $traces | xargs -n1 basename | tr '\n' ' '): AF_INET/6 destinations $inet — $outside outside loopback"
else outside=0; net="strace not available: the provider-host grep alone"; fi
clause j4 "$([ "$outside" = 0 ] && ! echo "$hl4" | grep -qE ':[1-9]' && echo PASS || echo FAIL)" "no outbound network call: the gate's Studios, the seed and the in-gate program make no request off 127.0.0.1" \
  "$net" "provider hosts named in the logs: $hl4" "engines: $(grep -ohE 'engine=[a-z]+' "$RUN/seed.log" | sort -u | tr '\n' ' ')"

M="$REPO/studio/auth_matrix_test.go"
caps=$(api /api/meta | jq -r '.capabilities | join(",")')
j5l=(); j5bad=""
mrow() { local f; for f in "$REPO"/studio/auth_matrix_test.go "$REPO"/studio/tag_token_test.go; do local l; l=$(grep -nF -- "$1" "$f" | head -1 | cut -d: -f1); [ -n "$l" ] && { echo "$(basename "$f"):$l"; return; }; done; }
for spec in "scope_tags|scope_tags|GET /api/runs?tag.<k>=<v>" \
            "panel_token_tags|panel_token_tags|func TestAuthMatrixTagToken" \
            "/api/traces?id_prefix=|-|GET /api/traces?id_prefix=" \
            "/api/traces?not_run=|-|not_run" \
            "/api/sessions?id_prefix=|-|GET /api/sessions?id_prefix=" \
            "discovery_file|-|discovery_file" \
            "breakpoints surface|-|\"surface\""; do
  IFS='|' read -r what cap re <<< "$spec"
  r=$(mrow "$re")
  capok=$([ "$cap" = - ] && echo "" || (echo ",$caps," | grep -q ",$cap," && echo " · /api/meta capability \"$cap\" ✓" || echo " · /api/meta capability \"$cap\" MISSING"))
  [ "$what" = discovery_file ] && capok=" · /api/meta field $(api /api/meta | jq -e 'has("discovery_file")' >/dev/null && echo '"discovery_file" ✓' || echo '"discovery_file" MISSING')"
  [ -z "$r" ] && j5bad="$j5bad $what(matrix)"
  echo "$capok" | grep -q MISSING && j5bad="$j5bad $what(meta)"
  j5l+=("$what — $([ -n "$r" ] && echo "row at $r" || echo "NO ROW in auth_matrix_test.go / tag_token_test.go")$capok")
done
nr=$(grep -n 'q.Get("not_run")' "$REPO/studio/traces.go" | head -1 | cut -d: -f1)
sf=$(grep -rn 'json:"surface' "$REPO"/studio/*.go | grep -v _test | head -1 | cut -d: -f1-2 | sed "s|$REPO/||")
am=$(cd "$REPO" && go test -count=1 -run '^(TestAuthMatrix|TestAuthMatrixCompletenessCheck|TestAuthMatrixTagToken|TestMetaDiscoveryFile)$' ./studio 2>&1 | tail -1)
echo "$am" | grep -q '^ok' || j5bad="$j5bad tests"
clause j5 "$([ -z "$j5bad" ] && echo PASS || echo FAIL)" "every new route / param in the auth matrix and /api/meta (scope_tags, panel_token_tags, /api/traces?id_prefix= / ?not_run=, /api/sessions?id_prefix=, discovery_file, the breakpoints surface)" \
  "${j5l[@]}" "where the unlisted ones live: not_run studio/traces.go:${nr:-?}; surface ${sf:-?} (PUT /api/runtimes/{id}/breakpoints body; validated in playground_test.go, not in the matrix)" \
  "go test -run '^(TestAuthMatrix|TestAuthMatrixCompletenessCheck|TestAuthMatrixTagToken|TestMetaDiscoveryFile)\$' ./studio → $am" ${j5bad:+"missing:$j5bad"}

C="$REPO/docs/record-contract.md"; CT="$REPO/obsdb/testdata/contract"
j6l=(); j6bad=""
for f in weft.flow weft.attempt.layer weft.attempt.wait_ms http.response.status_code weft.tool.denied_by weft.tool.error_masked weft.tool.args_repaired weft.repair weft.tool.origin weft.caller.remote weft.output.type weft.attempt.giveup_reason parallelism wire; do
  d=$(grep -c -F "\`$f\`" "$C"); dl=$(grep -n -F "\`$f\`" "$C" | head -1 | cut -d: -f1)
  fx=$(grep -rlF "\"$f\"" "$CT" 2>/dev/null | sed "s|$CT/||" | cut -d/ -f1 | sort -u | head -3 | tr '\n' ' ')
  [ "$d" -gt 0 ] && [ -n "$fx" ] || j6bad="$j6bad $f"
  j6l+=("$f — record-contract.md ×$d (line ${dl:-—}) · fixtures ${fx:-NONE}")
done
dm=$(jq -r '[.runs[] | select(.derived) | .message_count] | map(tostring) | join(",")' "$CT/stock_genai/expect.json")
dmd=$(grep -n 'message_count' "$C" | grep -i deriv | head -1 | cut -d: -f1)
[ -n "$dm" ] && [ -n "$dmd" ] || j6bad="$j6bad derived-message_count"
j6l+=("derived message_count — record-contract.md line ${dmd:-NONE} · fixture stock_genai/expect.json message_count $dm")
t6=$(cd "$REPO" && go test -count=1 -run 'TestContract|TestRecordContract' ./obsdb 2>&1 | tail -1)
t6v=$(cd "$REPO" && go test -count=1 -run 'TestContract|TestRecordContract' -v ./obsdb 2>&1 | grep -E '^--- (PASS|FAIL|SKIP): Test(RecordContract[A-Za-z]*|Contract) ' | awk '{print $3"="$2}' | tr -d ':' | tr '\n' ' ')
echo "$t6" | grep -q '^ok' || j6bad="$j6bad tests"
echo "$t6v" | grep -q 'TestRecordContractMigrationNames=PASS' || j6bad="$j6bad TestRecordContractMigrationNames"
clause j6 "$([ -z "$j6bad" ] && echo PASS || echo FAIL)" "every new record attribute in docs/record-contract.md with a fixture" \
  "${j6l[@]}" "go test -run 'TestContract|TestRecordContract' ./obsdb → $t6: $t6v" ${j6bad:+"missing:$j6bad"}

pt=$(cd "$REPO/studio/web" && bunx vitest run src/lib/palette.test.ts 2>&1 | grep -E 'Tests +[0-9]' | sed -E 's/ +/ /g' | head -1)
pok=$(echo "$ps" | jq -r '(.measured == .last_gzip) and (.over | not)' 2>/dev/null)
clause j7 "$([ "$pok" = true ] && [ "$room" -ge "$CAP_FLOOR" ] && [ "$VRC" = 0 ] && echo "$pt" | grep -q passed && ! echo "$pt" | grep -q failed && echo PASS || echo FAIL)" "the panel under the cap with its ledger row (= g); words from src/lib (the vocab audit = a1, the palette colour ratchet)" \
  "$(echo "$ps" | jq -r '"measured \(.measured) B gzip · ledger last row \(.last_label) = \(.last_gzip) B · cap \(.cap) B"' 2>/dev/null || echo "$ps") · headroom $room B" \
  "vocab audit exit $VRC; bunx vitest run src/lib/palette.test.ts (the colour-literal and wash ratchets) →$pt"

# ---- 13. (k) the Coverage ledger -------------------------------------------
if [ -f "$TODO" ]; then
  open=$(sed -n '/^## Coverage ledger/,$p' "$TODO" | grep -E '^\| [A-Z]' | awk -F'|' '{gsub(/ /,"",$5); if ($5=="open") {gsub(/ /,"",$2); gsub(/ /,"",$4); print $2" ("$4")"}}')
  nopen=$(echo -n "$open" | grep -c .)
  total=$(sed -n '/^## Coverage ledger/,$p' "$TODO" | grep -cE '^\| UX-')
  notflight=""; for r in $(echo "$open" | awk '{print $2}' | tr -d '()' | sort -u); do [[ " $IN_FLIGHT " == *" $r "* ]] || notflight="$notflight $r"; done
  kst=$([ "$nopen" = 0 ] && echo PASS || ([ -z "$notflight" ] && echo RECORDED || echo FAIL))
  clause k "$kst" "the Coverage ledger has zero open rows (except the items in flight: ${IN_FLIGHT:-none})" \
    "$TODO: $total rows, $nopen open${open:+: $(echo "$open" | tr '\n' ' ')}"
else clause k FAIL "the Coverage ledger has zero open rows" "no TODO at $TODO (set TODO=)"; fi

# ---- the end ----------------------------------------------------------------
stop "$SPID" "$SWRAP"; trap - EXIT; cleanup
say "studios stopped; DBs $RUN/{studio,fresh}.db, logs $RUN/*.log, straces $RUN/*.strace, shots $RUN/shots ($(ls "$RUN/shots" | wc -l) files) — wall $(( $(date +%s) - T0 )) s"
if [ ${#FAILED[@]} = 0 ]; then say "GATE 10b: PASSED"; exit 0; fi
say "GATE 10b: FAILED ($(printf '%s\n' "${FAILED[@]}" | awk '!s[$0]++' | paste -sd, | sed 's/,/, /g'))"
exit 1
