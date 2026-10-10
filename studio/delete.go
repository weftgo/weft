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

// mayDelete is the delete capability's rule: the server token, or no
// Token configured and a loopback Host (setup A, without the
// AllowOrigins widening — db.path's rule, mayReadDBPath). Never a panel
// token, whatever its scope.
func (s *Server) mayDelete(w http.ResponseWriter, r *http.Request) bool {
	id := idFrom(r)
	switch {
	case id.panel != nil:
		writeError(w, r, http.StatusForbidden, "forbidden",
			"deletion answers the dev token (or loopback in setup A) only, never a panel token")
		return false
	case id.server, s.token == "" && isLoopbackName(hostOnly(r.Host)):
		return true
	default:
		writeError(w, r, http.StatusForbidden, "forbidden",
			"deletion answers a loopback Host only when no studio.Token is configured")
		return false
	}
}

// serveDeleteRun answers DELETE /api/runs/{id}: the run and its
// subagent children (run ids carry slashes: everything after
// /api/runs/ is the id). A live stream following the run emits nothing
// new; a later read is 404.
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
// or interrupted — whose last_seen is older than d, with its children,
// from obsdb's tables only. It sweeps once when New returns, then every
// min(d/10, 1h), never more often than once a minute; each sweep logs
// one Debug line (slog's default logger) with its count, and a failed
// sweep one Warn line — it never stops the server. 0 (the default) is
// off: the local sink keeps everything until told otherwise. api/meta
// reports it as retention. The sweeper runs until Close, so a Studio
// with Retain is built with New and closed, never through Handler(...).
// The weft binary reads it from --retain / WEFT_RETAIN.
func Retain(d time.Duration) Option {
	return func(c *config) {
		if d < 0 {
			d = 0
		}
		c.retain = d
	}
}

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
	s.stopSweep = func() { cancel(); <-done }
	iv := s.sweepEvery
	if iv <= 0 {
		iv = sweepInterval(s.retain)
	}
	go func() {
		defer close(done)
		t := time.NewTicker(iv)
		defer t.Stop()
		for {
			s.sweepOnce(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// sweepOnce is one sweep: errors go to the logger, never further.
func (s *Server) sweepOnce(ctx context.Context) {
	cutoff := s.now().Add(-s.retain)
	n, err := s.db.Sweep(ctx, cutoff)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("studio: retention sweep failed", "retention", s.retain.String(), "err", err)
		}
		return
	}
	slog.Debug("studio: retention sweep", "deleted_runs", n, "retention", s.retain.String(), "cutoff", cutoff)
}
