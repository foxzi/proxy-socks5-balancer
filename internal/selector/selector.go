package selector

import (
	"sync"
	"sync/atomic"

	"proxy-socks5-balancer/internal/upstream"
)

// Selector owns the live upstream set and picks the active one by lowest
// latency, with a switch margin to avoid flapping between similar proxies.
type Selector struct {
	mu     sync.RWMutex
	ups    []*upstream.Upstream
	cur    atomic.Pointer[upstream.Upstream]
	margin float64
}

func New(margin float64) *Selector {
	return &Selector{margin: margin}
}

// SetUpstreams replaces the upstream set (used on config reload).
func (s *Selector) SetUpstreams(ups []*upstream.Upstream) {
	s.mu.Lock()
	s.ups = ups
	s.mu.Unlock()
	// Drop the active pointer if it is no longer part of the set.
	cur := s.cur.Load()
	if cur != nil {
		found := false
		for _, u := range ups {
			if u == cur {
				found = true
				break
			}
		}
		if !found {
			s.cur.Store(nil)
		}
	}
}

// SetMargin updates the switch margin (used on config reload).
func (s *Selector) SetMargin(m float64) { s.margin = m }

// Upstreams returns a snapshot copy of the current set.
func (s *Selector) Upstreams() []*upstream.Upstream {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*upstream.Upstream, len(s.ups))
	copy(out, s.ups)
	return out
}

// Current returns the active upstream, or nil if none is healthy.
func (s *Selector) Current() *upstream.Upstream { return s.cur.Load() }

// Reselect recomputes the active upstream after a check cycle.
// Only the healthy upstreams in the best (lowest) priority tier are
// considered. Within that tier it switches when the current is unhealthy or
// in a worse tier, or another upstream is faster by more than the margin
// (latency is weight-adjusted).
func (s *Selector) Reselect() (changed bool, from, to *upstream.Upstream) {
	ups := s.Upstreams()

	// Best priority value present among healthy upstreams.
	bestPrio := 0
	havePrio := false
	for _, u := range ups {
		if !u.Healthy() {
			continue
		}
		if !havePrio || u.Priority < bestPrio {
			bestPrio, havePrio = u.Priority, true
		}
	}

	var best *upstream.Upstream
	if havePrio {
		for _, u := range ups {
			if !u.Healthy() || u.Priority != bestPrio {
				continue
			}
			if best == nil || u.EffectiveLatency() < best.EffectiveLatency() {
				best = u
			}
		}
	}

	cur := s.cur.Load()
	if best == nil {
		// No healthy upstream: keep nothing active.
		if cur != nil {
			s.cur.Store(nil)
			return true, cur, nil
		}
		return false, cur, nil
	}
	if cur == nil || !cur.Healthy() {
		s.cur.Store(best)
		return cur != best, cur, best
	}
	if best != cur {
		// A better priority tier always wins over the margin rule.
		if best.Priority < cur.Priority {
			s.cur.Store(best)
			return true, cur, best
		}
		threshold := float64(cur.EffectiveLatency()) * (1 - s.margin)
		if float64(best.EffectiveLatency()) < threshold {
			s.cur.Store(best)
			return true, cur, best
		}
	}
	return false, cur, cur
}
