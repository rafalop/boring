//go:build linux || darwin

package vpnd

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/alebeck/boring/internal/log"
	"github.com/alebeck/boring/internal/vpn"
)

const dialTimeout = 30 * time.Second

// remoteConn hands SSH connection setup off to a privilege-dropped child
// process running as the invoking (sudo) user, so it naturally resolves
// that user's ~/.ssh/config, known_hosts, identity files and ssh-agent
// -- see sshhelper.go, which implements the child side of this. Every
// dialed connection is relayed back to this process over the control
// socket via file-descriptor passing, so the actual data plane still
// runs entirely in this (root) process, exactly as with localConn.
type remoteConn struct {
	name string
	cmd  *exec.Cmd
	fc   *frameConn

	mu      sync.Mutex
	pending map[uint64]chan dialOutcome
	nextID  atomic.Uint64

	disconn chan struct{}
	once    sync.Once
}

type dialOutcome struct {
	conn net.Conn
	err  error
}

// dialRemote spawns the SSH helper as su and connects it to req.Host.
func dialRemote(su invokingUser, req dialRemoteRequest) (*remoteConn, error) {
	ex, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("could not determine executable path: %v", err)
	}

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("could not create control socket: %v", err)
	}
	parentFile := os.NewFile(uintptr(fds[0]), "vpn-ssh-helper-parent")
	childFile := os.NewFile(uintptr(fds[1]), "vpn-ssh-helper-child")

	parentFC, err := net.FileConn(parentFile)
	parentFile.Close()
	if err != nil {
		childFile.Close()
		return nil, fmt.Errorf("could not wrap control socket: %v", err)
	}
	ctrl, ok := parentFC.(*net.UnixConn)
	if !ok {
		parentFC.Close()
		childFile.Close()
		return nil, fmt.Errorf("control socket is not a unix connection")
	}
	fc := newFrameConn(ctrl)

	cmd := exec.Command(ex, SSHHelperFlag)
	cmd.ExtraFiles = []*os.File{childFile}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: su.Uid, Gid: su.Gid}}
	cmd.Env = helperEnv(su)
	// Append to the daemon's own log file, so a helper crash is visible
	// instead of silently lost -- the daemon's own stderr isn't a real
	// terminal either (it was launched detached), so there's nowhere
	// better to put this.
	if logFile, err := os.OpenFile(vpn.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); err == nil {
		cmd.Stderr = logFile
		defer logFile.Close()
	}

	if err := cmd.Start(); err != nil {
		ctrl.Close()
		childFile.Close()
		return nil, fmt.Errorf("could not start SSH helper as %s: %v", su.Username, err)
	}
	// The child has its own dup of this via ExtraFiles now; holding our
	// copy open too would stop us from ever seeing EOF on ctrl if the
	// child exits, since that end of the pair wouldn't actually be fully
	// closed.
	childFile.Close()
	log.Debugf("%v: spawned SSH helper (pid %d) as %s", req.Name, cmd.Process.Pid, su.Username)

	rc := &remoteConn{
		name:    req.Name,
		cmd:     cmd,
		fc:      fc,
		pending: make(map[uint64]chan dialOutcome),
		disconn: make(chan struct{}),
	}

	go func() {
		cmd.Wait()
		rc.once.Do(func() { close(rc.disconn) })
	}()

	if err := fc.writeMsg(connectRequest{
		Name: req.Name, Host: req.Host, User: req.User, Port: req.Port,
		IdentityFile: req.IdentityFile, KeepAlive: req.KeepAlive,
	}, -1); err != nil {
		rc.close()
		return nil, fmt.Errorf("could not send connect request to SSH helper: %v", err)
	}

	raw, err := fc.readFrame()
	if err != nil {
		rc.close()
		return nil, fmt.Errorf("could not read connect response from SSH helper: %v", err)
	}
	resp, err := decode[connectResponse](raw)
	if err != nil {
		rc.close()
		return nil, err
	}
	if !resp.OK {
		rc.close()
		return nil, fmt.Errorf("%s", resp.Error)
	}

	go rc.readLoop()

	return rc, nil
}

// helperEnv builds a minimal, correct environment for the SSH helper --
// running as su, not the daemon's own (root, sudo-mangled) environment.
func helperEnv(su invokingUser) []string {
	env := []string{
		"HOME=" + su.HomeDir,
		"USER=" + su.Username,
		"LOGNAME=" + su.Username,
		"PATH=/usr/local/bin:/usr/bin:/bin",
	}
	// Best-effort: if the daemon's own environment happens to carry these
	// (e.g. a non-default sudoers env_keep), pass them through -- root
	// can otherwise access another user's ssh-agent socket over the
	// Unix DAC bypass just fine, it just needs to know its path, which
	// sudo does not preserve or expose by default.
	for _, k := range []string{"SSH_AUTH_SOCK", "BORING_SSH_CONFIG", "DEBUG"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func (rc *remoteConn) readLoop() {
	for {
		raw, err := rc.fc.readFrame()
		if err != nil {
			rc.failPending(err)
			return
		}
		resp, err := decode[dialResponse](raw)
		if err != nil {
			log.Errorf("%v: %v", rc.name, err)
			continue
		}

		rc.mu.Lock()
		ch, ok := rc.pending[resp.ID]
		if ok {
			delete(rc.pending, resp.ID)
		}
		rc.mu.Unlock()

		if !resp.OK {
			if ok {
				ch <- dialOutcome{err: fmt.Errorf("%s", resp.Error)}
			}
			continue
		}

		fd, hasFD := rc.fc.popFD()
		if !ok {
			// No one is waiting for this response (e.g. it timed out
			// already); still need to drain its fd so it doesn't leak.
			if hasFD {
				syscall.Close(fd)
			}
			continue
		}
		if !hasFD {
			ch <- dialOutcome{err: fmt.Errorf("SSH helper did not attach a connection")}
			continue
		}
		f := os.NewFile(uintptr(fd), "vpn-dial")
		c, err := net.FileConn(f)
		f.Close()
		if err != nil {
			ch <- dialOutcome{err: fmt.Errorf("could not wrap dialed connection: %v", err)}
			continue
		}
		ch <- dialOutcome{conn: c}
	}
}

// failPending unblocks every in-flight dial() call once the control
// connection is gone, rather than making each one wait out its own
// timeout.
func (rc *remoteConn) failPending(err error) {
	rc.mu.Lock()
	pending := rc.pending
	rc.pending = make(map[uint64]chan dialOutcome)
	rc.mu.Unlock()
	for _, ch := range pending {
		ch <- dialOutcome{err: fmt.Errorf("SSH helper connection lost: %v", err)}
	}
}

func (rc *remoteConn) dial(network, addr string) (net.Conn, error) {
	id := rc.nextID.Add(1)
	ch := make(chan dialOutcome, 1)

	rc.mu.Lock()
	rc.pending[id] = ch
	rc.mu.Unlock()

	if err := rc.fc.writeMsg(dialRequest{ID: id, Network: network, Addr: addr}, -1); err != nil {
		rc.mu.Lock()
		delete(rc.pending, id)
		rc.mu.Unlock()
		return nil, fmt.Errorf("could not send dial request to SSH helper: %v", err)
	}

	select {
	case out := <-ch:
		return out.conn, out.err
	case <-time.After(dialTimeout):
		rc.mu.Lock()
		delete(rc.pending, id)
		rc.mu.Unlock()
		return nil, fmt.Errorf("timed out waiting for SSH helper to dial %s", addr)
	case <-rc.disconn:
		return nil, fmt.Errorf("SSH helper is gone")
	}
}

func (rc *remoteConn) disconnected() <-chan struct{} {
	return rc.disconn
}

func (rc *remoteConn) close() {
	rc.fc.c.Close()
	select {
	case <-rc.disconn:
	case <-time.After(5 * time.Second):
		log.Warningf("%v: SSH helper did not exit in time, killing it", rc.name)
		if rc.cmd.Process != nil {
			rc.cmd.Process.Kill()
		}
		<-rc.disconn
	}
}

var _ conn = (*remoteConn)(nil)
