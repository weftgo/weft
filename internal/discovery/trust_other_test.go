//go:build !unix

package discovery

import (
	"errors"
	"io/fs"
)

type foreignOwner struct{ fs.FileInfo }

func mkfifo(string) error { return errors.New("no FIFOs here") }
