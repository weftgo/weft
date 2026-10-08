package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/weftgo/weft/internal/discovery"
	"github.com/weftgo/weft/version"
)

// The discovery file (plan B3, internal/discovery): `weft studio` and
// `weft dev` write studio.json once the listener is bound — the real
// port in it — and remove it on a clean exit; otel.Install and
// runtime.Install read it when WEFT_STUDIO_URL is unset, so an app
// joins the running Studio with no configuration. A reuse writes
// nothing: the running Studio owns the file.

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
	w, err := writeDiscovery(url, token, dbPath)
	if err != nil {
		_, _ = fmt.Fprintf(out, "studio: no discovery file (%v); apps need WEFT_STUDIO_URL=%s\n", err, url)
		return nil
	}
	_, _ = fmt.Fprintf(out, "studio: apps find this Studio through %s (WEFT_STUDIO_URL overrides it, WEFT_DISCOVERY=off ignores it)\n", w.Path)
	return w
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
