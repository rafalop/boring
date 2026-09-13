package vpnd

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/alebeck/boring/internal/log"
	"github.com/alebeck/boring/internal/ssh_config"
	"golang.org/x/crypto/ssh"
)

const (
	initReconnectWait = 500 * time.Millisecond
	maxReconnectWait  = 1 * time.Minute
	reconnectTimeout  = 15 * time.Minute
)

// resolveHops resolves the ssh_config hop chain for host, exactly the
// way Session.prepare() did before this was factored out: it's used
// both when connecting directly in this process (the non-sudo path) and
// inside the privilege-dropped SSH helper (the sudo path), which calls
// it as the real invoking user so it naturally resolves that user's own
// ~/.ssh/config, known_hosts, identity files and ssh-agent.
func resolveHops(host, user, port, identityFile string) ([]ssh_config.Hop, error) {
	sc, err := ssh_config.ParseSSHConfig(host, user)
	if err != nil {
		return nil, fmt.Errorf("could not parse SSH config: %v", err)
	}

	if user != "" {
		sc.User = user
	}
	if port != "" {
		if sc.Port, err = strconv.Atoi(port); err != nil {
			return nil, fmt.Errorf("invalid port %q", port)
		}
	}
	if identityFile != "" {
		sc.IdentityFiles = []string{identityFile}
	}
	if sc.HostName == "" {
		sc.HostName = host
	}
	sc.EnsureUser()

	return sc.ToHops()
}

// localConn holds a live *ssh.Client in this process, dialing through
// hops[0], then hops[1] through hops[0]'s connection, and so on, exactly
// like `ssh -J`. It manages its own keepalive and reports disconnection
// by closing the channel returned from disconnected.
type localConn struct {
	name      string // for logging only
	client    *ssh.Client
	disconn   chan struct{}
	keepAlive *int
	wg        sync.WaitGroup
}

// dialLocal establishes hops[0..n] in order, each subsequent hop dialed
// through the previous one's connection.
func dialLocal(name string, hops []ssh_config.Hop, keepAlive *int) (*localConn, error) {
	if len(hops) == 0 {
		return nil, fmt.Errorf("no connections specified")
	}

	var c *ssh.Client
	var wg sync.WaitGroup

	for _, j := range hops {
		addr := fmt.Sprintf("%v:%v", j.HostName, j.Port)
		n, err := wrapClient(c, addr, j.ClientConfig)
		if err != nil {
			safeClose(c)
			// Wait for all connections established until here to close
			wg.Wait()
			return nil, fmt.Errorf("could not connect to host %v: %v", addr, err)
		}
		log.Debugf("%v: connected to host %v (client %p)", name, j.HostName, n)

		wg.Add(1)
		go func(n, c *ssh.Client) {
			defer wg.Done()
			n.Wait()
			log.Debugf("%v: closed client %p to %v", name, n, n.RemoteAddr())
			// Close previous client when new one closes, this propagates
			safeClose(c)
		}(n, c)

		c = n
	}

	lc := &localConn{name: name, client: c, disconn: make(chan struct{}), keepAlive: keepAlive}
	go func() {
		c.Wait()
		close(lc.disconn)
	}()
	// Wait for all wrapped clients to close in case of disconnection
	lc.wg.Add(1)
	go func() { defer lc.wg.Done(); wg.Wait() }()
	lc.wg.Add(1)
	go func() { defer lc.wg.Done(); lc.runKeepAlive() }()

	return lc, nil
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

func safeClose(c *ssh.Client) {
	if c != nil {
		c.Close()
	}
}

func (lc *localConn) runKeepAlive() {
	// panics if nil, this should never happen
	interv := *lc.keepAlive

	if interv == 0 {
		log.Infof("%v: disabling keep-alives since set to 0", lc.name)
		return
	}

	for {
		select {
		case <-lc.disconn:
			return
		case <-time.After(time.Duration(interv) * time.Second):
			_, _, err := lc.client.SendRequest("keepalive@golang.org", true, nil)
			if err != nil {
				log.Errorf("%v: error sending keepalive: %v", lc.name, err)
				// Close the client, this triggers the reconnection logic
				lc.client.Close()
				return
			}
			log.Debugf("%v: sent keep-alive", lc.name)
		}
	}
}

func (lc *localConn) dial(network, addr string) (net.Conn, error) {
	return lc.client.Dial(network, addr)
}

func (lc *localConn) disconnected() <-chan struct{} {
	return lc.disconn
}

func (lc *localConn) close() {
	lc.client.Close()
	lc.wg.Wait()
}
