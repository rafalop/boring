//go:build linux || darwin

package vpnd

import "syscall"

// isProcessAlive reports whether pid refers to a still-running process,
// using the POSIX convention that signal 0 performs no action beyond
// existence/permission checks.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
