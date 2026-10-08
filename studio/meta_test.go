package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/otel"
)

// metaOf reads api/meta from a test server, as a loopback client.
func metaOf(t *testing.T, ts *httptest.Server, host string) metaDoc {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/meta", nil)
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("meta: %d %s", resp.StatusCode, b)
	}
	var doc metaDoc
	decode(t, string(b), &doc)
	return doc
}

// runThrough records one scripted run of agent through a Studio
// destination built with dest's options, and flushes it.
func runThrough(t *testing.T, ts *httptest.Server, agentOpts []core.Option, dest ...otel.DestOption) string {
	t.Helper()
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, "", dest...), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	opts := append([]core.Option{core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider())}, agentOpts...)
	res, err := core.New(wefttest.Script(wefttest.Say("done")), opts...).Generate(ctx, core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	return res.ID
}

// TestMetaContent pins meta.content (plan B5): Studio's ingest policy
// is "as_received", and latest is the newest run's content mark with
// Studio's note and the fix naming its cause — null before any run.
func TestMetaContent(t *testing.T) {
	for _, c := range []struct {
		name      string
		agent     []core.Option
		dest      []otel.DestOption
		mark, fix string
	}{
		{"content on", nil, nil, "full", ""},
		{"a content-off destination", nil, []otel.DestOption{otel.NoContent()}, "stripped", "otel.NoContent()"},
		{"an agent capturing none", []core.Option{core.Content(false)}, nil, "none", "weft.Content(false)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := New(Open(filepath.Join(t.TempDir(), "weft.db")))
			t.Cleanup(func() { _ = srv.Close() })
			ts := httptest.NewServer(srv.Handler())
			t.Cleanup(ts.Close)
			if m := metaOf(t, ts, ""); m.Content.Ingest != "as_received" || m.Content.Latest != nil {
				t.Fatalf("meta.content before any run = %+v, want as_received and latest null", m.Content)
			}
			id := runThrough(t, ts, append([]core.Option{core.Name("orders")}, c.agent...), c.dest...)
			var got *metaContentRun
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
				if got = metaOf(t, ts, "").Content.Latest; got != nil && got.Mark != markUnmarked {
					break
				}
			}
			if got == nil || got.RunID != id || got.Mark != c.mark || got.Note == "" ||
				(c.fix == "") != (got.Fix == "") || !strings.Contains(got.Fix, c.fix) {
				t.Errorf("meta.content.latest = %+v, want run %s marked %s, fix naming %q", got, id, c.mark, c.fix)
			}
		})
	}
}

// TestMetaManifestCheck pins meta.manifest_check: each manifest agent
// is hashed as the core hashes it (sha256 of core.Manifest(agent) —
// weft.manifest.hash), compared with that agent's latest stored run.
func TestMetaManifestCheck(t *testing.T) {
	a := core.New(wefttest.Script(), core.Name("orders"), core.Instructions("v1"))
	b := core.New(wefttest.Script(), core.Name("billing"))
	both, err := core.Manifest(a, b)
	if err != nil {
		t.Fatal(err)
	}
	hashes, err := manifestAgentHashes(both)
	if err != nil {
		t.Fatal(err)
	}
	for i, ag := range []*core.Agent{a, b} {
		one, _ := core.Manifest(ag)
		sum := sha256.Sum256(one)
		if hashes[i].hash != hex.EncodeToString(sum[:]) {
			t.Errorf("agent %s: hash %s, want core's %x", hashes[i].name, hashes[i].hash, sum)
		}
	}

	// A weft.json made from the agents that ran: checked, not stale.
	// One made before the instructions changed: stale for that agent.
	older, _ := core.Manifest(core.New(wefttest.Script(), core.Name("orders"), core.Instructions("v0")), b)
	for _, c := range []struct {
		manifest []byte
		stale    []string
	}{{both, nil}, {older, []string{"orders"}}} {
		srv := New(Open(filepath.Join(t.TempDir(), "weft.db")), Manifest(c.manifest))
		ts := httptest.NewServer(srv.Handler())
		runThrough(t, ts, []core.Option{core.Name("orders"), core.Instructions("v1")})
		var got *metaManifest
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if got = metaOf(t, ts, "").ManifestCheck; got != nil && got.Checked > 0 {
				break
			}
		}
		if got == nil || got.Agents != 2 || got.Checked != 1 || strings.Join(got.Stale, ",") != strings.Join(c.stale, ",") {
			t.Errorf("manifest_check = %+v, want 2 agents, 1 checked, stale %v", got, c.stale)
		}
		ts.Close()
		_ = srv.Close()
	}
	// No manifest: null.
	srv := New(Open(filepath.Join(t.TempDir(), "weft.db")))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	if m := metaOf(t, ts, ""); m.ManifestCheck != nil {
		t.Errorf("manifest_check without a manifest = %+v, want null", m.ManifestCheck)
	}
}

// TestMetaDBPathSetupA pins db.path/db.size in setup A (no Token): a
// loopback Host reads them; a Host let in only by AllowOrigins reads
// the kind alone. (Setup B and the panel tokens: TestAuthMatrix.)
func TestMetaDBPathSetupA(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weft.db")
	srv := New(Open(path), AllowOrigins("http://studio.example"))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	if m := metaOf(t, ts, ""); m.DB.Kind != "sqlite" || m.DB.Path != path || m.DB.Size == nil || *m.DB.Size <= 0 {
		t.Errorf("loopback meta.db = %+v, want sqlite at %s with a size", m.DB, path)
	}
	if m := metaOf(t, ts, "studio.example"); m.DB.Kind != "sqlite" || m.DB.Path != "" || m.DB.Size != nil {
		t.Errorf("AllowOrigins host meta.db = %+v, want the kind only", m.DB)
	}
}

// TestMetaRuntimes pins meta.runtimes: the runtimes holding a command
// stream now — 0 without Playground(true), 0 before any dials in, 1
// while one streams, 0 again once its stream ends.
func TestMetaRuntimes(t *testing.T) {
	off := New(Open(filepath.Join(t.TempDir(), "off.db")))
	t.Cleanup(func() { _ = off.Close() })
	offTS := httptest.NewServer(off.Handler())
	t.Cleanup(offTS.Close)
	if n := metaOf(t, offTS, "").Runtimes; n != 0 {
		t.Errorf("runtimes without the playground = %d, want 0", n)
	}

	srv := New(Open(filepath.Join(t.TempDir(), "weft.db")), Playground(true))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	if n := metaOf(t, ts, "").Runtimes; n != 0 {
		t.Errorf("runtimes before any runtime = %d, want 0", n)
	}
	resp, err := http.Post(ts.URL+"/api/runtime/register", "application/json",
		strings.NewReader(`{"runtime_id":"rt_1","agents":[{"name":"a","manifest":"{\"weft\":1,\"agents\":[{\"name\":\"a\",\"model\":{},\"policy\":{},\"tools\":[]}]}","limits":{"max_steps":10,"parallelism":4}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register: %d", resp.StatusCode)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/runtime/commands?runtime=rt_1", nil)
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, stream.Body) }()
	waitRuntimes := func(want int) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			n := metaOf(t, ts, "").Runtimes
			if n == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("runtimes = %d, want %d", n, want)
			}
		}
	}
	waitRuntimes(1)
	cancel()
	_ = stream.Body.Close()
	waitRuntimes(0)
}
