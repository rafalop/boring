//go:build windows

package main

import "fmt"

// launchDaemonOS refuses to spawn a daemon: boring-vpn's TUN device and
// routing table code has no Windows implementation yet.
func launchDaemonOS(name string, arg ...string) (int, error) {
	return 0, fmt.Errorf("boring-vpn is not supported on Windows yet")
}
