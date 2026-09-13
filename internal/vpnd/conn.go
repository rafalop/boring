package vpnd

import "net"

// conn abstracts however the SSH connection a session forwards traffic
// through is actually held.
//
//   - localConn wraps a live *ssh.Client in this same process. Used when
//     the daemon isn't running under sudo, so there's no separate
//     invoking user to hand the connection to -- e.g. a genuine root
//     login, or a Linux setup using setcap instead of sudo. SSH config
//     resolution happens as whoever the daemon's own process is.
//   - remoteConn hands the connection off to a privilege-dropped child
//     process running as the invoking (sudo) user, so it naturally uses
//     that user's ~/.ssh/config, known_hosts, identity files and
//     ssh-agent -- see sshhelper.go. Every dialed connection is relayed
//     back to this process over a Unix socket via file-descriptor
//     passing.
type conn interface {
	// dial opens a connection to addr through the underlying SSH
	// connection, i.e. a "direct-tcpip" channel -- the same mechanism
	// `ssh -L` uses.
	dial(network, addr string) (net.Conn, error)
	// disconnected is closed when the underlying connection is lost.
	disconnected() <-chan struct{}
	// close tears down the underlying connection and waits for any of
	// its background goroutines/processes to finish.
	close()
}
