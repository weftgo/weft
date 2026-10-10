package studio

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// Deletion and retention (plan A7, §13.13): DELETE /api/runs/{id},
// DELETE /api/sessions/{id} and Retain's sweeper. All three touch
// obsdb's tables only — a SQLite file that also holds thread's sessions
// keeps them byte-identical: the app's conversation store is the app's.

// deleteGroup is the capability "delete": both verbs and nothing else.
func deleteGroup() routeGroup {
	return routeGroup{
		name:       "delete",
		capability: "delete",
		register: func(mux *http.ServeMux, s *Server) {
			mux.HandleFunc("DELETE /api/runs/", s.serveDeleteRun)
			mux.HandleFunc("DELETE /api/sessions/", s.serveDeleteSession)
		},
	}
}

// Notes the two verbs answer with: what was deleted, said plainly.
const (
	deleteRunNote = "the run's observability rows (spans, records, app logs, search index) and its subagent children's " +
		"are deleted from studio's database"
	deleteSessionNote = "the session's observability rows (every run's spans, records, app logs, search index, " +
		"children included) are deleted from studio's database; the app's conversation store is untouched"
)

// deleteDoc is both verbs' 200 body.
type deleteDoc struct {
	Deleted   string `json:"deleted"` // "run" | "session"
	RunID     string `json:"run_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Note      string `json:"note"`
}

// mayDelete is the delete capability's rule — db.path's, mayReadDBPath:
// the server token, or no Token configured and a loopback Host (setup
// A, without the AllowOrigins widening); never a panel token, whatever
// its scope. Only the refusal's wording is its own.
func (s *Server) mayDelete(w http.ResponseWriter, r *http.Request) bool {
	if mayReadDBPath(s, r) {
		return true
	}
	msg := "deletion answers a loopback Host only when no studio.Token is configured"
	if idFrom(r).panel != nil {
		msg = "deletion answers the dev token (or loopback in setup A) only, never a panel token"
	}
	writeError(w, r, http.StatusForbidden, "forbidden", msg)
	return false
}

// serveDeleteRun answers DELETE /api/runs/{id}: the run and its
// subagent children (run ids carry slashes: everything after
// /api/runs/ is the id). A later read is 404. A live stream following
// the deleted run emits nothing more unless the app keeps writing to
// it; a later write starts a new run row under the same id.
func (s *Server) serveDeleteRun(w http.ResponseWriter, r *http.Request) {
	if !s.mayDelete(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	if id == "" {
		notFound(w, r, "no such api route "+r.URL.Path)
		return
	}
	if err := s.db.DeleteRun(r.Context(), id); err != nil {
		deleteError(w, r, "run", id, err)
		return
	}
	writeJSON(w, r, http.StatusOK, deleteDoc{Deleted: "run", RunID: id, Note: deleteRunNote})
}

// serveDeleteSession answers DELETE /api/sessions/{id}: the session's
// observability rows — never the app's conversation store.
func (s *Server) serveDeleteSession(w http.ResponseWriter, r *http.Request) {
	if !s.mayDelete(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if id == "" || strings.Contains(id, "/") {
		notFound(w, r, "no such api route "+r.URL.Path)
		return
	}
	if err := s.db.DeleteSession(r.Context(), id); err != nil {
		deleteError(w, r, "session", id, err)
		return
	}
	writeJSON(w, r, http.StatusOK, deleteDoc{Deleted: "session", SessionID: id, Note: deleteSessionNote})
}

func deleteError(w http.ResponseWriter, r *http.Request, noun, id string, err error) {
	if errors.Is(err, obsdb.ErrNotFound) {
		notFound(w, r, "no "+noun+" "+id)
		return
	}
	writeError(w, r, http.StatusInternalServerError, "internal", "delete "+noun+" "+id+": "+err.Error())
}

// Retain keeps runs for d after they were last seen: a sweeper deletes
// (obsdb.DB.Sweep) every run that is not running — succeeded, failed
// or interrupted (a run silent past obsdb.InterruptedAfter counts as
// interrupted, hence not running, and is swept) — whose last_seen is
// older than d, with its children, from obsdb's tables only. It sweeps
// once when New returns, then every min(d/10, 1h), never more often
// than once a minute; each sweep logs one Debug line (slog's default
// logger) with its count, and a failed sweep one Warn line — it never
// stops the server. Once the DB is closed (obsdb.ErrClosed) the sweeper
// stops on its own with one Debug line. 0 (the default) is off: the
// local sink keeps everything until told otherwise. api/meta reports it
// as retention.
//
// d must be at least an hour: New panics on 0 < d < 1h. The sweeper's
// not-running rule counts a run silent for 30 s as interrupted, so a
// short retention would sweep a live run paused at a breakpoint.
//
// The sweeper runs until Close: a Studio with Retain is built with New
// and closed, never through Handler(...), whose server nothing can
// close (its sweeper ends only when the DB does). The weft binary reads
// it from --retain / WEFT_RETAIN.
func Retain(d time.Duration) Option {
	return func(c *config) {
		if d < 0 {
			d = 0
		}
		c.retain = d
	}
}

// minRetention is Retain's floor; retainFloorMsg the refusal's words.
// The weft binary's --retain check says the same (cmd/weft's
// resolveRetain; its test pins the two equal).
const (
	minRetention   = time.Hour
	retainFloorMsg = "retention must be at least 1h: the sweeper's not-running rule counts a run silent for 30 s " +
		"as interrupted, so a short retention would sweep a live run paused at a breakpoint"
)

// sweepInterval is Retain's timer: min(d/10, 1h), floored at a minute.
func sweepInterval(d time.Duration) time.Duration {
	iv := min(d/10, time.Hour)
	return max(iv, time.Minute)
}

// startSweeper runs Retain's sweeper until stopSweep is called.
func (s *Server) startSweeper() {
	if s.retain <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.sweepMu.Lock()
	s.stopSweep = func() { cancel(); <-done }
	s.sweepMu.Unlock()
	iv := s.sweepEvery
	if iv <= 0 {
		iv = sweepInterval(s.retain)
	}
	go func() {
		defer close(done)
		t := time.NewTicker(iv)
		defer t.Stop()
		for {
			if !s.sweepOnce(ctx) {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// sweepOnce is one sweep: errors go to the logger, never further. It
// reports false when the sweeper should stop: the DB was closed under
// it (the pipeline owns it and shut down), which no later tick fixes.
func (s *Server) sweepOnce(ctx context.Context) bool {
	cutoff := s.now().Add(-s.retain)
	n, err := s.db.Sweep(ctx, cutoff)
	if err != nil {
		if errors.Is(err, obsdb.ErrClosed) {
			slog.Debug("studio: sweeper stopped: database closed", "retention", s.retain.String())
			return false
		}
		if ctx.Err() == nil {
			slog.Warn("studio: retention sweep failed", "retention", s.retain.String(), "err", err)
		}
		return true
	}
	slog.Debug("studio: retention sweep", "deleted_runs", n, "retention", s.retain.String(), "cutoff", cutoff)
	return true
}
