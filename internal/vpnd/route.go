package vpnd

import "net"

// originalRoute records where the kernel would send a destination before
// a vpn session started routing part of the address space through its
// TUN device -- used to carve exclude_subnets back out to wherever they
// were already going, and to reverse that again on cleanup.
type originalRoute struct {
	Interface string
	Gateway   net.IP
}

// excludeRoute is one route added for an exclude_subnets entry. It's
// recorded (interface + gateway, not the TUN device) so it can be
// removed the same way it was added -- by us on a clean stop, or by
// crash-recovery on a later daemon start after this process died
// without getting the chance.
type excludeRoute struct {
	Dest *net.IPNet
	Via  originalRoute
}
