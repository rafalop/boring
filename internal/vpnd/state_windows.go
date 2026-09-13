//go:build windows

package vpnd

// isProcessAlive is never actually consulted on Windows: HasPrivileges
// always returns false there, so the daemon refuses to start before
// cleanupStaleState would ever run.
func isProcessAlive(pid int) bool {
	return false
}
