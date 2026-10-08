//go:build !unix

package discovery

import "io/fs"

// openFlags: no non-blocking open where FIFOs are not files (Windows).
const openFlags = 0

// checkFile: modes and owners do not map (Windows); the loopback and
// freshness checks are the trust rule there.
func checkFile(fs.FileInfo) error { return nil }

// alive is best effort where signal 0 does not exist (Windows): a pid
// is taken as alive, and the file's age alone decides staleness.
func alive(pid int) bool { return pid > 0 }
