package checker

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"proxy-socks5-balancer/internal/selector"
	"proxy-socks5-balancer/internal/upstream"
)

// Settings are the health-check parameters (swappable on reload).
type Settings struct {
	Interval time.Duration
	Timeout  time.Duration
	Required int
	URLs     []string
}

// Checker periodically probes every upstream and triggers reselection.
type Checker struct {
	sel      *selector.Selector
	settings atomic.Pointer[Settings]

	trigger   chan struct{} // coalesced request for an out-of-band cycle
	lastCycle atomic.Int64  // unixnano of the last cycle start

	// OnSwitch, if set, is called when the active upstream changes.
	OnSwitch func(from, to *upstream.Upstream)
}

func New(sel *selector.Selector, s Settings) *Checker {
	c := &Checker{sel: sel, trigger: make(chan struct{}, 1)}
	c.settings.Store(&s)
	return c
}

// SetSettings swaps the check settings (used on config reload).
func (c *Checker) SetSettings(s Settings) { c.settings.Store(&s) }

// Trigger requests an out-of-band check cycle, e.g. after a frontend dial
// error. Calls within the check timeout window are ignored to avoid hammering
// upstreams, and concurrent requests coalesce into a single cycle.
func (c *Checker) Trigger() {
	s := c.settings.Load()
	last := c.lastCycle.Load()
	if last != 0 && time.Since(time.Unix(0, last)) < s.Timeout {
		return
	}
	select {
	case c.trigger <- struct{}{}:
	default:
	}
}

// Run blocks until ctx is cancelled, probing on each interval.
func (c *Checker) Run(ctx context.Context) {
	c.cycle(ctx) // probe immediately on start
	for {
		s := c.settings.Load()
		select {
		case <-ctx.Done():
			return
		case <-c.trigger:
			c.cycle(ctx)
		case <-time.After(s.Interval):
			c.cycle(ctx)
		}
	}
}

func (c *Checker) cycle(ctx context.Context) {
	c.lastCycle.Store(time.Now().UnixNano())
	s := c.settings.Load()
	ups := c.sel.Upstreams()

	var wg sync.WaitGroup
	for _, u := range ups {
		wg.Add(1)
		go func(u *upstream.Upstream) {
			defer wg.Done()
			c.probe(ctx, u, s)
		}(u)
	}
	wg.Wait()

	if changed, from, to := c.sel.Reselect(); changed {
		slog.Info("active upstream changed",
			"from", name(from), "to", name(to),
			"latency_ms", latencyMS(to))
		if c.OnSwitch != nil {
			c.OnSwitch(from, to)
		}
	}
}

// probe runs all check URLs through the upstream and reports the result.
func (c *Checker) probe(ctx context.Context, u *upstream.Upstream, s *Settings) {
	client := &http.Client{
		Timeout: s.Timeout,
		Transport: &http.Transport{
			DialContext:       u.DialContext,
			DisableKeepAlives: true,
		},
		// Do not follow redirects: a 3xx already proves the upstream reached
		// the origin, and following it could land on an unrelated page (login,
		// captive portal, a host the proxy cannot reach) and fail the check.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	var ok int
	var totalLatency time.Duration
	var lastErr string
	for _, url := range s.URLs {
		start := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		resp.Body.Close()
		// 2xx and 3xx count as success (many probe URLs answer with 301/302);
		// only 4xx/5xx mark the URL as failed.
		if resp.StatusCode >= 400 {
			lastErr = "http " + resp.Status
			continue
		}
		ok++
		totalLatency += time.Since(start)
	}

	var latency time.Duration
	if ok > 0 {
		latency = totalLatency / time.Duration(ok)
	}
	passed := ok >= s.Required
	if passed {
		lastErr = "" // empty error signals a passing cycle to Report
	}
	u.Report(ok, len(s.URLs), latency, lastErr)

	slog.Debug("checked upstream",
		"upstream", u.Name, "addr", u.Addr,
		"ok", ok, "total", len(s.URLs),
		"latency_ms", latency.Milliseconds(),
		"healthy", u.Healthy(), "passed", passed)
}

func name(u *upstream.Upstream) string {
	if u == nil {
		return "none"
	}
	return u.Name
}

func latencyMS(u *upstream.Upstream) int64 {
	if u == nil {
		return 0
	}
	return u.Latency().Milliseconds()
}
