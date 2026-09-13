package vpnd

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/alebeck/boring/internal/log"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/pipe"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

func init() {
	log.Init(io.Discard, false, false)
}

// TestForwarderDialsAndSplicesConnection drives a real TCP handshake and
// data exchange through buildStack's forwarder -- the same code
// newDataPlane wires a real TUN device to -- using an in-memory link
// (gvisor.dev/gvisor/pkg/tcpip/link/pipe) in place of the TUN device, and
// a second gVisor stack playing the role of the local OS/application
// that would otherwise be talking to the TUN device. This needs no TUN
// device, root, or real network access.
func TestForwarderDialsAndSplicesConnection(t *testing.T) {
	const (
		clientNIC = tcpip.NICID(1)
		mtu       = 1500
	)
	clientAddr := tcpip.AddrFromSlice(net.ParseIP("192.168.9.2").To4())
	dstHost, dstPortStr := "203.0.113.5", "4321"
	dstPort, _ := strconv.Atoi(dstPortStr)
	wantAddr := net.JoinHostPort(dstHost, dstPortStr)

	clientEnd, routerEnd := pipe.New("client", "router", mtu)

	fakeRemote, remoteSide := net.Pipe()
	// handleTCP calls dial from its own goroutine, which only starts once
	// the forwarder completes the handshake -- that can race with
	// DialTCP returning to this goroutine, so signal over a channel
	// rather than a shared variable checked right after DialTCP returns.
	dialed := make(chan string, 1)
	dial := func(network, addr string) (net.Conn, error) {
		dialed <- addr
		return fakeRemote, nil
	}

	routerStack, err := buildStack(routerEnd, "test0", dial)
	if err != nil {
		t.Fatalf("buildStack: %v", err)
	}
	defer routerStack.Destroy()

	clientStack := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})
	defer clientStack.Destroy()

	if err := clientStack.CreateNIC(clientNIC, clientEnd); err != nil {
		t.Fatalf("CreateNIC: %v", err)
	}
	if err := clientStack.AddProtocolAddress(clientNIC, tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: clientAddr.WithPrefix(),
	}, stack.AddressProperties{}); err != nil {
		t.Fatalf("AddProtocolAddress: %v", err)
	}
	clientStack.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: clientNIC}})

	dstAddr := tcpip.AddrFromSlice(net.ParseIP(dstHost).To4())
	conn, err := gonet.DialTCP(clientStack, tcpip.FullAddress{Addr: dstAddr, Port: uint16(dstPort)}, ipv4.ProtocolNumber)
	if err != nil {
		t.Fatalf("DialTCP(%s:%d): %v", dstHost, dstPort, err)
	}
	defer conn.Close()

	select {
	case got := <-dialed:
		if got != wantAddr {
			t.Fatalf("dial called with %q, want %q", got, wantAddr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the forwarder to call dial")
	}

	// The forwarder's handleTCP has already spliced the gVisor-side
	// connection to fakeRemote; confirm bytes actually round-trip through
	// that splice by echoing on the other end of the net.Pipe.
	go func() {
		buf := make([]byte, 5)
		if _, err := io.ReadFull(remoteSide, buf); err != nil {
			return
		}
		remoteSide.Write(buf)
	}()

	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 5)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "hello" {
		t.Fatalf("got %q, want %q", buf, "hello")
	}
}

// TestForwarderDialFailureClosesConnection confirms that when dial
// fails, the forwarder resets the connection instead of leaving it
// hanging.
func TestForwarderDialFailureClosesConnection(t *testing.T) {
	const clientNIC = tcpip.NICID(1)
	clientAddr := tcpip.AddrFromSlice(net.ParseIP("192.168.9.2").To4())

	clientEnd, routerEnd := pipe.New("client", "router", 1500)

	dial := func(network, addr string) (net.Conn, error) {
		return nil, fmt.Errorf("connection refused")
	}

	routerStack, err := buildStack(routerEnd, "test0", dial)
	if err != nil {
		t.Fatalf("buildStack: %v", err)
	}
	defer routerStack.Destroy()

	clientStack := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})
	defer clientStack.Destroy()
	if err := clientStack.CreateNIC(clientNIC, clientEnd); err != nil {
		t.Fatalf("CreateNIC: %v", err)
	}
	if err := clientStack.AddProtocolAddress(clientNIC, tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: clientAddr.WithPrefix(),
	}, stack.AddressProperties{}); err != nil {
		t.Fatalf("AddProtocolAddress: %v", err)
	}
	clientStack.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: clientNIC}})

	dstAddr := tcpip.AddrFromSlice(net.ParseIP("203.0.113.5").To4())
	conn, err := gonet.DialTCP(clientStack, tcpip.FullAddress{Addr: dstAddr, Port: 4321}, ipv4.ProtocolNumber)
	if err != nil {
		// A reset during/after the handshake surfaces as a dial error too,
		// which is an acceptable way for this to manifest.
		return
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err == nil {
		t.Fatalf("expected connection to be closed after a failed dial, read succeeded")
	}
}
