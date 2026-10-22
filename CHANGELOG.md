# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

No unreleased changes yet.

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

[Unreleased]: https://example.com/compare/v0.1.0...HEAD
[0.1.0]: https://example.com/releases/tag/v0.1.0
