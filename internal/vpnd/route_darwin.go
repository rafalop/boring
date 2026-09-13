//go:build darwin

package vpnd

import (
	"fmt"
	"hash/fnv"
	"net"
	"os/exec"
	"strings"
)

// p2pAddrs derives a deterministic, likely-unique point-to-point address
// pair from the interface name. utun devices are strictly
// point-to-point and refuse to come up without a local/peer address
// pair, which boring-vpn otherwise has no use for. Addresses are drawn
// from the CGNAT range (100.64.0.0/10), set aside for carrier-grade NAT
// and unlikely to collide with real routed traffic.
func p2pAddrs(ifName string) (local, peer net.IP) {
	h := fnv.New32a()
	h.Write([]byte(ifName))
	v := h.Sum32()
	b1 := 64 + byte(v>>16)&0x3f // keep within 100.64.0.0-100.127.255.255
	b2 := byte(v >> 8)
	b3 := byte(v) &^ 1 // even, so b3/b3+1 forms a matched pair
	return net.IPv4(100, b1, b2, b3), net.IPv4(100, b1, b2, b3+1)
}

func linkUp(ifName string) error {
	local, peer := p2pAddrs(ifName)
	return run("ifconfig", ifName, local.String(), peer.String(), "up")
}

func addRoute(ifName string, dest *net.IPNet) error {
	return run("route", "add", "-net", dest.String(), "-interface", ifName)
}

func delRoute(ifName string, dest *net.IPNet) error {
	return run("route", "delete", "-net", dest.String(), "-interface", ifName)
}

// currentRoute shells out to `route get`, parsing its text output --
// there's no netlink equivalent on BSD without a much larger dependency.
func currentRoute(ip net.IP) (originalRoute, error) {
	out, err := exec.Command("route", "get", ip.String()).CombinedOutput()
	if err != nil {
		return originalRoute{}, fmt.Errorf("route get %v: %v: %s", ip, err, out)
	}
	var iface, gateway string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "interface:"); ok {
			iface = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, "gateway:"); ok {
			gateway = strings.TrimSpace(v)
		}
	}
	if iface == "" {
		return originalRoute{}, fmt.Errorf("could not parse interface from `route get %v` output", ip)
	}
	return originalRoute{Interface: iface, Gateway: net.ParseIP(gateway)}, nil
}

// addRouteVia adds a route for dest through the interface and gateway
// recorded in via, so it takes precedence (by longest-prefix-match) over
// a broader vpn subnet route that would otherwise also match dest.
func addRouteVia(dest *net.IPNet, via originalRoute) error {
	if via.Gateway != nil {
		return run("route", "add", "-net", dest.String(), via.Gateway.String())
	}
	return run("route", "add", "-net", dest.String(), "-interface", via.Interface)
}

// delRouteVia removes a route previously added by addRouteVia.
func delRouteVia(dest *net.IPNet, via originalRoute) error {
	if via.Gateway != nil {
		return run("route", "delete", "-net", dest.String(), via.Gateway.String())
	}
	return run("route", "delete", "-net", dest.String(), "-interface", via.Interface)
}

// removeInterface destroys ifName if it still exists. It's expected to
// usually be a no-op during crash recovery: a utun device disappears on
// its own once the process that created it dies. It's only here as a
// safety net for the case where it somehow didn't.
func removeInterface(ifName string) error {
	if err := exec.Command("ifconfig", ifName).Run(); err != nil {
		return nil // already gone, the common case
	}
	return run("ifconfig", ifName, "destroy")
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, out)
	}
	return nil
}
