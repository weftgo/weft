//go:build unix

// The crash test kills a process with SIGKILL — a unix signal — so it
// runs on unix only. The durability rules it pins (a committed
// transaction survives its writer's death; the lock row dies with the
// process that took it) are SQLite's and this backend's everywhere; the
// signal is the part unix owns.

package sqlite_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/sqlite"
)

// The crash test (plan §7, shaped like jsonl's): a helper process is
// SIGKILLed mid-turn — after committing some appends, before the rest —
// and the session must load with exactly the committed entries, every
// one a whole transaction, none torn, none half: sqlite's atomic commit
// is the all-or-nothing Append this backend promises. The helper is
// this same test binary re-executed (-test.run=TestCrashHelper).
func TestCrashMidAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crash.db")
	ctx := context.Background()

	cmd := exec.Command(os.Args[0], "-test.run=TestCrashHelper", "-test.count=1")
	cmd.Env = append(os.Environ(), "WEFT_THREAD_SQLITE_CRASH_DB="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Kill the helper once it has committed at least three appends.
	// More may land before the kill lands — that is fine; the assertion
	// is the prefix property: some k of the 8, all whole.
	killed := make(chan struct{})
	go func() {
		defer close(killed)
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if sc.Text() == "e3" {
				_ = cmd.Process.Kill() // SIGKILL, no cleanup, no flush
				return
			}
		}
	}()
	<-killed
	_, _ = cmd.Process.Wait()

	fresh, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("a crashed database must open: %v", err)
	}
	h, entries, report, err := fresh.Load(ctx, "s_crash")
	if err != nil {
		t.Fatalf("a crashed session must load: %v", err)
	}
	if report != nil {
		t.Errorf("a committed-only load reported repairs: %+v", report)
	}
	if h.ID != "s_crash" {
		t.Fatalf("header = %+v", h)
	}
	if len(entries) < 3 || len(entries) > 8 {
		t.Fatalf("%d entries after the crash, want a prefix of the 8 between 3 and 8", len(entries))
	}
	for i, e := range entries {
		want := fmt.Sprintf("entry %d", i)
		if got := e.(thread.MessageEntry).Message.Text(); got != want {
			t.Fatalf("entry %d of the prefix: %q, want %q — committed order is arrival order", i, got, want)
		}
	}
	// The lock row died with the helper: the fresh handle takes over
	// and keeps appending from the committed seq, no gap, no duplicate.
	if err := fresh.Append(ctx, "s_crash", thread.MessageEntry{
		ID:      "e_after",
		Created: time.Now().UTC(),
		Message: weft.User("after the crash"),
	}); err != nil {
		t.Fatalf("append after takeover: %v", err)
	}
	_, reloaded, _, err := fresh.Load(ctx, "s_crash")
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded) != len(entries)+1 {
		t.Fatalf("%d entries after the takeover append, want %d — the seq must continue, not restart",
			len(reloaded), len(entries)+1)
	}
}

// The crash matrix's second point: a writer killed while holding the
// lock row. The row survives (it is data), but its holder is gone, so
// a live writer is refused (ErrLocked) until the death is observed —
// then the takeover fires and the session continues. Flock's
// death-release, rebuilt on the lock table.
func TestCrashLockDiesWithProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hold.db")
	marker := filepath.Join(dir, "held")
	ctx := context.Background()

	cmd := exec.Command(os.Args[0], "-test.run=TestCrashHelper", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"WEFT_THREAD_SQLITE_CRASH_DB="+path,
		"WEFT_THREAD_SQLITE_CRASH_HOLD="+marker)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("the helper never took the lock")
		}
		time.Sleep(10 * time.Millisecond)
	}

	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	err = st.Append(ctx, "s_hold", thread.MessageEntry{ID: "e_live", Created: time.Now().UTC()})
	if !errors.Is(err, thread.ErrLocked) {
		t.Fatalf("Append while the holder lives: err = %v, want ErrLocked", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()

	if err := st.Append(ctx, "s_hold", thread.MessageEntry{
		ID:      "e_take",
		Created: time.Now().UTC(),
		Message: weft.User("the takeover"),
	}); err != nil {
		t.Fatalf("Append after the holder died: %v — a crashed writer must not strand its session", err)
	}
	_, entries, _, err := st.Load(ctx, "s_hold")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("%d entries after the takeover, want 2 (the helper's and ours)", len(entries))
	}
}

// TestCrashHelper is the re-executed half of both crash tests. With
// WEFT_THREAD_SQLITE_CRASH_DB set it creates the session and appends
// eight entries, one Append each — a whole transaction per line —
// printing its progress to stdout (fmt, not t.Log: a killed process
// flushes nothing, and the parent reads the pipe live). With
// WEFT_THREAD_SQLITE_CRASH_HOLD also set it stops after the first
// append and holds the lock until killed. When the environment is
// unset it is an ordinary, empty, passing test.
func TestCrashHelper(t *testing.T) {
	path := os.Getenv("WEFT_THREAD_SQLITE_CRASH_DB")
	if path == "" {
		return // the parent's own run, or a plain `go test`
	}
	st, err := sqlite.Open(path)
	if err != nil {
		fmt.Println("helper: open failed:", err)
		os.Exit(2)
	}
	ctx := context.Background()
	if hold := os.Getenv("WEFT_THREAD_SQLITE_CRASH_HOLD"); hold != "" {
		if err := st.Create(ctx, thread.Header{ID: "s_hold", Created: time.Now().UTC()}); err != nil {
			fmt.Println("helper: create failed:", err)
			os.Exit(2)
		}
		if err := st.Append(ctx, "s_hold", thread.MessageEntry{
			ID: "e_held", Created: time.Now().UTC(), Message: weft.User("held"),
		}); err != nil {
			fmt.Println("helper: append failed:", err)
			os.Exit(2)
		}
		if err := os.WriteFile(hold, []byte("held"), 0o600); err != nil {
			fmt.Println("helper: marker failed:", err)
			os.Exit(2)
		}
		time.Sleep(time.Hour) // killed by the parent
		return
	}
	if err := st.Create(ctx, thread.Header{ID: "s_crash", Created: time.Now().UTC()}); err != nil {
		fmt.Println("helper: create failed:", err)
		os.Exit(2)
	}
	for i := 0; i < 8; i++ {
		fmt.Printf("e%d\n", i) // the parent kills on e3
		if err := st.Append(ctx, "s_crash", thread.MessageEntry{
			ID:      fmt.Sprintf("e_c%d", i),
			Created: time.Now().UTC(),
			Message: weft.User(fmt.Sprintf("entry %d", i)),
		}); err != nil {
			fmt.Println("helper: append failed:", err)
			os.Exit(2)
		}
	}
	fmt.Println("done") // the parent should have killed us before here
	time.Sleep(time.Hour)
}

// A second process writing a different session while the first holds
// its own: WAL serializes them, neither is ErrLocked — the lock is per
// session, never per database (the fleet shape: many agents, one file).
func TestTwoProcessesDistinctSessions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fleet.db")
	marker := filepath.Join(dir, "parent-done")
	ctx := context.Background()

	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, thread.Header{ID: "s_parent", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, "s_parent", thread.MessageEntry{
		ID: "e_p", Created: time.Now().UTC(), Message: weft.User("parent"),
	}); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestFleetHelper", "-test.count=1")
	cmd.Env = append(os.Environ(), "WEFT_THREAD_SQLITE_FLEET_DB="+path, "WEFT_THREAD_SQLITE_FLEET_DONE="+marker)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the second process failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "fleet ok") {
		t.Fatalf("the second process never finished:\n%s", out)
	}
	if p, err := st.List(ctx, thread.Query{}); err != nil || p.Total != 2 {
		t.Errorf("List over two processes: total %d, err %v — both sessions must survive", p.Total, err)
	}
	_, child, _, err := st.Load(ctx, "s_child")
	if err != nil || len(child) != 1 {
		t.Errorf("the child session: %d entries, err %v", len(child), err)
	}
}

// TestFleetHelper is TestTwoProcessesDistinctSessions' other process.
func TestFleetHelper(t *testing.T) {
	path := os.Getenv("WEFT_THREAD_SQLITE_FLEET_DB")
	if path == "" {
		return
	}
	st, err := sqlite.Open(path)
	if err != nil {
		fmt.Println("helper: open failed:", err)
		os.Exit(2)
	}
	ctx := context.Background()
	if err := st.Create(ctx, thread.Header{ID: "s_child", Created: time.Now().UTC()}); err != nil {
		fmt.Println("helper: create failed:", err)
		os.Exit(2)
	}
	if err := st.Append(ctx, "s_child", thread.MessageEntry{
		ID: "e_c", Created: time.Now().UTC(), Message: weft.User("child"),
	}); err != nil {
		fmt.Println("helper: append failed:", err)
		os.Exit(2)
	}
	fmt.Println("fleet ok")
}
