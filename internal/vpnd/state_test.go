package vpnd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSanitizeReplacesSlash(t *testing.T) {
	if got := sanitize("a/b"); got != "a_b" {
		t.Errorf("sanitize(%q) = %q, want %q", "a/b", got, "a_b")
	}
	if got := sanitize("plain-name"); got != "plain-name" {
		t.Errorf("sanitize(%q) = %q, want it unchanged", "plain-name", got)
	}
}

func TestStatePathUsesStateDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BORING_VPN_STATE_DIR", dir)

	got := statePath("office")
	want := filepath.Join(dir, "boring-vpn-office.json")
	if got != want {
		t.Errorf("statePath(office) = %q, want %q", got, want)
	}
}

func TestWriteReadRemoveState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BORING_VPN_STATE_DIR", dir)

	s := sessionState{
		Name:  "office",
		PID:   1234,
		Iface: "bvpnoffice1",
		Excludes: []routeRecord{
			{Dest: "10.0.5.0/24", Interface: "eth0", Gateway: "192.168.1.1"},
		},
	}
	if err := writeState(s); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	path := statePath("office")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file not written: %v", err)
	}

	removeState("office")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("state file still present after removeState: err = %v", err)
	}

	// Removing an already-absent state file must not be treated as an error
	// (no way to observe a returned error since removeState only logs, but
	// it must at least not panic).
	removeState("office")
}

func TestCleanupStaleStateRemovesDeadPID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BORING_VPN_STATE_DIR", dir)

	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("could not run helper process: %v", err)
	}
	deadPID := cmd.Process.Pid

	s := sessionState{
		Name:  "stale",
		PID:   deadPID,
		Iface: "bvpnstale1",
		Excludes: []routeRecord{
			{Dest: "10.0.5.0/24", Interface: "nonexistent0", Gateway: "192.168.1.1"},
		},
	}
	if err := writeState(s); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	cleanupStaleState()

	if _, err := os.Stat(statePath("stale")); !os.IsNotExist(err) {
		t.Errorf("stale state file was not removed: err = %v", err)
	}
}

func TestCleanupStaleStateKeepsLivePID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BORING_VPN_STATE_DIR", dir)

	s := sessionState{Name: "live", PID: os.Getpid(), Iface: "bvpnlive1"}
	if err := writeState(s); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	cleanupStaleState()

	if _, err := os.Stat(statePath("live")); err != nil {
		t.Errorf("state file for a live pid was removed: %v", err)
	}
}

func TestCleanupStaleStateRemovesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BORING_VPN_STATE_DIR", dir)

	path := statePath("corrupt")
	if err := os.WriteFile(path, []byte("{not valid json"), 0600); err != nil {
		t.Fatalf("could not write corrupt fixture: %v", err)
	}

	cleanupStaleState() // must not panic

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("corrupt state file was not removed: err = %v", err)
	}
}
