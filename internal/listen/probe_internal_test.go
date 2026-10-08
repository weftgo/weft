package listen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestTokenGoesToLoopbackOnly: the probe's bearer is attached when the
// dialled host is loopback (an unspecified host is dialled on
// 127.0.0.1) and never for a non-loopback address — a fixed token does
// not leave the machine for whatever holds that port.
func TestTokenGoesToLoopbackOnly(t *testing.T) {
	for _, c := range []struct {
		addr   string
		bearer bool
	}{
		{"127.0.0.1:7331", true},
		{"127.5.6.7:7331", true},
		{"[::1]:7331", true},
		{"localhost:7331", true},
		{"LOCALHOST:7331", true},
		{":7331", true},
		{"0.0.0.0:7331", true},
		{"[::]:7331", true},
		{"192.168.1.5:7331", false},
		{"10.0.0.5:7331", false},
		{"[2001:db8::1]:7331", false},
		{"studio.example:7331", false},
		{"localhost.evil.example:7331", false},
	} {
		req, err := probeRequest(context.Background(), c.addr, "fixed-token")
		if err != nil {
			t.Fatalf("%s: %v", c.addr, err)
		}
		got := req.Header.Get("Authorization")
		if want := c.bearer; (got != "") != want || (want && got != "Bearer fixed-token") {
			t.Errorf("probe of %s: Authorization %q, want bearer = %v", c.addr, got, want)
		}
	}
	// No token, no header — loopback or not.
	req, _ := probeRequest(context.Background(), "127.0.0.1:7331", "")
	if h := req.Header.Get("Authorization"); h != "" {
		t.Errorf("tokenless probe sent Authorization %q", h)
	}
}

// TestSameFileReasons: equal paths match; a missing file is a
// different file, not an error; a stat that fails otherwise is "could
// not compare", never "another database".
func TestSameFileReasons(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.db"), filepath.Join(dir, "b.db")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if same, err := sameFile(a, a+"/."); !same || err != nil {
		t.Errorf("cleaned-equal paths: %v, %v", same, err)
	}
	if same, err := sameFile(a, b); same || err != nil {
		t.Errorf("two files: %v, %v; want different, no error", same, err)
	}
	if same, err := sameFile(a, filepath.Join(dir, "not-yet.db")); same || err != nil {
		t.Errorf("a file not created yet: %v, %v; want different, no error", same, err)
	}
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs a directory the user cannot search")
	}
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	hidden := filepath.Join(locked, "weft.db")
	if err := os.WriteFile(hidden, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	same, err := sameFile(hidden, a)
	if same || err == nil {
		t.Fatalf("unstat-able running path: %v, %v; want an error", same, err)
	}
	// The verdict probe returns for it, from a Studio's meta naming it.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"studio_version": "v0", "pid": 42, "db": map[string]string{"kind": "sqlite", "path": hidden}})
	}))
	defer ts.Close()
	v := probe(context.Background(), strings.TrimPrefix(ts.URL, "http://"), Request{DBPath: a, Timeout: time.Second})
	if want := "a Studio on " + hidden + " (could not compare: "; v.reuse || !strings.HasPrefix(v.why, want) || strings.Contains(v.why, "another database") {
		t.Errorf("verdict %+v, want %q…", v, want)
	}
}
