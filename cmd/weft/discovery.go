package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/weftgo/weft/internal/discovery"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/version"
)

// The discovery file (plan B3, internal/discovery): `weft studio` and
// `weft dev` write studio.json once the listener is bound — the real
// port in it — and remove it on a clean exit; otel.Install and
// runtime.Install read it when WEFT_STUDIO_URL is unset and the
// pipeline names no otel.Studio or otel.Local (the README's "When
// weft/otel reads the file"), so an app joins the running Studio with
// no configuration. A reuse writes
// nothing: the running Studio owns the file. Under WEFT_DISCOVERY=off
// (discovery.Off) neither command writes one, and the start says so in
// one line.

// discoveryOffLine is the start line under WEFT_DISCOVERY=off.
const discoveryOffLine = "studio: no discovery file written (WEFT_DISCOVERY=off)"

// writeDiscovery writes the file for the Studio at url. dbPath is the
// SQLite file, "" for a database with no file.
func writeDiscovery(url, token, dbPath string) (*discovery.Written, error) {
	info := discovery.Info{
		URL:     url,
		Token:   token,
		DB:      dbPath,
		PID:     os.Getpid(),
		Started: time.Now().UTC().Truncate(time.Second),
		Version: version.Runtime(),
	}
	return discovery.Write(info)
}

// announce writes the discovery file and says where in one line (a
// failure is said too: the Studio serves regardless — apps then need
// WEFT_STUDIO_URL).
func announce(out io.Writer, url, token, dbPath string) *discovery.Written {
	if discovery.Off(os.Getenv) {
		_, _ = fmt.Fprintf(out, "%s; apps need WEFT_STUDIO_URL=%s\n", discoveryOffLine, url)
		return nil
	}
	w, err := writeDiscovery(url, token, dbPath)
	if err != nil {
		_, _ = fmt.Fprintf(out, "studio: no discovery file (%v); apps need WEFT_STUDIO_URL=%s\n", err, url)
		return nil
	}
	_, _ = fmt.Fprintf(out, "studio: apps find this Studio through %s (WEFT_STUDIO_URL overrides it, WEFT_DISCOVERY=off ignores it)\n", w.Path)
	return w
}

// knownStudio is the address (host:port) of the Studio this user's
// discovery file names when it serves a database file, "" otherwise
// (no file trusted, WEFT_DISCOVERY=off, a start with no database
// file): the port policy probes it first (listen.Request.Known), so a
// Studio on the same file is found whatever its port (UX-S5a-13).
func knownStudio(dbPath string) string {
	if dbPath == "" || discovery.Off(os.Getenv) {
		return ""
	}
	info, _, ok := discovery.Lookup(func(k string) string {
		if k == "WEFT_STUDIO_URL" {
			return ""
		}
		return os.Getenv(k)
	}, slog.New(slog.DiscardHandler))
	if !ok || info.DB == "" {
		return ""
	}
	u, err := url.Parse(info.URL)
	if err != nil {
		return ""
	}
	return u.Host
}

// discoveryOption names the discovery file a start is about to write to
// its server (studio.DiscoveryFile: /api/meta's discovery_file, which
// Studio's connect lines print); none under WEFT_DISCOVERY=off.
func discoveryOption() []studio.Option {
	if discovery.Off(os.Getenv) {
		return nil
	}
	if p := discovery.Path(); p != "" {
		return []studio.Option{studio.DiscoveryFile(p)}
	}
	return nil
}

// reuseNotes are the lines a reusing start adds after its reuse line:
// --rotate-token was not applied (the running Studio serves its token),
// and the running Studio's discovery file is missing, when it is.
func reuseNotes(out io.Writer, addr string, rotate bool) {
	if rotate {
		_, _ = fmt.Fprintln(out, "studio: --rotate-token not applied: the running Studio keeps its token (stop it, then start with --rotate-token)")
	}
	if discovery.Find("http://"+addr) == "" {
		_, _ = fmt.Fprintf(out, "studio: the running Studio wrote no discovery file; apps need WEFT_STUDIO_URL=http://%s\n", addr)
	}
}

// refreshEvery is how often a serving Studio re-stamps its discovery
// file's started time, so it stays fresh past discovery.MaxAge; a
// variable so a test may shorten it.
var refreshEvery = time.Hour

// keepFresh refreshes w every refreshEvery until the returned stop is
// called (stop waits for the refresher). Nil-safe.
func keepFresh(w *discovery.Written) (stop func()) {
	if w == nil {
		return func() {}
	}
	done, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		t := time.NewTicker(refreshEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				_ = w.Refresh()
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done); <-exited }) }
}
