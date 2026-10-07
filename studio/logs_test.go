package studio

import (
	"net/http"
	"strings"
	"testing"
)

// TestLogsRoute pins GET /api/runs/{id}/logs (plan A7) over the
// fixture database: r_ok's two app log lines in time order, each with
// its index, severity name and number, body, attributes and span; the
// page, the severity filter that keeps indexes, the bad parameters, a
// run with spans and no logs (empty, no badge), a run with no span
// (the not_recorded badge with this route's reason and the tracer
// fix) and an unknown run (404).
func TestLogsRoute(t *testing.T) {
	h := Handler(DB(fixtureDB(t)))

	code, _, body := get(t, h, "/studio/api/runs/r_ok/logs")
	if code != http.StatusOK {
		t.Fatalf("logs: %d %s", code, body)
	}
	golden(t, "logs-ok.golden.json", body)

	code, _, body = get(t, h, "/studio/api/runs/r_ok/logs?limit=1")
	if code != http.StatusOK {
		t.Fatalf("logs?limit=1: %d %s", code, body)
	}
	golden(t, "logs-ok-paged.golden.json", body)
	code, _, body = get(t, h, "/studio/api/runs/r_ok/logs?from=1&limit=1")
	if code != http.StatusOK || !strings.Contains(body, `"index":1`) || strings.Contains(body, `"index":0`) || !strings.Contains(body, `"next_from":2`) {
		t.Errorf("logs?from=1&limit=1 = %d %s, want index 1 and next_from 2 (a full page)", code, body)
	}
	code, _, body = get(t, h, "/studio/api/runs/r_ok/logs?from=2")
	if code != http.StatusOK || strings.TrimSpace(body) != `{"logs":[]}` {
		t.Errorf("logs?from=2 = %d %q, want an empty last page", code, body)
	}

	for _, sev := range []string{"warn", "WARN", "warning", "13"} {
		code, _, body = get(t, h, "/studio/api/runs/r_ok/logs?severity="+sev)
		if code != http.StatusOK || !strings.Contains(body, `"index":1`) || strings.Contains(body, `"index":0`) ||
			!strings.Contains(body, `"severity":"WARN"`) {
			t.Errorf("logs?severity=%s = %d %s, want the warning alone at index 1", sev, code, body)
		}
	}
	code, _, body = get(t, h, "/studio/api/runs/r_ok/logs?severity=error")
	if code != http.StatusOK || !strings.HasPrefix(body, `{"logs":[]`) {
		t.Errorf("logs?severity=error = %d %s, want none", code, body)
	}
	for _, q := range []string{"severity=loud", "severity=25", "from=-1", "from=x", "limit=-2"} {
		if code, _, body := get(t, h, "/studio/api/runs/r_ok/logs?"+q); code != http.StatusBadRequest {
			t.Errorf("logs?%s = %d %s, want 400", q, code, body)
		}
	}

	// r_fail has a span and logged nothing: empty, no badge.
	code, _, body = get(t, h, "/studio/api/runs/r_fail/logs")
	if code != http.StatusOK || strings.Contains(body, "badge") || !strings.HasPrefix(body, `{"logs":[]`) {
		t.Errorf("r_fail logs = %d %s, want an empty list without a badge", code, body)
	}
	// r_stale was recorded without a tracer: nothing to attribute
	// through.
	code, _, body = get(t, h, "/studio/api/runs/r_stale/logs")
	if code != http.StatusOK {
		t.Fatalf("r_stale logs: %d %s", code, body)
	}
	golden(t, "logs-not-recorded.golden.json", body)
	// The child id's slashes route like every run sub-route.
	if code, _, body := get(t, h, "/studio/api/runs/r_sub/0/call_3/logs"); code != http.StatusOK || !strings.HasPrefix(body, `{"logs":[]`) {
		t.Errorf("child logs = %d %s", code, body)
	}
	if code, _, body := get(t, h, "/studio/api/runs/r_nope/logs"); code != http.StatusNotFound {
		t.Errorf("unknown run logs = %d %s, want 404", code, body)
	}
}

// TestRunRowDeltaCount: the run row carries delta_count, the deltas
// obsdb counted and never stored.
func TestRunRowDeltaCount(t *testing.T) {
	h := Handler(DB(fixtureDB(t)))
	for _, path := range []string{"/studio/api/runs/r_ok", "/studio/api/runs"} {
		code, _, body := get(t, h, path)
		if code != http.StatusOK || !strings.Contains(body, `"delta_count":0`) {
			t.Errorf("%s = %d %s, want delta_count on the row", path, code, body)
		}
	}
}
