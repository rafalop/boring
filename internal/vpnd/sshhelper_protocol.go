//go:build linux || darwin

package vpnd

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"syscall"
)

// This file defines the wire protocol spoken between the daemon (root)
// and its privilege-dropped SSH helper child (see sshhelper.go) over a
// Unix domain socket, plus a small framed-connection type that makes it
// safe under concurrent use.
//
// Naively, one write (one WriteMsgUnix call) does not correspond to one
// read (one ReadMsgUnix call) on a SOCK_STREAM socket: if a reader falls
// even briefly behind -- easily triggered by several goroutines writing
// concurrently -- the kernel coalesces multiple writes into a single
// read. Every message is therefore length-prefixed ([4-byte big-endian
// length][JSON payload]) and frameConn buffers partial reads and splits
// out however many complete messages a single underlying read
// contained.
//
// File descriptors need extra care: Linux only guarantees that
// ancillary data (SCM_RIGHTS) arrives in the same relative order as the
// bytes it was sent with, not that it lines up with any particular
// read() call's boundaries. frameConn accounts for this by queuing
// every fd it receives (in arrival order) separately from the byte
// buffer, and having the caller pop one off once it has decoded a
// message it knows should carry one (dialResponse with OK true) --
// since both the bytes and the fds arrive in order, this always
// associates the right fd with the right message regardless of how the
// kernel chose to chunk the underlying reads.

// connectRequest is the first message sent by the parent, describing
// which host to connect to.
type connectRequest struct {
	Name         string `json:"name"` // vpn session name, for logging only
	Host         string `json:"host"`
	User         string `json:"user"`
	Port         string `json:"port"`
	IdentityFile string `json:"identity"`
	KeepAlive    *int   `json:"keep_alive"`
}

// connectResponse answers a connectRequest.
type connectResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// dialRequest asks the child to open network/addr through its SSH
// connection. Multiple requests can be in flight concurrently,
// distinguished by ID.
type dialRequest struct {
	ID      uint64 `json:"id"`
	Network string `json:"network"`
	Addr    string `json:"addr"`
}

// dialResponse answers a dialRequest. When OK, a file descriptor for the
// connection was sent along with it (see frameConn).
type dialResponse struct {
	ID    uint64 `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

const maxFrameSize = 1 << 20 // sanity bound, real messages are a few hundred bytes

// frameConn is a length-prefixed, fd-queue-aware wrapper around a Unix
// control socket. Safe for concurrent writers; reads are expected from
// a single goroutine (as both sides of this protocol do).
type frameConn struct {
	c *net.UnixConn

	writeMu sync.Mutex

	buf []byte
	fds []int
}

func newFrameConn(c *net.UnixConn) *frameConn {
	return &frameConn{c: c}
}

// writeMsg sends v as one length-prefixed frame, optionally with fd
// attached as ancillary data (pass -1 for none).
func (fc *frameConn) writeMsg(v any, fd int) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame, uint32(len(data)))
	copy(frame[4:], data)

	var oob []byte
	if fd >= 0 {
		oob = syscall.UnixRights(fd)
	}

	fc.writeMu.Lock()
	defer fc.writeMu.Unlock()
	_, _, err = fc.c.WriteMsgUnix(frame, oob, nil)
	return err
}

// readFrame returns the next complete message's raw JSON bytes,
// buffering and refilling from the socket as needed.
func (fc *frameConn) readFrame() ([]byte, error) {
	for {
		if msg, ok := fc.takeFrame(); ok {
			return msg, nil
		}
		if err := fc.fill(); err != nil {
			return nil, err
		}
	}
}

func (fc *frameConn) takeFrame() ([]byte, bool) {
	if len(fc.buf) < 4 {
		return nil, false
	}
	n := binary.BigEndian.Uint32(fc.buf[:4])
	if n > maxFrameSize {
		// Desynced framing -- treat as fatal rather than trying to
		// allocate/read an unbounded amount.
		fc.buf = nil
		return nil, false
	}
	if uint32(len(fc.buf)) < 4+n {
		return nil, false
	}
	msg := fc.buf[4 : 4+n]
	fc.buf = fc.buf[4+n:]
	return msg, true
}

func (fc *frameConn) fill() error {
	tmp := make([]byte, 65536)
	oob := make([]byte, syscall.CmsgSpace(4*8)) // room for a handful of fds
	n, oobn, _, _, err := fc.c.ReadMsgUnix(tmp, oob)
	if err != nil {
		return err
	}
	fc.buf = append(fc.buf, tmp[:n]...)
	if oobn > 0 {
		scms, err := syscall.ParseSocketControlMessage(oob[:oobn])
		if err == nil {
			for _, scm := range scms {
				if fds, err := syscall.ParseUnixRights(&scm); err == nil {
					fc.fds = append(fc.fds, fds...)
				}
			}
		}
	}
	return nil
}

// popFD returns the next queued file descriptor, if any. Call this only
// after decoding a message that's expected to carry one (a dialResponse
// with OK true) -- see the file comment for why this, rather than
// reading OOB data per-message, is what's actually safe here.
func (fc *frameConn) popFD() (int, bool) {
	if len(fc.fds) == 0 {
		return -1, false
	}
	fd := fc.fds[0]
	fc.fds = fc.fds[1:]
	return fd, true
}

func decode[T any](raw []byte) (T, error) {
	var v T
	err := json.Unmarshal(raw, &v)
	if err != nil {
		err = fmt.Errorf("could not decode message: %v", err)
	}
	return v, err
}
