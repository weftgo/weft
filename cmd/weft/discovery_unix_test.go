//go:build !windows

package main

import (
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/weftgo/weft/internal/discovery"
)

// TestStudioStopsOnSIGHUP (review finding 5): `weft studio` stops
// gracefully on SIGHUP (the terminal closing) like SIGINT and SIGTERM,
// so its discovery file is removed.
func TestStudioStopsOnSIGHUP(t *testing.T) {
	skipWithoutSelfSignal(t)
	dir := discoveryDir(t)
	base := freeBase(t, 1)
	var out syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- serveWith("sqlite://"+filepath.Join(t.TempDir(), "weft.db"), want{addr: loop(base), span: 1}, "tok", &out, afterBoot{})
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if resp, err := http.Get("http://" + loop(base) + "/api/meta"); err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("studio never served: %s", out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, discovery.FileName)); err != nil {
		t.Fatalf("no discovery file while serving: %v", err)
	}
	signalSelf(t, syscall.SIGHUP)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve after SIGHUP: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve never returned after SIGHUP")
	}
	if _, err := os.Stat(filepath.Join(dir, discovery.FileName)); !os.IsNotExist(err) {
		t.Errorf("the discovery file outlived a SIGHUP: %v", err)
	}
	notListening(t, loop(base))
}
