package vpnd

import (
	"os/user"
	"testing"
)

func TestDetectSudoUserNotRoot(t *testing.T) {
	// This test process is never root, regardless of what SUDO_USER
	// says -- detectSudoUser must not report sudo based on the env vars
	// alone.
	t.Setenv("SUDO_USER", "someone")
	t.Setenv("SUDO_UID", "1000")
	t.Setenv("SUDO_GID", "1000")

	_, ok, err := detectSudoUser()
	if err != nil {
		t.Fatalf("detectSudoUser() error = %v, want nil", err)
	}
	if ok {
		t.Error("detectSudoUser() reported sudo while not running as root")
	}
}

func TestHomeDirViaShellMatchesRealHome(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Skipf("could not determine current user: %v", err)
	}

	got, err := homeDirViaShell(u.Username)
	if err != nil {
		t.Fatalf("homeDirViaShell(%q) error: %v", u.Username, err)
	}
	if got != u.HomeDir {
		t.Errorf("homeDirViaShell(%q) = %q, want %q", u.Username, got, u.HomeDir)
	}
}

func TestHomeDirViaShellUnknownUser(t *testing.T) {
	_, err := homeDirViaShell("this-user-should-not-exist-anywhere-0x1")
	if err == nil {
		t.Error("expected an error for a nonexistent user, got nil")
	}
}

func TestHomeDirFallsBackWhenLookupFails(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Skipf("could not determine current user: %v", err)
	}

	// homeDir's primary path (os/user.Lookup) already works in this
	// test environment, so this mainly guards against a regression that
	// makes homeDir diverge from homeDirViaShell for a real user -- the
	// actual "lookup fails but the shell still resolves it" scenario
	// (e.g. a macOS user absent from /etc/passwd when this binary was
	// cross-compiled without cgo) isn't reproducible on whatever this
	// test happens to run on.
	got, err := homeDir(u.Username)
	if err != nil {
		t.Fatalf("homeDir(%q) error: %v", u.Username, err)
	}
	if got != u.HomeDir {
		t.Errorf("homeDir(%q) = %q, want %q", u.Username, got, u.HomeDir)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"alice":   `'alice'`,
		"o'brien": `'o'\''brien'`,
		"":        `''`,
		"a b":     `'a b'`,
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
