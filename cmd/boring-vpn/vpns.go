package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alebeck/boring/internal/log"
	"github.com/alebeck/boring/internal/table"
	"github.com/alebeck/boring/internal/vpn"
	"golang.org/x/sync/errgroup"
)

const daemonTimeout = 10 * time.Second

var errOpFailed = errors.New("operation failed")

// prepare loads the configuration and ensures the daemon is running
func prepare() (*vpn.Config, error) {
	var conf *vpn.Config
	ctx, cancel := context.WithTimeout(context.Background(), daemonTimeout)
	g, ctx := errgroup.WithContext(ctx)
	defer cancel()

	g.Go(func() error {
		var err error
		if isTerm {
			if err = ensureConfig(); err != nil {
				return fmt.Errorf("could not create config file: %v", err)
			}
		}
		if conf, err = vpn.Load(); err != nil {
			if errors.Is(err, fs.ErrNotExist) && !isTerm {
				conf = &vpn.Config{}
				return nil
			}
			return fmt.Errorf("could not load boring-vpn config: %v", err)
		}
		return nil
	})

	g.Go(func() error {
		return ensureDaemon(ctx)
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return conf, nil
}

func controlVpns(args []string, kind vpn.CmdKind) {
	if args[0] == "--all" || args[0] == "-a" {
		if len(args) != 1 {
			log.Fatalf("'--all' does not take any additional arguments.")
		}
		args = []string{"*"}
	}

	conf, err := prepare()
	if err != nil {
		log.Fatalf("Startup: %s", err.Error())
	}

	vs := conf.VpnsMap
	var m string
	if kind == vpn.Down {
		vs, err = getRunningVpns()
		if err != nil {
			log.Fatalf("Could not get running vpns: %v", err)
		}
		m = "running "
	}

	keep, notMatched := filterByPatterns(vs, args)
	if len(keep) == 0 {
		msg := fmt.Sprintf("No %svpns match pattern '%s'.", m, args[0])
		if len(args) > 1 {
			msg = fmt.Sprintf("No %svpns match any provided pattern.", m)
		}
		log.Fatalf("%s", msg)
	}
	for _, pat := range notMatched {
		log.Warningf("No %svpns match pattern '%s'.", m, pat)
	}

	// Issue concurrent commands for all matched vpns
	var g errgroup.Group
	for n := range keep {
		g.Go(func() error {
			if kind == vpn.Up {
				return upVpn(vs[n])
			}
			return downVpn(vs[n])
		})
	}
	if err := g.Wait(); err != nil {
		os.Exit(1)
	}
}

func upVpn(v *vpn.Desc) error {
	resp, err := sendCmd(vpn.Cmd{Kind: vpn.Up, Vpn: v})
	if err != nil {
		log.Errorf("Could not transmit 'up' command: %v", err)
		return errOpFailed
	}
	if !resp.Success {
		// cannot use errors.Is because error is transmitted as string over IPC
		if strings.HasSuffix(resp.Error, vpn.AlreadyRunning.Error()) {
			log.Infof("Vpn '%v' is already running.", v.Name)
			return nil
		}
		log.Errorf("Could not start vpn '%v': %v", v.Name, resp.Error)
		return errOpFailed
	}

	log.Infof("Started vpn '%s': routing %s via %s.", log.Green+log.Bold+v.Name+log.Reset,
		strings.Join(v.Subnets, ", "), v.Host)
	return nil
}

func downVpn(v *vpn.Desc) error {
	// Daemon only needs the name, so simplify
	v = &vpn.Desc{Name: v.Name}
	resp, err := sendCmd(vpn.Cmd{Kind: vpn.Down, Vpn: v})
	if err != nil {
		log.Errorf("Could not transmit 'down' command: %v", err)
		return errOpFailed
	}
	if !resp.Success {
		log.Errorf("Vpn '%v' could not be stopped: %v", v.Name, resp.Error)
		return errOpFailed
	}
	log.Infof("Stopped vpn '%s'.", log.Green+log.Bold+v.Name+log.Reset)
	return nil
}

func getRunningVpns() (map[string]*vpn.Desc, error) {
	resp, err := sendCmd(vpn.Cmd{Kind: vpn.List})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	m := make(map[string]*vpn.Desc, len(resp.Vpns))
	for _, v := range resp.Vpns {
		m[v.Name] = &v
	}
	return m, nil
}

func listVpns() {
	conf, err := prepare()
	if err != nil {
		log.Fatalf("Startup: %s", err.Error())
	}

	vs, err := getRunningVpns()
	if err != nil {
		log.Fatalf("Could not list vpns: %v", err)
	}

	if len(vs) == 0 && len(conf.Vpns) == 0 {
		log.Infof("No vpns configured.")
		return
	}

	all := orderVpnsForList(conf.Vpns, vs)
	printVpnList(all)
}

// orderVpnsForList combines configured and running vpns into an ordered slice.
// Config order is preserved; running-but-not-configured vpns are appended sorted by name.
func orderVpnsForList(conf []vpn.Desc, vs map[string]*vpn.Desc) []*vpn.Desc {
	var all []*vpn.Desc
	visited := make(map[string]bool)
	for i := range conf {
		v := &conf[i]
		if q, ok := vs[v.Name]; ok {
			all = append(all, q)
			visited[q.Name] = true
			continue
		}
		all = append(all, v)
	}
	var extra []string
	for name := range vs {
		if !visited[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		all = append(all, vs[name])
	}
	return all
}

func printVpnList(all []*vpn.Desc) {
	tbl := table.New("Status", "Name", "Subnets", "Via")
	for _, v := range all {
		tbl.AddRow(status(v), v.Name, strings.Join(v.Subnets, ", "), v.Host)
	}
	log.Emitf("%v", tbl)
}

func filterByPatterns(vs map[string]*vpn.Desc, pats []string) (map[string]bool, []string) {
	keep := make(map[string]bool, len(vs))
	var notMatched []string
	for _, pat := range pats {
		n, err := filterGlob(vs, keep, pat)
		if err != nil {
			log.Fatalf("Malformed glob pattern '%v'.", pat)
		}
		if n == 0 {
			notMatched = append(notMatched, pat)
		}
	}
	return keep, notMatched
}

func filterGlob(
	vs map[string]*vpn.Desc, keep map[string]bool, pat string) (
	n int, err error) {
	// Fail early if pattern is malformed; if this passes we can
	// ignore the error return value of the following matches
	if _, err = filepath.Match(pat, ""); err != nil {
		return
	}
	for v := range vs {
		if m, _ := filepath.Match(pat, v); m {
			keep[v] = true
			n++
		}
	}
	return
}
