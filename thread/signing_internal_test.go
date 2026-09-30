package thread

import (
	"testing"
	"time"
)

// TestChallengeCanonical (step 2.2's review focus: "canonical encoding
// of the signed payload — no ambiguity between fields"): two claim
// sets that a plain concatenation could not tell apart produce
// different MACs, because every field is length-prefixed; the same
// claims always produce the same MAC; and every field the challenge
// covers moves the MAC — leave one out and it shows.
func TestChallengeCanonical(t *testing.T) {
	key := []byte("canonical-test-key")
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	mac := func(session, call, tool, args, nonce, keyID, kind, reason, content, who string) []byte {
		return challengeMAC(key, SignedDecision{
			Session: session, RunID: "r1", CallID: call, Tool: tool, ArgsSHA256: args, Expiry: at,
			Nonce: nonce, KeyID: keyID, Kind: Outcome(kind), Reason: reason, Content: content, Who: who,
		})
	}
	// The split ambiguity: ("ab","c") vs ("a","bc") differ.
	a := mac("ab", "c", "tool", "h1", "n1", "k1", "approve", "", "", "avi")
	b := mac("a", "bc", "tool", "h1", "n1", "k1", "approve", "", "", "avi")
	if string(a) == string(b) {
		t.Fatal("field-split claims share a MAC")
	}
	// Determinism: the same claims, the same MAC.
	same := mac("s", "c", "tool", "h1", "n1", "k1", "approve", "", "", "avi")
	if string(same) != string(mac("s", "c", "tool", "h1", "n1", "k1", "approve", "", "", "avi")) {
		t.Fatal("same claims, different MACs")
	}
	// Every field matters.
	base := mac("s", "c", "tool", "h1", "n1", "k1", "approve", "", "", "avi")
	variants := map[string][]byte{
		"session": mac("s2", "c", "tool", "h1", "n1", "k1", "approve", "", "", "avi"),
		"call":    mac("s", "c2", "tool", "h1", "n1", "k1", "approve", "", "", "avi"),
		"tool":    mac("s", "c", "tool2", "h1", "n1", "k1", "approve", "", "", "avi"),
		"args":    mac("s", "c", "tool", "h2", "n1", "k1", "approve", "", "", "avi"),
		"nonce":   mac("s", "c", "tool", "h1", "n2", "k1", "approve", "", "", "avi"),
		"keyid":   mac("s", "c", "tool", "h1", "n1", "k2", "approve", "", "", "avi"),
		"kind":    mac("s", "c", "tool", "h1", "n1", "k1", "deny", "", "", "avi"),
		"reason":  mac("s", "c", "tool", "h1", "n1", "k1", "approve", "why", "", "avi"),
		"content": mac("s", "c", "tool", "h1", "n1", "k1", "approve", "", "what", "avi"),
		"who":     mac("s", "c", "tool", "h1", "n1", "k1", "approve", "", "", "bee"),
	}
	for name, m := range variants {
		if string(m) == string(base) {
			t.Errorf("the %s field does not move the MAC", name)
		}
	}
	// The expiry moves it too.
	sd := SignedDecision{Session: "s", RunID: "r1", CallID: "c", Tool: "tool", ArgsSHA256: "h1", Expiry: at, Nonce: "n1", KeyID: "k1", Kind: OutcomeApprove, Who: "avi"}
	if string(challengeMAC(key, sd)) != string(base) {
		t.Fatal("the struct form and the helper disagree")
	}
	later := sd
	later.Expiry = at.Add(time.Second)
	if string(challengeMAC(key, later)) == string(base) {
		t.Error("the expiry does not move the MAC")
	}
	// UnixNano wraps every 2^64ns: an expiry that far on must not share
	// the MAC, or a captured expired signature revives with a forged
	// expiry centuries out.
	wrapped := sd
	for range 4 {
		wrapped.Expiry = wrapped.Expiry.Add(1 << 62)
	}
	if string(challengeMAC(key, wrapped)) == string(base) {
		t.Error("an expiry 2^64ns later shares the MAC")
	}
	run := sd
	run.RunID = "r2"
	if string(challengeMAC(key, run)) == string(base) {
		t.Error("the run does not move the MAC")
	}
	// The always-grant flag moves it: an "approve and always allow"
	// cannot be downgraded to a plain approve in flight.
	always := sd
	always.Always = true
	if string(challengeMAC(key, always)) == string(base) {
		t.Error("the always flag does not move the MAC")
	}
	// The flag is its own field: approve+always is not the kind
	// "approve+always" without it.
	glued := sd
	glued.Kind = "approve+always"
	if string(challengeMAC(key, always)) == string(challengeMAC(key, glued)) {
		t.Error("the always flag and the kind share bytes")
	}
}
