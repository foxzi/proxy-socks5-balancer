package upstream

import (
	"net"
	"sync/atomic"
	"time"
)

// traffic counts client bytes relayed through an upstream and the current
// rate derived from periodic samples. Health-check probes are not counted.
type traffic struct {
	rx, tx         atomic.Int64 // bytes received from / sent to the upstream
	rxRate, txRate atomic.Int64 // bytes per second at the last sample
	lastRx, lastTx int64        // counters at the last sample (sampler only)
	lastAt         time.Time
}

// Track wraps a connection dialed through u so its bytes are counted.
func (u *Upstream) Track(c net.Conn) net.Conn {
	return &trackedConn{Conn: c, u: u}
}

// SampleRate updates the bytes-per-second rate from the counters. It must be
// called from a single goroutine (the periodic sampler).
func (u *Upstream) SampleRate(now time.Time) {
	t := &u.traffic
	rx, tx := t.rx.Load(), t.tx.Load()
	if !t.lastAt.IsZero() {
		if dt := now.Sub(t.lastAt).Seconds(); dt > 0 {
			t.rxRate.Store(int64(float64(rx-t.lastRx) / dt))
			t.txRate.Store(int64(float64(tx-t.lastTx) / dt))
		}
	}
	t.lastRx, t.lastTx, t.lastAt = rx, tx, now
}

type trackedConn struct {
	net.Conn
	u *Upstream
}

func (c *trackedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.u.traffic.rx.Add(int64(n))
	return n, err
}

func (c *trackedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.u.traffic.tx.Add(int64(n))
	return n, err
}

// CloseWrite keeps half-close working through the wrapper.
func (c *trackedConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// UpstreamName reports which upstream carries this connection (access log).
func (c *trackedConn) UpstreamName() string { return c.u.Name }
