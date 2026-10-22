# proxy-socks5-balancer

A Go service that health-checks several local SOCKS5 proxies (provided by v2ray/xray instances) and publishes one stable SOCKS5 port and one stable HTTP-proxy port that always route through the fastest working upstream. Configure the browser or program once; the balancer switches the upstream transparently when one fails or a faster one appears.

Documentation: [English](docs/en.md) | [Russian](docs/ru.md)

Example configs: [examples/](examples/) | Changes: [CHANGELOG.md](CHANGELOG.md)

## Features

- One stable SOCKS5 port and one stable HTTP-proxy port (CONNECT tunneling and plain HTTP forwarding).
- Periodic health checks of every upstream through its own SOCKS5 using several URLs; an upstream is healthy if at least `required` of them respond.
- Selection of the healthy upstream with the lowest latency, with a switch margin to avoid flapping.
- Upstream `priority` tiers (lower wins, higher tiers are standby) and `weight` to prefer faster-looking upstreams within a tier.
- Hysteresis: configurable `up_threshold` / `down_threshold` (defaults: fast cold start, marked down after 2 consecutive failed cycles) and optional latency smoothing (`latency_ewma`).
- Passive health: a failed dial through the active upstream triggers an immediate re-check.
- Status web page (auto-refresh every 5 s), `/status.json`, Prometheus `/metrics` and `/healthz`.
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

The checker probes all upstreams every `interval`. The selector keeps a single active upstream: among the healthy upstreams in the best (lowest) `priority` tier, the one with the lowest latency (divided by `weight`). It switches only when the current one becomes unhealthy, another healthy one in the same tier is faster by more than `switch_margin`, or a healthy upstream appears in a better (lower-number) tier. The tier change preempts immediately, regardless of `switch_margin`; the margin applies only when choosing among upstreams within the same tier. Every inbound connection is forwarded through the active upstream. If no upstream is healthy, new connections fail with `no healthy upstream available`.

Passive health: when a frontend fails to dial through the active upstream, the balancer triggers an out-of-band health re-check instead of waiting for the next `interval`. The re-check is rate-limited by `check.timeout` and coalesced. A dial failure does not mark the upstream down by itself, so a single bad target host cannot eject a healthy upstream.

## Quick start

Requires Go 1.24.

```
go build -o balancer ./cmd/balancer
```

Edit `config.yaml` (list your local SOCKS5 upstreams), then run:

```
./balancer -config config.yaml
```

Check the build version with `./balancer -version` (it is also logged at
startup and shown in the status page footer).

Browser setup:

- SOCKS5 proxy: `127.0.0.1:10800`, or
- HTTP proxy: `127.0.0.1:10801`.

Status page: http://127.0.0.1:10802

A systemd unit is provided in `deploy/proxy-socks5-balancer.service` (binary at `/usr/bin/proxy-socks5-balancer`, config at `/etc/proxy-socks5-balancer/config.yaml`, user `proxy-balancer`).

## Packages (deb / rpm)

Build `.deb` and `.rpm` packages with [nfpm](https://nfpm.goreleaser.com/) running in Docker (nothing to install on the host):

```sh
git tag -a v0.1.0 -m "release 0.1.0"   # version comes from the git tag
deploy/build-packages.sh
# -> dist/proxy-socks5-balancer_0.1.0_amd64.deb
# -> dist/proxy-socks5-balancer-0.1.0-1.x86_64.rpm
```

The version is derived from `git describe --tags`; override it explicitly with
`VERSION=1.2.3 deploy/build-packages.sh`. It is stamped into the binary
(`-version`) and used as the package version.

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
| `log.level` | `info` | Log verbosity: debug, info, warn, error |
| `log.format` | `text` | Log format: text or json (stdout, journald-friendly) |
| `check.interval` | `15s` | Time between check cycles |
| `check.timeout` | `5s` | Timeout of each check request |
| `check.required` | `1` | Minimum number of check URLs that must succeed (HTTP status < 400) |
| `check.switch_margin` | `0.3` | Fraction by which another upstream must be faster to trigger a switch |
| `check.up_threshold` | `1` | Consecutive successful check cycles before an upstream is marked healthy |
| `check.down_threshold` | `2` | Consecutive failed check cycles before a healthy upstream is marked down |
| `check.latency_ewma` | `0` | Latency smoothing factor, 0..1; `0` uses the raw latest sample |
| `check.urls` | none (required) | URLs fetched through each upstream |
| `on_switch.command` | empty | Shell command run on each active-upstream change (disabled if empty) |
| `on_switch.timeout` | `10s` | Max run time of the on_switch command |
| `upstreams[].name` | none (required) | Unique upstream name |
| `upstreams[].socks5` | none (required) | Upstream SOCKS5 address |
| `upstreams[].priority` | `0` | Lower value wins; only healthy upstreams in the best (lowest) tier are used, higher-number tiers are standby/failover |
| `upstreams[].weight` | `1` | Defaults to 1; `0` is accepted and treated as 1, a negative value is rejected. A higher weight divides the measured latency for selection, so the upstream is preferred within its tier |

`latency_ewma` smooths the measured latency over time to reduce selection flapping: `smoothed = ewma*new + (1-ewma)*old`. A value like `0.3` is a reasonable start.

Validation fails on: no upstreams, no check URLs, `required` greater than the number of URLs, empty upstream name, empty SOCKS5 address, duplicate upstream name, `latency_ewma` outside 0..1, negative `weight`.

## Status page

`http://<listen.status>/` shows, for each upstream: name, address, priority/weight, health (up/down), latency, ok/total, last check time and last error, and marks the active upstream. `http://<listen.status>/status.json` returns the same data as JSON.

The status server also exposes:

- `GET /metrics`: Prometheus text-format metrics, e.g. `balancer_upstream_up{name="xray-1"}` (1 healthy / 0 down), `balancer_upstream_latency_ms{name="xray-1"}`, `balancer_frontend_connections_total{frontend="socks5"}`, `balancer_frontend_dial_errors_total`.
- `GET /healthz`: 200 (body starts with `ok`) when at least one upstream is healthy, otherwise 503. Useful for load balancers, systemd and monitoring.

## Config reload

```
kill -HUP <pid>
```

Reloads the config file and swaps upstreams, margin, check settings (`interval`, `timeout`, `required`, `urls`, `up_threshold`, `down_threshold`, `latency_ewma`) and the `on_switch` hook live. Listen addresses are not rebound. Because upstreams are rebuilt, all of them start as unhealthy and the active selection is dropped; the balancer re-probes on the next check cycle, so for up to one `interval` new connections may fail with `no healthy upstream available`. SIGINT/SIGTERM shut the service down gracefully.

## On-switch hook

Set `on_switch.command` to run a command whenever the active upstream changes. It runs via `sh -c` in the background, as the service user. The event is passed as environment variables: `BALANCER_OLD`, `BALANCER_OLD_ADDR`, `BALANCER_NEW`, `BALANCER_NEW_ADDR`, `BALANCER_NEW_LATENCY_MS`.

```yaml
on_switch:
  command: 'notify-send "proxy -> $BALANCER_NEW ($BALANCER_NEW_LATENCY_MS ms)"'
  timeout: 10s
```

## Logging

Logs are structured (Go `slog`) and written to stdout, so journald captures them directly. `log.level` is `debug|info|warn|error`; `debug` adds a line per upstream per check cycle. `log.format` is `text` or `json`.

## Project layout

```
cmd/balancer/main.go    entrypoint, flags, wiring, signal handling
internal/config         config loading and validation
internal/upstream       upstream model, health state, SOCKS5 dialing
internal/checker        periodic probing
internal/selector       priority-tier + weighted min-latency selection with switch margin
internal/frontend       inbound SOCKS5 and HTTP proxy servers
internal/status         status web page, JSON, /metrics and /healthz
internal/metrics        runtime counters for /metrics
internal/hook           on_switch command hook
deploy/                 systemd unit, packaging scripts, build-packages.sh
docs/                   full documentation (en, ru)
```
