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

	// pinnedName, when non-empty, forces the active upstream to the one with
	// this name (manual override from the status page). It is kept by name so
	// it survives a config reload that rebuilds the upstream objects.
	pinnedName string
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

// Pin forces the active upstream to the one named, overriding automatic
// selection. It returns false if no upstream has that name. The change takes
// effect immediately; the pin survives config reloads by matching on name.
func (s *Selector) Pin(name string) (changed bool, from, to *upstream.Upstream, ok bool) {
	s.mu.Lock()
	found := false
	for _, u := range s.ups {
		if u.Name == name {
			found = true
			break
		}
	}
	if found {
		s.pinnedName = name
	}
	s.mu.Unlock()
	if !found {
		return false, nil, nil, false
	}
	changed, from, to = s.Reselect()
	return changed, from, to, true
}

// Unpin clears the manual pin and returns to automatic selection.
func (s *Selector) Unpin() (changed bool, from, to *upstream.Upstream) {
	s.mu.Lock()
	s.pinnedName = ""
	s.mu.Unlock()
	return s.Reselect()
}

// PinnedName returns the name of the pinned upstream, or "" when automatic.
func (s *Selector) PinnedName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pinnedName
}

// Reselect recomputes the active upstream after a check cycle.
// Only the healthy upstreams in the best (lowest) priority tier are
// considered. Within that tier it switches when the current is unhealthy or
// in a worse tier, or another upstream is faster by more than the margin
// (latency is weight-adjusted).
func (s *Selector) Reselect() (changed bool, from, to *upstream.Upstream) {
	ups := s.Upstreams()
	cur := s.cur.Load()

	// Manual pin: honor it while the pinned upstream exists and is healthy.
	// If it was removed on reload, clear the pin. If it is only unhealthy, fall
	// through to automatic selection so traffic keeps flowing; the pin stays
	// and resumes once the upstream recovers.
	if pinned := s.PinnedName(); pinned != "" {
		var pin *upstream.Upstream
		for _, u := range ups {
			if u.Name == pinned {
				pin = u
				break
			}
		}
		if pin == nil {
			s.mu.Lock()
			if s.pinnedName == pinned {
				s.pinnedName = ""
			}
			s.mu.Unlock()
		} else if pin.Healthy() {
			if cur != pin {
				s.cur.Store(pin)
				return true, cur, pin
			}
			return false, cur, cur
		}
	}

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
