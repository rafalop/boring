package vpnd

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
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
//
// The returned error distinguishes "definitely not running under sudo"
// (nil error, ok false -- proceed with local resolution, e.g. a genuine
// root login) from "sudo was detected but its details couldn't be
// resolved" (non-nil error -- this must NOT be treated the same as the
// former: silently falling back to resolving as root reproduces the
// exact bug this whole mechanism exists to avoid, just less visibly).
func detectSudoUser() (invokingUser, bool, error) {
	if os.Geteuid() != 0 {
		return invokingUser{}, false, nil
	}
	name := os.Getenv("SUDO_USER")
	if name == "" {
		return invokingUser{}, false, nil
	}

	// Prefer $SUDO_UID/$SUDO_GID (set by sudo itself, on both Linux and
	// macOS) over looking the name up in a user database: os/user has a
	// pure-Go fallback that only ever consults /etc/passwd, used
	// whenever cgo isn't available -- notably including any binary
	// that's cross-compiled for macOS from a non-Mac host, which is how
	// this project's own `make build-vpn-grid` produces its darwin
	// build. macOS's real user accounts live in Directory Services, not
	// /etc/passwd, so that fallback silently fails to find anyone there.
	uid, err1 := strconv.ParseUint(os.Getenv("SUDO_UID"), 10, 32)
	gid, err2 := strconv.ParseUint(os.Getenv("SUDO_GID"), 10, 32)
	if err1 != nil || err2 != nil {
		return invokingUser{}, false, fmt.Errorf(
			"detected sudo (user %q) but $SUDO_UID/$SUDO_GID are missing or invalid", name)
	}

	home, err := homeDir(name)
	if err != nil {
		return invokingUser{}, false, fmt.Errorf(
			"detected sudo (user %q) but could not determine their home directory: %v", name, err)
	}

	return invokingUser{Uid: uint32(uid), Gid: uint32(gid), HomeDir: home, Username: name}, true, nil
}

// homeDir resolves username's home directory without depending on
// os/user's pure-Go fallback being able to find them (see
// detectSudoUser): it tries the normal lookup first, since that's
// correct and doesn't spawn a process whenever cgo is available or the
// user happens to be in /etc/passwd anyway, then falls back to shell
// tilde expansion, which is resolved by the C library exactly like any
// other program's user lookups, independent of how this Go binary
// itself was built.
func homeDir(username string) (string, error) {
	if u, err := user.Lookup(username); err == nil && u.HomeDir != "" {
		return u.HomeDir, nil
	}
	return homeDirViaShell(username)
}

// homeDirViaShell resolves username's home directory through the
// shell's own tilde expansion, which goes through the C library's real
// user lookup regardless of how this Go binary itself was built --
// split out from homeDir so it can be tested directly, independent of
// whether the primary os/user lookup would also have worked.
func homeDirViaShell(username string) (string, error) {
	out, err := exec.Command("sh", "-c", "eval echo ~"+shellQuote(username)).Output()
	if err != nil {
		return "", fmt.Errorf("could not resolve ~%s: %v", username, err)
	}
	home := strings.TrimSpace(string(out))
	if home == "" || home == "~"+username {
		return "", fmt.Errorf("could not resolve ~%s", username)
	}
	return home, nil
}

// shellQuote makes username safe to interpolate into the sh -c string
// above. Usernames can't contain single quotes, but this is cheap
// insurance against a surprising one somehow getting through.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
