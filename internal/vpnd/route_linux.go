//go:build linux

package vpnd

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
)

func linkUp(ifName string) error {
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("could not find interface %q: %v", ifName, err)
	}
	return netlink.LinkSetUp(link)
}

func addRoute(ifName string, dest *net.IPNet) error {
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("could not find interface %q: %v", ifName, err)
	}
	return netlink.RouteAdd(&netlink.Route{LinkIndex: link.Attrs().Index, Dst: dest})
}

func delRoute(ifName string, dest *net.IPNet) error {
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("could not find interface %q: %v", ifName, err)
	}
	return netlink.RouteDel(&netlink.Route{LinkIndex: link.Attrs().Index, Dst: dest})
}

// currentRoute returns the route the kernel currently uses to reach ip,
// before we've made any routing changes of our own.
func currentRoute(ip net.IP) (originalRoute, error) {
	routes, err := netlink.RouteGet(ip)
	if err != nil {
		return originalRoute{}, err
	}
	if len(routes) == 0 {
		return originalRoute{}, fmt.Errorf("no route found")
	}
	link, err := netlink.LinkByIndex(routes[0].LinkIndex)
	if err != nil {
		return originalRoute{}, fmt.Errorf("could not resolve interface: %v", err)
	}
	return originalRoute{Interface: link.Attrs().Name, Gateway: routes[0].Gw}, nil
}

// addRouteVia adds a route for dest through the interface and gateway
// recorded in via, so it takes precedence (by longest-prefix-match) over
// a broader vpn subnet route that would otherwise also match dest.
func addRouteVia(dest *net.IPNet, via originalRoute) error {
	link, err := netlink.LinkByName(via.Interface)
	if err != nil {
		return fmt.Errorf("could not find interface %q: %v", via.Interface, err)
	}
	return netlink.RouteAdd(&netlink.Route{LinkIndex: link.Attrs().Index, Gw: via.Gateway, Dst: dest})
}

// delRouteVia removes a route previously added by addRouteVia.
func delRouteVia(dest *net.IPNet, via originalRoute) error {
	link, err := netlink.LinkByName(via.Interface)
	if err != nil {
		return fmt.Errorf("could not find interface %q: %v", via.Interface, err)
	}
	return netlink.RouteDel(&netlink.Route{LinkIndex: link.Attrs().Index, Gw: via.Gateway, Dst: dest})
}

// removeInterface deletes ifName if it still exists. It's expected to
// usually be a no-op during crash recovery: a TUN device isn't marked
// persistent, so it disappears on its own once the process that created
// it dies. It's only here as a safety net for the case where it somehow
// didn't.
func removeInterface(ifName string) error {
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return nil // already gone, the common case
	}
	return netlink.LinkDel(link)
}
