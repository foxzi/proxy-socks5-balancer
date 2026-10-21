# proxy-socks5-balancer: documentation

[README](../README.md) | [Russian version](ru.md)

## Contents

1. Overview
2. Architecture
3. Configuration
4. Health-check algorithm
5. Selection strategy
6. Frontends
7. Status page and JSON
8. Signals
9. Adding an upstream
10. Running under systemd
11. Troubleshooting
12. deb/rpm packages

## 1. Overview

proxy-socks5-balancer is a Go service (Go 1.24; dependencies: `golang.org/x/net/proxy` for the SOCKS5 dialer and `gopkg.in/yaml.v3`). It health-checks several local SOCKS5 proxies (for example, provided by v2ray/xray instances) and publishes one stable SOCKS5 port and one stable HTTP-proxy port. Every connection to these ports goes through the fastest working upstream. The browser or program is configured once and never changed again: when an upstream fails or a faster one appears, the balancer switches transparently.

At any moment there is a single active upstream. Connections are not spread across several upstreams at once, there is no authentication, and there is no per-site routing.

## 2. Architecture

```
                         +--> 127.0.0.1:11080  xray-1 (SOCKS5)
browser / program        |
  --> SOCKS5 :10800 --+  |
  --> HTTP   :10801 --+--+--> 127.0.0.1:11081  xray-2 (SOCKS5)
                      |  |
        selector -----+  +--> ...
           ^
           |
        checker  (probes every upstream each `interval`)

  status :10802  -> web page and /status.json
```

Components:

- `cmd/balancer/main.go`: entrypoint, flag `-config` (default `config.yaml`), wiring of components, signal handling.
- `internal/config`: config loading, defaults and validation.
- `internal/upstream`: upstream model, health state, SOCKS5 dialing.
- `internal/checker`: periodic probing of upstreams.
- `internal/selector`: min-latency selection with switch margin.
- `internal/frontend`: inbound SOCKS5 and HTTP proxy servers.
- `internal/status`: status web page and JSON endpoint.

Both frontends use the same dial function: it asks the selector for the current upstream and dials the target through that upstream's SOCKS5. If there is no active upstream, the dial fails with `no healthy upstream available`.

Build and run:

```
go build -o balancer ./cmd/balancer
./balancer -config config.yaml
```

## 3. Configuration

The config is a YAML file (default `config.yaml` in the current directory, override with `-config`).

Example:

```yaml
listen:
  socks5: "127.0.0.1:10800"
  http:   "127.0.0.1:10801"
  status: "127.0.0.1:10802"
check:
  interval: 15s
  timeout: 5s
  required: 2
  switch_margin: 0.3
  urls:
    - "https://www.google.com/generate_204"
    - "https://www.cloudflare.com/cdn-cgi/trace"
    - "https://api.github.com"
upstreams:
  - name: xray-1
    socks5: "127.0.0.1:11080"
  - name: xray-2
    socks5: "127.0.0.1:11081"
```

### listen

| Field | Default | Meaning |
|-------|---------|---------|
| `listen.socks5` | `127.0.0.1:10800` | Address of the inbound SOCKS5 server. Point browsers and programs here. |
| `listen.http` | `127.0.0.1:10801` | Address of the inbound HTTP proxy. |
| `listen.status` | `127.0.0.1:10802` | Address of the status web page and `/status.json`. |

Listen addresses are read at startup. They are not changed by a config reload; to change them, restart the service.

### check

| Field | Default | Meaning |
|-------|---------|---------|
| `check.interval` | `15s` | Time between check cycles. |
| `check.timeout` | `5s` | Timeout of each individual check request. |
| `check.required` | `1` | Minimum number of check URLs (N of M) that must respond with HTTP status < 400 for an upstream to be healthy. |
| `check.switch_margin` | `0.3` | Fraction by which another healthy upstream must be faster than the current one to trigger a switch (0.3 = 30% faster). |
| `check.urls` | none | List of URLs fetched through each upstream. At least one is required. |

### upstreams

A list of local SOCKS5 proxies. At least one is required.

| Field | Default | Meaning |
|-------|---------|---------|
| `upstreams[].name` | none | Unique name, shown on the status page. Must not be empty. |
| `upstreams[].socks5` | none | Address of the upstream SOCKS5 proxy (`host:port`). Must not be empty. |

### Validation

Loading fails (at startup the process exits; on reload the error is logged and the previous configuration stays in effect) if:

- there are no upstreams;
- there are no check URLs;
- `required` is greater than the number of URLs;
- an upstream name is empty;
- an upstream `socks5` address is empty;
- two upstreams have the same name.

## 4. Health-check algorithm

Every `interval` the checker runs one cycle. In the cycle, all upstreams are checked in parallel. For each upstream, every URL from `check.urls` is fetched through that upstream's SOCKS5 with a per-request `timeout`.

- A request counts as successful if it gets an HTTP response with status < 400.
- The upstream passes the cycle if at least `required` of the M URLs succeeded (N of M).
- The latency of the upstream is the average latency of the successful requests.
- The number of successful and total requests (`ok/total`), the check time and the last error are recorded.

Example: with `required: 2` and three URLs, an upstream whose requests give 204, 200 and a timeout passes (2 of 3), and its latency is the average of the two successful requests. If only one request succeeds, it fails the cycle.

### Hysteresis

- An upstream becomes healthy immediately after the first successful cycle. This gives a fast cold start: the first working upstream is usable as soon as the first cycle finishes.
- A healthy upstream is marked down only after 2 consecutive failed cycles. A single transient error does not take an upstream out of service (anti-flapping).

With the default `interval` of 15s, a dead upstream is therefore detected within roughly two intervals, plus the request timeouts.

## 5. Selection strategy

The strategy is minimum latency with a switch margin. The active upstream is the healthy one with the lowest latency, but to avoid flapping between proxies of similar speed, the selector switches only when:

1. the current upstream becomes unhealthy, or
2. another healthy upstream is faster than the current one by more than `switch_margin`.

If no upstream is healthy, there is no active upstream and new connections fail with `no healthy upstream available`.

### Numeric example

`switch_margin: 0.3`, current active upstream `xray-1`.

| xray-1 latency | xray-2 latency | Result |
|----------------|----------------|--------|
| 200 ms | 170 ms | stay on xray-1 (xray-2 is 15% faster, below 30%) |
| 200 ms | 150 ms | stay on xray-1 (25% faster, below 30%) |
| 200 ms | 120 ms | switch to xray-2 (40% faster, above 30%) |
| 200 ms, unhealthy | 400 ms | switch to xray-2 (current is unhealthy, no margin needed) |

"X% faster" is computed as `(current - candidate) / current`. For example, `(200 - 120) / 200 = 0.4`.

A lower `switch_margin` makes the balancer follow the fastest upstream more eagerly; a higher one makes it stickier.

## 6. Frontends

Both frontends forward every connection through the currently selected upstream's SOCKS5. Neither requires authentication, so keep them bound to a loopback address unless you understand the consequences.

### SOCKS5 (`listen.socks5`)

A minimal inbound SOCKS5 server: no authentication, CONNECT command only.

### HTTP proxy (`listen.http`)

- `CONNECT` is supported for tunneling HTTPS.
- Plain HTTP requests are forwarded.
- If the upstream cannot be reached, the client receives HTTP 502 (Bad Gateway).

## 7. Status page and JSON

`http://<listen.status>/` (by default http://127.0.0.1:10802) is an HTML page that refreshes itself every 5 seconds. It shows the SOCKS5 and HTTP addresses and a table with, for each upstream:

| Column | Meaning |
|--------|---------|
| Active | Badge on the upstream currently used for all connections |
| Name | Upstream name |
| Address | Upstream SOCKS5 address |
| Health | up or down |
| Latency | Average latency of successful requests, `-` if not measured |
| OK/Total | Successful requests out of all check URLs in the last cycle |
| Checked | Time since the last check, `never` if not checked yet |
| Last error | Last error, if any |

`http://<listen.status>/status.json` returns the same data as a JSON array, one object per upstream:

```json
[
  {
    "Name": "xray-1",
    "Addr": "127.0.0.1:11080",
    "Healthy": true,
    "Latency": 143000000,
    "OK": 3,
    "Total": 3,
    "LastErr": "",
    "CheckedAt": "2025-01-01T12:00:00.123456789+03:00",
    "Active": true
  }
]
```

| Field | Type | Meaning |
|-------|------|---------|
| `Name` | string | Upstream name |
| `Addr` | string | Upstream SOCKS5 address |
| `Healthy` | bool | Current health state (after hysteresis) |
| `Latency` | number | Average latency in nanoseconds (Go `time.Duration`); 143000000 is 143 ms |
| `OK` | number | Successful check requests in the last cycle |
| `Total` | number | Total check requests in the last cycle |
| `LastErr` | string | Last error text, empty if none |
| `CheckedAt` | string | Time of the last check (RFC 3339); a zero time means never checked |
| `Active` | bool | True for the upstream currently in use |

Quick query:

```
curl -s http://127.0.0.1:10802/status.json
```

## 8. Signals

| Signal | Action |
|--------|--------|
| `SIGHUP` | Reload the config file. Upstreams, `switch_margin` and check settings (`interval`, `timeout`, `required`, `urls`) are swapped live; the listeners are not restarted and listen addresses are not changed. If the new config is invalid, the error is logged and the current configuration stays. |
| `SIGINT`, `SIGTERM` | Graceful shutdown. |

```
kill -HUP <pid>
```

Under systemd: `systemctl reload proxy-socks5-balancer`.

## 9. Adding an upstream

1. Start another local SOCKS5 proxy (for example, another xray instance on `127.0.0.1:11082`).
2. Add it to the config:

   ```yaml
   upstreams:
     - name: xray-1
       socks5: "127.0.0.1:11080"
     - name: xray-2
       socks5: "127.0.0.1:11081"
     - name: xray-3
       socks5: "127.0.0.1:11082"
   ```

3. Send `SIGHUP` to the balancer: `kill -HUP <pid>`.
4. Check the status page: the new upstream appears in the table. It becomes healthy after its first successful check cycle.

Names must be unique. Removing an upstream works the same way: edit the config and send `SIGHUP`.

## 10. Running under systemd

A unit file is provided at `deploy/proxy-socks5-balancer.service`. It expects:

- binary: `/usr/local/bin/proxy-socks5-balancer`;
- config: `/etc/proxy-socks5-balancer/config.yaml`;
- a non-root user and group `proxy-balancer`.

Example setup:

```
sudo useradd --system --no-create-home --shell /usr/sbin/nologin proxy-balancer
sudo install -m 0755 balancer /usr/local/bin/proxy-socks5-balancer
sudo install -d /etc/proxy-socks5-balancer
sudo install -m 0644 config.yaml /etc/proxy-socks5-balancer/config.yaml
sudo install -m 0644 deploy/proxy-socks5-balancer.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now proxy-socks5-balancer
```

## 11. Troubleshooting

### Connections fail with "no healthy upstream available"

No upstream is currently healthy, so there is no active upstream. Open the status page and check:

- Are the upstreams listed as down? Read the last error of each.
- Are the upstream SOCKS5 proxies (xray/v2ray) actually running and listening on the configured addresses?
- Can the upstream reach the check URLs? Try manually, for example:
  `curl -x socks5h://127.0.0.1:11080 https://www.google.com/generate_204`
- Is `check.required` too strict? With `required: 3` all three URLs must succeed; one blocked URL fails the upstream. Lower `required` or change `check.urls`.
- Is `check.timeout` too small for a slow upstream? Increase it.

### All upstreams are down at startup

The first cycle runs right after start. An upstream becomes healthy after the first successful cycle, so a short delay is normal. If it persists, see the previous section. Remember that a down mark needs 2 consecutive failed cycles, but an up mark needs only one.

### The active upstream does not change to a faster one

This is expected when the faster one is not faster by more than `switch_margin`. See the numeric example in section 5. Lower `switch_margin` and send `SIGHUP` if you want more eager switching.

### The active upstream flaps

Increase `switch_margin` or `check.interval`, or add more `check.urls` and increase `required` so that single slow responses matter less.

### Reload does not apply

Check the service log for `reload failed:`. The config failed validation, and the previous configuration is still in use. Changes to `listen` addresses are never applied by reload; restart the service.

### Startup error

The process exits on config errors and when a listener cannot start (for example, the port is already in use). Check the log message.

### How to read the status page

- The row with the `active` badge is the upstream carrying all traffic.
- Health `down` with a last error explains why the upstream failed its cycles.
- `OK/Total` of `1/3` with `required: 2` means the upstream failed the cycle.
- Latency of `-` means no successful measurement yet.
- Checked shows how long ago the last cycle for that upstream completed; it should stay within about one `interval`.

## 12. deb/rpm packages

Both packages are built from a single `nfpm.yaml` description with [nfpm](https://nfpm.goreleaser.com/) running in Docker, so nothing needs to be installed on the host.

Build:

```sh
VERSION=0.1.0 deploy/build-packages.sh
```

The script builds the binary (`CGO_ENABLED=0`, from the local module cache) into `dist/`, then runs nfpm twice and produces:

- `dist/proxy-socks5-balancer_0.1.0_amd64.deb`
- `dist/proxy-socks5-balancer-0.1.0-1.x86_64.rpm`

Package layout:

| Path | Purpose |
|------|---------|
| `/usr/bin/proxy-socks5-balancer` | binary |
| `/etc/proxy-socks5-balancer/config.yaml` | config (marked `config|noreplace`, kept on upgrade) |
| `/usr/lib/systemd/system/proxy-socks5-balancer.service` | systemd unit |
| `/usr/share/doc/proxy-socks5-balancer/` | README and documentation |

Maintainer scripts (`deploy/scripts/`):

- `postinstall` - creates the `proxy-balancer` system user and runs `systemctl daemon-reload`;
- `preremove` - stops the service before removal;
- `postremove` - runs `systemctl daemon-reload` after removal.

Install:

```sh
sudo apt install ./dist/proxy-socks5-balancer_0.1.0_amd64.deb     # Debian/Ubuntu
sudo dnf install ./dist/proxy-socks5-balancer-0.1.0-1.x86_64.rpm   # RHEL/Fedora

sudoedit /etc/proxy-socks5-balancer/config.yaml
sudo systemctl enable --now proxy-socks5-balancer
```
