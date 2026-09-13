//go:build windows

package vpnd

func HasPrivileges() bool {
	return false
}

func PrivilegeError() string {
	return "boring-vpn is not supported on Windows yet"
}
