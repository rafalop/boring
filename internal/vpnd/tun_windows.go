//go:build windows

package vpnd

import (
	"fmt"

	"golang.zx2c4.com/wireguard/tun"
)

// createTUN is never actually reached: HasPrivileges always returns
// false on Windows, so the daemon refuses to start before any session
// gets far enough to call this.
func createTUN(name string, mtu int) (tun.Device, error) {
	return nil, fmt.Errorf("boring-vpn is not supported on Windows yet")
}
