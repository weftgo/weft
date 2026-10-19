package studio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/weftgo/weft/obsdb"
)

// The conversation export (UX-B-29):
//
//	GET /api/sessions/{id}/export?format=jsonl
//
// A session as one JSON-lines download, read from obsdb's tables and
// nothing else (the run export's rule: data, never a write). The lines,
// in order:
//
//   - one "session" record: the format (weft.session.export/1), the
//     session's id, public id, agent and turn count, and how many of
//     its turns the file carries;
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
//     response_cap) past either cap, the timeline's not_recorded /
//     stripped / hidden, and not_recorded naming obsdb.SessionEventReader
//     when the database cannot read the state log.
//
// jsonl is the only format: a session is many runs, and the per-run
// json, otlp and wefttest exports stay the run page's. The scope is the
// session document's: a panel token reads its public id's turns and
// marks only; a read-scoped token's turns carry the request block
// badged hidden, as the run export's jsonl does.

// sessionExportVersion names the session export's line shape.
const sessionExportVersion = "weft.session.export/1"

// sessionExportTurnCap bounds the turns one session export reads.
var sessionExportTurnCap = 200

var sessionExportFormat = exportFormat{ext: "session.jsonl", contentType: "application/x-ndjson"}

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
	hideRequests := !readsPrompts(r)
	var turns bytes.Buffer
	exported := 0
	var holes []stepHole
	for i, rr := range det.Runs {
		if i >= sessionExportTurnCap {
			holes = append(holes, stepHole{Hole: string(obsdb.HoleTruncated), Cause: string(obsdb.CauseResponseCap),
				Reason: fmt.Sprintf("the session has %d turns; this export carries the first %d — nothing was lost", len(det.Runs), sessionExportTurnCap),
				Fix:    "export the later turns one by one: GET /api/runs/{id}/export?format=jsonl",
				Count:  len(det.Runs) - sessionExportTurnCap})
			break
		}
		x, err := s.collectExport(ctx, rr.ID, hideRequests)
		if errors.Is(err, obsdb.ErrNotFound) {
			continue // deleted between the reads
		}
		if err != nil {
			dbError(w, r, "turn", rr.ID, err)
			return
		}
		x.spans = spansFor(r, x.spans)
		b, err := x.jsonl()
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "internal", "export turn "+rr.ID+": "+err.Error())
			return
		}
		turns.Write(b)
		exported++
	}
	var marks []sessionEntry
	if er, ok := s.db.(obsdb.SessionEventReader); ok {
		mb := s.newMarkBuilder(r, det)
		visible, all, err := mb.scope(r, er)
		if err != nil {
			dbError(w, r, "session events", id, err)
			return
		}
		read := 0
		for read < timelineMarkCap && read < visible {
			page, err := er.SessionEvents(ctx, obsdb.SessionEventQuery{SessionID: id, PublicID: mb.pub, Offset: read,
				Limit: min(obsdb.MaxSessionEventLimit, timelineMarkCap-read)})
			if err != nil {
				dbError(w, r, "session events", id, err)
				return
			}
			for _, e := range page.Events {
				if m, ok := mb.mark(e); ok {
					marks = append(marks, m)
				}
			}
			read += len(page.Events)
			if len(page.Events) == 0 {
				break
			}
		}
		truncatedAt := 0
		if visible > read {
			truncatedAt = read
		}
		holes = append(holes, mb.holes(det.Runs, all, truncatedAt, false)...)
	} else {
		holes = append(holes, stepHole{Hole: string(obsdb.HoleNotRecorded),
			Reason: "the session's state log is not in this export: " + offTimeline,
			Fix:    "serve Studio over obsdb/sqlite or obsdb/clickhouse"})
	}

	var out bytes.Buffer
	line := func(kind string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		out.WriteString(`{"record":` + strconv.Quote(kind))
		if len(b) > 2 {
			out.WriteByte(',')
			out.Write(b[1:])
		} else {
			out.WriteByte('}')
		}
		out.WriteByte('\n')
		return nil
	}
	err = line("session", struct {
		Format        string `json:"format"`
		SessionID     string `json:"session_id"`
		PublicID      string `json:"public_id,omitempty"`
		Agent         string `json:"agent"`
		Turns         int    `json:"turns"`
		TurnsExported int    `json:"turns_exported"`
		Marks         int    `json:"marks"`
	}{sessionExportVersion, det.ID, det.PublicID, det.Agent, len(det.Runs), exported, len(marks)})
	out.Write(turns.Bytes())
	for _, m := range marks {
		if err == nil {
			// Wrapped: a mark has a "record" field of its own (thread |
			// compaction), the line's kind beside it.
			err = line("session_mark", struct {
				Mark sessionEntry `json:"mark"`
			}{m})
		}
	}
	for _, h := range holes {
		if err == nil {
			err = line("session_hole", h)
		}
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "export session "+id+": "+err.Error())
		return
	}
	writeDownload(w, r, id, sessionExportFormat, out.Bytes())
}
