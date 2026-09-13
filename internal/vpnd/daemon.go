package vpnd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/alebeck/boring/internal/buildinfo"
	"github.com/alebeck/boring/internal/ipc"
	"github.com/alebeck/boring/internal/log"
	"github.com/alebeck/boring/internal/vpn"
)

type daemon struct {
	ctx    context.Context
	cancel context.CancelFunc
	ln     net.Listener

	// TODO: write proper concurrent map structure for this
	sessions map[string]*Session
	mutex    sync.RWMutex

	once sync.Once
	wg   sync.WaitGroup
}

func newDaemon(parent context.Context, ln net.Listener) (*daemon, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	sessions := make(map[string]*Session)
	d := &daemon{ctx: ctx, cancel: cancel, ln: ln, sessions: sessions}

	go func() {
		// Parent-driven shutdown
		<-parent.Done()
		log.Infof("Received signal: %v", parent.Err())
		d.stop()
	}()

	cleanup := func() {
		log.Infof("Cleaning up...")
		d.stop()
		d.wg.Wait()

		// Take snapshot of sessions to stop
		d.mutex.Lock()
		ss := make([]*Session, 0, len(d.sessions))
		for _, s := range d.sessions {
			ss = append(ss, s)
		}
		d.mutex.Unlock()

		// Drain
		for _, s := range ss {
			s.Close()
		}
		for _, s := range ss {
			<-s.Closed
		}
		log.Infof("Done.")
	}
	return d, cleanup
}

func respond(conn net.Conn, opErr error, vs map[string]vpn.Desc) {
	resp := vpn.Resp{Success: true, Vpns: vs, Info: vpn.Info{Commit: buildinfo.Commit}}
	if opErr != nil {
		resp.Success = false
		resp.Error = opErr.Error()
	}
	if err := ipc.Write(resp, conn); err != nil {
		log.Errorf("could not send response: %v", err)
	}
}

func (d *daemon) handleConn(conn net.Conn) {
	defer conn.Close()

	// Tie deadline to context cancel
	done := make(chan struct{})
	go func() {
		select {
		case <-d.ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-done:
		}
	}()
	defer close(done)

	// Read command
	var cmd vpn.Cmd
	if err := ipc.Read(&cmd, conn); err != nil {
		// Ignore cases where client aborts connection
		if !errors.Is(err, io.EOF) {
			log.Errorf("Could not receive command: %v", err)
		}
		return
	}
	log.Debugf("Received command %v", cmd)

	if (cmd.Kind == vpn.Up || cmd.Kind == vpn.Down) && cmd.Vpn == nil {
		err := fmt.Errorf("no vpn specified")
		respond(conn, err, nil)
		return
	}

	// Execute command
	switch cmd.Kind {
	case vpn.Nop:
		respond(conn, nil, nil)
	case vpn.Up:
		d.upVpn(conn, cmd.Vpn)
	case vpn.Down:
		d.downVpn(conn, cmd.Vpn)
	case vpn.List:
		d.listVpns(conn)
	case vpn.Shutdown:
		log.Infof("Shutdown command received.")
		respond(conn, nil, nil)
		d.stop()
	default:
		err := fmt.Errorf("unknown command: %v", cmd.Kind)
		respond(conn, err, nil)
	}
}

func (d *daemon) upVpn(conn net.Conn, desc *vpn.Desc) {
	var err error
	defer func() { respond(conn, err, nil) }()

	d.mutex.RLock()
	_, exists := d.sessions[desc.Name]
	d.mutex.RUnlock()
	if exists {
		err = vpn.AlreadyRunning
		log.Errorf("%v: could not start: %v", desc.Name, err)
		return
	}

	s := FromDesc(desc)
	if err = s.Open(); err != nil {
		log.Errorf("%v: could not start: %v", s.Name, err)
		return
	}

	d.mutex.Lock()
	d.sessions[s.Name] = s
	d.mutex.Unlock()

	// Register closing logic
	go func() {
		<-s.Closed
		d.mutex.Lock()
		delete(d.sessions, s.Name)
		d.mutex.Unlock()
		log.Infof("Stopped vpn %s", s.Name)
	}()
}

func (d *daemon) downVpn(conn net.Conn, q *vpn.Desc) {
	var err error
	defer func() { respond(conn, err, nil) }()

	d.mutex.RLock()
	s, ok := d.sessions[q.Name]
	d.mutex.RUnlock()
	if !ok {
		err = fmt.Errorf("vpn not running")
		log.Errorf("%v: could not stop: %v", q.Name, err)
		return
	}

	if err = s.Close(); err != nil {
		log.Errorf("%v: could not stop: %v", s.Name, err)
		return
	}
	<-s.Closed
}

func (d *daemon) listVpns(conn net.Conn) {
	d.mutex.RLock()
	vs := make(map[string]vpn.Desc, len(d.sessions))
	for n, s := range d.sessions {
		vs[n] = *s.Desc
	}
	d.mutex.RUnlock()
	respond(conn, nil, vs)
}

func initLogging(path string) {
	logFile, err := os.OpenFile(
		path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	log.Init(logFile, true, runtime.GOOS != "windows")
}

func listen() (l net.Listener, err error) {
	l, err = net.Listen("unix", vpn.Socket)
	if err == nil {
		return
	}
	// If the daemon was terminated forcefully, the domain socket
	// may be in a bad state where it exists but doesn't allow binding.
	// We try to identify this and delete the socket file, if necessary.
	if _, statErr := os.Stat(vpn.Socket); statErr == nil {
		if _, dialErr := net.Dial("unix", vpn.Socket); dialErr != nil {
			log.Warningf("Found unresponsive socket, deleting...")
			os.Remove(vpn.Socket)
			l, err = net.Listen("unix", vpn.Socket)
		}
	}
	return
}

func (d *daemon) serve() {
	for {
		conn, err := d.ln.Accept()
		if err != nil {
			if d.ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			log.Errorf("Failed to accept connection: %v", err)
			continue
		}
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.handleConn(conn)
		}()
	}
}

// stop breaks out of the Accept loop in serve.
func (d *daemon) stop() {
	d.once.Do(func() {
		log.Infof("Stopping.")
		d.cancel()
		d.ln.Close()
	})
}

// Run starts the boring-vpn daemon. Unlike boring's daemon, this one
// exclusively manages vpn sessions -- which need a TUN device and
// routing table changes -- so it refuses to start at all unless the
// process already has the privileges that requires.
func Run() {
	initLogging(vpn.LogFile)
	log.Infof("Daemon starting")

	if !HasPrivileges() {
		log.Fatalf("%s", PrivilegeError())
	}

	cleanupStaleState()

	ln, err := listen()
	if err != nil {
		log.Fatalf("Failed to setup listener: %v", err)
	}
	log.Infof("Listening on %s", ln.Addr())

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	d, cleanup := newDaemon(ctx, ln)
	defer cleanup()

	d.serve()
}
