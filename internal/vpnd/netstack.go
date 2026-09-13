package vpnd

import (
	"context"
	"fmt"
	"hash/fnv"
	"net"
	"strconv"
	"sync"

	"github.com/alebeck/boring/internal/log"
	"golang.zx2c4.com/wireguard/tun"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/waiter"
)

const (
	nicID          = tcpip.NICID(1)
	defaultMTU     = 1420
	channelSize    = 512
	tcpMaxInFlight = 1024
	// packetBufSize is sized generously above any MTU we'd realistically
	// configure, since a TUN device delivers whole, unfragmented-by-us IP
	// datagrams and we don't use any segmentation offload.
	packetBufSize = 1 << 16
	// tunOffset reserves space for the virtio-net header the kernel's TUN
	// driver prepends/expects when NIC offload negotiation is on -- see
	// wireguard-go's device.MessageTransportHeaderSize, which reserves
	// the same 16 bytes for the same reason and is the reference for
	// this value. Reserving it unconditionally is harmless when offload
	// isn't active.
	tunOffset = 16
)

// DialFunc opens a connection to addr, tunneled over SSH. It is called
// once per TCP connection captured from the TUN device.
type DialFunc func(network, addr string) (net.Conn, error)

// dataPlane owns a TUN device and the userspace network stack that
// terminates TCP connections captured from it, forwarding each one
// through dial -- an SSH "direct-tcpip" channel, the same mechanism
// `ssh -L` uses. It never forwards raw IP packets to the remote side;
// only fully-terminated TCP byte streams are tunneled.
type dataPlane struct {
	dev    tun.Device
	ifName string
	dial   DialFunc

	stack    *stack.Stack
	endpoint *channel.Endpoint

	cancel context.CancelFunc
	wg     sync.WaitGroup

	routes        []*net.IPNet
	excludeRoutes []excludeRoute
}

// newDataPlane creates a TUN device and wires a gVisor userspace network
// stack to it, ready to have routes added via addRoutes.
func newDataPlane(name string, mtu int, dial DialFunc) (dp *dataPlane, err error) {
	if mtu <= 0 {
		mtu = defaultMTU
	}

	dev, err := createTUN(name, mtu)
	if err != nil {
		return nil, fmt.Errorf("could not create TUN device: %v", err)
	}
	defer func() {
		if err != nil {
			dev.Close()
		}
	}()

	ifName, err := dev.Name()
	if err != nil {
		return nil, fmt.Errorf("could not determine TUN device name: %v", err)
	}
	if actual, err := dev.MTU(); err == nil && actual > 0 {
		mtu = actual
	}

	ep := channel.New(channelSize, uint32(mtu), "")

	st, err := buildStack(ep, ifName, dial)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			st.Destroy()
		}
	}()

	dp = &dataPlane{dev: dev, ifName: ifName, dial: dial, stack: st, endpoint: ep}

	ctx, cancel := context.WithCancel(context.Background())
	dp.cancel = cancel
	dp.wg.Add(2)
	go dp.pumpFromTUN(ctx)
	go dp.pumpToTUN(ctx)

	return dp, nil
}

// buildStack wires up the gVisor userspace network stack shared by real
// sessions and tests alike: a single NIC bound to ep, in promiscuous and
// spoofing mode so it can act as a router for arbitrary subnets instead
// of only ever answering for its own assigned address, with a TCP
// forwarder that dials every accepted connection via dial. Tests bind ep
// to an in-memory link (see gvisor.dev/gvisor/pkg/tcpip/link/pipe)
// instead of a real TUN device to drive this same code without needing
// root or a kernel network interface.
func buildStack(ep stack.LinkEndpoint, ifName string, dial DialFunc) (*stack.Stack, error) {
	st := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})

	if err := st.CreateNICWithOptions(nicID, ep, stack.NICOptions{Name: ifName}); err != nil {
		st.Destroy()
		return nil, fmt.Errorf("could not create NIC: %v", err)
	}
	if err := st.SetPromiscuousMode(nicID, true); err != nil {
		st.Destroy()
		return nil, fmt.Errorf("could not set promiscuous mode: %v", err)
	}
	if err := st.SetSpoofing(nicID, true); err != nil {
		st.Destroy()
		return nil, fmt.Errorf("could not set address spoofing: %v", err)
	}
	st.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: nicID},
		{Destination: header.IPv6EmptySubnet, NIC: nicID},
	})

	fwd := tcp.NewForwarder(st, 0, tcpMaxInFlight, func(r *tcp.ForwarderRequest) { handleTCP(r, dial) })
	st.SetTransportProtocolHandler(tcp.ProtocolNumber, fwd.HandlePacket)

	return st, nil
}

// tunName derives a short, likely-unique interface name from a vpn
// session name, since interface names are limited to 15 characters on
// Linux and must be safe regardless of what characters the vpn name
// contains.
func tunName(sessionName string) string {
	h := fnv.New32a()
	h.Write([]byte(sessionName))
	base := sessionName
	if len(base) > 6 {
		base = base[:6]
	}
	return fmt.Sprintf("bvpn%s%x", base, h.Sum32()&0xffff)
}

// pumpFromTUN reads raw IP packets from the TUN device and injects them
// into the network stack as inbound traffic.
func (dp *dataPlane) pumpFromTUN(ctx context.Context) {
	defer dp.wg.Done()

	batch := dp.dev.BatchSize()
	if batch < 1 {
		batch = 1
	}
	bufs := make([][]byte, batch)
	sizes := make([]int, batch)
	for i := range bufs {
		bufs[i] = make([]byte, tunOffset+packetBufSize)
	}

	for {
		n, err := dp.dev.Read(bufs, sizes, tunOffset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Errorf("vpn: could not read from TUN device: %v", err)
			return
		}
		for i := 0; i < n; i++ {
			pktBytes := bufs[i][tunOffset : tunOffset+sizes[i]]
			if len(pktBytes) == 0 {
				continue
			}
			var proto tcpip.NetworkProtocolNumber
			switch header.IPVersion(pktBytes) {
			case 4:
				proto = header.IPv4ProtocolNumber
			case 6:
				proto = header.IPv6ProtocolNumber
			default:
				continue
			}
			// Copy out of the read buffer, which is reused on the next Read.
			data := make([]byte, len(pktBytes))
			copy(data, pktBytes)
			pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
				Payload: buffer.MakeWithData(data),
			})
			dp.endpoint.InjectInbound(proto, pkt)
			pkt.DecRef()
		}
	}
}

// pumpToTUN reads packets the stack produced (replies, or anything else
// routed out via our NIC) and writes them back out the TUN device, where
// the kernel delivers them to the process that "sent" the original
// request as if the remote host had replied directly.
func (dp *dataPlane) pumpToTUN(ctx context.Context) {
	defer dp.wg.Done()

	buf := make([]byte, tunOffset+packetBufSize)
	for {
		pkt := dp.endpoint.ReadContext(ctx)
		if pkt == nil {
			return
		}
		view := pkt.ToView()
		data := view.AsSlice()
		if tunOffset+len(data) > len(buf) {
			buf = make([]byte, tunOffset+len(data))
		}
		n := copy(buf[tunOffset:], data)
		if _, err := dp.dev.Write([][]byte{buf[:tunOffset+n]}, tunOffset); err != nil {
			log.Errorf("vpn: could not write to TUN device: %v", err)
		}
		view.Release()
		pkt.DecRef()
	}
}

// handleTCP is called by the tcp.Forwarder for every inbound SYN. The
// request's destination is whatever the original application was trying
// to reach, taken from the routed (not necessarily NIC-owned) address --
// that's what we dial through the SSH connection.
func handleTCP(r *tcp.ForwarderRequest, dial DialFunc) {
	id := r.ID()
	dst := net.JoinHostPort(id.LocalAddress.String(), strconv.Itoa(int(id.LocalPort)))

	var wq waiter.Queue
	ep, tcpErr := r.CreateEndpoint(&wq)
	if tcpErr != nil {
		log.Debugf("vpn: could not accept connection to %v: %v", dst, tcpErr)
		r.Complete(true)
		return
	}
	r.Complete(false)

	local := gonet.NewTCPConn(&wq, ep)

	remote, err := dial("tcp", dst)
	if err != nil {
		log.Debugf("vpn: could not dial %v: %v", dst, err)
		local.Close()
		return
	}

	splice(local, remote)
}

// addRoutes brings the TUN interface up and adds the routes needed for
// subnets to be carried through it, plus a more specific route for each
// excludeSubnets back out whatever the original route for it was --
// this relies on longest-prefix-match to win over the broader included
// route, rather than true CIDR-set subtraction.
func (dp *dataPlane) addRoutes(subnets, excludes []*net.IPNet) error {
	if err := linkUp(dp.ifName); err != nil {
		return fmt.Errorf("could not bring up interface %v: %v", dp.ifName, err)
	}

	// Capture the pre-existing route for each excluded subnet before
	// adding any of our own -- an exclude is typically a more specific
	// range inside a subnet we're about to route through the tunnel.
	type pendingExclude struct {
		dest *net.IPNet
		via  originalRoute
	}
	var pending []pendingExclude
	for _, ex := range excludes {
		orig, err := currentRoute(ex.IP)
		if err != nil {
			log.Warningf("vpn: could not determine original route for excluded subnet %v, skipping: %v", ex, err)
			continue
		}
		pending = append(pending, pendingExclude{ex, orig})
	}

	for _, sn := range subnets {
		if err := addRoute(dp.ifName, sn); err != nil {
			return fmt.Errorf("could not add route for %v: %v", sn, err)
		}
		dp.routes = append(dp.routes, sn)
	}

	for _, p := range pending {
		if err := addRouteVia(p.dest, p.via); err != nil {
			log.Warningf("vpn: could not add exclude route for %v: %v", p.dest, err)
			continue
		}
		dp.excludeRoutes = append(dp.excludeRoutes, excludeRoute{Dest: p.dest, Via: p.via})
	}

	return nil
}

func (dp *dataPlane) removeRoutes() {
	for _, sn := range dp.routes {
		if err := delRoute(dp.ifName, sn); err != nil {
			log.Warningf("vpn: could not remove route for %v: %v", sn, err)
		}
	}
	for _, ex := range dp.excludeRoutes {
		if err := delRouteVia(ex.Dest, ex.Via); err != nil {
			log.Warningf("vpn: could not remove exclude route for %v: %v", ex.Dest, err)
		}
	}
}

// excludeRouteRecords returns a serializable snapshot of the exclude
// routes currently applied, for crash-recovery state.
func (dp *dataPlane) excludeRouteRecords() []routeRecord {
	if len(dp.excludeRoutes) == 0 {
		return nil
	}
	recs := make([]routeRecord, 0, len(dp.excludeRoutes))
	for _, ex := range dp.excludeRoutes {
		rec := routeRecord{Dest: ex.Dest.String(), Interface: ex.Via.Interface}
		if ex.Via.Gateway != nil {
			rec.Gateway = ex.Via.Gateway.String()
		}
		recs = append(recs, rec)
	}
	return recs
}

// Close tears the data plane down in the order that's safe: routes
// first (so nothing new gets sent into a soon-to-be-gone tunnel), then
// the netstack and TUN device.
func (dp *dataPlane) Close() {
	dp.removeRoutes()
	dp.cancel()
	dp.dev.Close() // unblocks the blocking Read in pumpFromTUN
	dp.wg.Wait()
	dp.stack.Destroy()
}
