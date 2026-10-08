package main

import (
	"fmt"
	"io"
	"os"
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

// written is a discovery file this process wrote.
type written struct {
	path string
	info discovery.Info
}

// remove removes the file if it is still this Studio's.
func (w written) remove() { discovery.Remove(w.path, w.info) }

// writeDiscovery writes the file for the Studio at url. dbPath is the
// SQLite file, "" for a database with no file.
func writeDiscovery(url, token, dbPath string) (written, error) {
	info := discovery.Info{
		URL:     url,
		Token:   token,
		DB:      dbPath,
		PID:     os.Getpid(),
		Started: time.Now().UTC().Truncate(time.Second),
		Version: version.Runtime(),
	}
	path, err := discovery.Write(info)
	if err != nil {
		return written{}, err
	}
	return written{path: path, info: info}, nil
}

// announce writes the discovery file and says where in one line (a
// failure is said too: the Studio serves regardless — apps then need
// WEFT_STUDIO_URL).
func announce(out io.Writer, url, token, dbPath string) written {
	w, err := writeDiscovery(url, token, dbPath)
	if err != nil {
		_, _ = fmt.Fprintf(out, "studio: no discovery file (%v); apps need WEFT_STUDIO_URL=%s\n", err, url)
		return written{}
	}
	_, _ = fmt.Fprintf(out, "studio: apps find this Studio through %s (WEFT_STUDIO_URL overrides it, WEFT_DISCOVERY=off ignores it)\n", w.path)
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
