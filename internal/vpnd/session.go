package vpnd

import (
	"fmt"
	"net"
	"os"
	"time"

	"github.com/alebeck/boring/internal/log"
	"github.com/alebeck/boring/internal/vpn"
)

// Session is a representation internal to the vpnd package, describing
// a VPN session that is running or about to be run.
type Session struct {
	prepared       bool
	subnets        []*net.IPNet
	excludeSubnets []*net.IPNet
	conn           conn
	data           *dataPlane
	stop           chan struct{}
	Closed         chan struct{}
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
		s.conn.close()
		return fmt.Errorf("could not set up data plane: %v", err)
	}
	if err = data.addRoutes(s.subnets, s.excludeSubnets); err != nil {
		data.Close()
		s.conn.close()
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
	if _, isSudo := detectSudoUser(); !isSudo {
		// We're not running as root via sudo -- either a genuine root
		// login or an unprivileged process (which will fail the
		// privilege check before ever reaching this point). Either way,
		// there's no separate invoking user to hand SSH connection setup
		// off to, so resolve it here, exactly as boring's own tunnels do.
		//
		// (When we are running via sudo, resolution instead happens
		// inside the privilege-dropped SSH helper -- see makeClient.)
		if _, err := resolveHops(s.Host, s.User, s.Port.String(), s.IdentityFile); err != nil {
			return err
		}
	}

	var err error
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

// makeClient establishes the SSH connection this session forwards
// traffic through -- directly in this process if we're not running
// under sudo, or via a privilege-dropped helper (running as the
// invoking user) if we are. See conn.go for why the split exists.
func (s *Session) makeClient() error {
	if su, ok := detectSudoUser(); ok {
		c, err := dialRemote(su, dialRemoteRequest{
			Name: s.Name, Host: s.Host, User: s.User,
			Port: s.Port.String(), IdentityFile: s.IdentityFile, KeepAlive: s.KeepAlive,
		})
		if err != nil {
			return err
		}
		s.conn = c
		return nil
	}

	hops, err := resolveHops(s.Host, s.User, s.Port.String(), s.IdentityFile)
	if err != nil {
		return err
	}
	c, err := dialLocal(s.Name, hops, s.KeepAlive)
	if err != nil {
		return err
	}
	s.conn = c
	return nil
}

// dial opens a connection to addr through the SSH connection, i.e. a
// "direct-tcpip" channel -- the same mechanism `ssh -L` uses. This is
// how captured traffic ultimately reaches the remote network.
func (s *Session) dial(network, addr string) (net.Conn, error) {
	return s.conn.dial(network, addr)
}

func (s *Session) run() {
	stopped := false
	select {
	case <-s.stop:
		log.Infof("%v: received stop signal", s.Name)
		stopped = true
	case <-s.conn.disconnected():
	}

	// Routes and the TUN device/netstack must go before the SSH
	// connection: this aborts in-flight connections, which unblocks any
	// splice() still dialing or copying through it.
	s.data.Close()
	removeState(s.Name)
	s.conn.close()
	if !stopped {
		if err := s.reconnectLoop(); err == nil {
			// Successfully re-connected
			return
		}
	}
	s.Status = vpn.Closed
	close(s.Closed)
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
