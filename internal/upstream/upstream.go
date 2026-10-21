package upstream

import (
	"context"
	"net"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// hysteresis: how many consecutive failures mark a healthy upstream as down.
// Recovery is immediate (one success) for a fast cold start.
const downThreshold = 2

// Upstream is a single proxy endpoint and its live health state.
type Upstream struct {
	Name string
	Addr string // SOCKS5 host:port

	mu         sync.RWMutex
	healthy    bool
	latency    time.Duration
	okCount    int
	total      int
	lastErr    string
	checkedAt  time.Time
	upStreak   int
	downStreak int
}

// New creates an upstream (initially unhealthy until first check).
func New(name, addr string) *Upstream {
	return &Upstream{Name: name, Addr: addr}
}

// DialContext dials target through this upstream's SOCKS5 proxy.
func (u *Upstream) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d, err := proxy.SOCKS5("tcp", u.Addr, nil, &net.Dialer{Timeout: 10 * time.Second})
	if err != nil {
		return nil, err
	}
	if cd, ok := d.(proxy.ContextDialer); ok {
		return cd.DialContext(ctx, network, addr)
	}
	return d.Dial(network, addr)
}

// Report records the outcome of a check cycle and applies hysteresis.
func (u *Upstream) Report(ok, total int, latency time.Duration, lastErr string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.okCount = ok
	u.total = total
	u.latency = latency
	u.lastErr = lastErr
	u.checkedAt = time.Now()

	pass := lastErr == "" // caller decides pass via empty error on success
	if pass {
		// Recover immediately on the first successful cycle.
		u.upStreak++
		u.downStreak = 0
		u.healthy = true
	} else {
		u.downStreak++
		u.upStreak = 0
		if u.downStreak >= downThreshold {
			u.healthy = false
		}
	}
}

// Snapshot is an immutable view of the upstream state for status/selection.
type Snapshot struct {
	Name      string
	Addr      string
	Healthy   bool
	Latency   time.Duration
	OK        int
	Total     int
	LastErr   string
	CheckedAt time.Time
}

func (u *Upstream) Snapshot() Snapshot {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return Snapshot{
		Name:      u.Name,
		Addr:      u.Addr,
		Healthy:   u.healthy,
		Latency:   u.latency,
		OK:        u.okCount,
		Total:     u.total,
		LastErr:   u.lastErr,
		CheckedAt: u.checkedAt,
	}
}

func (u *Upstream) Healthy() bool {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.healthy
}

func (u *Upstream) Latency() time.Duration {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.latency
}
