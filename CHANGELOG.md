# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.2] - 2026-10-24

### Added

- Health-check logging at `info`/`warn` level: `upstream up`, `upstream down`
  and `upstream check failed` with the error of every failed probe URL.
- `active upstream changed` now logs the `reason` and the previous upstream
  latency (`from_latency_ms`).
- Optional access log (`log.access: true`): every finished proxied connection
  is logged at `info` level as `msg=access` with proto, client, target,
  upstream, sent/received bytes and duration. Applied on restart only.
- Per-upstream traffic speed and byte counters: total speed in the status page
  header, a "Speed" column, `RxBytes`/`TxBytes`/`RxRate`/`TxRate` fields in
  `/status.json` and the `balancer_upstream_bytes_total{name,direction}`
  metric. Counters reset on reload and restart.

## [0.2.1] - 2026-10-23

### Added

- GitHub Actions CI (`gofmt`, `go vet`, race tests, build) on push and pull
  requests.
- GitHub Actions release workflow that builds `linux/amd64` and `linux/arm64`
  binaries, `.deb`/`.rpm` packages via nfpm and `.tar.gz` archives, then
  attaches them to a GitHub release on `vX.Y.Z` tags.
- `LICENSE` file (MIT), CI/release badges and an "Install from releases"
  section in the README.

### Fixed

- `nfpm.yaml` homepage now points at the real repository.

## [0.2.0] - 2026-10-23

### Added

- Manual upstream override on the status page. Each row has a "Switch here"
  button that pins the active upstream to that proxy and disables automatic
  selection; a "Back to auto" button restores it. Served via `POST /pin`
  (form field `name`) and `POST /auto`. The pin is kept by name so it survives
  a config reload, falls back to the best healthy upstream while the pinned one
  is down, and fires the `on_switch` hook like an automatic switch.
- Build-time version stamping. The binary reports its version via `-version`,
  logs it at startup and shows it in the status page footer. The version is
  taken from the git tag (`git describe`) and injected with `-ldflags`;
  `deploy/build-packages.sh` uses it for the binary and the deb/rpm version.

### Changed

- Health check accepts any `2xx`/`3xx` probe response and no longer follows
  redirects, so a `301`/`302` from a probe URL counts as success.

## [0.1.0] - 2026-10-22

First working version. The service fronts several local SOCKS5 upstreams
(v2ray/xray) and publishes one stable SOCKS5 port and one stable HTTP-proxy
port that always route through the fastest healthy upstream.

### Added

- Stable SOCKS5 frontend (`:10800`) and HTTP-proxy frontend (`:10801`,
  CONNECT tunneling and plain HTTP forwarding).
- Periodic health checker that probes every upstream through its own SOCKS5
  using several URLs; an upstream is healthy when at least `required` respond.
- Selector: priority tiers (`priority`, lower wins, higher tiers are standby)
  and weighted (`weight`) minimum-latency selection with a `switch_margin`
  to avoid flapping.
- Configurable hysteresis (`up_threshold` / `down_threshold`) and optional
  latency smoothing (`latency_ewma`).
- Passive health: a failed dial through the active upstream triggers an
  out-of-band re-check (rate-limited by `check.timeout`, coalesced) without
  marking the upstream down directly.
- Status web page (auto-refresh), `/status.json`, Prometheus `/metrics` and
  `/healthz` endpoints.
- Structured logging via `slog` (`log.level`, `log.format` text or json).
- `on_switch` command hook executed when the active upstream changes, with
  `BALANCER_OLD`, `BALANCER_OLD_ADDR`, `BALANCER_NEW`, `BALANCER_NEW_ADDR`
  and `BALANCER_NEW_LATENCY_MS` in the environment.
- Live config reload on `SIGHUP` without restarting the listeners; graceful
  shutdown on `SIGINT` / `SIGTERM`.
- deb/rpm packaging via nfpm and a hardened systemd unit.

### Documentation

- English and Russian guides under `docs/`, a `README.md`, an example
  `config.yaml`, example configurations under `examples/`, and an
  architecture diagram.

[Unreleased]: https://github.com/foxzi/proxy-socks5-balancer/compare/v0.2.2...HEAD
[0.2.2]: https://github.com/foxzi/proxy-socks5-balancer/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/foxzi/proxy-socks5-balancer/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/foxzi/proxy-socks5-balancer/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/foxzi/proxy-socks5-balancer/releases/tag/v0.1.0
