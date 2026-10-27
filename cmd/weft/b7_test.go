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
	"testing"
	"time"

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
	for _, want := range []string{
		"ok   studio    reachable at " + url + " (discovery file " + file + ")",
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
