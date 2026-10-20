package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/weftgo/weft/obsdb"
)

// The conversation export (UX-B-29):
//
//	GET /api/sessions/{id}/export?format=jsonl
//
// A session as one JSON-lines download, read from obsdb's tables and
// nothing else (the run export's rule: data, never a write), streamed:
// each line goes out as it is read, so the server holds one turn's
// export at a time, never the file. The lines, in order:
//
//   - one "session" record, from the reads made before the first byte:
//     the format (weft.session.export/1), the session's id, public id,
//     agent, its turn count and the state-log marks in the token's
//     scope (what the file can hold, before either cap);
//   - every turn's run export, oldest turn first, exactly as
//     GET /api/runs/{id}/export?format=jsonl writes it (each turn's
//     lines begin with its own "run" record, so a reader groups the
//     lines after it under that turn) — at most sessionExportTurnCap
//     turns;
//   - the session's state log, one "session_mark" record per thread
//     record or compaction marker ({"record":"session_mark","mark":…},
//     the mark as the timeline serves it), in record-time order
//     (timelineMarkCap at most), when the database reads session
//     events;
//   - one "session_hole" record per hole: truncated (cause
//     response_cap) past the turn cap, the mark cap or the byte cap
//     (sessionExportByteCap, 64 MiB of every line before the holes —
//     the session line, the turns', the marks': a turn or mark line
//     that would pass it is not written, nor anything of its kind
//     after it); a
//     plain truncated hole when a read failed after the first byte (the
//     status was already sent: the lines before it are whole); the
//     timeline's not_recorded / stripped / hidden, whether the turns
//     ran to the end or stopped; and not_recorded
//     naming obsdb.SessionEventReader when the database cannot read
//     the state log;
//   - one "session_end" record, last: turns_exported, marks_exported
//     and bytes (the lines before it). A file without it was cut in
//     transit.
//
// The turns' rows come from the Session read and their children from
// one obsdb.TreeReader.ChildRuns read for them all (the per-turn Run
// read only without a TreeReader), priced on one coster; each turn's
// events, transcript, compactions, spans and requests are its own reads.
//
// jsonl is the only format: a session is many runs, and the per-run
// json, otlp and wefttest exports stay the run page's. The scope is the
// session document's: a panel token reads its public id's turns and
// marks only; a read-scoped token's turns carry the request block
// badged hidden, as the run export's jsonl does, and its marks
// content_hidden.

// sessionExportVersion names the session export's line shape.
const sessionExportVersion = "weft.session.export/1"

// sessionExportTurnCap bounds the turns one session export reads.
var sessionExportTurnCap = 200

// sessionExportByteCap bounds the bytes of every line one session
// export writes before its holes — the session line, the turns' and the
// marks' lines: 64 MiB. The holes and the session_end line close the
// file past it. A variable for tests.
var sessionExportByteCap = 64 << 20

var sessionExportFormat = exportFormat{ext: "session.jsonl", contentType: "application/x-ndjson"}

// sessionStream writes the export's lines: the download headers before
// the first, the byte count after each.
type sessionStream struct {
	w       http.ResponseWriter
	r       *http.Request
	id      string
	started bool
	bytes   int
	err     error // the first write error: the client went away
}

func (st *sessionStream) start() {
	if st.started {
		return
	}
	st.started = true
	w := st.w
	w.Header().Set("Content-Type", sessionExportFormat.contentType)
	w.Header().Set("Content-Disposition", contentDisposition(st.id, sessionExportFormat))
	w.WriteHeader(http.StatusOK)
}

// line renders {"record": kind, …v's fields}.
func sessionLine(kind string, v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(b)+len(kind)+16)
	out = append(out, `{"record":`...)
	out = append(out, strconv.Quote(kind)...)
	if len(b) > 2 {
		out = append(out, ',')
		out = append(out, b[1:]...)
	} else {
		out = append(out, '}')
	}
	return append(out, '\n'), nil
}

func (st *sessionStream) write(b []byte) {
	st.start()
	if st.err != nil {
		return
	}
	if _, err := st.w.Write(b); err != nil {
		st.err = err
		return
	}
	st.bytes += len(b)
}

// fits is whether n more bytes stay within sessionExportByteCap.
func (st *sessionStream) fits(n int) bool { return st.bytes+n <= sessionExportByteCap }

// byteCapHole is the truncated hole the byte cap leaves, naming what of
// kind it stopped.
func byteCapHole(what string, left int) stepHole {
	return stepHole{Hole: string(obsdb.HoleTruncated), Cause: string(obsdb.CauseResponseCap),
		Reason: fmt.Sprintf("the export reached its %d MiB cap: %d %s after it are not in this file — nothing was lost", sessionExportByteCap>>20, left, what),
		Fix:    "export the later turns one by one: GET /api/runs/{id}/export?format=jsonl; page the marks: GET /api/sessions/{id}/entries",
		Count:  left}
}

// readFailedHole is the hole a read failing after the first byte
// leaves: the status was sent, so the file says it.
func readFailedHole(what string, err error) stepHole {
	return stepHole{Hole: string(obsdb.HoleTruncated),
		Reason: "a read failed mid-export (" + what + ": " + err.Error() + "): the lines before this one are whole, the rest are not in this file",
		Fix:    "export again"}
}

// serveSessionExport answers GET /api/sessions/{id}/export?format=jsonl.
func (s *Server) serveSessionExport(w http.ResponseWriter, r *http.Request, id string) {
	if f := r.URL.Query().Get("format"); f != "" && f != "jsonl" {
		badRequest(w, r, "format must be jsonl: a session exports as JSON lines — its turns' run exports in turn order, then its state log; a single turn's json, otlp or wefttest export is GET /api/runs/{id}/export")
		return
	}
	if !s.scopeSessionID(w, r, id) {
		return
	}
	ctx := r.Context()
	det, err := s.db.Session(ctx, id)
	if err != nil {
		sessionReadError(w, r, id, err)
		return
	}
	if pub := panelPublicID(r); pub != "" {
		s.scopeSessionDetail(ctx, &det, pub)
	}
	// The state log's scope before the first byte: its count is the
	// head's, and a failure here is still an error status.
	er, hasMarks := s.db.(obsdb.SessionEventReader)
	var mb *markBuilder
	var visible, all int
	if hasMarks {
		mb = s.newMarkBuilder(r, det)
		if visible, all, err = mb.scope(r, er); err != nil {
			dbError(w, r, "session events", id, err)
			return
		}
	}
	st := &sessionStream{w: w, r: r, id: id}
	head, err := sessionLine("session", struct {
		Format    string `json:"format"`
		SessionID string `json:"session_id"`
		PublicID  string `json:"public_id,omitempty"`
		Agent     string `json:"agent"`
		Turns     int    `json:"turns"`
		Marks     int    `json:"marks"`
	}{sessionExportVersion, det.ID, det.PublicID, det.Agent, len(det.Runs), visible})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "export session "+id+": "+err.Error())
		return
	}
	if r.Method == http.MethodHead {
		st.start()
		return
	}
	st.write(head)

	var holes []stepHole
	turns := det.Runs
	if len(turns) > sessionExportTurnCap {
		holes = append(holes, stepHole{Hole: string(obsdb.HoleTruncated), Cause: string(obsdb.CauseResponseCap),
			Reason: fmt.Sprintf("the session has %d turns; this export carries the first %d — nothing was lost", len(det.Runs), sessionExportTurnCap),
			Fix:    "export the later turns one by one: GET /api/runs/{id}/export?format=jsonl",
			Count:  len(det.Runs) - sessionExportTurnCap})
		turns = turns[:sessionExportTurnCap]
	}
	hideRequests := !readsPrompts(r)
	c := s.coster(ctx)
	kids, batched := exportTurnChildren(ctx, s.db, c, turns)
	exported, failed := 0, false
	for i, rr := range turns {
		if st.err != nil {
			return // the client went away
		}
		var x *runExport
		if batched {
			x, err = s.collectExportOf(ctx, c, obsdb.RunDetail{RunRow: rr, Children: kids[rr.ID]}, hideRequests)
		} else {
			x, err = s.collectExport(ctx, rr.ID, hideRequests)
		}
		if errors.Is(err, obsdb.ErrNotFound) {
			continue // deleted between the reads
		}
		if err != nil {
			holes = append(holes, readFailedHole("turn "+rr.ID, err))
			failed = true
			break
		}
		x.spans = spansFor(r, x.spans)
		b, err := x.jsonl()
		if err != nil {
			holes = append(holes, readFailedHole("turn "+rr.ID, err))
			failed = true
			break
		}
		if !st.fits(len(b)) {
			holes = append(holes, byteCapHole("turns", len(turns)-i))
			if visible > 0 {
				holes = append(holes, byteCapHole("marks", visible))
			}
			failed = true // past the cap: no mark line either
			break
		}
		st.write(b)
		exported++
	}
	marks := 0
	switch {
	case hasMarks && !failed:
		read, stopped := 0, false
	pages:
		for read < timelineMarkCap && read < visible {
			page, err := er.SessionEvents(ctx, obsdb.SessionEventQuery{SessionID: id, PublicID: mb.pub, Offset: read,
				Limit: min(obsdb.MaxSessionEventLimit, timelineMarkCap-read)})
			if err != nil {
				holes = append(holes, readFailedHole("session events", err))
				stopped = true
				break
			}
			for j, e := range page.Events {
				m, ok := mb.mark(e)
				if !ok {
					continue
				}
				// Wrapped: a mark has a "record" field of its own (thread |
				// compaction), the line's kind beside it.
				b, err := sessionLine("session_mark", struct {
					Mark sessionEntry `json:"mark"`
				}{m})
				if err != nil {
					holes = append(holes, readFailedHole("session mark", err))
					stopped = true
					break pages
				}
				if !st.fits(len(b)) {
					holes = append(holes, byteCapHole("marks", visible-read-j))
					stopped = true
					break pages
				}
				st.write(b)
				marks++
			}
			read += len(page.Events)
			if len(page.Events) == 0 {
				break
			}
		}
		truncatedAt := 0
		if visible > read && !stopped {
			truncatedAt = read
		}
		holes = append(holes, mb.holes(det.Runs, all, truncatedAt, false)...)
	case hasMarks:
		// The turns stopped (a failed read, the byte cap): no mark line,
		// but the timeline's holes still say what the file could not
		// hold whatever it carries — a panel token's hidden marks above
		// all, counted by the scope read before the first byte.
		holes = append(holes, mb.holes(det.Runs, all, 0, false)...)
	case !hasMarks:
		holes = append(holes, stepHole{Hole: string(obsdb.HoleNotRecorded),
			Reason: "the session's state log is not in this export: " + offTimeline,
			Fix:    "serve Studio over obsdb/sqlite or obsdb/clickhouse"})
	}
	for _, h := range holes {
		if b, err := sessionLine("session_hole", h); err == nil {
			st.write(b)
		}
	}
	end, err := sessionLine("session_end", struct {
		TurnsExported int `json:"turns_exported"`
		MarksExported int `json:"marks_exported"`
		Bytes         int `json:"bytes"`
	}{exported, marks, st.bytes})
	if err == nil {
		st.write(end)
	}
	if st.err != nil {
		slog.DebugContext(ctx, "studio: a session export's client went away", "session", id, "err", st.err)
	}
}

// exportTurnChildren reads the turns' children in one
// obsdb.TreeReader.ChildRuns read and primes c with their subagent
// trees (one read per level for every turn at once), so each turn's
// export reads no row and no cost tree of its own. ok false — no
// TreeReader, or the read failed or passed its cap — leaves each turn
// to collectExport's own Run read.
func exportTurnChildren(ctx context.Context, db obsdb.DB, c *coster, turns []obsdb.RunRow) (map[string][]obsdb.RunRow, bool) {
	tr, ok := db.(obsdb.TreeReader)
	if !ok || len(turns) == 0 {
		return nil, false
	}
	ids := make([]string, len(turns))
	for i, t := range turns {
		ids[i] = t.ID
	}
	kids, err := tr.ChildRuns(ctx, ids)
	if err != nil {
		slog.DebugContext(ctx, "studio: a session export's turn children could not be read at once; reading each turn", "err", err)
		return nil, false
	}
	var all []obsdb.RunRow
	for _, id := range ids {
		c.prime(id, kids[id])
		all = append(all, subagents(id, c.kids[id])...)
	}
	c.primeTrees(all)
	return kids, true
}

// contentDisposition is writeDownload's attachment header for id.
func contentDisposition(id string, f exportFormat) string {
	name := id + "." + f.ext
	ascii := strings.Map(func(c rune) rune {
		if c == '/' || c == '\\' || c == '"' || c < 0x20 || c >= 0x7f {
			return '_'
		}
		return c
	}, name)
	cd := `attachment; filename="` + ascii + `"`
	for _, c := range name {
		if c >= 0x80 {
			cd += "; filename*=UTF-8''" + rfc5987(strings.ReplaceAll(name, "/", "_"))
			break
		}
	}
	return cd
}
