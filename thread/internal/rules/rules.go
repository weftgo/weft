// Package rules holds the small rules every thread.Storage backend
// must answer identically: the List limit, the metadata and title
// filters, the paging cursor, and the line discipline of a session's
// bytes. Memory and jsonl call these directly; a backend outside the
// thread module (sqlite) keeps its own copy, and the threadtest
// conformance table pins every copy to one answer.
//
// The package imports nothing from thread — thread imports it — so the
// rules speak in plain values: maps, byte slices, times and ids.
package rules

import (
	"bytes"
	"encoding/json"
	"maps"
	"strings"
	"time"
)

// The List page bounds: a zero or negative Query.Limit reads as
// DefaultLimit, and anything above MaxLimit clamps to it.
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// LimitOf normalizes Query.Limit: zero and negatives mean
// DefaultLimit, values above MaxLimit clamp.
func LimitOf(n int) int {
	switch {
	case n <= 0:
		return DefaultLimit
	case n > MaxLimit:
		return MaxLimit
	default:
		return n
	}
}

// MetaMatch reports whether meta holds every pair of want, exactly: a
// wanted key must be present and carry the wanted value. An empty want
// matches everything.
func MetaMatch(meta, want map[string]string) bool {
	for k, v := range want {
		got, ok := meta[k]
		if !ok || got != v {
			return false
		}
	}
	return true
}

// TitleMatches reports whether the title contains the search as a
// substring, folding case.
func TitleMatches(title, search string) bool {
	return strings.Contains(strings.ToLower(title), strings.ToLower(search))
}

// TitleOf returns a session's current title from its entry lines: the
// last info entry carrying a non-empty title — the rule
// Session.Title answers with — or "" when none ever did. A line that
// does not decode is not a title.
func TitleOf(lines [][]byte) string {
	title := ""
	for _, line := range lines {
		if t := InfoTitle(line); t != "" {
			title = t
		}
	}
	return title
}

// InfoTitle returns the title one entry line sets: non-empty only for
// an info entry that carries one.
func InfoTitle(line []byte) string {
	var head struct {
		Type  string `json:"type"`
		Title string `json:"title"`
	}
	if json.Unmarshal(line, &head) != nil || head.Type != "info" {
		return ""
	}
	return head.Title
}

// AfterCursor reports whether a session (created, id) lies strictly
// after the paging cursor (before, beforeID) in List order — newest
// first by created, ties by id descending. A zero before is no cursor:
// everything is after it. With a cursor, a session qualifies when it
// was created strictly before the cursor's time, or at exactly that
// time with an id strictly below beforeID; an empty beforeID therefore
// admits nothing from the cursor's own instant.
func AfterCursor(created time.Time, id string, before time.Time, beforeID string) bool {
	if before.IsZero() {
		return true
	}
	if created.Before(before) {
		return true
	}
	return beforeID != "" && created.Equal(before) && id < beforeID
}

// CompareNewestFirst orders two sessions for List: newest first by
// created, ties by id descending.
func CompareNewestFirst(aCreated time.Time, aID string, bCreated time.Time, bID string) int {
	if c := bCreated.Compare(aCreated); c != 0 {
		return c
	}
	return strings.Compare(bID, aID)
}

// CloneMeta copies a metadata map so a header never aliases the
// caller's; an empty map clones to nil.
func CloneMeta(kv map[string]string) map[string]string {
	if len(kv) == 0 {
		return nil
	}
	return maps.Clone(kv)
}

// SplitLines splits on '\n', complete lines only: the bytes after the
// last newline, if any, are a torn tail and are not returned. The
// lines alias b.
func SplitLines(b []byte) [][]byte {
	var lines [][]byte
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, b[:i])
		b = b[i+1:]
	}
	return lines
}

// Torn reports whether the bytes end mid-line: a writer cut before its
// newline. Bytes ending in '\n' — and no bytes at all — are complete.
func Torn(b []byte) bool {
	return len(b) > 0 && b[len(b)-1] != '\n'
}

// CompleteLen returns the length of b's complete prefix: everything up
// to and including the last newline, 0 when b holds none. Truncating
// to it is the torn-tail repair.
func CompleteLen(b []byte) int {
	return bytes.LastIndexByte(b, '\n') + 1
}
