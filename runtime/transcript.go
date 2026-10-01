package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/thread"
)

// Source transcripts (WEFT-PLAYGROUND.md §10.3): a command names a
// source run; the runtime needs its messages. In order (the earlier
// the better — no content crosses the network twice):
//
//  1. thread storage, when runtime.Threads is set (readers never
//     lock, so an open session is readable);
//  2. the runtime's own local obsdb — otel.LocalDB() — whose input
//     record makes every run self-contained (D1);
//  3. GET /api/runs/{id}/transcript from Studio, with this link's
//     token.

// sourceTranscript resolves a source run's messages through the three
// paths in order. An unresolvable source is an error the executor
// logs and proceeds without — a playground re-run from nothing beats a
// dev tool that wedges.
func (l *link) sourceTranscript(ctx context.Context, runID string) ([]weft.Message, error) {
	if l.cfg.threads != nil {
		if msgs, err := transcriptFromThread(ctx, l.cfg.threads, runID); err == nil {
			return msgs, nil
		}
	}
	if db := otel.LocalDB(); db != nil {
		if msgs, err := transcriptFromObsdb(ctx, db, runID); err == nil {
			return msgs, nil
		}
	}
	return l.transcriptFromStudio(ctx, runID)
}

// transcriptFromObsdb reads the run's messages bodies from the local
// sink: one JSON array of weft.Message per messages record, in order.
func transcriptFromObsdb(ctx context.Context, db obsdb.DB, runID string) ([]weft.Message, error) {
	bodies, err := db.Transcript(ctx, runID)
	if err != nil {
		return nil, err
	}
	return decodeBodies(bodies)
}

// transcriptFromStudio is path 3: the Studio API's transcript
// ({batches: [{index, step, messages}]}), with the link's token.
func (l *link) transcriptFromStudio(ctx context.Context, runID string) ([]weft.Message, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		l.url("/api/runs/"+runID+"/transcript"), nil)
	if err != nil {
		return nil, err
	}
	bearerAuth(req, l.token)
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("studio transcript: %s", resp.Status)
	}
	var body struct {
		Batches []struct {
			Index    int64           `json:"index"`
			Step     int             `json:"step"`
			Messages json.RawMessage `json:"messages"`
		} `json:"batches"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	bodies := make([]json.RawMessage, 0, len(body.Batches))
	for _, b := range body.Batches {
		bodies = append(bodies, b.Messages)
	}
	return decodeBodies(bodies)
}

// decodeBodies turns messages bodies ([]weft.Message each, or null)
// into one concatenated transcript.
func decodeBodies(bodies []json.RawMessage) ([]weft.Message, error) {
	var msgs []weft.Message
	for _, body := range bodies {
		if len(body) == 0 || string(body) == "null" {
			continue
		}
		var batch []weft.Message
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, fmt.Errorf("messages body: %w", err)
		}
		msgs = append(msgs, batch...)
	}
	return msgs, nil
}

// transcriptFromThread is path 1: the session's entries, read-side.
// A thread run id is "<session>-t<n>" (thread mints it at Send), and
// the TurnEntry carrying that id closes the turn — so the turn's
// messages are the MessageEntries and CustomMessageEntries between
// the previous TurnEntry and its own (both kinds are the ones that
// enter the model's context, ADR 0011 §2). Turn boundaries come from
// the ledger, never from counting prompts: a steered user message is
// transcript inside its turn, not a turn opener.
func transcriptFromThread(ctx context.Context, st thread.Storage, runID string) ([]weft.Message, error) {
	session, _, err := parseThreadRunID(runID)
	if err != nil {
		return nil, err
	}
	_, entries, _, err := st.Load(ctx, session)
	if err != nil {
		return nil, err
	}
	return threadTurnMessages(entries, runID)
}

// parseThreadRunID splits "<session>-t<n>".
func parseThreadRunID(runID string) (session string, turn int, err error) {
	i := strings.LastIndex(runID, "-t")
	if i <= 0 {
		return "", 0, fmt.Errorf("not a thread run id %q", runID)
	}
	var n int
	if _, err := fmt.Sscanf(runID[i+2:], "%d", &n); err != nil || n < 1 {
		return "", 0, fmt.Errorf("not a thread run id %q", runID)
	}
	return runID[:i], n, nil
}

// threadTurnMessages collects one turn's messages: the entries after
// the TurnEntry that precedes its own, up to it.
func threadTurnMessages(entries []thread.Entry, runID string) ([]weft.Message, error) {
	end, prevEnd := -1, -1
	for i, e := range entries {
		te, ok := e.(thread.TurnEntry)
		if !ok {
			continue
		}
		if te.RunID == runID {
			end = i
			break
		}
		prevEnd = i
	}
	if end == -1 {
		// No TurnEntry carries the id. A turn still running would have
		// none yet, but a reader cannot tell an open turn from a wrong
		// id — refuse rather than guess (the obsdb and Studio paths
		// resolve an in-flight source).
		return nil, fmt.Errorf("no turn %q in this session", runID)
	}
	var msgs []weft.Message
	for _, e := range entries[prevEnd+1 : end] {
		switch e := e.(type) {
		case thread.MessageEntry:
			msgs = append(msgs, e.Message)
		case thread.CustomMessageEntry:
			msgs = append(msgs, e.Message)
		}
	}
	return msgs, nil
}
