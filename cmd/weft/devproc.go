package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// child is one run of the app, in its own process group (unix) so a
// stop reaches `go run`'s own child — the app binary — too.
type child struct {
	cmd  *exec.Cmd
	pid  int
	done chan struct{} // closed when the process has exited and been reaped
	err  error         // cmd.Wait's, read after done
}

// startChild starts argv with env, its output passed through. Its
// stdin is not the terminal: in its own process group a read from the
// terminal would stop it (SIGTTIN).
func startChild(argv, env []string, stdout, stderr io.Writer) (*child, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// A grandchild that outlives the app holding the output pipe must
	// not hold Wait (a pipe exists when the writer is not a file).
	cmd.WaitDelay = 2 * time.Second
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &child{cmd: cmd, pid: cmd.Process.Pid, done: make(chan struct{})}
	go func() {
		c.err = cmd.Wait()
		close(c.done)
	}()
	return c, nil
}

// stop sends sig to the app's group, waits up to devStopGrace for the
// group to be gone, then kills what is left. It returns once the app
// has been reaped.
func (c *child) stop(sig os.Signal) {
	deadline := time.Now().Add(devStopGrace)
	_ = c.signal(sig)
	select {
	case <-c.done:
	case <-time.After(time.Until(deadline)):
		c.kill()
		<-c.done
	}
	c.reapUntil(deadline)
}

// reap cleans up after an app that exited on its own: whatever is left
// of its group is killed.
func (c *child) reap() { c.reapUntil(time.Now()) }

// reapUntil waits until deadline for the group to empty, then kills
// what is left of it.
func (c *child) reapUntil(deadline time.Time) {
	for c.groupAlive() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if c.groupAlive() {
		c.kill()
	}
}

// exitCode is the app's exit code: -1 when a signal ended it.
func (c *child) exitCode() int {
	var ee *exec.ExitError
	switch {
	case c.err == nil:
		return 0
	case errors.As(c.err, &ee):
		return ee.ExitCode()
	default:
		return -1
	}
}

// status says how the app ended.
func (c *child) status() string {
	if code := c.exitCode(); code >= 0 {
		return fmt.Sprintf("exit code %d", code)
	}
	return c.err.Error()
}
