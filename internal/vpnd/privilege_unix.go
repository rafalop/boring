//go:build linux || darwin

package vpnd

import "os"

// HasPrivileges reports whether the current process can create a TUN
// device and modify the routing table.
func HasPrivileges() bool {
	return os.Geteuid() == 0
}

func PrivilegeError() string {
	return "boring-vpn requires root privileges to create a TUN device and modify routes; " +
		"re-run with sudo, e.g. \"sudo boring-vpn up <name>\" (this restarts the boring-vpn daemon as root)"
}
