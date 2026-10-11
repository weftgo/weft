package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/studio"
)

// connectedIDs lists the ids of srv's runtimes holding a live stream.
func connectedIDs(srv *studio.Server) []string {
	var ids []string
	for _, v := range srv.Runtime().Snapshot() {
		if v.Connected {
			ids = append(ids, v.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// waitConnected waits until srv lists exactly n connected runtimes.
func waitConnected(t *testing.T, srv *studio.Server, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ids := connectedIDs(srv)
		if len(ids) == n {
			return ids
		}
		if time.Now().After(deadline) {
			t.Fatalf("connected runtimes = %v, want %d", ids, n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestStableRuntimeID: a runtime's id is stable per (service, cwd,
// agent set) — the same app restarted in the same place registers under
// the same id, so a Studio link to /runtimes/<id> survives the restart
// — and a second live process (here a second link) on that id is told
// `<id>-<pid>` by Studio and adopts it.
func TestStableRuntimeID(t *testing.T) {
	srv := studio.New(studio.Open(filepath.Join(t.TempDir(), "studio.db")), studio.Playground(true))
	t.Cleanup(func() { _ = srv.Close() })
	agents := func() []*core.Agent {
		return []*core.Agent{
			core.New(wefttest.Script(), core.Name("orders")),
			core.New(wefttest.Script(), core.Name("acme-support")),
		}
	}
	install := func() func() {
		return Install(Local(srv), Agents(agents()...), Enabled(true))
	}

	wd, _ := os.Getwd()
	want := stableRuntimeID(serviceName(), wd, []string{"acme-support", "orders"})
	if !regexp.MustCompile(`^rt_[0-9a-f]{16}$`).MatchString(want) {
		t.Fatalf("stable id %q is not rt_ + 16 hex", want)
	}

	// Sequential: the app restarts — same id.
	stop := install()
	first := waitConnected(t, srv, 1)
	stop()
	waitConnected(t, srv, 0)
	stop = install()
	second := waitConnected(t, srv, 1)
	if first[0] != want || second[0] != want {
		t.Fatalf("ids across a restart = %s then %s, want %s both times", first[0], second[0], want)
	}
	if v := srv.Runtime().Snapshot(); len(v) != 1 || v[0].Cwd != wd {
		t.Errorf("runtimes after the restart = %+v, want one entry with cwd %s", v, wd)
	}

	// Concurrent: a second live link with the same inputs gets -<pid>.
	stop2 := install()
	t.Cleanup(stop2)
	both := waitConnected(t, srv, 2)
	suffixed := fmt.Sprintf("%s-%d", want, os.Getpid())
	if both[0] != want || both[1] != suffixed {
		t.Errorf("two live links = %v, want [%s %s]", both, want, suffixed)
	}
	stop()

	// The inputs: a different cwd, service or agent set is another id;
	// the agents' order is not.
	base := stableRuntimeID("acme-api", "/srv/a", []string{"x", "y"})
	for name, other := range map[string]string{
		"cwd":     stableRuntimeID("acme-api", "/srv/b", []string{"x", "y"}),
		"service": stableRuntimeID("acme-web", "/srv/a", []string{"x", "y"}),
		"agents":  stableRuntimeID("acme-api", "/srv/a", []string{"x"}),
	} {
		if other == base {
			t.Errorf("a different %s gives the same id %s", name, base)
		}
	}
	if stableRuntimeID("acme-api", "/srv/a", []string{"y", "x"}) != base {
		t.Error("the agents' order changed the id")
	}
}
