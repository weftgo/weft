package obsdb

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Compaction is one compaction a run's records name (ADR 0028 §8),
// read by DB.Compactions. Two scopes:
//
//   - CompactionRun: a compaction view, the messages record the core
//     emits when a PrepareStep sent a request whose messages are not the
//     run's transcript. The request whose messages_ref names Index saw
//     the growth records up to Index, concatenated, with the transcript
//     range [FromSeq, ToSeq) replaced by Messages. It is never part of
//     the plain transcript (Transcript and TranscriptBatches hold growth
//     records only) and never applies to another request.
//   - CompactionSession: the informational marker weft/thread emits
//     (a record of kind compaction) when the session compacts, filed
//     under the last run that produced the compacted context (or, for
//     a context no run produced, under the next run). The next run's
//     input record holds the compacted context literally; the marker
//     carries no messages and a reader never applies it. Index and
//     Step are -1, FromSeq and ToSeq 0.
//
// Replaced and Entries are the change in messages: for a view,
// ToSeq-FromSeq and the length of Messages; for a session marker, the
// counts thread reported over the session's context. TokensBefore and
// TokensAfter are thread's estimates (session scope only; 0 for a
// view, whose tokens the core does not estimate). Reason is thread's
// compaction reason (manual, threshold, overflow, from_hook, trim);
// "" for a view.
type Compaction struct {
	Scope        string
	Hash         string // weft.compaction.hash
	Index        int64
	Step         int
	FromSeq      int64
	ToSeq        int64
	Messages     json.RawMessage
	Reason       string
	Replaced     int
	Entries      int
	TokensBefore int64
	TokensAfter  int64
}

// The two compaction scopes (weft.compaction.scope) and the one
// weft.messages.reason ADR 0028 §8 defines.
const (
	CompactionRun     = "run"
	CompactionSession = "session"
	ReasonCompacted   = "compacted"
	// RecordCompaction is the weft.record of thread's session marker.
	RecordCompaction = "compaction"
)

// CompactionOf reads one stored record as a Compaction, so every
// backend parses alike. kind is the record's weft.record, index its
// position, step its weft.step.index, attrs its attributes (numbers as
// numbers or numeric strings) and body its body. ok is false for a
// record that names no compaction — a growth messages record, any
// other kind. A messages record whose weft.messages.reason is not one
// this build knows is an error: ADR 0028 §8 has readers fail loudly
// rather than mistake a view they cannot apply for something else.
func CompactionOf(kind string, index int64, step int, attrs map[string]any, body []byte) (c Compaction, ok bool, err error) {
	switch kind {
	case "messages":
		reason := attr(attrs, attrMessagesReason)
		switch reason {
		case "":
			return Compaction{}, false, nil
		case ReasonCompacted:
		default:
			return Compaction{}, false, fmt.Errorf("obsdb: messages record %d: unknown weft.messages.reason %q", index, reason)
		}
		if index < 0 {
			// Stored at -1 (DeriveRecord): a view no request can name.
			return Compaction{}, false, fmt.Errorf("obsdb: a messages record with weft.messages.reason %q has no usable weft.messages.index", reason)
		}
		c = Compaction{
			Scope:    attr(attrs, attrCompactionScope),
			Hash:     attr(attrs, attrCompactionHash),
			Index:    index,
			Step:     step,
			FromSeq:  int64(attrInt(attrs, attrFromSeq)),
			ToSeq:    int64(attrInt(attrs, attrToSeq)),
			Messages: json.RawMessage(body),
		}
		if c.Scope == "" {
			c.Scope = CompactionRun
		}
		c.Replaced = int(c.ToSeq - c.FromSeq)
		c.Entries = attrIntOr(attrs, attrMessagesCount, -1)
		if c.Entries < 0 {
			var msgs []json.RawMessage
			if json.Unmarshal(body, &msgs) == nil {
				c.Entries = len(msgs)
			} else {
				c.Entries = 0
			}
		}
		return c, true, nil
	case RecordCompaction:
		var b struct {
			Scope        string `json:"scope"`
			Hash         string `json:"hash"`
			Reason       string `json:"reason"`
			Replaced     int    `json:"replaced"`
			Entries      int    `json:"entries"`
			TokensBefore int64  `json:"tokens_before"`
			TokensAfter  int64  `json:"tokens_after"`
		}
		_ = json.Unmarshal(body, &b) // a body that does not parse keeps the attributes' scope and hash
		c = Compaction{
			Scope: firstSet(attr(attrs, attrCompactionScope), b.Scope),
			Hash:  firstSet(attr(attrs, attrCompactionHash), b.Hash),
			Index: -1, Step: -1,
			Reason: b.Reason, Replaced: b.Replaced, Entries: b.Entries,
			TokensBefore: b.TokensBefore, TokensAfter: b.TokensAfter,
		}
		if c.Scope == "" {
			c.Scope = CompactionSession
		}
		return c, true, nil
	}
	return Compaction{}, false, nil
}

// SortCompactions orders DB.Compactions' answer: the views by index,
// then the session markers in the order given (a backend reads them in
// emission order). A marker is filed under the run that produced the
// compacted context, so it follows that run's own views.
func SortCompactions(cs []Compaction) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i].Scope == CompactionSession, cs[j].Scope == CompactionSession
		if a != b {
			return b
		}
		return !a && cs[i].Index < cs[j].Index
	})
}
