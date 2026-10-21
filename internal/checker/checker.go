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
}

func New(sel *selector.Selector, s Settings) *Checker {
	c := &Checker{sel: sel}
	c.settings.Store(&s)
	return c
}

// SetSettings swaps the check settings (used on config reload).
func (c *Checker) SetSettings(s Settings) { c.settings.Store(&s) }

// Run blocks until ctx is cancelled, probing on each interval.
func (c *Checker) Run(ctx context.Context) {
	c.cycle(ctx) // probe immediately on start
	for {
		s := c.settings.Load()
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.Interval):
			c.cycle(ctx)
		}
	}
}

func (c *Checker) cycle(ctx context.Context) {
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
