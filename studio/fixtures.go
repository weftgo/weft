package studio

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"sort"

	"github.com/weftgo/weft"
)

// "Save as fixture" (WEFT-PLAYGROUND §7 P4, D4): turn a recorded run
// into wefttest replay fixtures — the files `wefttest.Record` would
// have written at the model seam, rebuilt from the run's `messages`
// records (the input record makes every run self-contained, D1).
// The loop you just lived becomes a CI regression test: the files go
// into the suite's testdata, and wefttest.Replay answers from them.
//
// The fidelity notes (the fixture is a reconstruction, not a capture
// of the model's stream):
//   - the events are synthesised from the recorded assistant messages
//     the way the scripted engine does, but signatures ARE carried —
//     the messages records keep them byte-for-byte (R3);
//   - the request key is wefttest's own canonical form (replay.go's
//     keyDoc) over the messages prefix and the run's tool catalogue,
//     with thinking, tool choice and the sequential flag at their zero
//     values (omitted) — an agent configured with non-zero defaults
//     records requests this key cannot reproduce;
//   - the stop reason is inferred (tool calls → stop_tool_calls, else
//     end_turn) and per-step usage is not reconstructed (the run row's
//     usage is the run's, not the step's);
//   - the system prompt is left empty: it is recorded for the reviewer
//     only and never keyed (wefttest/replay.go:74).
type fixtureFile struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

// buildFixtures renders one file per recorded assistant message:
// dir/test/<seq>-<key>.json in wefttest's naming (five-digit sequence,
// the key Replay matches on).
func buildFixtures(msgs []weft.Message, tools []string) []fixtureFile {
	var files []fixtureFile
	sorted := append([]string(nil), tools...)
	sort.Strings(sorted)
	seq := 0
	for i, msg := range msgs {
		if msg.Role != weft.RoleAssistant {
			continue
		}
		seq++
		key := fixtureKey(msgs[:i], sorted)
		doc := fixtureDoc{
			Model: weft.ModelInfo{Provider: "weft", Name: "recorded"},
			Request: fixtureRequest{
				System:        "",
				fixtureKeyDoc: fixtureKeyDoc{Messages: msgs[:i], Tools: sorted},
			},
			Events: fixtureEvents(msg),
		}
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			continue // messages round-trip through encoding/json by contract
		}
		files = append(files, fixtureFile{
			Name: fmt.Sprintf("%05d-%s.json", seq, key),
			Body: string(b) + "\n",
		})
	}
	return files
}

// fixtureKeyDoc mirrors wefttest's keyDoc field-for-field (replay.go:
// the JSON bytes are the key's input, so the tags must match exactly).
type fixtureKeyDoc struct {
	Messages   []weft.Message         `json:"messages"`
	Tools      []string               `json:"tools,omitempty"`
	Thinking   *weft.ThinkingConfig   `json:"thinking,omitempty"`
	ToolChoice *weft.ToolChoiceConfig `json:"tool_choice,omitempty"`
	Sequential bool                   `json:"sequential,omitempty"`
}

type fixtureRequest struct {
	System string `json:"system"`
	fixtureKeyDoc
}

// fixtureEvent is wefttest's own envelope (snake_case, ADR 0004).
type fixtureEvent struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Signature string          `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Args      json.RawMessage `json:"args,omitempty"`
	Reason    weft.StopReason `json:"reason,omitempty"`
}

func fixtureEvents(msg weft.Message) []fixtureEvent {
	var out []fixtureEvent
	var calls int
	for _, p := range msg.Content {
		switch p := p.(type) {
		case weft.ReasoningPart:
			out = append(out, fixtureEvent{Type: "reasoning", Text: p.Text, Signature: p.Signature})
		case weft.TextPart:
			out = append(out, fixtureEvent{Type: "text", Text: p.Text})
		case weft.ToolCallPart:
			calls++
			args := p.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			out = append(out, fixtureEvent{Type: "tool_call", ID: p.ID, Name: p.Name, Args: args})
		}
	}
	reason := weft.StopEndTurn
	if calls > 0 {
		reason = weft.StopToolCalls
	}
	out = append(out, fixtureEvent{Type: "finish", Reason: reason})
	return out
}

type fixtureDoc struct {
	Model   weft.ModelInfo `json:"model"`
	Request fixtureRequest `json:"request"`
	Events  []fixtureEvent `json:"events"`
	Error   string         `json:"error"`
}

// fixtureKey is wefttest's hashKeyDoc over the canonical form.
func fixtureKey(prefix []weft.Message, tools []string) string {
	b, err := json.Marshal(fixtureKeyDoc{Messages: prefix, Tools: tools})
	if err != nil {
		panic(fmt.Sprintf("studio: fixture key: %v", err))
	}
	h := fnv.New64a()
	_, _ = h.Write(b)
	return fmt.Sprintf("%016x", h.Sum64())
}

// servePlaygroundFixture is POST /api/playground/fixtures: the run's
// transcript as wefttest fixture files, named the way Replay loads
// them. The files are returned, not written server-side — the user
// drops them into the suite's testdata (the panel never writes to
// your code or files; WEFT-DEVTOOLS §9).
func (s *Server) servePlaygroundFixture(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RunID string   `json:"run_id"`
		Tools []string `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RunID == "" {
		badRequest(w, r, "fixture body: run_id is required")
		return
	}
	// A panel token fixtures inside its public id only (S4.6) — like
	// every run-id route in api.go. The export is a full transcript:
	// a read-scoped token must not read another public id's turns.
	if !s.scopeRunID(w, r, req.RunID) {
		return
	}
	row, err := s.db.Run(r.Context(), req.RunID)
	if err != nil {
		notFound(w, r, "unknown run "+req.RunID)
		return
	}
	bodies, err := s.db.Transcript(r.Context(), req.RunID)
	if err != nil || len(bodies) == 0 {
		badRequest(w, r, "the run has no readable transcript to fixture (content capture off?)")
		return
	}
	var msgs []weft.Message
	for _, body := range bodies {
		var batch []weft.Message
		if err := json.Unmarshal(body, &batch); err == nil {
			msgs = append(msgs, batch...)
		}
	}
	// The tool catalogue the run's requests carried — the caller names
	// it (the drawer knows the registered set); a run without tools
	// keys on the messages alone.
	files := buildFixtures(msgs, req.Tools)
	if len(files) == 0 {
		badRequest(w, r, "the run recorded no assistant turns to fixture")
		return
	}
	writeJSON(w, r, http.StatusOK, struct {
		RunID string        `json:"run_id"`
		Agent string        `json:"agent"`
		Files []fixtureFile `json:"files"`
	}{RunID: req.RunID, Agent: row.Agent, Files: files})
}
