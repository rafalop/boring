package vpnd

import "testing"

func TestParseCIDRs(t *testing.T) {
	nets, err := parseCIDRs([]string{"10.0.0.0/8", "192.168.1.0/24"})
	if err != nil {
		t.Fatalf("parseCIDRs error: %v", err)
	}
	if len(nets) != 2 {
		t.Fatalf("len(nets) = %d, want 2", len(nets))
	}
	if nets[0].String() != "10.0.0.0/8" {
		t.Errorf("nets[0] = %v, want 10.0.0.0/8", nets[0])
	}
	if nets[1].String() != "192.168.1.0/24" {
		t.Errorf("nets[1] = %v, want 192.168.1.0/24", nets[1])
	}
}

func TestParseCIDRsEmpty(t *testing.T) {
	nets, err := parseCIDRs(nil)
	if err != nil {
		t.Fatalf("parseCIDRs(nil) error: %v", err)
	}
	if len(nets) != 0 {
		t.Errorf("len(nets) = %d, want 0", len(nets))
	}
}

func TestParseCIDRsInvalid(t *testing.T) {
	_, err := parseCIDRs([]string{"not-a-cidr"})
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestTunNameDeterministic(t *testing.T) {
	a := tunName("office-vpn")
	b := tunName("office-vpn")
	if a != b {
		t.Errorf("tunName not deterministic: %q != %q", a, b)
	}
}

func TestTunNameDiffersByInput(t *testing.T) {
	a := tunName("office-vpn")
	b := tunName("home-vpn")
	if a == b {
		t.Errorf("tunName(%q) and tunName(%q) collided: %q", "office-vpn", "home-vpn", a)
	}
}

func TestTunNameLength(t *testing.T) {
	// Linux limits interface names to 15 characters (IFNAMSIZ - 1).
	for _, name := range []string{"a", "office-vpn", "a-very-long-vpn-session-name-indeed"} {
		got := tunName(name)
		if len(got) > 15 {
			t.Errorf("tunName(%q) = %q, length %d exceeds 15", name, got, len(got))
		}
	}
}
