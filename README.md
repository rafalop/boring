<div align="center">

<h1>The <code>boring</code> tunnel manager</h1>

<img src="assets/gopher.png" width="200">

A simple command line SSH tunnel manager that just works.

[![GitHub Actions Workflow Status](https://img.shields.io/github/actions/workflow/status/alebeck/boring/test_and_cover.yml?branch=main&style=flat&logo=github&label=CI)](https://github.com/alebeck/boring/actions/workflows/test_and_cover.yml)
[![GitHub Release](https://img.shields.io/github/v/release/alebeck/boring?color=orange)](https://github.com/alebeck/boring/releases/latest)
[![Go Report Card](https://goreportcard.com/badge/github.com/alebeck/boring)](https://goreportcard.com/report/github.com/alebeck/boring)
[![Coverage Status](https://coveralls.io/repos/github/alebeck/boring/badge.svg?branch=main)](https://coveralls.io/github/alebeck/boring?branch=main)
![Static Badge](https://img.shields.io/badge/license-MIT-blue?)

Get it: `brew install boring`

</div>

## Demo
![Screenshot](./assets/dark.gif)

## Features

* Ultra lightweight and fast
* Local, remote and dynamic (SOCKS5) port forwarding
* Works with SSH config and `ssh-agent`
* Supports Unix sockets
* Automatic re-connection and keep-alives
* Human-friendly TOML configuration
* Cross platform support
* Smart shell completions

## Usage

```
Usage:
  boring list, l [-g <group>]    List all tunnels
  boring open, o (-a | -g <group> | <patterns>...)
    <patterns>...                Open tunnels matching any glob pattern
    -a, --all                    Open all tunnels
    -g, --group <group>          Open all tunnels in a group
  boring close, c                Close tunnels (same options as 'open')
  boring edit, e                 Edit the configuration file
  boring version, v              Show the version number
  boring help, h                 Show this help message
```

## Configuration

By default, `boring` reads its configuration from `~/.boring.toml` on macOS and Windows, and from `$XDG_CONFIG_HOME/boring/.boring.toml` on Linux. If `$XDG_CONFIG_HOME` is not set, it defaults to `~/.config`. The location of the config file can be overriden by setting `$BORING_CONFIG`. The config is a simple TOML file describing your tunnels:

```toml
# simple tunnel
[[tunnels]]
name = "dev"
local = "9000"
remote = "localhost:9000"
host = "dev-server"  # automatically matches host against SSH config

# example of an explicit host (no SSH config)
[[tunnels]]
name = "prod"
local = "5001"
remote = "localhost:5001"
host = "prod.example.com"
user = "root"
identity = "~/.ssh/id_prod"  # will try default ones if not set

# ... more tunnels
```

Currently, supported options at tunnel level are:

| **Option**    | **Description**                                                                                                                                                                    |
|---------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `name`        | Alias for the tunnel. **Required.**                                                                                                                                                |
| `local`       | Local address. Can be a `"$host:$port"` network address or a Unix socket. Can be abbreviated as `"$port"` in local and socks modes. **Required** in local, remote and socks modes. |
| `remote`      | Remote address. As above, but can be abbreviated in remote and socks-remote modes. **Required** in local, remote and socks-remote modes.                                           |
| `host`        | Either a host alias that matches SSH configs or the actual hostname. **Required.**                                                                                                 |
| `mode`        | Mode of the tunnel. Can be either `"local"`, `"remote"`, `"socks"` or `"socks-remote"`. Default is `"local"`.                                                                      |
| `user`        | SSH user. If not set, tries to read it from SSH config, defaulting to `$USER`.                                                                                                     |
| `identity`    | SSH identity file. If not set, tries to read it from SSH config and `ssh-agent`, defaulting to standard identity files.                                                            |
| `port`        | SSH port. If not set, tries to read it from SSH config, defaulting to `22`.                                                                                                        |
| `group`        | Group that the tunnel is assigned to. Groups are only shown in `list` view if at least one tunnel has a group assigned. Can be used for grouped `open`, `close`, and `list`.                         |

Options that can be provided at global and tunnel level (tunnel level takes precedence):

| **Option**    | **Description**                                                                                                     |
|---------------|---------------------------------------------------------------------------------------------------------------------|
| `keep_alive`  | Keep-alive interval **in seconds**. Default: `120` (2 minutes).                                                     |

You can influence the behavior of `boring` via a couple of environment variables:
<details>
  <summary>Show</summary>

  | **Variable**       | **Description**        | **Default**                                                                        |
  |--------------------|------------------------|------------------------------------------------------------------------------------|
  | `$BORING_CONFIG`   | Config file location   | `~/.boring.toml` (Mac & Windows) and `$XDG_CONFIG_HOME/boring/.boring.toml`(Linux) |
  | `$BORING_LOG_FILE` | Log file location      | `/tmp/boringd.log`                                                                 |
  | `$BORING_SOCK`     | Socket location        | `/tmp/boringd.sock`                                                                |
  | `$DEBUG`           | Enable verbose logging | ` `                                                                                |
    

</details>

## Installation

### Homebrew

```sh
brew install boring
```

### Pre-built

Get one of the pre-built binaries from the [releases page](https://github.com/alebeck/boring/releases). Then move the binary to a location in your `$PATH`.

### Build yourself

```sh
git clone https://github.com/alebeck/boring && cd boring
make
```

Then move the binary in `dist` to a location in your `$PATH`.

<details>
  <summary>Note for Windows users</summary>
  Windows is fully supported since release 0.6.0. Users currently have to build from source, which is very easy. Make sure Go >= 1.25 is installed and then compile via

  ```batch
  git clone https://github.com/alebeck/boring && cd boring
  .\build_win.bat
  ```

  Then, move the executable to a location in your `%PATH%`.
</details>

### Shell completion

Shell completion scripts are available for `bash`, `zsh`, and `fish`.

If `boring` was installed via Homebrew, and you have Homebrew completions enabled, nothing needs to be done.

Otherwise, install completions by adding the following to your shell's config file:

#### Bash

```sh
eval "$(boring --shell bash)"
```

#### Zsh

```sh
source <(boring --shell zsh)
```

#### Fish

```sh
boring --shell fish | source
```

## `boring-vpn` (experimental)

`boring-vpn` is a separate, standalone binary in this repository that adds
sshuttle-style subnet routing over an SSH connection: instead of forwarding a
single port, it transparently routes all traffic for one or more
subnets/IPs through an SSH connection. It is entirely independent of
`boring` -- separate binary, separate config file, separate daemon and
socket -- so building or running `boring` never pulls in any of
`boring-vpn`'s dependencies or behavior.

### How it works

No IP packets are actually tunneled to the remote host. Locally, a TUN
device plus a userspace TCP/IP stack terminate each TCP connection captured
for a routed subnet, then forward it through a plain SSH `direct-tcpip`
channel -- the same mechanism `ssh -L` uses -- to the real destination. The
remote side just needs an ordinary, unprivileged SSH login
(`AllowTcpForwarding yes`, the default); no extra software, root, or TUN
device is needed there.

### Requirements & limitations (v1)

* **Linux and macOS only.** Not supported on Windows yet.
* **TCP only.** No UDP forwarding and no DNS interception -- point your
  resolver at the remote network directly if you need that.
* **Requires root.** The daemon creates a TUN device and modifies routing
  tables, so every `boring-vpn` command needs to run as root.
* **macOS routing is unverified on real hardware.** It's implemented
  against documented `ifconfig`/`route` behavior but hasn't been tested on
  an actual Mac yet.

### Configuration

`boring-vpn` reads its own config file, independent of `.boring.toml`:
`~/.boring-vpn.toml` on macOS, `$XDG_CONFIG_HOME/boring-vpn/.boring-vpn.toml`
on Linux (override with `$BORING_VPN_CONFIG`).

```toml
[[vpns]]
name = "office-vpn"
host = "bastion"                        # matches ssh config, like boring
subnets = ["10.0.0.0/8", "192.168.50.0/24"]
exclude_subnets = ["10.0.5.0/24"]       # optional: keep this going through the normal route
mtu = 1420                               # optional, defaults to 1420
```

| **Option**        | **Description**                                                                                       |
|--------------------|--------------------------------------------------------------------------------------------------------|
| `name`             | Name for the vpn. **Required.**                                                                        |
| `host`             | Host alias (matches SSH config) or hostname. **Required.**                                             |
| `subnets`          | CIDR ranges to route through the tunnel. **Required**, at least one.                                   |
| `exclude_subnets`  | (Optional) CIDR ranges to keep routing normally, even if they fall inside `subnets`.                    |
| `mtu`              | (Optional) TUN device MTU. Defaults to 1420.                                                            |
| `user`, `identity`, `port`, `keep_alive` | Same meaning as for `boring` tunnels.                                            |

### Usage

```
Usage:
  boring-vpn list, ls                      List all vpns
  boring-vpn up (-a | <patterns>...)       Start vpns matching any glob pattern
  boring-vpn down (-a | <patterns>...)     Stop vpns (same options as 'up')
  boring-vpn edit, e                       Edit the configuration file
  boring-vpn version, v                    Show the version number
  boring-vpn help, h                       Show this help message
```

Since the daemon runs as root, every command needs it too:

```sh
sudo boring-vpn up office-vpn
```

Running `boring list` also shows any vpns you have configured, since they're
easy to forget about otherwise -- `boring` and `boring-vpn` don't share a
daemon, so this is informational only, with a reminder to use
`sudo boring-vpn up`.

#### SSH authentication under sudo

Since the daemon has to run as root, it resolves your SSH connection
through a privilege-dropped helper that runs as the user named in
`$SUDO_USER` -- so `~/.ssh/config`, `known_hosts`, and identity files all
resolve exactly as they would in your own shell. The one thing that
doesn't carry over automatically is `ssh-agent`: `sudo` strips
`$SSH_AUTH_SOCK` by default, and there's no way to recover that value
afterward -- it isn't hidden, it's simply never passed through.

If you authenticate with an explicit `identity` file, this doesn't affect
you at all. If you rely on `ssh-agent` (e.g. a hardware token or a key
you don't keep unencrypted on disk), either:

```sh
sudo --preserve-env=SSH_AUTH_SOCK boring-vpn up office-vpn
```

or add `Defaults env_keep += "SSH_AUTH_SOCK"` to your sudoers file (`visudo`)
so it survives every time. Prefer `--preserve-env=SSH_AUTH_SOCK` over the
broader `sudo -E`: both pass through this one variable equally well and
neither leaks something like `LD_PRELOAD` (sudo strips that regardless of
`-E`), but plain `-E` also hands the daemon your entire environment --
`$HOME` included -- for no benefit here, since the privilege-dropped helper
already resolves `$HOME` correctly on its own.

### Building

```sh
make build-vpn
```

The binary lands in `./dist/boring-vpn`. This pulls in `boring-vpn`'s own
(much heavier) dependencies -- a userspace network stack, netlink -- the
first time; `boring`'s own build is never affected by them.

## Further Links
* pkg.go.dev: https://pkg.go.dev/github.com/alebeck/boring
* Coveralls: https://coveralls.io/github/alebeck/boring?branch=main

## Credits
Go gopher logo by Renee French.
