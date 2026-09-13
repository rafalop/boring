package vpnd

import (
	"os"
	"os/user"
	"strconv"
)

// invokingUser describes the non-root user "sudo" elevated from, when
// the daemon is running as root via sudo.
type invokingUser struct {
	Uid, Gid uint32
	HomeDir  string
	Username string
}

// detectSudoUser reports the user that invoked sudo, if this process is
// root and was in fact started via sudo. It's how the daemon decides
// whether to hand SSH connection setup off to a privilege-dropped child
// (see sshhelper.go) instead of resolving SSH config as itself: since
// the daemon has to run fully as root to create TUN devices and modify
// routes, resolving ~/.ssh/config, known_hosts, and identity files
// directly as root would resolve them against root's own (usually
// empty) home directory instead of the actual operator's.
func detectSudoUser() (invokingUser, bool) {
	if os.Geteuid() != 0 {
		return invokingUser{}, false
	}
	name := os.Getenv("SUDO_USER")
	if name == "" {
		return invokingUser{}, false
	}
	u, err := user.Lookup(name)
	if err != nil {
		return invokingUser{}, false
	}
	uid, err1 := strconv.ParseUint(u.Uid, 10, 32)
	gid, err2 := strconv.ParseUint(u.Gid, 10, 32)
	if err1 != nil || err2 != nil || u.HomeDir == "" {
		return invokingUser{}, false
	}
	return invokingUser{Uid: uint32(uid), Gid: uint32(gid), HomeDir: u.HomeDir, Username: name}, true
}

// dialRemoteRequest carries what the SSH helper needs to resolve and
// connect -- deliberately just the raw config fields, not anything
// already resolved, since resolution needs to happen in the helper (as
// the real invoking user) for this to be worth doing at all.
type dialRemoteRequest struct {
	Name         string
	Host         string
	User         string
	Port         string
	IdentityFile string
	KeepAlive    *int
}
