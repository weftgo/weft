package obsdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Searcher is the optional search capability of a DB (plan A7): runs
// found by what they said and did, not only by who ran them. Both
// first-party backends implement it — obsdb/sqlite through an FTS5
// trigram index maintained on write, obsdb/clickhouse through
// case-insensitive substring scans over the stored records — and the
// obsdbtest conformance table holds them to the same answers. A
// backend that does not implement it is a database that cannot search:
// Studio then leaves its search capability off and says why.
type Searcher interface {
	// Search answers the runs SearchQuery matches, sorted as it asks,
	// with Total counting the whole match. NextBefore and NextBeforeID
	// stay nil and "": a search pages by SearchQuery.Offset.
	Search(ctx context.Context, q SearchQuery) (RunPage, error)
}

// SearchQuery selects runs for Search. Its RunQuery carries the
// filters /api/runs reads — agent, status, session, public id (the
// panel token's scope), parent, playground, experiment, metadata — with
// the same meaning, so a scope applied to one applies to the other;
// RunQuery.Before and BeforeID are not read (a search pages by Offset)
// and RunQuery.Limit is the page size (0 = 50, max 500).
//
// The search fields:
//
//   - Text: the search terms (ParseSearch's grammar). A run matches
//     when every term occurs, case-insensitively, as a substring of at
//     least one of the run's search fields — its prompt (the turn's
//     own: PromptText), its answer (the text of its last
//     assistant message that carries text), its error, or the name of
//     a tool it called (tool_start events) — each term free to match a
//     different field. A content-off run has no prompt or answer:
//     only its error and tool names can match.
//   - Tool: the exact name of a tool the run called.
//   - Model: the exact model name the run row carries.
//   - Error: a case-insensitive substring of the run's error.
//   - Since, Until: Started ≥ Since, Started < Until (zero = open).
//   - MinTokens: input + output tokens ≥ MinTokens (0 = any).
//   - Sort: the order (SearchSort; "" is SortStarted).
//   - Offset: rows to skip in that order.
type SearchQuery struct {
	RunQuery
	Text, Tool, Model, Error string
	Since, Until             time.Time
	MinTokens                int64
	Sort                     SearchSort
	Offset                   int
}

// SearchSort is a search's order. Every order breaks ties by Started,
// then run id, both descending, so a result is total and stable.
type SearchSort string

const (
	SortStarted  SearchSort = "started"  // newest first (the default)
	SortDuration SearchSort = "duration" // longest first: Finished (LastSeen while unfinished) − Started
	SortTokens   SearchSort = "tokens"   // most input + output tokens first
)

// Search bounds: a query is a few words, never a document.
const (
	MaxSearchTerms     = 16
	MaxSearchTermBytes = 256
	MaxSearchOffset    = 100_000
)

// ErrInvalidSearch is returned by ParseSearch and Search for a query
// outside the grammar or its bounds; Studio answers it 400.
var ErrInvalidSearch = errors.New("obsdb: invalid search")

// ParseSearch splits search text into its terms: whitespace separates
// terms, and a double-quoted span is one term, spaces kept (a phrase:
// "refund failed" matches those two words adjacent, in that order). An
// unclosed quote runs to the end of the text; an empty quoted span is
// no term. Terms are matched case-insensitively as substrings — no
// stemming, no operators: AND, OR and * are ordinary text. More than
// MaxSearchTerms terms, or a term longer than MaxSearchTermBytes, is
// ErrInvalidSearch.
func ParseSearch(text string) ([]string, error) {
	var terms []string
	var cur strings.Builder
	quoted, inTerm := false, false
	flush := func() {
		if cur.Len() > 0 {
			terms = append(terms, cur.String())
		}
		cur.Reset()
		inTerm = false
	}
	for _, r := range text {
		switch {
		case r == '"':
			if quoted {
				quoted = false
				flush()
			} else {
				flush()
				quoted = true
			}
		case !quoted && unicode.IsSpace(r):
			if inTerm {
				flush()
			}
		default:
			cur.WriteRune(r)
			inTerm = true
		}
	}
	flush()
	if len(terms) > MaxSearchTerms {
		return nil, fmt.Errorf("%w: %d terms, at most %d", ErrInvalidSearch, len(terms), MaxSearchTerms)
	}
	for _, t := range terms {
		if len(t) > MaxSearchTermBytes {
			return nil, fmt.Errorf("%w: a term of %d bytes, at most %d", ErrInvalidSearch, len(t), MaxSearchTermBytes)
		}
	}
	return terms, nil
}

// CheckSearch validates the parts of a SearchQuery a backend cannot
// read safely: the sort, the offset and the text's terms (returned).
func CheckSearch(q SearchQuery) ([]string, error) {
	switch q.Sort {
	case "", SortStarted, SortDuration, SortTokens:
	default:
		return nil, fmt.Errorf("%w: unknown sort %q", ErrInvalidSearch, q.Sort)
	}
	if q.Offset < 0 || q.Offset > MaxSearchOffset {
		return nil, fmt.Errorf("%w: offset must be 0..%d", ErrInvalidSearch, MaxSearchOffset)
	}
	if q.MinTokens < 0 {
		return nil, fmt.Errorf("%w: min_tokens must be non-negative", ErrInvalidSearch)
	}
	for _, s := range []string{q.Tool, q.Model, q.Error} {
		if len(s) > MaxSearchTermBytes {
			return nil, fmt.Errorf("%w: a filter of %d bytes, at most %d", ErrInvalidSearch, len(s), MaxSearchTermBytes)
		}
	}
	return ParseSearch(q.Text)
}

// PromptPreviewRunes caps RunRow.PromptPreview.
const PromptPreviewRunes = 200

// PromptPreview cuts a prompt to its first PromptPreviewRunes runes —
// the cut both backends make in SQL (substr, leftUTF8), here for a
// writer that holds the text in Go.
func PromptPreview(s string) string {
	if utf8.RuneCountInString(s) <= PromptPreviewRunes {
		return s
	}
	n := 0
	for i := range s {
		if n == PromptPreviewRunes {
			return s[:i]
		}
		n++
	}
	return s
}

// MessageText is one message of a messages record body reduced to what
// search reads: its role and the text of its text parts, joined by
// "\n" (every part of type "text", an empty one included, in order),
// and whether it carries a tool call (a part of type "tool_call").
type MessageText struct {
	Role, Text string
	ToolCall   bool
}

// MessagesText reads a messages record body — a JSON array of
// core.Message — into its messages' text, in order. A body that does
// not parse reads as no messages: search indexes what it can read and
// never fails a write over it.
func MessagesText(body []byte) []MessageText {
	var msgs []struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &msgs); err != nil {
		return nil
	}
	out := make([]MessageText, len(msgs))
	for i, m := range msgs {
		var parts []string
		call := false
		for _, p := range m.Content {
			switch p.Type {
			case "text":
				parts = append(parts, p.Text)
			case "tool_call":
				call = true
			}
		}
		out[i] = MessageText{Role: m.Role, Text: strings.Join(parts, "\n"), ToolCall: call}
	}
	return out
}

// PromptText is the turn's own prompt as one messages record body
// holds it. The input record (weft.messages.input: what the run was
// fed — on a continued conversation the whole history, the turn's
// question last) gives its last user message with non-empty text; any
// other record its first. A record with no user text gives none.
//
// A run's prompt (TurnPrompt) is its input record's when that holds
// one, else the lowest-index record's that does — so a message steered
// in at a later step is never the prompt — with one exception, the
// resume: an input record that ends at an assistant message carrying
// tool calls was cut there by the core (core/loop.go), and what the run
// was fed after that message — the rebuilt tool message, then a new
// prompt if the resume carried one — is the next growth record (index
// + 1). Its user text before its first assistant message is the turn's
// prompt when there is any; a plain resume (Approve or Deny, no new
// prompt) keeps the input record's.
func PromptText(body []byte, input bool) (string, bool) {
	msgs := MessagesText(body)
	if input {
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == "user" && msgs[i].Text != "" {
				return msgs[i].Text, true
			}
		}
		return "", false
	}
	for _, m := range msgs {
		if m.Role == "user" && m.Text != "" {
			return m.Text, true
		}
	}
	return "", false
}

// TurnPrompt is a run's prompt from its input record and the record
// at the next index (nil when none is stored): PromptText's resume rule
// over the pair — the next record's first user text before its first
// assistant message when the input record ends at an assistant message
// with tool calls, else the input record's last user text.
func TurnPrompt(input, next []byte) (string, bool) {
	in := MessagesText(input)
	if n := len(in); n > 0 && in[n-1].Role == "assistant" && in[n-1].ToolCall && next != nil {
		for _, m := range MessagesText(next) {
			if m.Role == "assistant" {
				break
			}
			if m.Role == "user" && m.Text != "" {
				return m.Text, true
			}
		}
	}
	return PromptText(input, true)
}

// LastAssistantText is the text of a body's last assistant message
// with non-empty text: the run's answer when the body is its latest
// messages record holding one.
func LastAssistantText(body []byte) (string, bool) {
	msgs := MessagesText(body)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && msgs[i].Text != "" {
			return msgs[i].Text, true
		}
	}
	return "", false
}
