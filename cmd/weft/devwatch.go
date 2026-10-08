package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watchGo watches the directory trees under roots for .go changes and
// sends one path per burst: a change starts (or restarts) a debounce
// window, and the window's end sends the burst's first path. A send
// waits for nobody — while one restart is pending another burst adds
// nothing. Directories named in skipDirs are not descended into (a
// root is always watched); directories created later are added as
// they appear. A watcher error is one line on out, never a stop.
func watchGo(roots []string, debounce time.Duration, out io.Writer) (<-chan string, func(), error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, fmt.Errorf("watch: %w", err)
	}
	for _, r := range roots {
		if err := addTree(w, r, true); err != nil {
			_ = w.Close()
			return nil, nil, fmt.Errorf("watch %s: %w", r, err)
		}
	}
	changes := make(chan string, 1)
	quit := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		var (
			timer   *time.Timer
			fire    <-chan time.Time
			pending string
		)
		for {
			select {
			case <-quit:
				if timer != nil {
					timer.Stop()
				}
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if ev.Has(fsnotify.Create) {
					if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
						_ = addTree(w, ev.Name, false)
						continue
					}
				}
				if !strings.HasSuffix(ev.Name, ".go") || ev.Op == fsnotify.Chmod {
					continue
				}
				if pending == "" {
					pending = ev.Name
				}
				if timer == nil {
					timer = time.NewTimer(debounce)
				} else {
					timer.Reset(debounce)
				}
				fire = timer.C
			case <-fire:
				fire = nil
				select {
				case changes <- pending:
				default: // a restart is already pending
				}
				pending = ""
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				_, _ = fmt.Fprintln(out, "weft dev: watch:", err)
			}
		}
	}()
	stop := func() {
		close(quit)
		<-finished
		_ = w.Close()
	}
	return changes, stop, nil
}

// addTree adds dir and every directory below it, skipping skipDirs'
// names (root itself is added whatever its name).
func addTree(w *fsnotify.Watcher, dir string, root bool) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir {
				return err
			}
			return nil // a subdirectory that vanished or is unreadable
		}
		if !d.IsDir() {
			return nil
		}
		if (path != dir || !root) && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if err := w.Add(path); err != nil && path == dir {
			return err
		}
		return nil
	})
}
