package store

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/weftgo/weft"
)

// memStore is the in-process Store behind Memory(): a map behind a
// mutex. It exists for tests and examples, and as the reference
// behaviour the durable backends are compared against — both run the
// storetest table.
type memStore struct {
	mu   sync.Mutex
	runs map[string]memRow
}

type memRow struct {
	rec    RunRecord // Events always empty here; events live below
	events []weft.Event
}

// Memory returns a ready-to-use in-process store: records in a map
// behind a mutex, gone when the process is. For tests, examples, and
// as the reference behaviour the durable backends are compared
// against.
func Memory() Store { return &memStore{runs: map[string]memRow{}} }

// Save upserts the row and merges events by position: the events
// already held are kept, r.Events beyond them are appended.
func (m *memStore) Save(_ context.Context, r RunRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.runs[r.ID]
	if !ok {
		row = memRow{}
	}
	if held := len(row.events); len(r.Events) > held {
		row.events = append(slices.Clone(row.events), r.Events[held:]...)
	}
	r.Events = nil // the row never carries events; they live in the store
	row.rec = r
	m.runs[r.ID] = row
	return nil
}

// Append adds events in arrival order and bumps the heartbeat.
func (m *memStore) Append(_ context.Context, id string, ev ...weft.Event) error {
	if len(ev) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.runs[id]
	if !ok {
		return fmtNotFound(id)
	}
	row.events = append(row.events, ev...)
	row.rec.Heartbeat = time.Now()
	m.runs[id] = row
	return nil
}

// Get returns the whole record with its events in order.
func (m *memStore) Get(_ context.Context, id string) (RunRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.runs[id]
	if !ok {
		return RunRecord{}, fmtNotFound(id)
	}
	rec := row.rec
	rec.Events = slices.Clone(row.events)
	rec.Status = DeriveStatus(rec.Status, rec.Heartbeat, time.Now())
	return rec, nil
}

// List pages the runs newest first, without events.
func (m *memStore) List(_ context.Context, q Query) (Page, error) {
	m.mu.Lock()
	ids := make([]string, 0, len(m.runs))
	rows := make(map[string]memRow, len(m.runs))
	for id, row := range m.runs {
		ids = append(ids, id)
		rows[id] = row
	}
	m.mu.Unlock()

	now := time.Now()
	matching := make([]RunRecord, 0, len(ids))
	for _, id := range ids {
		rec := rows[id].rec
		rec.Status = DeriveStatus(rec.Status, rec.Heartbeat, now)
		if !q.matches(&rec, rows[id].rec.Status) {
			continue
		}
		matching = append(matching, rec)
	}
	total := len(matching)

	// Newest first; the id tiebreak keeps the order deterministic when
	// two runs share a start time.
	slices.SortFunc(matching, func(a, b RunRecord) int {
		if c := b.Started.Compare(a.Started); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	start := 0
	if !q.Before.IsZero() {
		start = len(matching)
		for i, rec := range matching {
			if rec.Started.Before(q.Before) {
				start = i
				break
			}
		}
	}
	page := matching[start:]
	if n := limitOf(q.Limit); len(page) > n {
		page = page[:n]
	}
	for i := range page {
		page[i].Events = nil
		page[i].Result = nil // the list body lesson (ADR 0010 §0.1): Get returns everything
	}
	return Page{Runs: page, Total: total}, nil
}

// Delete removes the run and its events; children survive with their
// ParentID cleared.
func (m *memStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[id]; !ok {
		return fmtNotFound(id)
	}
	delete(m.runs, id)
	for child, row := range m.runs {
		if row.rec.ParentID == id {
			row.rec.ParentID = ""
			m.runs[child] = row
		}
	}
	return nil
}

// matches decides whether rec (with its derived status) satisfies the
// query. storedStatus is the row's status before derivation, which
// Query.Status=Running needs: a stale running row reads Interrupted,
// so it must not match a Running filter even though the stored value
// is running.
func (q Query) matches(rec *RunRecord, storedStatus Status) bool {
	if q.Agent != "" && rec.Agent != q.Agent {
		return false
	}
	switch q.Status {
	case "":
	case Interrupted:
		if rec.Status != Interrupted {
			return false
		}
	case Running:
		if storedStatus != Running || rec.Status != Running {
			return false
		}
	default:
		if rec.Status != q.Status {
			return false
		}
	}
	switch q.ParentID {
	case "":
		if rec.ParentID != "" {
			return false
		}
	case "*":
	default:
		if rec.ParentID != q.ParentID {
			return false
		}
	}
	for k, v := range q.Tags {
		if rec.Tags[k] != v {
			return false
		}
	}
	return true
}

// fmtNotFound names the run in the ErrNotFound wrap, the shape every
// backend shares.
func fmtNotFound(id string) error {
	return fmt.Errorf("%w: %s", ErrNotFound, id)
}

// cloneTags copies a tags map so a record never aliases the option's.
func cloneTags(kv map[string]string) map[string]string {
	if len(kv) == 0 {
		return nil
	}
	return maps.Clone(kv)
}
