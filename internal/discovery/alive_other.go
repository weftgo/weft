//go:build !unix

package discovery

// alive is best effort where signal 0 does not exist (Windows): a pid
// is taken as alive, and the file's age alone decides staleness.
func alive(pid int) bool { return pid > 0 }
