//go:build linux || darwin

package vpnd

import (
	"fmt"
	"net"
	"os"
	"syscall"

	"github.com/alebeck/boring/internal/log"
)

// SSHHelperFlag, when passed as the sole argument to the boring-vpn
// binary, tells it to run as the (privilege-dropped) SSH helper child
// instead of the daemon or CLI -- see dialRemote for how it's spawned.
const SSHHelperFlag = "--ssh-helper"

// helperCtrlFD is the file descriptor the parent's control socket
// arrives on: 0, 1, 2 are stdin/stdout/stderr, so the first (and only)
// entry in exec.Cmd.ExtraFiles lands on 3.
const helperCtrlFD = 3

// RunSSHHelper is the entry point for the privilege-dropped SSH helper
// child process. It resolves and holds the SSH connection as whichever
// user it was started as (dialRemote drops privileges to the invoking
// sudo user before spawning it), then serves dial requests from the
// parent over the inherited control socket, handing back each dialed
// connection as a file descriptor.
//
// It never touches the TUN device, routes, or any other part of the vpn
// session -- only the SSH connection itself, which is the only part
// that needs to resolve ~/.ssh/config, known_hosts, identity files and
// ssh-agent correctly, which in turn is the only reason this process
// exists instead of just doing everything in the (root) daemon.
func RunSSHHelper() {
	// internal/ssh_config logs through internal/log, which panics on a
	// nil instance until Init is called -- this process never goes
	// through cmd/boring-vpn's normal initLogging, so do it here.
	// Interactive (not silent) so Warningf/Errorf/Infof actually emit,
	// wherever the parent directed our stderr to; no colors since
	// that's never a real terminal.
	log.Init(os.Stderr, true, false)

	fc, err := ctrlConn(helperCtrlFD)
	if err != nil {
		fmt.Fprintf(os.Stderr, "boring-vpn ssh helper: %v\n", err)
		os.Exit(1)
	}

	raw, err := fc.readFrame()
	if err != nil {
		fmt.Fprintf(os.Stderr, "boring-vpn ssh helper: could not read connect request: %v\n", err)
		os.Exit(1)
	}
	req, err := decode[connectRequest](raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "boring-vpn ssh helper: %v\n", err)
		os.Exit(1)
	}

	hops, err := resolveHops(req.Host, req.User, req.Port, req.IdentityFile)
	if err == nil {
		var lc *localConn
		lc, err = dialLocal(req.Name, hops, req.KeepAlive)
		if err == nil {
			log.Debugf("%v: SSH helper (uid %d) connected", req.Name, os.Getuid())
			fc.writeMsg(connectResponse{OK: true}, -1)
			serveHelper(fc, lc)
			return
		}
	}

	errMsg := err.Error()
	if os.Getenv("SSH_AUTH_SOCK") == "" {
		errMsg += ". Note: SSH_AUTH_SOCK is not set for this helper -- sudo strips it by " +
			"default and there's no way to recover it after the fact. If you use ssh-agent, " +
			"either set an explicit 'identity' in your vpn config, or re-run with " +
			"\"sudo --preserve-env=SSH_AUTH_SOCK boring-vpn up <name>\""
	}
	log.Errorf("%v: SSH helper could not connect: %v", req.Name, errMsg)
	fc.writeMsg(connectResponse{OK: false, Error: errMsg}, -1)
	os.Exit(1)
}

func ctrlConn(fd int) (*frameConn, error) {
	f := os.NewFile(uintptr(fd), "vpn-ssh-helper-ctrl")
	nc, err := net.FileConn(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("could not wrap control socket: %v", err)
	}
	c, ok := nc.(*net.UnixConn)
	if !ok {
		nc.Close()
		return nil, fmt.Errorf("control socket is not a unix connection")
	}
	return newFrameConn(c), nil
}

// serveHelper answers dial requests from the parent for as long as lc
// stays connected. Each accepted dial gets its own dedicated socketpair:
// this end is spliced to the SSH channel right here, the other end's fd
// is handed to the parent, which treats it as a plain net.Conn.
func serveHelper(fc *frameConn, lc *localConn) {
	go func() {
		<-lc.disconnected()
		// Unblocks the readFrame loop below; the parent will treat this
		// exactly like the local (non-sudo) case's disconnect: closing
		// the whole session down and reconnecting from scratch, which
		// means respawning this helper too.
		fc.c.Close()
	}()

	for {
		raw, err := fc.readFrame()
		if err != nil {
			lc.close()
			return
		}
		req, err := decode[dialRequest](raw)
		if err != nil {
			log.Errorf("SSH helper: %v", err)
			continue
		}

		go func(req dialRequest) {
			remote, err := lc.dial(req.Network, req.Addr)
			if err != nil {
				fc.writeMsg(dialResponse{ID: req.ID, OK: false, Error: err.Error()}, -1)
				return
			}

			fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
			if err != nil {
				remote.Close()
				fc.writeMsg(dialResponse{ID: req.ID, OK: false, Error: fmt.Sprintf("socketpair: %v", err)}, -1)
				return
			}
			ownFile := os.NewFile(uintptr(fds[0]), "vpn-dial-local")
			ownConn, err := net.FileConn(ownFile)
			ownFile.Close()
			if err != nil {
				remote.Close()
				syscall.Close(fds[1])
				fc.writeMsg(dialResponse{ID: req.ID, OK: false, Error: fmt.Sprintf("could not wrap local half: %v", err)}, -1)
				return
			}

			go splice(ownConn, remote)
			fc.writeMsg(dialResponse{ID: req.ID, OK: true}, fds[1])
			syscall.Close(fds[1]) // the message above already dup'd it to the parent
		}(req)
	}
}
