package vpnd

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/alebeck/boring/internal/log"
	"github.com/alebeck/boring/internal/ssh_config"
	"github.com/alebeck/boring/internal/vpn"
	"golang.org/x/crypto/ssh"
)

const (
	initReconnectWait = 500 * time.Millisecond
	maxReconnectWait  = 1 * time.Minute
	reconnectTimeout  = 15 * time.Minute
)

// Session is a representation internal to the vpnd package, describing
// a VPN session that is running or about to be run.
type Session struct {
	prepared       bool
	hops           []ssh_config.Hop
	subnets        []*net.IPNet
	excludeSubnets []*net.IPNet
	client         *ssh.Client
	data           *dataPlane
	stop           chan struct{}
	Closed         chan struct{}
	wg             sync.WaitGroup
	*vpn.Desc
}

func FromDesc(desc *vpn.Desc) *Session {
	return &Session{Desc: desc}
}

func (s *Session) Open() (err error) {
	if !s.prepared {
		if err = s.prepare(); err != nil {
			return err
		}
	}

	if err = s.makeClient(); err != nil {
		return err
	}
	log.Debugf("%v: connected to server", s.Name)

	data, err := newDataPlane(tunName(s.Name), s.MTU, s.dial)
	if err != nil {
		s.client.Close()
		return fmt.Errorf("could not set up data plane: %v", err)
	}
	if err = data.addRoutes(s.subnets, s.excludeSubnets); err != nil {
		data.Close()
		s.client.Close()
		return fmt.Errorf("could not set up routes: %v", err)
	}
	s.data = data

	state := sessionState{Name: s.Name, PID: os.Getpid(), Iface: data.ifName, Excludes: data.excludeRouteRecords()}
	if err := writeState(state); err != nil {
		log.Warningf("%v: could not write crash-recovery state: %v", s.Name, err)
	}

	if s.stop == nil {
		s.stop = make(chan struct{})
		s.Closed = make(chan struct{})
	}

	go s.run()

	log.Infof("%v: vpn is up (%s, routing %s)", s.Name, data.ifName, s.Subnets)
	s.Status = vpn.Open
	s.LastConn = time.Now()
	return
}

func (s *Session) prepare() error {
	// We need to pass the user as it's needed for matching Match blocks
	sc, err := ssh_config.ParseSSHConfig(s.Host, s.User)
	if err != nil {
		return fmt.Errorf("could not parse SSH config: %v", err)
	}

	// Override values manually set by user
	if s.User != "" {
		sc.User = s.User
	}
	if s.Port != "" {
		if sc.Port, err = strconv.Atoi(s.Port.String()); err != nil {
			return fmt.Errorf("invalid port %q", s.Port)
		}
	}
	if s.IdentityFile != "" {
		sc.IdentityFiles = []string{s.IdentityFile}
	}

	// If s.Host could not be resolved from ssh config, take it literally
	if sc.HostName == "" {
		sc.HostName = s.Host
	}

	sc.EnsureUser()

	// Infer series of hops from ssh config
	if s.hops, err = sc.ToHops(); err != nil {
		return err
	}

	if s.subnets, err = parseCIDRs(s.Subnets); err != nil {
		return fmt.Errorf("subnets: %v", err)
	}
	if len(s.subnets) == 0 {
		return fmt.Errorf("vpn requires at least one subnet")
	}
	if s.excludeSubnets, err = parseCIDRs(s.ExcludeSubnets); err != nil {
		return fmt.Errorf("exclude_subnets: %v", err)
	}

	s.prepared = true
	return nil
}

func parseCIDRs(specs []string) ([]*net.IPNet, error) {
	nets := make([]*net.IPNet, 0, len(specs))
	for _, spec := range specs {
		_, n, err := net.ParseCIDR(spec)
		if err != nil {
			return nil, fmt.Errorf("invalid subnet %q: %v", spec, err)
		}
		nets = append(nets, n)
	}
	return nets, nil
}

func (s *Session) makeClient() error {
	if len(s.hops) == 0 {
		return fmt.Errorf("no connections specified")
	}

	var c *ssh.Client
	var wg sync.WaitGroup

	// Connect through all jump hosts
	for _, j := range s.hops {
		addr := fmt.Sprintf("%v:%v", j.HostName, j.Port)
		n, err := wrapClient(c, addr, j.ClientConfig)
		if err != nil {
			safeClose(c)
			// Wait for all connections established until here to close
			wg.Wait()
			return fmt.Errorf("could not connect to host %v: %v", addr, err)
		}
		log.Debugf("%v: connected to host %v (client %p)", s.Name, j.HostName, n)

		// Add new client to wait group
		wg.Add(1)
		go func(n, c *ssh.Client) {
			defer wg.Done()
			n.Wait()
			log.Debugf("%v: closed client %p to %v", s.Name, n, n.RemoteAddr())
			// Close previous client when new one closes, this propagates
			safeClose(c)
		}(n, c)

		c = n
	}

	// Wait for all wrapped clients to close in case of session stopping or reconnection
	go s.waitFor(func() { wg.Wait() })

	s.client = c
	return nil
}

func wrapClient(old *ssh.Client, addr string, conf *ssh.ClientConfig) (*ssh.Client, error) {
	if old == nil {
		return ssh.Dial("tcp", addr, conf)
	}

	conn, err := old.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}

	ncc, chans, reqs, err := ssh.NewClientConn(conn, addr, conf)
	if err != nil {
		return nil, err
	}

	return ssh.NewClient(ncc, chans, reqs), nil
}

// dial opens a connection to addr through the SSH connection, i.e. a
// "direct-tcpip" channel -- the same mechanism `ssh -L` uses. This is
// how captured traffic ultimately reaches the remote network once the
// data plane (TUN device + netstack) is wired in.
func (s *Session) dial(network, addr string) (net.Conn, error) {
	return s.client.Dial(network, addr)
}

func (s *Session) run() {
	disconn := make(chan struct{})
	go func() {
		s.client.Wait()
		close(disconn)
	}()

	go s.waitFor(func() { s.keepAlive(disconn) })

	stopped := false
	select {
	case <-s.stop:
		log.Infof("%v: received stop signal", s.Name)
		stopped = true
		s.client.Close()
	case <-disconn:
	}

	// Routes and the TUN device/netstack must go before the SSH client:
	// this aborts in-flight connections, which unblocks any splice()
	// still dialing or copying through it.
	s.data.Close()
	removeState(s.Name)
	s.client.Close()
	s.wg.Wait()
	if !stopped {
		if err := s.reconnectLoop(); err == nil {
			// Successfully re-connected
			return
		}
	}
	s.Status = vpn.Closed
	close(s.Closed)
}

func (s *Session) keepAlive(cancel chan struct{}) {
	// panics if nil, this should never happen
	interv := *s.KeepAlive

	if interv == 0 {
		log.Infof("%v: disabling keep-alives since set to 0", s.Name)
		return
	}

	for {
		select {
		case <-cancel:
			return
		case <-time.After(time.Duration(interv) * time.Second):
			_, _, err := s.client.SendRequest("keepalive@golang.org", true, nil)
			if err != nil {
				log.Errorf("%v: error sending keepalive: %v", s.Name, err)
				// Close the client, this triggers the reconnection logic
				s.client.Close()
				return
			}
			log.Debugf("%v: sent keep-alive", s.Name)
		}
	}
}

func (s *Session) reconnectLoop() error {
	s.Status = vpn.Reconn
	timeout := time.After(reconnectTimeout)
	wait := time.NewTimer(2 * time.Millisecond) // First time try (essent.) immediately
	waitTime := initReconnectWait

	for {
		select {
		case <-timeout:
			return fmt.Errorf("re-connect timeout")
		case <-s.stop:
			return fmt.Errorf("re-connect interrupted by stop signal")
		case <-wait.C:
			log.Infof("%v: try re-connect...", s.Name)
			err := s.Open()
			if err == nil {
				return nil
			}
			log.Errorf("%v: could not re-connect: %v. Retrying in %v...",
				s.Name, err, waitTime)
			wait.Reset(waitTime)
			waitTime *= 2
			if waitTime > maxReconnectWait {
				waitTime = maxReconnectWait
			}
		}
	}
}

func (s *Session) Close() error {
	if s.Status == vpn.Closed {
		return fmt.Errorf("trying to stop a stopped vpn")
	}
	close(s.stop)
	return nil
}

// Logic registered with waitFor will be waited for upon the session
// stopping and reconnecting.
func (s *Session) waitFor(f func()) {
	s.wg.Add(1)
	defer s.wg.Done()
	f()
}

func safeClose(c *ssh.Client) {
	if c != nil {
		c.Close()
	}
}
