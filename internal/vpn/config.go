package vpn

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/alebeck/boring/internal/paths"
)

const fileName = ".boring-vpn.toml"

var defaultKeepAliveInterval = 2 * 60 // seconds

var Path string

func init() {
	if Path = os.Getenv("BORING_VPN_CONFIG"); Path == "" {
		Path = filepath.Join(getConfigHome(), fileName)
	}
	Path = filepath.ToSlash(Path)
	Path = paths.ReplaceTilde(Path)
}

func getConfigHome() string {
	if runtime.GOOS == "linux" {
		// Follow XDG specification on Linux
		h := os.Getenv("XDG_CONFIG_HOME")
		if h == "" {
			h = "~/.config"
		}
		return filepath.Join(h, "boring-vpn")
	}
	return "~"
}

// StringOrInt handles both string and integer values in the TOML config.
type StringOrInt string

func (s *StringOrInt) UnmarshalTOML(v any) error {
	switch value := v.(type) {
	case int64:
		*s = StringOrInt(strconv.FormatInt(value, 10))
	case string:
		*s = StringOrInt(value)
	default:
		return fmt.Errorf("unsupported type: %T", v)
	}
	return nil
}

func (s StringOrInt) String() string {
	return string(s)
}

// Desc describes a VPN session for user-facing purposes, e.g. in the
// config file.
type Desc struct {
	Name           string      `toml:"name" json:"name"`
	Host           string      `toml:"host" json:"host"`
	User           string      `toml:"user" json:"user"`
	IdentityFile   string      `toml:"identity" json:"identity"`
	Port           StringOrInt `toml:"port" json:"port"`
	KeepAlive      *int        `toml:"keep_alive" json:"keep_alive"`
	Subnets        []string    `toml:"subnets" json:"subnets,omitempty"`
	ExcludeSubnets []string    `toml:"exclude_subnets" json:"exclude_subnets,omitempty"`
	MTU            int         `toml:"mtu" json:"mtu,omitempty"`
	Status         Status      `toml:"-" json:"status"`
	LastConn       time.Time   `toml:"-" json:"last_conn"`
}

// Config represents the boring-vpn configuration as parsed from .boring-vpn.toml
type Config struct {
	Vpns      []Desc           `toml:"vpns"`
	KeepAlive *int             `toml:"keep_alive"`
	VpnsMap   map[string]*Desc `toml:"-"`
}

// Load parses the boring-vpn configuration file
func Load() (*Config, error) {
	cfg := Config{KeepAlive: &defaultKeepAliveInterval}

	if _, err := toml.DecodeFile(Path, &cfg); err != nil {
		return nil, fmt.Errorf("could not decode config file: %w", err)
	}

	for i := range cfg.Vpns {
		v := &cfg.Vpns[i]
		if v.KeepAlive == nil {
			v.KeepAlive = cfg.KeepAlive
		}
	}

	expand := func(s string) string { return os.Expand(s, expandWithDefault) }
	for i := range cfg.Vpns {
		v := &cfg.Vpns[i]
		v.Host = expand(v.Host)
		v.User = expand(v.User)
		v.IdentityFile = expand(v.IdentityFile)
		v.Port = StringOrInt(expand(v.Port.String()))
		for j, s := range v.Subnets {
			v.Subnets[j] = expand(s)
		}
		for j, s := range v.ExcludeSubnets {
			v.ExcludeSubnets[j] = expand(s)
		}
	}

	m, err := buildVpnsMap(cfg.Vpns)
	if err != nil {
		return nil, err
	}
	cfg.VpnsMap = m
	return &cfg, nil
}

func buildVpnsMap(vpns []Desc) (map[string]*Desc, error) {
	m := make(map[string]*Desc)
	for i := range vpns {
		v := &vpns[i]
		if _, exists := m[v.Name]; exists {
			return nil, fmt.Errorf("found duplicated vpn name '%v'", v.Name)
		}
		if v.Name == "" || strings.Contains(v.Name, " ") ||
			specialPrefix(v.Name) || containsGlob(v.Name) {
			return nil, fmt.Errorf("vpn names cannot be empty, contain spaces,"+
				" start with special characters, or contain glob characters '*?['."+
				" Found '%v'.", v.Name)
		}
		m[v.Name] = v
	}
	return m, nil
}

func specialPrefix(s string) bool {
	if s == "" {
		return false
	}
	firstChar := s[0]
	return !(firstChar >= 'A' && firstChar <= 'Z' ||
		firstChar >= 'a' && firstChar <= 'z' ||
		firstChar >= '0' && firstChar <= '9')
}

func containsGlob(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// expandWithDefault resolves an environment variable reference, supporting
// the ${VAR:-default} syntax. If the variable is unset or empty and a default
// is provided after ":-", the default value is returned.
func expandWithDefault(key string) string {
	if varName, defaultVal, found := strings.Cut(key, ":-"); found {
		if val := os.Getenv(varName); val != "" {
			return val
		}
		return defaultVal
	}
	return os.Getenv(key)
}
