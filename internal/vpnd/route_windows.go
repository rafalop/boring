//go:build windows

package vpnd

import (
	"fmt"
	"net"
)

// Windows routing (WinTun addressing, route table via netsh/route.exe or
// an equivalent Go API) is not implemented -- this stub only exists to
// keep the package buildable on Windows. None of it is ever reached:
// HasPrivileges always returns false on Windows, so the daemon refuses
// to start first.

func linkUp(ifName string) error {
	return fmt.Errorf("boring-vpn does not support Windows yet")
}

func addRoute(ifName string, dest *net.IPNet) error {
	return fmt.Errorf("boring-vpn does not support Windows yet")
}

func delRoute(ifName string, dest *net.IPNet) error {
	return fmt.Errorf("boring-vpn does not support Windows yet")
}

func currentRoute(ip net.IP) (originalRoute, error) {
	return originalRoute{}, fmt.Errorf("boring-vpn does not support Windows yet")
}

func addRouteVia(dest *net.IPNet, via originalRoute) error {
	return fmt.Errorf("boring-vpn does not support Windows yet")
}

func delRouteVia(dest *net.IPNet, via originalRoute) error {
	return fmt.Errorf("boring-vpn does not support Windows yet")
}

func removeInterface(ifName string) error {
	return nil
}
