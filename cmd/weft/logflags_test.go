package main

import (
	"errors"
	"flag"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// P8-fix-a 13 (UX-E-37): --log-level / --log-format pick the server
// log's handler; the environment mirrors them; a bad value is a usage
// error naming its source; nothing set leaves slog.Default alone.
func TestLogFlags(t *testing.T) {
	install := func(t *testing.T, args ...string) (*strings.Builder, func(), error) {
		t.Helper()
		fs := flag.NewFlagSet("weft studio", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		lf := addLogFlags(fs)
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		restore, err := lf.install(&b)
		return &b, restore, err
	}
	t.Setenv("WEFT_LOG_LEVEL", "")
	t.Setenv("WEFT_LOG_FORMAT", "")

	prev := slog.Default()
	_, restore, err := install(t)
	if err != nil || slog.Default() != prev {
		t.Fatalf("no flag: err %v, default replaced %v", err, slog.Default() != prev)
	}
	restore()

	b, restore, err := install(t, "--log-level", "warn", "--log-format", "json")
	if err != nil {
		t.Fatal(err)
	}
	slog.Info("hidden")
	slog.Warn("studio: ingest batch refused", "request_id", "r1")
	restore()
	if slog.Default() != prev {
		t.Error("restore did not put the previous default back")
	}
	if out := b.String(); strings.Contains(out, "hidden") || !strings.Contains(out, `"request_id":"r1"`) {
		t.Errorf("warn + json: %q", out)
	}

	t.Setenv("WEFT_LOG_LEVEL", "debug")
	b, restore, err = install(t)
	if err != nil {
		t.Fatal(err)
	}
	slog.Debug("seen")
	restore()
	if !strings.Contains(b.String(), "level=DEBUG") {
		t.Errorf("WEFT_LOG_LEVEL=debug, text: %q", b.String())
	}

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--log-level", "loud"}, `--log-level "loud"`},
		{[]string{"--log-format", "xml"}, `--log-format "xml"`},
	} {
		_, _, err := install(t, c.args...)
		if !errors.Is(err, errUsage) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want a usage error naming %s", c.args, err, c.want)
		}
	}
}
