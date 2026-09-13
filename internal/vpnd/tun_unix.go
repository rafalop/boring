//go:build linux || darwin

package vpnd

import "golang.zx2c4.com/wireguard/tun"

// createTUN creates a kernel TUN device with the given name (which the
// kernel may not honor exactly -- see the returned Device's Name()) and
// MTU.
func createTUN(name string, mtu int) (tun.Device, error) {
	return tun.CreateTUN(name, mtu)
}
