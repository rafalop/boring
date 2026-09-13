package vpn

import (
	"errors"
	"os"
	"path/filepath"
)

// Flag, when passed as the sole CLI argument, tells the boring-vpn binary
// to run as the daemon instead of the CLI.
const Flag = "--daemon"

const (
	sockName    = "boring-vpnd.sock"
	logFileName = "boring-vpnd.log"
)

var (
	LogFile        string
	Socket         string
	AlreadyRunning = errors.New("already running")
)

func init() {
	if LogFile = os.Getenv("BORING_VPN_LOG_FILE"); LogFile == "" {
		LogFile = filepath.Join(os.TempDir(), logFileName)
	}
	if Socket = os.Getenv("BORING_VPN_SOCK"); Socket == "" {
		Socket = filepath.Join(os.TempDir(), sockName)
	}
}
