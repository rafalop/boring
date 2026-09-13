package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/alebeck/boring/internal/buildinfo"
	"github.com/alebeck/boring/internal/log"
	"github.com/alebeck/boring/internal/vpn"
	"github.com/alebeck/boring/internal/vpnd"
	"golang.org/x/term"
)

var isTerm = os.Getenv("BORING_FORCE_INTERACTIVE") != "" ||
	term.IsTerminal(int(os.Stdout.Fd()))

func main() {
	// Run in daemon mode?
	if len(os.Args) == 2 && os.Args[1] == vpn.Flag {
		vpnd.Run()
		os.Exit(0)
	}

	// Run as the privilege-dropped SSH connection helper? (spawned by
	// the daemon itself, never invoked directly by a user)
	if len(os.Args) == 2 && os.Args[1] == vpnd.SSHHelperFlag {
		vpnd.RunSSHHelper()
		os.Exit(0)
	}

	initLogging()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "up":
		if len(os.Args) < 3 {
			log.Fatalf("'up' requires at least one 'pattern' argument, or an '--all/-a' flag.")
		}
		controlVpns(os.Args[2:], vpn.Up)
	case "down":
		if len(os.Args) < 3 {
			log.Fatalf("'down' requires at least one 'pattern' argument, or an '--all/-a' flag.")
		}
		controlVpns(os.Args[2:], vpn.Down)
	case "list", "ls":
		listVpns()
	case "edit", "e":
		editConfig()
	case "version", "v":
		printVersion()
	case "help", "h":
		printUsage()
	default:
		log.Printf("Unknown command: %v\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func initLogging() {
	useColors := isTerm && runtime.GOOS != "windows"
	log.Init(os.Stdout, isTerm, useColors)
}

func printVersion() {
	v := buildinfo.Version
	if v == "" {
		v = "snapshot"
		if buildinfo.Commit != "" {
			v += fmt.Sprintf(" (#%s)", buildinfo.Commit)
		}
	}
	log.Emitf("boring-vpn %s\n", v)
}

func printUsage() {
	log.Printf("`boring-vpn`: sshuttle-style subnet routing over an SSH connection\n\n")
	log.Printf("Usage:\n")
	log.Printf("  boring-vpn list, ls                 List all vpns\n")
	log.Printf("  boring-vpn up (-a | <patterns>...)      Start vpns matching any glob pattern\n")
	log.Printf("  boring-vpn down (-a | <patterns>...)    Stop vpns (same options as 'up')\n")
	log.Printf("  boring-vpn edit, e                  Edit the configuration file\n")
	log.Printf("  boring-vpn version, v               Show the version number\n")
	log.Printf("  boring-vpn help, h                  Show this help message\n")
	log.Printf("\nThe boring-vpn daemon runs as root, so `list`/`up`/`down` all require root\n" +
		"privileges too (e.g. run as `sudo boring-vpn up <name>`).\n")
	if runtime.GOOS == "windows" {
		log.Printf("Note: boring-vpn is not supported on Windows yet.\n")
	}
}
