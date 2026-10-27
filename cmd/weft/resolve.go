package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/weftgo/weft/internal/discovery"
	"github.com/weftgo/weft/internal/doctor"
)

// The one resolver every API client of the command line shares — runs,
// open, export, doctor and mcp (UX-S5a-01, A10): the Studio is --url,
// else WEFT_STUDIO_URL, else the discovery file a running `weft studio`
// or `weft dev` wrote (internal/discovery's reader and trust rules —
// loopback, fresh, 0600 and this user's — and WEFT_DISCOVERY=off), else
// the one default address. The token is --token, else
// WEFT_STUDIO_TOKEN, else the discovery file's when that file names the
// resolved Studio: a token goes only to the Studio it belongs to.

// Where a target's URL came from.
const (
	fromFlag      = "--url"
	fromEnv       = "WEFT_STUDIO_URL"
	fromDiscovery = "discovery file"
	fromDefault   = "the default address"
)

// studioTarget is the Studio a client talks to and why.
type studioTarget struct {
	base, token string
	from        string // fromFlag | fromEnv | fromDiscovery | fromDefault
	tokenFrom   string // "--token", "WEFT_STUDIO_TOKEN", "the discovery file", "none"
	// The discovery file this shell reads, whatever named the URL: path
	// "" when none is trusted; off under WEFT_DISCOVERY=off.
	file string
	info discovery.Info
	off  bool
}

// resolveStudio is the one resolver.
func resolveStudio(urlFlag, tokenFlag string) studioTarget {
	t := studioTarget{off: discovery.Off(os.Getenv)}
	if !t.off {
		// Read the file whatever named the URL: its token is used only
		// when it names the same Studio, and doctor reports it.
		t.info, t.file, _ = discovery.Lookup(func(k string) string {
			if k == "WEFT_STUDIO_URL" {
				return ""
			}
			return os.Getenv(k)
		}, slog.New(slog.DiscardHandler))
	}
	switch {
	case urlFlag != "":
		t.base, t.from = urlFlag, fromFlag
	case os.Getenv("WEFT_STUDIO_URL") != "":
		t.base, t.from = os.Getenv("WEFT_STUDIO_URL"), fromEnv
	case t.file != "":
		t.base, t.from = t.info.URL, fromDiscovery
	default:
		t.base, t.from = defaultURL, fromDefault
	}
	switch {
	case tokenFlag != "":
		t.token, t.tokenFrom = tokenFlag, "--token"
	case os.Getenv("WEFT_STUDIO_TOKEN") != "":
		t.token, t.tokenFrom = os.Getenv("WEFT_STUDIO_TOKEN"), "WEFT_STUDIO_TOKEN"
	case t.file != "" && t.info.Token != "" && t.namedByFile():
		t.token, t.tokenFrom = t.info.Token, "the discovery file"
	default:
		t.tokenFrom = "none"
	}
	return t
}

// namedByFile reports whether the discovery file names the resolved
// Studio.
func (t studioTarget) namedByFile() bool {
	return t.file != "" && strings.TrimRight(t.info.URL, "/") == strings.TrimRight(t.base, "/")
}

// explicit is whether the user named the Studio (--url, WEFT_STUDIO_URL).
func (t studioTarget) explicit() bool { return t.from == fromFlag || t.from == fromEnv }

// source is the target's origin in words, for the stderr line.
func (t studioTarget) source() string {
	switch t.from {
	case fromDiscovery:
		return "discovery file " + t.file
	case fromDefault:
		switch {
		case t.off:
			return "the default address; WEFT_DISCOVERY=off"
		default:
			return "the default address; no discovery file"
		}
	}
	return t.from
}

// say prints the one stderr line naming the Studio and where it came
// from, when the user did not name it (the discovery file, or the
// default address): `weft: Studio http://127.0.0.1:7347 (discovery
// file …)`. A Studio named by --url or WEFT_STUDIO_URL is not repeated
// back.
func (t studioTarget) say(stderr io.Writer) {
	if t.explicit() {
		return
	}
	_, _ = fmt.Fprintf(stderr, "weft: Studio %s (%s)\n", redactURL(t.base), t.source())
}

// redactURL is a URL for a message: a password in its userinfo is
// never printed.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "the --url given"
	}
	return strings.TrimRight(u.Redacted(), "/")
}

// discovery is the discovery file as the doctor reports it.
func (t studioTarget) discovery() doctor.Discovery {
	d := doctor.Discovery{Off: t.off}
	if t.file != "" {
		d.Path, d.URL, d.DB, d.PID = t.file, t.info.URL, t.info.DB, t.info.PID
	}
	return d
}
