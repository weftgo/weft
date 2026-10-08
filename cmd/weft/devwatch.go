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
// they appear — and a directory that arrives with .go files already in
// it (a checkout, a mv, a stash pop) is a change itself. Editor lock
// files (.#name.go) are not. A watcher error, or a directory that
// could not be watched (inotify's max_user_watches spent), is one line
// on out, never a stop.
func watchGo(roots []string, debounce time.Duration, out io.Writer) (<-chan string, func(), error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, fmt.Errorf("watch: %w", err)
	}
	for _, r := range roots {
		if _, err := addTree(w, r, true, out); err != nil {
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
				changed := ""
				if ev.Has(fsnotify.Create) {
					if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
						changed, _ = addTree(w, ev.Name, false, out)
						if changed == "" {
							continue
						}
					}
				}
				if changed == "" {
					if !isGoSource(ev.Name) || ev.Op == fsnotify.Chmod {
						continue
					}
					changed = ev.Name
				}
				if pending == "" {
					pending = changed
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

// isGoSource reports whether path is a .go file a save would change —
// not an editor's lock file (.#name.go).
func isGoSource(path string) bool {
	base := filepath.Base(path)
	return strings.HasSuffix(base, ".go") && !strings.HasPrefix(base, ".#")
}

// addTree adds dir and every directory below it, skipping skipDirs'
// names (root itself is added whatever its name), and returns the
// first .go file it met ("" for none). A directory below dir that
// cannot be watched is said on out — the first, then how many more —
// and skipped; dir itself failing is the error.
func addTree(w *fsnotify.Watcher, dir string, root bool, out io.Writer) (goFile string, err error) {
	failed := 0
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir {
				return err
			}
			return nil // a subdirectory that vanished or is unreadable
		}
		if !d.IsDir() {
			if goFile == "" && isGoSource(path) {
				goFile = path
			}
			return nil
		}
		if (path != dir || !root) && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if err := w.Add(path); err != nil {
			if path == dir && root {
				return err
			}
			if failed == 0 {
				_, _ = fmt.Fprintf(out, "weft dev: watch %s: %v\n", path, err)
			}
			failed++
		}
		return nil
	})
	if failed > 1 {
		_, _ = fmt.Fprintf(out, "weft dev: watch: %d more directories under %s not watched\n", failed-1, dir)
	}
	return goFile, err
}
