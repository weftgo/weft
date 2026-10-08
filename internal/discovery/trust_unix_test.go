//go:build unix

package discovery

import (
	"io/fs"
	"syscall"
)

// foreignOwner is fi as another user's file.
type foreignOwner struct{ fs.FileInfo }

func (f foreignOwner) Sys() any {
	st := *f.FileInfo.Sys().(*syscall.Stat_t)
	st.Uid++
	return &st
}

func mkfifo(path string) error { return syscall.Mkfifo(path, 0o600) }
