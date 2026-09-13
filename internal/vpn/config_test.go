package vpn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStringOrIntInvalidType(t *testing.T) {
	var s StringOrInt
	if err := s.UnmarshalTOML(struct{}{}); err == nil ||
		!strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("incorrect error: %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	orig := Path
	t.Cleanup(func() { Path = orig })
	Path = filepath.Join(t.TempDir(), "missing.toml")
	if _, err := Load(); err == nil {
		t.Error("expected error for missing config file")
	}
}

func loadFixture(t *testing.T, content string) *Config {
	orig := Path
	t.Cleanup(func() { Path = orig })
	Path = filepath.Join(t.TempDir(), ".boring-vpn.toml")
	if err := os.WriteFile(Path, []byte(content), 0600); err != nil {
		t.Fatalf("could not write fixture: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	return cfg
}

func TestLoadBasic(t *testing.T) {
	cfg := loadFixture(t, `
[[vpns]]
name = "office"
host = "bastion"
port = 2222
subnets = ["10.0.0.0/8", "192.168.50.0/24"]
exclude_subnets = ["10.0.5.0/24"]
mtu = 1300
`)

	if len(cfg.Vpns) != 1 {
		t.Fatalf("len(Vpns) = %d, want 1", len(cfg.Vpns))
	}
	v := cfg.Vpns[0]
	if v.Name != "office" {
		t.Errorf("Name = %q, want %q", v.Name, "office")
	}
	if v.Host != "bastion" {
		t.Errorf("Host = %q, want %q", v.Host, "bastion")
	}
	if v.Port.String() != "2222" {
		t.Errorf("Port = %q, want %q", v.Port.String(), "2222")
	}
	if len(v.Subnets) != 2 || v.Subnets[0] != "10.0.0.0/8" || v.Subnets[1] != "192.168.50.0/24" {
		t.Errorf("Subnets = %v, want [10.0.0.0/8 192.168.50.0/24]", v.Subnets)
	}
	if len(v.ExcludeSubnets) != 1 || v.ExcludeSubnets[0] != "10.0.5.0/24" {
		t.Errorf("ExcludeSubnets = %v, want [10.0.5.0/24]", v.ExcludeSubnets)
	}
	if v.MTU != 1300 {
		t.Errorf("MTU = %d, want 1300", v.MTU)
	}
	if got, ok := cfg.VpnsMap["office"]; !ok || got.Name != "office" {
		t.Errorf("VpnsMap[office] missing or wrong: %+v", got)
	}
}

func TestKeepAliveDefaulting(t *testing.T) {
	cfg := loadFixture(t, `
keep_alive = 42

[[vpns]]
name = "a"
host = "h"
subnets = ["10.0.0.0/8"]

[[vpns]]
name = "b"
host = "h"
subnets = ["10.0.0.0/8"]
keep_alive = 7
`)

	if *cfg.VpnsMap["a"].KeepAlive != 42 {
		t.Errorf("a.KeepAlive = %d, want 42 (global default)", *cfg.VpnsMap["a"].KeepAlive)
	}
	if *cfg.VpnsMap["b"].KeepAlive != 7 {
		t.Errorf("b.KeepAlive = %d, want 7 (tunnel-level override)", *cfg.VpnsMap["b"].KeepAlive)
	}
}

func TestExpandEnvVars(t *testing.T) {
	t.Setenv("TEST_VPN_HOST", "example.com")
	t.Setenv("TEST_VPN_USER", "alice")
	t.Setenv("TEST_VPN_IDENTITY", "/keys/id_ed25519")
	t.Setenv("TEST_VPN_PORT", "2222")
	t.Setenv("TEST_VPN_SUBNET", "10.1.0.0/16")

	cfg := loadFixture(t, `
[[vpns]]
name = "office"
host = "${TEST_VPN_HOST}"
user = "${TEST_VPN_USER}"
identity = "${TEST_VPN_IDENTITY}"
port = "${TEST_VPN_PORT}"
subnets = ["${TEST_VPN_SUBNET}"]
exclude_subnets = ["${TEST_VPN_SUBNET}"]
`)

	v := cfg.Vpns[0]
	if v.Host != "example.com" {
		t.Errorf("Host = %q, want %q", v.Host, "example.com")
	}
	if v.User != "alice" {
		t.Errorf("User = %q, want %q", v.User, "alice")
	}
	if v.IdentityFile != "/keys/id_ed25519" {
		t.Errorf("IdentityFile = %q, want %q", v.IdentityFile, "/keys/id_ed25519")
	}
	if v.Port.String() != "2222" {
		t.Errorf("Port = %q, want %q", v.Port.String(), "2222")
	}
	if len(v.Subnets) != 1 || v.Subnets[0] != "10.1.0.0/16" {
		t.Errorf("Subnets = %v, want [10.1.0.0/16]", v.Subnets)
	}
	if len(v.ExcludeSubnets) != 1 || v.ExcludeSubnets[0] != "10.1.0.0/16" {
		t.Errorf("ExcludeSubnets = %v, want [10.1.0.0/16]", v.ExcludeSubnets)
	}
}

func TestExpandDefault(t *testing.T) {
	t.Setenv("TEST_VPN_SET", "real")
	t.Setenv("TEST_VPN_EMPTY", "")
	// TEST_VPN_UNSET is intentionally unset.

	cfg := loadFixture(t, `
[[vpns]]
name = "set"
host = "${TEST_VPN_SET:-fallback}"
subnets = ["10.0.0.0/8"]

[[vpns]]
name = "empty"
host = "${TEST_VPN_EMPTY:-fallback}"
subnets = ["10.0.0.0/8"]

[[vpns]]
name = "unset"
host = "${TEST_VPN_UNSET:-fallback}"
subnets = ["10.0.0.0/8"]
`)

	cases := map[string]string{"set": "real", "empty": "fallback", "unset": "fallback"}
	for name, want := range cases {
		if got := cfg.VpnsMap[name].Host; got != want {
			t.Errorf("vpn %q Host = %q, want %q", name, got, want)
		}
	}
}

func TestDuplicateNames(t *testing.T) {
	orig := Path
	t.Cleanup(func() { Path = orig })
	Path = filepath.Join(t.TempDir(), ".boring-vpn.toml")
	content := `
[[vpns]]
name = "dup"
host = "h"
subnets = ["10.0.0.0/8"]

[[vpns]]
name = "dup"
host = "h2"
subnets = ["10.0.0.0/8"]
`
	if err := os.WriteFile(Path, []byte(content), 0600); err != nil {
		t.Fatalf("could not write fixture: %v", err)
	}
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Errorf("Load() error = %v, want a duplicated-name error", err)
	}
}

func TestInvalidNames(t *testing.T) {
	cases := []string{
		"",          // empty
		"has space", // spaces
		"-leading",  // special prefix
		"has*glob",  // glob char
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			orig := Path
			t.Cleanup(func() { Path = orig })
			Path = filepath.Join(t.TempDir(), ".boring-vpn.toml")
			content := "[[vpns]]\nname = " + quoteTOML(name) + "\nhost = \"h\"\nsubnets = [\"10.0.0.0/8\"]\n"
			if err := os.WriteFile(Path, []byte(content), 0600); err != nil {
				t.Fatalf("could not write fixture: %v", err)
			}
			if _, err := Load(); err == nil {
				t.Errorf("Load() with name %q: expected error, got none", name)
			}
		})
	}
}

func quoteTOML(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
