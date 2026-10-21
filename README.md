# proxy-socks5-balancer

A Go service that health-checks several local SOCKS5 proxies (provided by v2ray/xray instances) and publishes one stable SOCKS5 port and one stable HTTP-proxy port that always route through the fastest working upstream. Configure the browser or program once; the balancer switches the upstream transparently when one fails or a faster one appears.

Documentation: [English](docs/en.md) | [Russian](docs/ru.md)

## Features

- One stable SOCKS5 port and one stable HTTP-proxy port (CONNECT tunneling and plain HTTP forwarding).
- Periodic health checks of every upstream through its own SOCKS5 using several URLs; an upstream is healthy if at least `required` of them respond.
- Selection of the healthy upstream with the lowest latency, with a switch margin to avoid flapping.
- Hysteresis: fast cold start, marked down only after 2 consecutive failed cycles.
- Status web page (auto-refresh every 5 s) and `/status.json`.
- Live config reload on SIGHUP without restarting the listeners; graceful shutdown on SIGINT/SIGTERM.

## How it works

```
                      +--> 127.0.0.1:11080  xray-1 (SOCKS5)
browser / program     |
  --> balancer  ------+--> 127.0.0.1:11081  xray-2 (SOCKS5)
  SOCKS5 :10800       |
  HTTP   :10801       +--> ...
  status :10802
```

The checker probes all upstreams every `interval`. The selector keeps a single active upstream: the healthy one with the lowest latency. It switches only when the current one becomes unhealthy or another healthy one is faster by more than `switch_margin`. Every inbound connection is forwarded through the active upstream. If no upstream is healthy, new connections fail with `no healthy upstream available`.

## Quick start

Requires Go 1.24.

```
go build -o balancer ./cmd/balancer
```

Edit `config.yaml` (list your local SOCKS5 upstreams), then run:

```
./balancer -config config.yaml
```

Browser setup:

- SOCKS5 proxy: `127.0.0.1:10800`, or
- HTTP proxy: `127.0.0.1:10801`.

Status page: http://127.0.0.1:10802

A systemd unit is provided in `deploy/proxy-socks5-balancer.service` (binary at `/usr/bin/proxy-socks5-balancer`, config at `/etc/proxy-socks5-balancer/config.yaml`, user `proxy-balancer`).

## Packages (deb / rpm)

Build `.deb` and `.rpm` packages with [nfpm](https://nfpm.goreleaser.com/) running in Docker (nothing to install on the host):

```sh
VERSION=0.1.0 deploy/build-packages.sh
# -> dist/proxy-socks5-balancer_0.1.0_amd64.deb
# -> dist/proxy-socks5-balancer-0.1.0-1.x86_64.rpm
```

Install:

```sh
sudo apt install ./dist/proxy-socks5-balancer_0.1.0_amd64.deb   # Debian/Ubuntu
sudo dnf install ./dist/proxy-socks5-balancer-0.1.0-1.x86_64.rpm # RHEL/Fedora
```

The package creates the `proxy-balancer` system user, installs the systemd unit
and ships `config.yaml` as a protected config file (not overwritten on upgrade).
After install:

```sh
sudoedit /etc/proxy-socks5-balancer/config.yaml
sudo systemctl enable --now proxy-socks5-balancer
```

Packaging layout is defined in `nfpm.yaml`; maintainer scripts live in `deploy/scripts/`.

## Configuration reference

| Field | Default | Description |
|-------|---------|-------------|
| `listen.socks5` | `127.0.0.1:10800` | Inbound SOCKS5 address |
| `listen.http` | `127.0.0.1:10801` | Inbound HTTP proxy address |
| `listen.status` | `127.0.0.1:10802` | Status page address |
| `check.interval` | `15s` | Time between check cycles |
| `check.timeout` | `5s` | Timeout of each check request |
| `check.required` | `1` | Minimum number of check URLs that must succeed (HTTP status < 400) |
| `check.switch_margin` | `0.3` | Fraction by which another upstream must be faster to trigger a switch |
| `check.urls` | none (required) | URLs fetched through each upstream |
| `upstreams[].name` | none (required) | Unique upstream name |
| `upstreams[].socks5` | none (required) | Upstream SOCKS5 address |

Validation fails on: no upstreams, no check URLs, `required` greater than the number of URLs, empty upstream name, empty SOCKS5 address, duplicate upstream name.

## Status page

`http://<listen.status>/` shows, for each upstream: name, address, health (up/down), latency, ok/total, last check time and last error, and marks the active upstream. `http://<listen.status>/status.json` returns the same data as JSON.

## Config reload

```
kill -HUP <pid>
```

Reloads the config file and swaps upstreams, margin and check settings live. Listen addresses are not rebound. SIGINT/SIGTERM shut the service down gracefully.

## Project layout

```
cmd/balancer/main.go    entrypoint, flags, wiring, signal handling
internal/config         config loading and validation
internal/upstream       upstream model, health state, SOCKS5 dialing
internal/checker        periodic probing
internal/selector       min-latency selection with switch margin
internal/frontend       inbound SOCKS5 and HTTP proxy servers
internal/status         status web page and JSON endpoint
deploy/                 systemd unit
docs/                   full documentation (en, ru)
```
