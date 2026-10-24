package upstream

import (
	"context"
	"net"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// Upstream is a single proxy endpoint and its live health state.
type Upstream struct {
	Name     string
	Addr     string // SOCKS5 host:port
	Priority int    // lower value is preferred; only the best healthy tier is used
	Weight   int    // >1 makes the upstream look proportionally faster (min 1)

	upThreshold   int     // consecutive passes required to mark healthy
	downThreshold int     // consecutive failures required to mark down
	ewma          float64 // latency smoothing factor in [0,1]; 0 uses raw samples

	mu         sync.RWMutex
	healthy    bool
	latency    time.Duration
	okCount    int
	total      int
	lastErr    string
	checkedAt  time.Time
	upStreak   int
	downStreak int

	// Cumulative counters since start (for Prometheus).
	checkTotal    int64
	checkFailures int64

	traffic traffic
}

// Params configures an upstream's selection weight and health hysteresis.
type Params struct {
	Priority      int
	Weight        int
	UpThreshold   int
	DownThreshold int
	LatencyEWMA   float64
}

// New creates an upstream (initially unhealthy until first check).
func New(name, addr string, p Params) *Upstream {
	if p.Weight < 1 {
		p.Weight = 1
	}
	if p.UpThreshold < 1 {
		p.UpThreshold = 1
	}
	if p.DownThreshold < 1 {
		p.DownThreshold = 1
	}
	return &Upstream{
		Name:          name,
		Addr:          addr,
		Priority:      p.Priority,
		Weight:        p.Weight,
		upThreshold:   p.UpThreshold,
		downThreshold: p.DownThreshold,
		ewma:          p.LatencyEWMA,
	}
}

// EffectiveLatency is the latency used for selection, scaled by weight so a
// heavier upstream is preferred at equal raw latency.
func (u *Upstream) EffectiveLatency() time.Duration {
	u.mu.RLock()
	defer u.mu.RUnlock()
	w := u.Weight
	if w < 1 {
		w = 1
	}
	return u.latency / time.Duration(w)
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
// It returns true when the healthy state flipped in this call.
func (u *Upstream) Report(ok, total int, latency time.Duration, lastErr string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	was := u.healthy
	u.okCount = ok
	u.total = total
	u.checkedAt = time.Now()
	u.checkTotal++

	pass := lastErr == "" // caller decides pass via empty error on success
	if pass {
		// Smooth the latency sample when EWMA is enabled and a prior sample
		// exists, otherwise take the raw value.
		if u.ewma > 0 && u.latency > 0 {
			u.latency = time.Duration(u.ewma*float64(latency) + (1-u.ewma)*float64(u.latency))
		} else {
			u.latency = latency
		}
		u.lastErr = ""
		u.upStreak++
		u.downStreak = 0
		if u.upStreak >= u.upThreshold {
			u.healthy = true
		}
	} else {
		u.latency = latency
		u.lastErr = lastErr
		u.checkFailures++
		u.downStreak++
		u.upStreak = 0
		if u.downStreak >= u.downThreshold {
			u.healthy = false
		}
	}
	return u.healthy != was
}

// Snapshot is an immutable view of the upstream state for status/selection.
type Snapshot struct {
	Name          string
	Addr          string
	Priority      int
	Weight        int
	Healthy       bool
	Latency       time.Duration
	OK            int
	Total         int
	LastErr       string
	CheckedAt     time.Time
	CheckTotal    int64
	CheckFailures int64
	RxBytes       int64 // relayed bytes from the upstream since start
	TxBytes       int64 // relayed bytes to the upstream since start
	RxRate        int64 // bytes/s from the upstream at the last sample
	TxRate        int64 // bytes/s to the upstream at the last sample
}

func (u *Upstream) Snapshot() Snapshot {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return Snapshot{
		Name:          u.Name,
		Addr:          u.Addr,
		Priority:      u.Priority,
		Weight:        u.Weight,
		Healthy:       u.healthy,
		Latency:       u.latency,
		OK:            u.okCount,
		Total:         u.total,
		LastErr:       u.lastErr,
		CheckedAt:     u.checkedAt,
		CheckTotal:    u.checkTotal,
		CheckFailures: u.checkFailures,
		RxBytes:       u.traffic.rx.Load(),
		TxBytes:       u.traffic.tx.Load(),
		RxRate:        u.traffic.rxRate.Load(),
		TxRate:        u.traffic.txRate.Load(),
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
