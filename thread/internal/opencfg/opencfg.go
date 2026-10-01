// Package opencfg holds the resolved form of thread's open options —
// the one piece of the option vocabulary that thread (which declares
// the options), its in-tree backends (Memory) and thread/backend
// (which publishes the resolution to backend authors) all need, kept
// here so that none of them has to import another to share it.
package opencfg

import "log/slog"

// Config is an open's resolved configuration: the defaults with every
// option applied. thread/backend.Config is its published form.
type Config struct {
	// Salvage downgrades a malformed line from a load failure to a
	// skip reported in the LoadReport.
	Salvage bool
	// SyncEveryAppend is the durability cadence: true (the default)
	// fsyncs every Append; false defers the fsync to Flush.
	SyncEveryAppend bool
	// NoLock turns off the backend's cross-process writer lock.
	NoLock bool
	// Logger receives the backend's own reports. Never nil after
	// Resolve: the default is slog.Default(), read at open.
	Logger *slog.Logger
}

// Applier is what every open option is beneath thread's sealed
// OpenOption: a change to the configuration.
type Applier interface {
	ApplyOpen(*Config)
}

// Resolve folds opts over the defaults. A nil option, and a value
// that is not an option, change nothing.
func Resolve[O any](opts []O) Config {
	cfg := Config{SyncEveryAppend: true}
	for _, o := range opts {
		if a, ok := any(o).(Applier); ok && a != nil {
			a.ApplyOpen(&cfg)
		}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg
}
