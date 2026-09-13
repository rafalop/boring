//go:build windows

package vpnd

import "fmt"

// SSHHelperFlag exists so cmd/boring-vpn's dispatch compiles on Windows
// too. It's never actually reached: HasPrivileges always returns false
// on Windows, so the daemon refuses to start before any session gets
// far enough to spawn a helper.
const SSHHelperFlag = "--ssh-helper"

// RunSSHHelper is never actually invoked on Windows -- see SSHHelperFlag.
func RunSSHHelper() {}

// dialRemote's approach (dropping privileges via a Unix credential and
// passing dialed connections back via SCM_RIGHTS) has no Windows
// equivalent implemented yet, and detectSudoUser always returns false
// on Windows anyway (no SUDO_USER concept), so this is never called.
func dialRemote(su invokingUser, req dialRemoteRequest) (conn, error) {
	return nil, fmt.Errorf("boring-vpn does not support Windows yet")
}
