# Contributing to `boring`

Thanks for your interest in `boring`! :) Contributions are welcome, this guide
exists to make sure your time is well spent.

## Scope

`boring` aims to be small, focused, and lightweight. It is build to do only one thing (manage and run SSH tunnels) and to do it well. That means the bar for adding new features, config options, and dependencies is intentionally high.

`boring-vpn` (`cmd/boring-vpn`, `internal/vpn`, `internal/vpnd`) is a separate binary living in this repo for sshuttle-style subnet routing -- see the README. It exists precisely because that feature doesn't fit `boring`'s scope: it needs root and a TUN device. The heavier dependencies it needs for that (a userspace network stack, netlink) live in `internal/vpnd`, which only `cmd/boring-vpn` ever imports -- `internal/vpn` stays a small, dependency-light package (just the config/IPC types) that `cmd/boring` also imports, for the `boring list` hint. Keep it that way: `cmd/boring`, `internal/tunnel`, `internal/daemon`, and `internal/config` should never import `internal/vpnd`, and `internal/vpn` itself should never grow to need `internal/vpnd`'s dependencies.

## Before opening a PR

- Bug fixes and **small** improvements: open a PR directly.
- Anything that adds a feature, a config option, or a dependency: please
  open an issue first so we can agree on scope and approach.

## Submitting changes

- Run `make test` and make sure everything passes.
- Keep changes formatted (`gofmt`) and `go vet`-clean
- Keep PRs small and focused.

Thanks for your contributions!
