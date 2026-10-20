#!/usr/bin/env bash
# gate9.sh — the phase 9 gate (the conversation, end to end), end to
# end: a fresh Studio from $REPO, the arena built against this tree (a
# scratch go.work) and seeded offline into it (under strace when
# available), a content-off thread session exported into it (a demo
# gate9.sh writes and builds under $RUN, run twice: the agent's
# weft.Content(false), the destination's otel.NoContent()), the journeys
# (gate9.js: acme-support's timeline, the denial's drawer, the pool child,
# the panel's chips and hand-off, content-off, ADR 0021's sentence, U34's
# loop graph and the panel's loop line), `make studio-check` and the
# panel ledger, then the principle gate. One line per clause: PASS / FAIL
# / RECORDED with its evidence; the lot in $RUN/gate9.txt.
#
#   ARENA=… RUN=… PLAYWRIGHT_DIR=… scripts/ux-gate/gate9.sh
#
# REPO     the weft tree under test (default: this script's own tree)
# ARENA    the weft-arena checkout (default: $REPO/../weft-arena); never
#          modified — built against $REPO through a scratch go.work
#          ($RUN/arena.work: go 1.26.0, use $ARENA $REPO $REPO/core), since
#          an arena built against an older weft emits no thread records
# RUN      scratch dir for the DB, logs, binaries and screenshots
# GO_TEST_DIR  where the principle gate's go tests run (default $REPO)
# PLAYWRIGHT_DIR  a dir whose node_modules has playwright (pw.js)
# STUDIO_CHECK=0  skip `make studio-check` in $REPO (clause s then reads
#          the committed bundle only)
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="${REPO:-$(cd "$HERE/../.." && pwd)}"
ARENA="${ARENA:-$REPO/../weft-arena}"
[ -d "$ARENA" ] || { echo "no weft-arena checkout at $ARENA: set ARENA=" >&2; exit 2; }
ARENA="$(cd "$ARENA" && pwd)"
RUN="${RUN:-$PWD/gate9-run}"
ADDR="${ADDR:-127.0.0.1:7341}"
TOKEN="${TOKEN:-gate9tok}"
GO_TEST_DIR="${GO_TEST_DIR:-$REPO}"
AXE_DIR="${AXE_DIR:-$REPO/studio/web/node_modules/axe-core}"
STUDIO="http://$ADDR"
PREV_TAG="${PREV_TAG:-v0.17.0}" # the phase's range is $PREV_TAG..HEAD
export PLAYWRIGHT_DIR="${PLAYWRIGHT_DIR:-}" AXE_DIR GATE_SHOTS="$RUN/shots"
mkdir -p "$RUN/shots"
OUT="$RUN/gate9.txt"; : > "$OUT"
say() { echo "$*" | tee -a "$OUT"; }
api() { curl -sf -H "Authorization: Bearer $TOKEN" "$STUDIO$1"; }
noise='^(zoxide|Please ensure|If the issue|https://github.com/ajeetdsouza|Disable this message|$)'
FAILS=0
clause() { # id status title evidence…
  local id="$1" st="$2" title="$3"; shift 3
  [ "$st" = FAIL ] && FAILS=$((FAILS+1))
  say "($id) $st — $title"; for e in "$@"; do say "      $e"; done
}
T0=$(date +%s)

say "gate 9 — $(date -u +%FT%TZ)"
say "repo   $REPO @ $(git -C "$REPO" log -1 --format='%h %s' | cut -c1-100)$( [ -n "$(git -C "$REPO" status --porcelain)" ] && echo ' (DIRTY)')"
say "arena  $ARENA @ $(git -C "$ARENA" rev-parse --short HEAD) ($(git -C "$ARENA" status --porcelain | wc -l) uncommitted paths, built as is) against $REPO through $RUN/arena.work"
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

# ---- 2. the arena, built against $REPO, seeded offline -----------------
printf 'go 1.26.0\n\nuse (\n\t%s\n\t%s\n\t%s/core\n)\n' "$ARENA" "$REPO" "$REPO" > "$RUN/arena.work"
( cd "$ARENA" && GOWORK="$RUN/arena.work" go build -o "$RUN/weft-arena" ./cmd/weft-arena ) > "$RUN/arena-build.log" 2>&1
if [ $? -ne 0 ]; then say "arena build FAILED:"; tail -30 "$RUN/arena-build.log" | tee -a "$OUT"; exit 3; fi
SEED=( "$RUN/weft-arena" run --offline --studio --no-save --jobs 8 )
# traced <strace file> <cmd…>: runs it under strace -e connect when available
traced() {
  local f="$1"; shift
  if command -v strace >/dev/null && [ "${STRACE_ON:-1}" != 0 ]; then strace -f -qq -e trace=connect -o "$f" "$@"; else "$@"; fi
}
( cd "$ARENA" && WEFT_STUDIO_URL="$STUDIO" WEFT_STUDIO_TOKEN="$TOKEN" traced "$RUN/seed.strace" "${SEED[@]}" ) > "$RUN/seed.log" 2>&1
SEEDRC=$?
seedline=$(grep -E '^weft-arena — engine=' "$RUN/seed.log" | tail -1)
say "seed   exit $SEEDRC: $seedline"
failed=$(grep -E '^❌' "$RUN/seed.log" | awk '{print $2}' | tr '\n' ' ')
grep -E '^❌' -A3 "$RUN/seed.log" | grep -E '^(❌|     ❌)' | sed 's/^/         /' | tee -a "$OUT"
sleep 3 # the exporter's last batch
say "seed   landed: $(api '/api/runs?limit=1' | jq .total) top-level runs, $(api '/api/runs?limit=1&all=1' | jq .total) with subagent children, $(api '/api/sessions?limit=1' | jq .total) sessions"
say "seed   WARN/ERROR lines: $(grep -cE ' (WARN|ERROR) ' "$RUN/seed.log")"
# the one known stale arena check: studio16.search-stopped-by expects
# status=parked to be a 400 (a reading, not a status); weft accepts it
# since I3 (phase 8), so it fails by design until the arena follows.
if [ -z "$failed" ]; then sst=PASS
elif [ "$failed" = "studio16.search-stopped-by " ]; then sst=RECORDED
else sst=FAIL; fi
clause seed "$sst" "the arena's offline seed (expected: 225 passed, 1 failed — studio16.search-stopped-by, the stale I3 check — 12 skipped)" \
  "$seedline" "failed: ${failed:-none}$( [ "$sst" = RECORDED ] && echo ' — the known stale arena check (it expects ?status=parked to be a 400; weft accepts it since I3): arena-side, WEFT-ARENA-TODO.md')"

# ---- 3. the content-off demo: one thread session per mode (e) ----------
mkdir -p "$RUN/cdemo"
cat > "$RUN/cdemo/main.go" <<'GO'
// The phase 9 content-off demo (gate9.sh writes it under $RUN; it is not
// in the repo): one weft/thread session — a title (SetInfo), a parked
// RequireApproval call denied with a reason and a who, the resume, a
// label — exported to a Studio through otel.Install(otel.Studio(url,
// token)), content off: mode "agent" by the agent (weft.Content(false)),
// mode "dest" by the destination (otel.NoContent()). The SECRET-* words
// (prompt, args, result, reason, answer) must not reach Studio; the
// title and the label name must (decision 4: titles and labels are
// metadata), and the who — an opaque id — is weft.approval.actor run
// metadata, which survives content-off by contract. Offline: a wefttest
// script, a memory store, the loopback Studio.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

type wireIn struct {
	Account string `json:"account"`
}

func fail(what string, err error) {
	fmt.Println(what+":", err)
	os.Exit(1)
}

func main() {
	url, tok, pub, mode := os.Args[1], os.Args[2], os.Args[3], os.Args[4]
	var dest []otel.DestOption
	var agentOpts []weft.Option
	if mode == "dest" {
		dest = append(dest, otel.NoContent())
	} else {
		agentOpts = append(agentOpts, weft.Content(false))
	}
	stop := otel.Install(otel.Studio(url, tok, dest...), otel.NoEnv())
	defer stop()
	ctx := context.Background()

	wire := weft.Tool("wire_funds", "Wire funds to an account.",
		func(ctx context.Context, in wireIn) (string, error) { return "SECRET-RESULT-31", nil },
		weft.RequireApproval())
	agent := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "wire_funds", ID: "w1", Args: `{"account":"SECRET-ARGS-55"}`}),
			wefttest.Say("SECRET-ANSWER-88"),
		),
		weft.Options(append(agentOpts, weft.Name("vault-desk"), wire)...),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.PublicID(pub))
	if err != nil {
		fail("create", err)
	}
	if err := s.SetInfo(ctx, "Vault desk (content off)", nil); err != nil {
		fail("setinfo", err)
	}
	turn, err := s.Send(ctx, weft.User("SECRET-PROMPT-42: wire the funds"))
	if err != nil {
		fail("send", err)
	}
	if _, err := turn.Wait(); err != nil {
		fail("turn 1", err)
	}
	pend := s.Pending()
	if len(pend) != 1 {
		fail("pending", fmt.Errorf("%d calls, want 1", len(pend)))
	}
	d := thread.Deny(pend[0].CallID, "SECRET-REASON-77")
	d.Who = "op-9"
	next, err := s.Decide(ctx, d)
	if err != nil {
		fail("decide", err)
	}
	if next != nil {
		if _, err := next.Wait(); err != nil {
			fail("turn 2", err)
		}
	}
	if err := s.Label(ctx, s.Leaf(), "vault-closed"); err != nil {
		fail("label", err)
	}
	if err := s.Close(ctx); err != nil {
		fail("close", err)
	}
	fmt.Println("session", s.ID(), pub, mode)
	time.Sleep(2 * time.Second) // the exporter's last batch
}
GO
printf 'module gate9cdemo\n\ngo 1.26.0\n\nrequire github.com/weftgo/weft v0.17.0\n' > "$RUN/cdemo/go.mod"
printf 'go 1.26.0\n\nuse (\n\t.\n\t%s\n\t%s/core\n)\n' "$REPO" "$REPO" > "$RUN/cdemo/go.work"
OFF_PUBS=""
if ( cd "$RUN/cdemo" && GOPROXY=off go build -o "$RUN/cdemo-bin" . ) > "$RUN/cdemo-build.log" 2>&1; then
  for mode in agent dest; do
    pub="pub_gate9_${mode}off"
    traced "$RUN/cdemo-$mode.strace" "$RUN/cdemo-bin" "$STUDIO" "$TOKEN" "$pub" "$mode" > "$RUN/cdemo-$mode.log" 2>&1
    say "demo   content-off ($mode): exit $? — $(grep -vE "$noise" "$RUN/cdemo-$mode.log" | tail -1)"
    OFF_PUBS="${OFF_PUBS:+$OFF_PUBS,}$mode=$pub"
  done
  sleep 2
else say "demo   the content-off demo did not build: $(tail -5 "$RUN/cdemo-build.log" | tr '\n' ' ')"; fi

# ---- 4. the journeys (a)–(j), (p) ---------------------------------------
( cd "$HERE" && STUDIO="$STUDIO" TOKEN="$TOKEN" RUN="$RUN" OFF_PUBS="$OFF_PUBS" node gate9.js ) 2>&1 | grep -v -E "$noise" | tee "$RUN/gate9-ui.txt" >> "$OUT"
JSRC=${PIPESTATUS[0]}
cat "$RUN/gate9-ui.txt"
[ "$JSRC" -ne 0 ] && FAILS=$((FAILS+$(grep -cE '^\([a-z0-9]+\) FAIL' "$RUN/gate9-ui.txt")))
[ "$JSRC" -ge 2 ] && FAILS=$((FAILS+1))

# ---- (s) make studio-check + the ledger ---------------------------------
# The phase 9 floor: headroom ≥ 7,500 B, except the user-accepted UX-B-41
# countdown (P9-pre-web-fix5–7), which allows ≥ 6,820 B with the ledger's
# last row P9-pre-web-fix7.
FLOOR="${FLOOR:-6820}"; LAST_ROW="${LAST_ROW:-P9-pre-web-fix7}"
ps=$(cd "$HERE" && bun panel-size.ts "$REPO" 2>&1 | tail -1)
if [ "${STUDIO_CHECK:-1}" != 0 ]; then
  ( cd "$REPO" && make studio-check ) > "$RUN/studio-check.log" 2>&1; SCRC=$?
  sc="make studio-check → exit $SCRC: $(grep -E '^studio dist: ' "$RUN/studio-check.log" | tail -1); $(grep -E '^ +Tests +[0-9]' "$RUN/studio-check.log" | head -1 | sed -E 's/ +/ /g')"
  head9=$(grep -E '^total .* headroom' "$RUN/studio-check.log" | tail -1)
  kib=$(grep -oE '^studio dist: [0-9]+' "$RUN/studio-check.log" | tail -1 | grep -oE '[0-9]+$')
  dirty=$(git -C "$REPO" status --porcelain -- studio/dist studio/web/npm | wc -l)
else SCRC=0; sc="skipped (STUDIO_CHECK=0)"; head9=$(echo "$ps" | jq -r .line); kib=""; dirty=0; fi
measured=$(echo "$ps" | jq -r .measured); cap=$(echo "$ps" | jq -r .cap); last=$(echo "$ps" | jq -r .last_gzip); label=$(echo "$ps" | jq -r .last_label)
room=$((cap - measured))
if [ -z "$kib" ]; then kib=$(( $(find "$REPO/studio/dist" -type f -exec sh -c 'gzip -c "$1" | wc -c' _ {} \; | paste -sd+ | bc) / 1024 )); fi
sok=$([ "$SCRC" = 0 ] && [ "$measured" = "$last" ] && [ "$label" = "$LAST_ROW" ] && [ "$room" -ge "$FLOOR" ] && [ "$kib" -le 700 ] && [ "$dirty" = 0 ] && echo PASS || echo FAIL)
clause s "$sok" "make studio-check passes; the ledger's last row is $LAST_ROW and equals the measured gzip; headroom ≥ $FLOOR B (UX-B-41's accepted floor; 7,500 B otherwise); Studio dist ≤ 700 KiB" \
  "$sc" "the build's ledger line: ${head9:-?}" \
  "panel measured $measured B gzip = ledger last row $label $last B: $([ "$measured" = "$last" ] && echo yes || echo NO); last row $LAST_ROW: $([ "$label" = "$LAST_ROW" ] && echo yes || echo "NO ($label)"); cap $cap B → headroom $room B ($([ "$room" -ge "$FLOOR" ] && echo "≥ $FLOOR" || echo "UNDER $FLOOR"); $([ "$room" -ge 7500 ] && echo "≥ 7,500 too" || echo "under the 7,500 B phase floor: the UX-B-41 exception"))" \
  "Studio dist $kib KiB gzipped (≤ 700: $([ "$kib" -le 700 ] && echo yes || echo NO)); studio/dist + npm left clean by the rebuild: $([ "$dirty" = 0 ] && echo yes || echo "NO ($dirty paths)")"

# ---- (k) the principle gate -------------------------------------------
n=$(cd "$REPO" && go list -deps ./core | grep weft/ | grep -vc '^github.com/weftgo/weft/core\(/\|$\)')
gm=$(cd "$REPO" && git diff --quiet "$PREV_TAG" -- core/go.mod core/go.sum && echo unchanged || echo changed)
clause k1 "$([ "$n" = 0 ] && [ "$gm" = unchanged ] && echo PASS || echo FAIL)" "core imports nothing above the core module (AGENTS rule 6): packages outside github.com/weftgo/weft/core in its deps = 0, core/go.mod + go.sum unchanged since $PREV_TAG" \
  "$n outside · go.mod $gm — $(cd "$REPO" && go list -deps ./core | grep weft/ | tr '\n' ' ')"

T='TestReportingHookChangesNothingModelVisible|TestRequestRecordsChangeNothingModelVisible|TestCompactionViewChangesNothingModelVisible|TestRequestFactsOffComputeNothing'
res=$(cd "$GO_TEST_DIR/core" && go test -count=1 -run "^($T)\$" -v . 2>&1 | grep -E '^(--- |ok|FAIL|PASS)' | tr '\n' ' ')
nrun=$(echo "$res" | grep -oE -- '--- PASS' | wc -l)
TT='TestObservabilityNeverChangesTheSession|TestThreadRecordsRespectContentOption'
rest=$(cd "$GO_TEST_DIR" && go test -count=1 -run "^($TT)\$" -v ./thread 2>&1 | grep -E '^(--- |ok|FAIL|PASS)' | tr '\n' ' ')
nt=$(echo "$rest" | grep -oE -- '--- PASS' | wc -l)
TO='TestThreadRecordContentOff|TestThreadRecordContentOn'
reso=$(cd "$GO_TEST_DIR" && go test -count=1 -run "^($TO)\$" -v ./otel 2>&1 | grep -E '^(--- |ok|FAIL|PASS)' | tr '\n' ' ')
no=$(echo "$reso" | grep -oE -- '--- PASS' | wc -l)
k2ok=$(echo "$res$rest$reso" | grep -q FAIL && echo FAIL || ([ "$nrun" -ge 4 ] && [ "$nt" -ge 2 ] && [ "$no" -ge 2 ] && echo "$res" | grep -q 'ok ' && echo "$rest" | grep -q 'ok ' && echo "$reso" | grep -q 'ok ' && echo PASS || echo FAIL))
clause k2 "$k2ok" "observability on/off goldens identical: core's four, thread's session golden (no pipeline / no-op / panicking / real providers: entries and results identical) and its content option, otel's thread records content off/on" \
  "cd core && go test -run '^($T)\$' . → $nrun top-level PASS: $res" \
  "go test -run '^($TT)\$' ./thread → $nt top-level PASS: $rest" \
  "go test -run '^($TO)\$' ./otel → $no top-level PASS: $reso"

honesty="$REPO/studio/web/src/lib/honesty.ts"
golden="$REPO/studio/testdata/holes.golden.json"
causes=$(jq -r '.[] | .hole as $h | (.causes // {}) | keys[] | "\($h):\(.)"' "$golden" | tr '\n' ' ')
missing=""
for c in $(jq -r '.[] | (.causes // {}) | keys[]' "$golden"); do grep -qE "^\s+$c: \{" "$honesty" || missing="$missing $c"; done
holes=$(jq -r '.[].hole' "$golden" | sort -u | tr '\n' ' ')
newc=$(cd "$REPO" && git diff "$PREV_TAG" -- studio/testdata/holes.golden.json | grep -E '^\+' | grep -oE '"[a-z_]+": *\{' | tr -d '"{: ' | sort -u | tr '\n' ' ')
clause k3 "$([ -z "$missing" ] && echo PASS || echo FAIL)" "every hole cause in studio/testdata/holes.golden.json is a key of lib/honesty.ts CAUSES (the timeline's / session's holes included)" \
  "holes: $holes" "golden causes: $causes" "keys added since $PREV_TAG: ${newc:-none}" "${missing:+MISSING from CAUSES:$missing}${missing:-all present}"

M="$REPO/studio/auth_matrix_test.go"
caps=$(api /api/meta | jq -r '.capabilities | join(",")')
row() { grep -nE "$1" "$M" | head -1 | cut -d: -f1; }
k4lines=(); k4bad=""
for spec in "GET /api/sessions/{id}/timeline|timeline|name: \"GET /api/sessions/\{id\}/timeline\"" \
            "GET /api/sessions/{id}/entries|timeline|name: \"GET /api/sessions/\{id\}/entries\"" \
            "GET /api/sessions/{id}/entries?run=|timeline|name: \"GET /api/sessions/\{id\}/entries\?run=\"" \
            "GET /api/sessions/{id}/export (jsonl)|export|name: \"GET /api/sessions/\{id\}/export\"" \
            "GET /api/sessions?tag.<k>=<v>|-|name: \"GET /api/sessions\?tag\." \
            "GET /api/sessions/{id} (delegated, turn facts)|-|name: \"GET /api/sessions/\{id\}\""; do
  IFS='|' read -r what cap re <<< "$spec"
  r=$(row "$re")
  capok=$([ "$cap" = - ] && echo "n/a (a query parameter / an existing route)" || (echo ",$caps," | grep -q ",$cap," && echo "\"$cap\" ✓" || echo "\"$cap\" MISSING"))
  [ -z "$r" ] && k4bad="$k4bad $what;"
  echo "$capok" | grep -q MISSING && k4bad="$k4bad $what (meta);"
  k4lines+=("$what — auth_matrix_test.go row: $([ -n "$r" ] && echo "line $r" || echo NONE) · /api/meta capability: $capok")
done
am=$(cd "$GO_TEST_DIR" && go test -count=1 -run '^TestAuthMatrix' ./studio 2>&1 | tail -1)
echo "$am" | grep -q '^ok' || k4bad="$k4bad TestAuthMatrix;"
clause k4 "$([ -z "$k4bad" ] && echo PASS || echo FAIL)" "every new phase-9 route / flag in the auth matrix test and /api/meta capabilities" \
  "${k4lines[@]}" "go test -run '^TestAuthMatrix' ./studio → $am" ${k4bad:+"missing:$k4bad"}

# the attributes and record events docs/record-contract.md gained since $PREV_TAG
attrs=$(comm -13 <(git -C "$REPO" show "$PREV_TAG:docs/record-contract.md" | grep -oE '`(weft|http|gen_ai)\.[a-z_.]+[a-z]`' | tr -d '`' | LC_ALL=C sort -u) \
                 <(grep -oE '`(weft|http|gen_ai)\.[a-z_.]+[a-z]`' "$REPO/docs/record-contract.md" | tr -d '`' | LC_ALL=C sort -u))
lines=(); bad=""; rec=""
for a in $attrs; do
  d=$(grep -c -F "$a" "$REPO/docs/record-contract.md")
  f=$(grep -l -F "$a" "$REPO"/obsdb/testdata/contract/*/*.json 2>/dev/null | xargs -r -n1 dirname | xargs -r -n1 basename | sort -u | tr '\n' ',' | sed 's/,$//')
  if [ -z "$f" ]; then
    t=$(grep -rl -F "$a" "$REPO" --include='*_test.go' 2>/dev/null | sed "s|$REPO/||" | head -4 | tr '\n' ',' | sed 's/,$//')
    f="(no contract fixture; tests: ${t:-none})"; rec="$rec $a"
  fi
  [ "$d" -gt 0 ] || bad="$bad $a"
  lines+=("$a — record-contract.md ×$d · fixtures: $f")
done
ct=$(cd "$GO_TEST_DIR" && go test -count=1 -run '^TestContract$' ./obsdb 2>&1 | tail -1)
st=PASS; [ -n "$rec" ] && st=RECORDED; [ -n "$bad" ] && st=FAIL; [ -z "$attrs" ] && st=FAIL; echo "$ct" | grep -q '^ok' || st=FAIL
clause k5 "$st" "every attribute / record event docs/record-contract.md gained since $PREV_TAG ($(echo $attrs | wc -w)) with an obsdb/testdata/contract fixture" "${lines[@]}" \
  "go test -run TestContract ./obsdb → $ct" ${bad:+"not in docs/record-contract.md:$bad"} ${rec:+"documented, no contract fixture (RECORDED):$rec"}

hosts='api\.openai\.com|api\.anthropic\.com|generativelanguage\.googleapis\.com|aiplatform\.googleapis\.com|api\.z\.ai|open\.bigmodel\.cn|api\.moonshot|openrouter\.ai|api\.deepseek'
hl=$(for f in seed.log studio.log cdemo-agent.log cdemo-dest.log; do [ -f "$RUN/$f" ] && echo -n "$f:$(grep -cE "$hosts" "$RUN/$f") "; done)
traces=$(ls "$RUN"/seed.strace "$RUN"/cdemo-*.strace 2>/dev/null)
if [ -n "$traces" ]; then
  dests=$(perl -ne 'next unless /connect\(.*sa_family=AF_INET6?/; my ($port) = /sin6?_port=htons\((\d+)\)/; my ($addr) = /inet_addr\("([^"]+)"\)/; ($addr) = /inet_pton\(AF_INET6, "([^"]+)"/ unless $addr; print "$addr:$port\n"' $traces | sort | uniq -c | sort -rn)
  inet=$(echo "$dests" | awk 'NF{print $2" ×"$1}' | tr '\n' ' ')
  outside=$(echo "$dests" | awk 'NF{print $2}' | grep -vE '^(127\.|::1:|::ffff:127\.)' | wc -l)
  net="strace -f -e connect over the seed and both content-off demos ($(echo $traces | xargs -n1 basename | tr '\n' ' ')): AF_INET/6 destinations $inet — $outside outside loopback"
else outside=0; net="strace not available: the provider-host grep alone"; fi
clause k6 "$([ "$outside" = 0 ] && ! echo "$hl" | grep -qE ':[1-9]' && echo PASS || echo FAIL)" "no outbound network call (the seed runs --offline; the demos are wefttest scripts)" \
  "$net" "provider hosts named in the logs: $hl" "engine: $(grep -oE 'engine=[a-z]+' "$RUN/seed.log" | sort -u | tr '\n' ' ')"

pok=$(echo "$ps" | jq -r '(.measured == .last_gzip) and (.over | not)' 2>/dev/null)
clause k7 "$([ "$pok" = true ] && [ "$label" = "$LAST_ROW" ] && [ "$room" -ge "$FLOOR" ] && echo PASS || echo FAIL)" "the panel under the cap with its ledger row (= s)" \
  "$(echo "$ps" | jq -r '"measured \(.measured) B gzip (studio/dist/panel/panel.js, stamp-normalized) · ledger last row \(.last_label) = \(.last_gzip) B · cap \(.cap) B"' 2>/dev/null || echo "$ps")" \
  "the build's line: $(echo "$ps" | jq -r .line 2>/dev/null) · headroom $room B ≥ $FLOOR: $([ "$room" -ge "$FLOOR" ] && echo yes || echo NO)"

# ---- the end ------------------------------------------------------------
kill $SPID 2>/dev/null; wait $SPID 2>/dev/null; trap - EXIT
say "studio stopped; DB $RUN/studio.db, logs $RUN/{studio,seed,cdemo-agent,cdemo-dest,studio-check}.log, shots $RUN/shots ($(ls "$RUN/shots" | wc -l) files)"
say "gate 9: $([ $FAILS = 0 ] && echo 'no clause FAILed' || echo "$FAILS clause(s) FAILed") — wall $(( $(date +%s) - T0 )) s"
exit $([ $FAILS = 0 ] && echo 0 || echo 1)
