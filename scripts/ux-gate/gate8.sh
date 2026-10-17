#!/usr/bin/env bash
# gate8.sh — the phase 8 gate (audit §9, gate 8), end to end: a fresh
# Studio from $REPO, the arena's offline seed into it (under strace when
# available), examples/studio-local's turn exported into it (the logs
# path), the journeys (gate8.js: the failed-run journey, the subagent
# tree, the panel's Timeline, U25's shapes, I3, U11), H9's browser
# clauses (h9.js, with a scripted director built under $RUN), `make
# studio-check` and the panel ledger, then the principle gate. One line
# per clause: PASS / FAIL / RECORDED with its evidence; the lot in
# $RUN/gate8.txt.
#
#   ARENA=… RUN=… PLAYWRIGHT_DIR=… scripts/ux-gate/gate8.sh
#
# REPO     the weft tree under test (default: this script's own tree)
# ARENA    the weft-arena checkout (default: $REPO/../weft-arena); built
#          as it is, never modified — against ITS go.work sibling
#          ($ARENA/../weft), which the header names
# RUN      scratch dir for the DB, logs, binaries and screenshots
# GO_TEST_DIR  where the principle gate's go tests run (default $REPO)
# PLAYWRIGHT_DIR  a dir whose node_modules has playwright (pw.js)
# STUDIO_CHECK=0  skip `make studio-check` in $REPO (clause d then reads
#          the committed bundle only)
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="${REPO:-$(cd "$HERE/../.." && pwd)}"
ARENA="${ARENA:-$REPO/../weft-arena}"
[ -d "$ARENA" ] || { echo "no weft-arena checkout at $ARENA: set ARENA=" >&2; exit 2; }
ARENA="$(cd "$ARENA" && pwd)"
RUN="${RUN:-$PWD/gate8-run}"
ADDR="${ADDR:-127.0.0.1:7341}"
APP_ADDR="${APP_ADDR:-127.0.0.1:7342}"
TOKEN="${TOKEN:-gate8tok}"
GO_TEST_DIR="${GO_TEST_DIR:-$REPO}"
AXE_DIR="${AXE_DIR:-$REPO/studio/web/node_modules/axe-core}"
STUDIO="http://$ADDR"
export PLAYWRIGHT_DIR="${PLAYWRIGHT_DIR:-}" AXE_DIR GATE_SHOTS="$RUN/shots"
mkdir -p "$RUN/shots"
OUT="$RUN/gate8.txt"; : > "$OUT"
say() { echo "$*" | tee -a "$OUT"; }
api() { curl -sf -H "Authorization: Bearer $TOKEN" "$STUDIO$1"; }
noise='^(zoxide|Please ensure|If the issue|https://github.com/ajeetdsouza|Disable this message|$)'
FAILS=0
clause() { # id status title evidence…
  local id="$1" st="$2" title="$3"; shift 3
  [ "$st" = FAIL ] && FAILS=$((FAILS+1))
  say "($id) $st — $title"; for e in "$@"; do say "      $e"; done
}

say "gate 8 — $(date -u +%FT%TZ)"
say "repo   $REPO @ $(git -C "$REPO" log -1 --format='%h %s' | cut -c1-100)$( [ -n "$(git -C "$REPO" status --porcelain)" ] && echo ' (DIRTY)')"
say "arena  $ARENA ($(git -C "$ARENA" status --porcelain | wc -l) uncommitted paths, built as is) against its go.work sibling $(cd "$ARENA/../weft" && pwd) @ $(git -C "$ARENA/../weft" rev-parse --short HEAD)"
say "run    $RUN"
if ss -ltn 2>/dev/null | grep -q " $ADDR "; then say "$ADDR is taken: $(ss -ltnp 2>/dev/null | grep " $ADDR ")"; exit 2; fi

# ---- 1. Studio from $REPO, a fresh DB ---------------------------------
( cd "$REPO" && go build -o "$RUN/weft" ./cmd/weft ) || { say "weft build failed"; exit 2; }
rm -f "$RUN"/studio.db*
"$RUN/weft" studio --addr "$ADDR" --db "sqlite://$RUN/studio.db" --token "$TOKEN" --open=false > "$RUN/studio.log" 2>&1 &
SPID=$!; echo "$SPID" > "$RUN/studio.pid"
trap 'kill $SPID 2>/dev/null; wait $SPID 2>/dev/null' EXIT
for i in $(seq 1 60); do api /api/meta >/dev/null && break; sleep 0.5; done
api /api/meta >/dev/null || { say "Studio did not answer /api/meta"; cat "$RUN/studio.log"; exit 2; }
say "studio $STUDIO pid $SPID: $(api /api/meta | jq -c '{weft_version, auth_required, pricing: .pricing.source}')"

# ---- 2. the arena, seeded offline -------------------------------------
( cd "$ARENA" && go build -o "$RUN/weft-arena" ./cmd/weft-arena ) > "$RUN/arena-build.log" 2>&1
if [ $? -ne 0 ]; then say "arena build FAILED:"; tail -30 "$RUN/arena-build.log" | tee -a "$OUT"; exit 3; fi
SEED=( "$RUN/weft-arena" run --offline --studio --no-save --jobs 8 )
if command -v strace >/dev/null && [ "${STRACE:-1}" != 0 ]; then
  SEED=( strace -f -qq -e trace=connect -o "$RUN/seed.strace" "${SEED[@]}" )
fi
( cd "$ARENA" && WEFT_STUDIO_URL="$STUDIO" WEFT_STUDIO_TOKEN="$TOKEN" "${SEED[@]}" ) > "$RUN/seed.log" 2>&1
SEEDRC=$?
say "seed   exit $SEEDRC: $(grep -E '^weft-arena — engine=' "$RUN/seed.log" | tail -1)"
grep -E '^❌' -A3 "$RUN/seed.log" | grep -E '^(❌|     ❌)' | sed 's/^/         /' | tee -a "$OUT"
sleep 3 # the exporter's last batch
say "seed   landed: $(api '/api/runs?limit=1' | jq .total) top-level runs, $(api '/api/runs?limit=1&all=1' | jq .total) with subagent children"
say "seed   WARN/ERROR lines: $(grep -cE ' (WARN|ERROR) ' "$RUN/seed.log")"

# ---- 3. examples/studio-local's turn, exported to this Studio (h) -----
LOGS_RUN=""
if ( cd "$REPO/examples/studio-local" && go build -o "$RUN/studio-local" . ) > "$RUN/studio-local-build.log" 2>&1; then
  mkdir -p "$RUN/sl"
  ( cd "$RUN/sl" && exec env WEFT_STUDIO_URL="$STUDIO" WEFT_STUDIO_TOKEN="$TOKEN" WEFT_DISCOVERY=off "$RUN/studio-local" -addr "$APP_ADDR" ) > "$RUN/studio-local.log" 2>&1 &
  APID=$!
  for i in $(seq 1 40); do curl -sf -o /dev/null "http://$APP_ADDR/" && break; sleep 0.25; done
  curl -s -D "$RUN/studio-local.headers" -o "$RUN/studio-local.reply" -X POST "http://$APP_ADDR/run" -d 'where is order 42?'
  sleep 4
  kill "$APID" 2>/dev/null; wait "$APID" 2>/dev/null
  LOGS_RUN=$(grep -i '^Weft-Scope:' "$RUN/studio-local.headers" | tr -d '\r' | sed -E 's/.*run=([^;]+).*/\1/')
  say "app    examples/studio-local (offline echo model, otel.Slog() bridge) on $APP_ADDR → run $LOGS_RUN: $(head -c 120 "$RUN/studio-local.reply")"
else say "app    examples/studio-local did not build: $(tail -3 "$RUN/studio-local-build.log")"; fi

# ---- 4. the journeys (a)–(c), (f)–(h) -----------------------------------
( cd "$HERE" && STUDIO="$STUDIO" TOKEN="$TOKEN" RUN="$RUN" LOGS_RUN="$LOGS_RUN" node gate8.js ) 2>&1 | grep -v -E "$noise" | tee "$RUN/gate8-ui.txt" >> "$OUT"
JSRC=${PIPESTATUS[0]}
cat "$RUN/gate8-ui.txt"
[ "$JSRC" -ne 0 ] && FAILS=$((FAILS+$(grep -cE '^\([a-z0-9]+\) FAIL' "$RUN/gate8-ui.txt")))
[ "$JSRC" -ge 2 ] && FAILS=$((FAILS+1))
rid=$(grep -oE 'request id [0-9a-f]+' "$RUN/gate8-ui.txt" | awk '{print $3}' | head -1)
line=$( [ -n "$rid" ] && grep -F "$rid" "$RUN/studio.log" | head -1 | cut -c1-220)
clause h2 "$([ -n "$line" ] && echo PASS || echo FAIL)" "U11.2: Studio's stderr names a refused ingest batch with its request id" \
  "studio.log: ${line:-no line names request id ${rid:-?}}"

# ---- (e) H9's browser clauses -----------------------------------------
mkdir -p "$RUN/demo"
cat > "$RUN/demo/main.go" <<'GO'
// The H9 /live demo (gate8.sh writes it under $RUN; it is not in the
// repo): an offline director run (a wefttest script) whose first model
// call fails retryably under mw.Retry (a 3 s backoff), then delegates
// to a worker subagent whose tool takes 4 s — exported to a Studio
// through otel.Install(otel.Studio(url, token)). No network beyond the
// loopback Studio.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/mw"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/wefttest"
)

type slowIn struct {
	Item string `json:"item"`
}

func main() {
	url, tok := os.Args[1], os.Args[2]
	stop := otel.Install(otel.Studio(url, tok), otel.NoEnv())
	defer stop()

	slow := weft.Tool("count_stock", "Count the stock of an item.",
		func(ctx context.Context, in slowIn) (string, error) {
			time.Sleep(4 * time.Second)
			return "42 units", nil
		})
	worker := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "count_stock", Args: `{"item":"widget"}`}),
			wefttest.Say("42 widgets in stock."),
		),
		weft.Name("worker"), slow,
	)
	director := weft.New(
		wefttest.Script(
			wefttest.Fail(fmt.Errorf("upstream: %w", weft.ErrStreamIdle)),
			wefttest.ToolCalls(wefttest.Call{Name: "worker", Args: `{"prompt":"count widgets"}`}),
			wefttest.Say("We have 42 widgets."),
		),
		weft.Name("director"),
		weft.WrapModel(mw.Retry(mw.BaseDelay(3*time.Second))),
		weft.Subagent("worker", "Count stock.", worker),
	)
	res, err := director.Generate(context.Background(), weft.Prompt("How many widgets do we have?"))
	if err != nil {
		fmt.Println("run failed:", err)
		return
	}
	fmt.Println("run", res.ID, res.Text())
	time.Sleep(2 * time.Second) // the exporter's last batch
}
GO
printf 'module gate8demo\n\ngo 1.26.0\n\nrequire github.com/weftgo/weft v0.16.0\n' > "$RUN/demo/go.mod"
printf 'go 1.26.0\n\nuse (\n\t.\n\t%s\n\t%s/core\n)\n' "$REPO" "$REPO" > "$RUN/demo/go.work"
if ( cd "$RUN/demo" && GOPROXY=off go build -o "$RUN/h9demo" . ) > "$RUN/h9demo-build.log" 2>&1; then
  # h9.js's runs-tree clause reads the newest 50 rows of "with child
  # runs": the arena's newest runs (runtime16.*, thread.*) delegate to no
  # subagent, so one demo run first puts a parent + child on that page.
  "$RUN/h9demo" "$STUDIO" "$TOKEN" > "$RUN/h9demo-first.log" 2>&1
  say "demo   one director run before h9.js (a parent + child on the newest page): $(tail -1 "$RUN/h9demo-first.log")"
  ( cd "$HERE" && STUDIO="$STUDIO" TOKEN="$TOKEN" DEMO="$RUN/h9demo $STUDIO $TOKEN" node h9.js ) 2>&1 | grep -v -E "$noise" > "$RUN/h9.txt"
  H9RC=${PIPESTATUS[0]}
  n=$(grep -cE '^\(.*\) (PASS|FAIL)' "$RUN/h9.txt"); nf=$(grep -cE '^\(.*\) FAIL' "$RUN/h9.txt")
  clause e "$([ "$H9RC" = 0 ] && [ "$nf" = 0 ] && echo PASS || echo FAIL)" \
    "H9's browser clauses (scripts/ux-gate/h9.js over the seeded Studio; DEMO $RUN/h9demo — a scripted director, its first call retried under mw.Retry, delegating to a worker)" \
    "$n clause lines, $nf FAIL, exit $H9RC:"
  sed 's/^/  /' "$RUN/h9.txt" | tee -a "$OUT"
else clause e FAIL "H9's browser clauses" "the demo did not build: $(tail -5 "$RUN/h9demo-build.log" | tr '\n' ' ')"; fi

# ---- (d) make studio-check + the ledger ---------------------------------
ps=$(cd "$HERE" && bun panel-size.ts "$REPO" 2>&1 | tail -1)
if [ "${STUDIO_CHECK:-1}" != 0 ]; then
  ( cd "$REPO" && make studio-check ) > "$RUN/studio-check.log" 2>&1; SCRC=$?
  sc="make studio-check → exit $SCRC: $(grep -E '^studio dist: ' "$RUN/studio-check.log" | tail -1); $(grep -E '^ +Tests +[0-9]' "$RUN/studio-check.log" | head -1 | sed -E 's/ +/ /g')"
  head8=$(grep -E '^total .* headroom' "$RUN/studio-check.log" | tail -1)
  kib=$(grep -oE '^studio dist: [0-9]+' "$RUN/studio-check.log" | tail -1 | grep -oE '[0-9]+$')
  dirty=$(git -C "$REPO" status --porcelain -- studio/dist studio/web/npm | wc -l)
else SCRC=0; sc="skipped (STUDIO_CHECK=0)"; head8=$(echo "$ps" | jq -r .line); kib=""; dirty=0; fi
measured=$(echo "$ps" | jq -r .measured); cap=$(echo "$ps" | jq -r .cap); last=$(echo "$ps" | jq -r .last_gzip)
room=$((cap - measured))
if [ -z "$kib" ]; then kib=$(( $(find "$REPO/studio/dist" -type f -exec sh -c 'gzip -c "$1" | wc -c' _ {} \; | paste -sd+ | bc) / 1024 )); fi
dok=$([ "$SCRC" = 0 ] && [ "$measured" = "$last" ] && [ "$room" -ge 8192 ] && [ "$kib" -le 700 ] && [ "$dirty" = 0 ] && echo PASS || echo FAIL)
clause d "$dok" "make studio-check passes; every panel delta in the ledger (total = its last row); headroom ≥ 8,192 B; Studio dist ≤ 700 KiB" \
  "$sc" "the build's ledger line: ${head8:-?}" \
  "panel measured $measured B gzip = ledger last row $(echo "$ps" | jq -r .last_label) $last B: $([ "$measured" = "$last" ] && echo yes || echo NO); cap $cap B → headroom $room B ($([ "$room" -ge 8192 ] && echo "≥ 8,192" || echo "UNDER 8,192"))" \
  "Studio dist $kib KiB gzipped (≤ 700: $([ "$kib" -le 700 ] && echo yes || echo NO)); studio/dist + npm left clean by the rebuild: $([ "$dirty" = 0 ] && echo yes || echo "NO ($dirty paths)")"

# ---- (k) the principle gate -------------------------------------------
n=$(cd "$REPO" && go list -deps ./core | grep weft/ | grep -vc '^github.com/weftgo/weft/core\(/\|$\)')
gm=$(cd "$REPO" && git diff --quiet v0.16.0 -- core/go.mod core/go.sum && echo unchanged || echo changed)
clause k1 "$([ "$n" = 0 ] && [ "$gm" = unchanged ] && echo PASS || echo FAIL)" "core imports nothing above the core module (AGENTS rule 6): packages outside github.com/weftgo/weft/core in its deps = 0, core/go.mod + go.sum unchanged since v0.16.0" \
  "$n outside · go.mod $gm — $(cd "$REPO" && go list -deps ./core | grep weft/ | tr '\n' ' ')"

T='TestReportingHookChangesNothingModelVisible|TestRequestRecordsChangeNothingModelVisible|TestCompactionViewChangesNothingModelVisible|TestRequestFactsOffComputeNothing'
res=$(cd "$GO_TEST_DIR/core" && go test -count=1 -run "^($T)\$" -v . 2>&1 | grep -E '^(--- |ok|FAIL|PASS)' | tr '\n' ' ')
nrun=$(echo "$res" | grep -oE -- '--- PASS' | wc -l)
clause k2 "$(echo "$res" | grep -q 'ok ' && ! echo "$res" | grep -q FAIL && [ "$nrun" -ge 4 ] && echo PASS || echo FAIL)" \
  "observability on/off goldens identical (the four model-visible / compute-nothing tests)" \
  "cd core && go test -count=1 -run '^($T)\$' . → $nrun top-level PASS: $res"

honesty="$REPO/studio/web/src/lib/honesty.ts"
golden="$REPO/studio/testdata/holes.golden.json"
causes=$(jq -r '.[] | .hole as $h | (.causes // {}) | keys[] | "\($h):\(.)"' "$golden" | tr '\n' ' ')
missing=""
for c in $(jq -r '.[] | (.causes // {}) | keys[]' "$golden"); do grep -qE "^\s+$c: \{" "$honesty" || missing="$missing $c"; done
clause k3 "$([ -z "$missing" ] && echo PASS || echo FAIL)" "every hole cause in studio/testdata/holes.golden.json is a key of lib/honesty.ts CAUSES" \
  "golden causes: $causes" "${missing:+MISSING from CAUSES:$missing}${missing:-all present}"

M="$REPO/studio/auth_matrix_test.go"
caps=$(api /api/meta | jq -r '.capabilities | join(",")')
row() { grep -nE "$1" "$M" | head -1 | cut -d: -f1; }
k4lines=(); k4bad=""
for spec in "spans_tree|spans_tree|name: \"GET /api/runs/\{id\}/spans\?tree=1\"" \
            "logs view (?severity=)|logs|name: \"GET /api/runs/\{id\}/logs(\?[^\"]*severity[^\"]*)?\"" \
            "id_prefix (/api/runs?id_prefix=)|-|name: \"GET /api/runs\?[^\"]*id_prefix" \
            "export tree (&tree=1)|export|name: \"GET /api/runs/\{id\}/export\?[^\"]*tree=1" \
            "attempt live frame (kinds=attempt)|live|name: \"GET /api/live\?[^\"]*kinds=[^\"]*attempt"; do
  IFS='|' read -r what cap re <<< "$spec"
  r=$(row "$re")
  capok=$([ "$cap" = - ] && echo "n/a (a query parameter)" || (echo ",$caps," | grep -q ",$cap," && echo "\"$cap\" ✓" || echo "\"$cap\" MISSING"))
  [ -z "$r" ] && k4bad="$k4bad $what;"
  echo "$capok" | grep -q MISSING && k4bad="$k4bad $what (meta);"
  k4lines+=("$what — auth_matrix_test.go row: $([ -n "$r" ] && echo "line $r" || echo NONE) · /api/meta capability: $capok")
done
other=$(cd "$REPO/studio" && for f in idprefix_test.go export_tree_test.go live_attempt_test.go logs_test.go; do [ -f "$f" ] && echo -n "$f(auth words ×$(grep -cE 'Bearer|panel token|read token|403|401' "$f")) "; done)
am=$(cd "$GO_TEST_DIR" && go test -count=1 -run '^TestAuthMatrix' ./studio 2>&1 | tail -1)
echo "$am" | grep -q '^ok' || k4bad="$k4bad TestAuthMatrix;"
clause k4 "$([ -z "$k4bad" ] && echo PASS || echo FAIL)" "every new phase-8 route / flag in the auth matrix test and /api/meta capabilities" \
  "${k4lines[@]}" "the items' own tests: $other" "go test -run '^TestAuthMatrix' ./studio → $am" ${k4bad:+"missing:$k4bad"}

attrs="weft.logs.bridged weft.attempt.wait_ms http.response.status_code weft.tool.timeout_ms weft.tool.queue_ms weft.tool.pending.source weft.subagent.pending weft.output.submitted prepared[] thinking.wire weft.playground.breakpoints"
lines=(); bad=""; rec=""
for a in $attrs; do
  # prepared[] and thinking.wire are request record body fields: inside
  # logs.json's string body their quotes are escaped (\"prepared\").
  case "$a" in prepared[]) doc=prepared; needle='\"prepared\"';; thinking.wire) doc=thinking.wire; needle='\"wire\"';; *) doc="$a"; needle="\"$a\"";; esac
  d=$(grep -c -F "$doc" "$REPO/docs/record-contract.md")
  f=$(grep -l -F "$needle" "$REPO"/obsdb/testdata/contract/*/*.json 2>/dev/null | xargs -r -n1 dirname | xargs -r -n1 basename | sort -u | tr '\n' ',' | sed 's/,$//')
  if [ -z "$f" ]; then
    t=$(grep -rl -F "$doc" "$REPO" --include='*_test.go' 2>/dev/null | sed "s|$REPO/||" | head -4 | tr '\n' ',' | sed 's/,$//')
    f="(no contract fixture; tests: ${t:-none})"; rec="$rec $a"
  fi
  [ "$d" -gt 0 ] || bad="$bad $a"
  lines+=("$a — record-contract.md ×$d · fixtures: $f")
done
ct=$(cd "$GO_TEST_DIR" && go test -count=1 -run '^TestContract$' ./obsdb 2>&1 | tail -1)
st=PASS; [ -n "$rec" ] && st=RECORDED; [ -n "$bad" ] && st=FAIL; echo "$ct" | grep -q '^ok' || st=FAIL
clause k5 "$st" "every new phase-8 attribute in docs/record-contract.md with an obsdb/testdata/contract fixture" "${lines[@]}" \
  "go test -run TestContract ./obsdb → $ct" ${bad:+"not in docs/record-contract.md:$bad"} ${rec:+"documented, no contract fixture (RECORDED):$rec"}

hosts='api\.openai\.com|api\.anthropic\.com|generativelanguage\.googleapis\.com|aiplatform\.googleapis\.com|api\.z\.ai|open\.bigmodel\.cn|api\.moonshot|openrouter\.ai|api\.deepseek'
hl=$(for f in seed.log studio.log studio-local.log; do [ -f "$RUN/$f" ] && echo -n "$f:$(grep -cE "$hosts" "$RUN/$f") "; done)
if [ -f "$RUN/seed.strace" ]; then
  dests=$(perl -ne 'next unless /connect\(.*sa_family=AF_INET6?/; my ($port) = /sin6?_port=htons\((\d+)\)/; my ($addr) = /inet_addr\("([^"]+)"\)/; ($addr) = /inet_pton\(AF_INET6, "([^"]+)"/ unless $addr; print "$addr:$port\n"' "$RUN/seed.strace" | sort | uniq -c | sort -rn)
  inet=$(echo "$dests" | awk 'NF{print $2" ×"$1}' | tr '\n' ' ')
  outside=$(echo "$dests" | awk 'NF{print $2}' | grep -vE '^(127\.|::1:|::ffff:127\.)' | wc -l)
  net="strace -f -e connect over the seed: AF_INET/6 destinations $inet — $outside outside loopback"
else outside=0; net="strace not available: the provider-host grep alone"; fi
clause k6 "$([ "$outside" = 0 ] && ! echo "$hl" | grep -qE ':[1-9]' && echo PASS || echo FAIL)" "no outbound network call (the seed runs --offline)" \
  "$net" "provider hosts named in the logs: $hl" "engine: $(grep -oE 'engine=[a-z]+' "$RUN/seed.log" | sort -u | tr '\n' ' ')"

pok=$(echo "$ps" | jq -r '(.measured == .last_gzip) and (.over | not)' 2>/dev/null)
clause k7 "$([ "$pok" = true ] && echo PASS || echo FAIL)" "the panel under the cap with its ledger row (= d)" \
  "$(echo "$ps" | jq -r '"measured \(.measured) B gzip (studio/dist/panel/panel.js, stamp-normalized) · ledger last row \(.last_label) = \(.last_gzip) B · cap \(.cap) B"' 2>/dev/null || echo "$ps")" \
  "the build's line: $(echo "$ps" | jq -r .line 2>/dev/null)"

# ---- the end ------------------------------------------------------------
kill $SPID 2>/dev/null; wait $SPID 2>/dev/null; trap - EXIT
say "studio stopped; DB $RUN/studio.db, logs $RUN/{studio,seed,studio-local,studio-check}.log, h9 $RUN/h9.txt, shots $RUN/shots"
say "gate 8: $([ $FAILS = 0 ] && echo 'no clause FAILed' || echo "$FAILS clause(s) FAILed")"
exit $([ $FAILS = 0 ] && echo 0 || echo 1)
