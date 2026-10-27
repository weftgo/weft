package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/weftgo/weft/internal/dblock"
	"github.com/weftgo/weft/internal/discovery"
)

// B7 (phase 10b): the CLI's one resolver, the token story and the
// one-Studio-per-database rule, end to end through `run`.

// TestBareDoctorFindsAddrStudio is B7's Done line: a Studio started
// with --addr on a free port is found by bare `weft doctor` — no flag,
// no env — through the discovery file, said on stderr; its token comes
// from the file; `weft runs` and `weft open --with-token` find it the
// same way; the start's banner says how to sign in; api/meta names the
// file; and a second `weft studio` on the same SQLite file — no --addr,
// so it would have bound 7331–7340 — is refused naming the first's URL
// and pid.
func TestBareDoctorFindsAddrStudio(t *testing.T) {
	skipWithoutSelfSignal(t)
	dir := discoveryDir(t)
	file := filepath.Join(dir, discovery.FileName)
	for _, k := range []string{"WEFT_STUDIO_URL", "WEFT_STUDIO_TOKEN", "WEFT_DISCOVERY", "WEFT_STUDIO_ADDR", "WEFT_DB"} {
		t.Setenv(k, "")
	}
	port := freeBase(t, 1)
	url := "http://" + loop(port)
	db := filepath.Join(t.TempDir(), "weft.db")
	var banner syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"studio", "--addr", loop(port), "--db", "sqlite://" + db}, &banner, &banner)
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(file); err == nil {
			if resp, err := http.Get(url + "/api/meta"); err == nil {
				_ = resp.Body.Close()
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("studio never served on %s:\n%s", url, banner.String())
		}
	}
	stopped := false
	defer func() {
		if !stopped {
			stopStudio(t, done)
		}
	}()
	if !strings.Contains(banner.String(), signInLine+"\n") {
		t.Errorf("banner lacks the sign-in line:\n%s", banner.String())
	}

	from := "weft: Studio " + url + " (discovery file " + file + ")\n"
	out, errb, code := weft("doctor")
	if code != 0 || errb != from {
		t.Errorf("bare weft doctor = exit %d, stderr %q, want %q\n%s", code, errb, from, out)
	}
	// The source is said once, on stderr (B7-fix).
	if strings.Contains(out, "(discovery file") {
		t.Errorf("doctor's stdout repeats the source:\n%s", out)
	}
	for _, want := range []string{
		"ok   studio    reachable at " + url + " (weft ",
		"ok   token     accepted",
		"ok   discovery " + file + " -> " + url + " (this Studio)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output lacks %q:\n%s", want, out)
		}
	}
	if _, errb, code := weft("runs"); code != 0 || !strings.HasPrefix(errb, from) || !strings.Contains(errb, "no runs match") {
		t.Errorf("bare weft runs = exit %d, stderr %q", code, errb)
	}
	tok, err := os.ReadFile(db + ".token")
	if err != nil {
		t.Fatal(err)
	}
	if out, errb, code := weft("open", "--with-token"); code != 0 || out != url+"/#token="+strings.TrimSpace(string(tok))+"\n" || errb != from {
		t.Errorf("bare weft open --with-token = exit %d %q %q, want the root signed in", code, out, errb)
	}
	b, err := newClient(url, strings.TrimSpace(string(tok))).get(context.Background(), "/api/meta", nil)
	var meta struct {
		DiscoveryFile string `json:"discovery_file"`
	}
	if err != nil || json.Unmarshal(b, &meta) != nil || meta.DiscoveryFile != file {
		t.Errorf("meta discovery_file = %q (%v), want %s", meta.DiscoveryFile, err, file)
	}

	// The second Studio on the same file: refused, exit 1, the first named.
	_, errb, code = weft("studio", "--db", "sqlite://"+db)
	if want := "weft: studio already running at " + url + " (pid " + strconv.Itoa(os.Getpid()) + ") on " + db + ":"; code != 1 || !strings.HasPrefix(errb, want) {
		t.Errorf("second weft studio = exit %d %q, want %q…", code, errb, want)
	}
	stopped = true
	if code := stopStudio(t, done); code != 0 {
		t.Errorf("exit %d after SIGTERM", code)
	}
}

// TestResolverOrder pins the one resolver (UX-S5a-01): --url >
// WEFT_STUDIO_URL > the discovery file > the default address; the
// token --token > WEFT_STUDIO_TOKEN > the file's, which goes only to
// the Studio the file names; WEFT_DISCOVERY=off reads no file; the
// stderr line only when the user named no Studio.
func TestResolverOrder(t *testing.T) {
	dir := discoveryDir(t)
	for _, k := range []string{"WEFT_STUDIO_URL", "WEFT_STUDIO_TOKEN", "WEFT_DISCOVERY"} {
		t.Setenv(k, "")
	}
	say := func(r studioTarget) string {
		var b strings.Builder
		r.say(&b)
		return b.String()
	}
	r := resolveStudio("", "")
	if r.base != defaultURL || r.token != "" || say(r) != "weft: Studio "+defaultURL+" (the default address; no discovery file)\n" {
		t.Errorf("nothing = %+v %q", r, say(r))
	}
	w, err := discovery.Write(discovery.Info{URL: "http://127.0.0.1:7347", Token: "filetok", PID: os.Getpid(), Started: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Remove)
	file := filepath.Join(dir, discovery.FileName)
	for _, c := range []struct {
		name, urlFlag, tokFlag, envURL, envTok, off string
		base, token, line                           string
	}{
		{"the file", "", "", "", "", "", "http://127.0.0.1:7347", "filetok", "weft: Studio http://127.0.0.1:7347 (discovery file " + file + ")\n"},
		{"env over the file", "", "", "http://127.0.0.1:7400", "", "", "http://127.0.0.1:7400", "", ""},
		{"--url over env", "http://127.0.0.1:7500", "", "http://127.0.0.1:7400", "", "", "http://127.0.0.1:7500", "", ""},
		{"--url naming the file's Studio takes its token", "http://127.0.0.1:7347/", "", "", "", "", "http://127.0.0.1:7347/", "filetok", ""},
		{"env token over the file's", "", "", "", "envtok", "", "http://127.0.0.1:7347", "envtok", "weft: Studio http://127.0.0.1:7347 (discovery file " + file + ")\n"},
		{"--token over env", "", "flagtok", "", "envtok", "", "http://127.0.0.1:7347", "flagtok", "weft: Studio http://127.0.0.1:7347 (discovery file " + file + ")\n"},
		{"off", "", "", "", "", "off", defaultURL, "", "weft: Studio " + defaultURL + " (the default address; WEFT_DISCOVERY=off)\n"},
	} {
		t.Setenv("WEFT_STUDIO_URL", c.envURL)
		t.Setenv("WEFT_STUDIO_TOKEN", c.envTok)
		t.Setenv("WEFT_DISCOVERY", c.off)
		r := resolveStudio(c.urlFlag, c.tokFlag)
		if r.base != c.base || r.token != c.token || say(r) != c.line {
			t.Errorf("%s: base %q token %q line %q, want %q %q %q", c.name, r.base, r.token, say(r), c.base, c.token, c.line)
		}
	}
}

// TestOpenIDKinds (UX-S5a-06): `weft open` tries a run id, then a
// public id (the conversation page), a session id, a trace id; a
// refusal other than 404 stops it.
func TestOpenIDKinds(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/public/pub_1", "/api/sessions/s_1":
			_, _ = w.Write([]byte(`{}`))
		case "/api/runs/locked":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"outside this token's scope"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such thing"}}`))
		}
	}))
	defer ts.Close()
	c := newClient(ts.URL, "")
	for id, want := range map[string]string{"pub_1": "/conversations/pub_1", "s_1": "/sessions/s_1"} {
		if got, err := pageOf(context.Background(), c, id); err != nil || got != want {
			t.Errorf("%s = %q %v, want %s", id, got, err, want)
		}
	}
	if _, err := pageOf(context.Background(), c, "locked"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("a 403 = %v, want it as the error", err)
	}
}

// metaDB is the db.path the Studio at addr serves, read with db's
// stable token ("" when nothing answers as that database's Studio).
func metaDB(addr, db string) string {
	tok, _ := os.ReadFile(db + ".token")
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/meta", nil)
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	var m struct {
		DB struct {
			Path string `json:"path"`
		} `json:"db"`
	}
	if json.NewDecoder(resp.Body).Decode(&m) != nil {
		return ""
	}
	return m.DB.Path
}

// starts runs `weft studio` starts in the background and stops every
// one still serving when the test ends — SIGTERM (signalSelf's guard
// absorbs a signal nobody waits for), then each waited for, bounded —
// so a broken lock fails the test instead of leaving Studios serving
// and the package hung (B7-fix2).
type starts struct {
	t       *testing.T
	results chan error
	pending int
}

func newStarts(t *testing.T) *starts {
	s := &starts{t: t, results: make(chan error, 8)}
	t.Cleanup(func() {
		if s.pending == 0 {
			return
		}
		signalSelf(t, syscall.SIGTERM)
		for s.pending > 0 {
			if _, ok := s.next(10 * time.Second); !ok {
				t.Errorf("%d start(s) still serving 10 s after SIGTERM", s.pending)
				return
			}
		}
	})
	return s
}

func (s *starts) start(f func() error) {
	s.pending++
	go func() { s.results <- f() }()
}

// next is the next start to return, within d; ok false when none did.
func (s *starts) next(d time.Duration) (error, bool) {
	select {
	case err := <-s.results:
		s.pending--
		return err, true
	case <-time.After(d):
		return nil, false
	}
}

// TestConcurrentStartsOneServes (B7-fix, review finding 1): two bare
// `weft studio` starts on one database at the same moment — the
// second's probe of the busy port would go out bare, get a 401 and
// move on — exactly one serves; the other is refused by the lock.
func TestConcurrentStartsOneServes(t *testing.T) {
	skipWithoutSelfSignal(t)
	discoveryDir(t)
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	base := freeBase(t, 3)
	db := filepath.Join(t.TempDir(), "weft.db")
	s := newStarts(t)
	gate := make(chan struct{})
	for range 2 {
		s.start(func() error {
			<-gate
			return serveWith("sqlite://"+db, want{addr: loop(base), span: 3}, "", &syncBuffer{}, afterBoot{})
		})
	}
	close(gate)
	err, ok := s.next(5 * time.Second)
	if !ok {
		t.Fatal("neither start was refused within 5 s: both serve the database")
	}
	named := err != nil && (strings.HasPrefix(err.Error(), "studio already running at ") || strings.HasPrefix(err.Error(), "another Studio holds "))
	if !named || !strings.Contains(err.Error(), db) {
		t.Fatalf("the refused start = %v, want refused naming %s", err, db)
	}
	serving := 0
	for deadline := time.Now().Add(10 * time.Second); serving == 0 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		for p := base; p < base+3; p++ {
			if metaDB(loop(p), db) == db {
				serving++
			}
		}
	}
	if serving != 1 {
		t.Fatalf("%d Studios serve %s, want exactly 1", serving, db)
	}
	signalSelf(t, syscall.SIGTERM)
	if err, ok := s.next(10 * time.Second); !ok || err != nil {
		t.Errorf("the serving start after SIGTERM: %v (returned %v)", err, ok)
	}
}

// TestOtherDirectoryRefusedAndRestart (B7-fix, review finding 2): a
// start whose discovery lookup finds another file (another directory)
// on a database a Studio serves is refused naming it (the lock file
// says who holds it), on another port and never bound; weft dev from
// there reuses it instead; and once the first stops, its lock is gone —
// a restart serves.
func TestOtherDirectoryRefusedAndRestart(t *testing.T) {
	discoveryDir(t)
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	base := freeBase(t, 3)
	db := filepath.Join(t.TempDir(), "weft.db")
	var first syncBuffer
	stop := bareStudio(t, "sqlite://"+db, base, 1, loop(base), "", false, &first)

	// Another directory: its discovery locations hold nothing.
	discoveryDir(t)
	s := newStarts(t)
	var second syncBuffer
	s.start(func() error {
		return serveWith("sqlite://"+db, want{addr: loop(base + 1), pinned: true}, "", &second, afterBoot{})
	})
	err, ok := s.next(5 * time.Second)
	if !ok {
		t.Fatal("the second start has not returned in 5 s: it serves the database too")
	}
	wantRefused(t, err, &second, loop(base))
	if metaDB(loop(base+1), db) != "" {
		t.Error("the refused start serves")
	}
	var dev syncBuffer
	r, err := devStudio("sqlite://"+db, want{addr: loop(base + 2), pinned: true}, "", false, &dev)
	if err != nil || r.url != "http://"+loop(base) || !strings.Contains(dev.String(), "reusing") {
		t.Fatalf("weft dev from another directory = %+v %v %q, want the running Studio reused", r, err, dev.String())
	}
	tok, _ := os.ReadFile(db + ".token")
	if r.token != strings.TrimSpace(string(tok)) || metaStatus(t, loop(base), r.token) != http.StatusOK {
		t.Errorf("weft dev's reuse token does not open the running Studio")
	}

	stop()
	var again syncBuffer
	bareStudio(t, "sqlite://"+db, base, 1, loop(base), "", false, &again)
	if metaDB(loop(base), db) != db {
		t.Error("a restart on the released database does not serve")
	}
}

// TestUntrustedHolderNamesNobody (B7-fix2, review findings 1 and 2):
// a lock held by something whose file names a non-loopback url, or a
// dead pid (a crashed Studio's leftover the new holder has not yet
// overwritten), names nobody — weft studio's refusal does not print
// it, and weft dev neither reuses it nor hands it the database's token.
func TestUntrustedHolderNamesNobody(t *testing.T) {
	discoveryDir(t)
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	db := filepath.Join(t.TempDir(), "weft.db")
	if err := os.WriteFile(db+".token", []byte("secret-stable-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, h := range map[string]dblock.Holder{
		"not loopback": {URL: "http://203.0.113.9:80", PID: os.Getpid()},
		"dead pid":     {URL: "http://127.0.0.1:1", PID: 999999999},
	} {
		l, err := dblock.Acquire(lockPath(db))
		if err != nil {
			t.Fatal(err)
		}
		l.SetHolder(h)
		port := freeBase(t, 1)
		s := newStarts(t)
		s.start(func() error {
			return serveWith("sqlite://"+db, want{addr: loop(port), pinned: true}, "", &syncBuffer{}, afterBoot{})
		})
		err, ok := s.next(5 * time.Second)
		if !ok {
			l.Release()
			t.Fatalf("%s: weft studio has not returned in 5 s: it serves a held database", name)
		}
		if err == nil || !strings.HasPrefix(err.Error(), "another Studio holds "+db) || strings.Contains(err.Error(), h.URL) {
			t.Errorf("%s: weft studio = %v, want refused naming nobody", name, err)
		}
		var dev syncBuffer
		r, err := devStudio("sqlite://"+db, want{addr: loop(port), pinned: true}, "", false, &dev)
		if r != nil {
			r.stop(syscall.SIGTERM) // a Studio it started (it must not have)
		}
		if err == nil || r != nil || strings.Contains(dev.String(), "reusing") || strings.Contains(err.Error(), h.URL) {
			t.Errorf("%s: weft dev = %+v %v %q, want refused, nothing reused", name, r, err, dev.String())
		}
		l.Release()
	}
}

// TestSymlinkedDBOneLock (B7-fix2, review finding 3): a database
// reached through a symlinked file has the one lock of the file it
// names — a second start through the link is refused.
func TestSymlinkedDBOneLock(t *testing.T) {
	discoveryDir(t)
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	base := freeBase(t, 2)
	real := filepath.Join(t.TempDir(), "c.db")
	var first syncBuffer
	bareStudio(t, "sqlite://"+real, base, 1, loop(base), "", false, &first)
	link := filepath.Join(t.TempDir(), "x.db")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	s := newStarts(t)
	s.start(func() error {
		return serveWith("sqlite://"+link, want{addr: loop(base + 1), pinned: true}, "", &syncBuffer{}, afterBoot{})
	})
	err, ok := s.next(5 * time.Second)
	if !ok {
		t.Fatal("the start through the symlink has not returned in 5 s: it serves the same file")
	}
	if err == nil || !strings.HasPrefix(err.Error(), "studio already running at http://"+loop(base)+" (pid ") {
		t.Errorf("start through the symlink = %v, want refused naming the first", err)
	}
}
