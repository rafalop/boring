package vpnd

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/alebeck/boring/internal/log"
)

// routeRecord is a serializable exclude route: enough to reconstruct and
// remove it again without the process that added it still being alive.
type routeRecord struct {
	Dest      string `json:"dest"`
	Interface string `json:"interface"`
	Gateway   string `json:"gateway,omitempty"`
}

func (r routeRecord) route() (*net.IPNet, originalRoute, error) {
	_, dest, err := net.ParseCIDR(r.Dest)
	if err != nil {
		return nil, originalRoute{}, fmt.Errorf("invalid dest %q: %v", r.Dest, err)
	}
	via := originalRoute{Interface: r.Interface}
	if r.Gateway != "" {
		via.Gateway = net.ParseIP(r.Gateway)
	}
	return dest, via, nil
}

// sessionState records what a running session applied to the system, so
// a later daemon start can undo it if this process is killed before it
// gets the chance to clean up itself. Routes that point at the TUN
// device (Subnets) don't need this: the kernel removes them on its own
// once the device is destroyed along with its owning process. Exclude
// routes point at the original gateway/interface instead, so they don't
// get that free cleanup and are the only thing recorded here.
type sessionState struct {
	Name     string        `json:"name"`
	PID      int           `json:"pid"`
	Iface    string        `json:"iface"`
	Excludes []routeRecord `json:"excludes,omitempty"`
}

func stateDir() string {
	if d := os.Getenv("BORING_VPN_STATE_DIR"); d != "" {
		return d
	}
	return os.TempDir()
}

func statePath(name string) string {
	return filepath.Join(stateDir(), "boring-vpn-"+sanitize(name)+".json")
}

func writeState(s sessionState) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(s.Name), data, 0600)
}

func removeState(name string) {
	if err := os.Remove(statePath(name)); err != nil && !os.IsNotExist(err) {
		log.Warningf("vpn: could not remove state file for %v: %v", name, err)
	}
}

// cleanupStaleState looks for state files left behind by a session whose
// process is no longer running -- i.e. the daemon was killed before it
// could clean up after itself -- and reverses what they recorded.
func cleanupStaleState() {
	pattern := filepath.Join(stateDir(), "boring-vpn-*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		log.Warningf("vpn: could not scan for stale state: %v", err)
		return
	}

	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var s sessionState
		if err := json.Unmarshal(data, &s); err != nil {
			log.Warningf("vpn: could not parse state file %v, removing: %v", path, err)
			os.Remove(path)
			continue
		}
		if isProcessAlive(s.PID) {
			// Owned by a still-running session (or, if this is somehow
			// racing another daemon starting, best to leave it alone).
			continue
		}

		log.Warningf("vpn: found state for %q left behind by pid %d, which is no longer running; cleaning up", s.Name, s.PID)
		for _, rec := range s.Excludes {
			dest, via, err := rec.route()
			if err != nil {
				log.Warningf("vpn: %v", err)
				continue
			}
			if err := delRouteVia(dest, via); err != nil {
				log.Warningf("vpn: could not remove stale exclude route %v: %v", rec.Dest, err)
			}
		}
		if s.Iface != "" {
			if err := removeInterface(s.Iface); err != nil {
				log.Warningf("vpn: could not remove stale interface %v: %v", s.Iface, err)
			}
		}
		os.Remove(path)
	}
}

// sanitize keeps a vpn name safe to use as a filename component. Names
// are already validated by vpn.Load's buildVpnsMap (no spaces or glob
// characters), but that doesn't rule out "/", which would otherwise
// turn into an unintended subdirectory here.
func sanitize(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == filepath.Separator {
			return '_'
		}
		return r
	}, name)
}
