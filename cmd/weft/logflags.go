package main

import (
	"flag"
	"io"
	"log/slog"
	"os"
	"strings"
)

// logFlags are --log-level and --log-format on `weft studio` and
// `weft dev` (UX-E-37): the Studio server's log — the refused ingest
// batches, the 5xx lines with their request ids, the sweeper — goes
// through slog.Default, and these pick its handler. Each mirrors an
// environment variable one to one (WEFT_LOG_LEVEL, WEFT_LOG_FORMAT; the
// flag wins). With neither set, slog.Default is left as Go starts it
// (INFO and above, log's text line on stderr).
type logFlags struct {
	level, format *string
}

func addLogFlags(fs *flag.FlagSet) logFlags {
	return logFlags{
		level: fs.String("log-level", "",
			"the server log's `level`: debug, info, warn or error (default: $WEFT_LOG_LEVEL, else info)"),
		format: fs.String("log-format", "",
			"the server log's `format` on stderr: text or json (default: $WEFT_LOG_FORMAT; else text when a level is set, Go's log line when neither is)"),
	}
}

// install sets slog.Default to the handler the flags (or their
// environment mirrors) name, writing to w; restore puts the previous
// default back. A value outside the lists is a usage error naming where
// it came from; nothing set leaves slog.Default alone.
func (f logFlags) install(w io.Writer) (restore func(), err error) {
	level, levelFrom := *f.level, "--log-level"
	if level == "" {
		level, levelFrom = os.Getenv("WEFT_LOG_LEVEL"), "WEFT_LOG_LEVEL"
	}
	format, formatFrom := *f.format, "--log-format"
	if format == "" {
		format, formatFrom = os.Getenv("WEFT_LOG_FORMAT"), "WEFT_LOG_FORMAT"
	}
	if level == "" && format == "" {
		return func() {}, nil
	}
	var lv slog.Level
	switch strings.ToLower(level) {
	case "", "info":
		lv = slog.LevelInfo
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		return nil, usageError("%s %q: not a level (debug, info, warn, error)", levelFrom, level)
	}
	opts := &slog.HandlerOptions{Level: lv}
	var h slog.Handler
	switch strings.ToLower(format) {
	case "", "text":
		h = slog.NewTextHandler(w, opts)
	case "json":
		h = slog.NewJSONHandler(w, opts)
	default:
		return nil, usageError("%s %q: not a format (text, json)", formatFrom, format)
	}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	return func() { slog.SetDefault(prev) }, nil
}
