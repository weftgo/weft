package mw

import (
	"encoding/json"
	"strings"
	"testing"
)

// repairJSON's fence handling, pinned directly (it is unexported;
// retry_test.go sets the internal-test precedent): the strip must eat
// a language tag and only a language tag.
func TestRepairJSONFenceTag(t *testing.T) {
	if got, ok := repairJSON("```json\n{\"a\":1}\n```"); !ok || got != `{"a":1}` {
		t.Errorf("repair(json-tagged fence) = %q, %v; want `%s`, true", got, ok, `{"a":1}`)
	}
	// A tag followed by a space before the newline is still a tag.
	if got, ok := repairJSON("```json \n{\"a\":1}\n```"); !ok || got != `{"a":1}` {
		t.Errorf("repair(tag + trailing space) = %q, %v; want `%s`, true", got, ok, `{"a":1}`)
	}
	// A bare fence whose payload starts with the literal letters
	// "json": there is no tag (it does not end the line), so the
	// payload must survive byte-for-byte in the returned string —
	// repairJSON reports it honestly as not valid JSON rather than
	// eating four bytes to fabricate one.
	got, ok := repairJSON("```\njsonry{\"a\":1}\n```")
	if ok {
		t.Errorf("repair(bare fence, jsonry payload) = %q, true; want ok=false", got)
	}
	if !strings.Contains(got, "jsonry") {
		t.Errorf("repair(bare fence, jsonry payload) = %q; the payload's literal letters must survive", got)
	}
	if json.Valid([]byte(got)) {
		t.Errorf("repair(bare fence, jsonry payload) = %q; it is not valid JSON and must not be reported as content it fixed away", got)
	}
}
