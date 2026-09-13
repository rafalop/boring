package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/alebeck/boring/internal/log"
	"github.com/alebeck/boring/internal/vpn"
)

const defaultConfig = `# An example vpn is defined below.
# All lines starting with '#' are comments.

# [[vpns]]
# name = "office-vpn"  # Name for the vpn
# host = "bastion"  # Hostname of the server, tries to match against ssh config
# subnets = ["10.0.0.0/8", "192.168.50.0/24"]  # Subnets to route through the tunnel
# exclude_subnets = ["10.0.5.0/24"]  # (Optional) Subnets to exclude from routing
# port = 22  # (Optional) Server port, defaults to 22
# user = "neo"  # (Optional) Username, tries ssh config and defaults to $USER
# identity = "~/.ssh/id_dev"  # (Optional) Key file, tries ssh config and defaults to default keys

`

func editConfig() {
	if err := ensureConfig(); err != nil {
		log.Fatalf("could not create config file: %v", err)
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
		if runtime.GOOS == "windows" {
			editor = "notepad"
		}
	}

	cmd := exec.Command(editor, vpn.Path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
}

// Checks if config file exists, otherwise creates it
func ensureConfig() error {
	if _, statErr := os.Stat(vpn.Path); statErr != nil {
		d := filepath.Dir(vpn.Path)
		if err := os.MkdirAll(d, 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(vpn.Path, os.O_RDWR|os.O_CREATE, 0600)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.WriteString(defaultConfig); err != nil {
			return err
		}
		log.Infof("Hi! Created boring-vpn config file: %s", vpn.Path)
	}
	return nil
}
