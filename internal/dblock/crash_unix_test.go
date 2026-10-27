//go:build unix

package dblock_test

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/weftgo/weft/internal/dblock"
)

func TestMain(m *testing.M) {
	// The helper: this binary re-executed holds the lock until killed.
	if db := os.Getenv("WEFT_DBLOCK_HOLD"); db != "" {
		l, err := dblock.Acquire(db)
		if err != nil {
			os.Exit(3)
		}
		l.SetHolder(dblock.Holder{URL: "http://127.0.0.1:1", PID: os.Getpid()})
		_, _ = os.Stdout.WriteString("held\n")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestReleasedOnCrash: flock belongs to the open file; the kernel drops
// it when the holder's process dies, however it dies — a SIGKILLed
// Studio leaves no stale lock (the file stays, naming a dead holder,
// and the next Acquire takes it).
func TestReleasedOnCrash(t *testing.T) {
	db := filepath.Join(t.TempDir(), "weft.db")
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "WEFT_DBLOCK_HOLD="+db)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(out).ReadString('\n'); err != nil || line != "held\n" {
		t.Fatalf("helper: %q %v", line, err)
	}
	if _, err := dblock.Acquire(db); !errors.Is(err, dblock.ErrHeld) {
		t.Fatalf("while another process holds it: %v, want ErrHeld", err)
	}
	if h, ok := dblock.ReadHolder(db); !ok || h.PID != cmd.Process.Pid {
		t.Errorf("holder = %+v %v, want the helper's pid", h, ok)
	}
	_ = cmd.Process.Kill() // SIGKILL: no deferred Release runs
	_ = cmd.Wait()
	// Its words outlive it, but name nobody: the pid is dead.
	if h, ok := dblock.ReadHolder(db); ok {
		t.Errorf("a killed holder is still named: %+v", h)
	}
	l, err := dblock.Acquire(db)
	if err != nil {
		t.Fatalf("after the holder was killed: %v, want the lock", err)
	}
	l.Release()
}
