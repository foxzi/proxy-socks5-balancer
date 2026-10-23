package selector

import (
	"testing"
	"time"

	"proxy-socks5-balancer/internal/upstream"
)

// healthyUpstream builds an upstream and marks it healthy with the given latency.
func healthyUpstream(name string, latencyMS int) *upstream.Upstream {
	u := upstream.New(name, "127.0.0.1:0", upstream.Params{UpThreshold: 1, DownThreshold: 1})
	u.Report(1, 1, time.Duration(latencyMS)*time.Millisecond, "")
	return u
}

func TestPinOverridesAutoSelection(t *testing.T) {
	fast := healthyUpstream("fast", 10)
	slow := healthyUpstream("slow", 500)
	s := New(0.3)
	s.SetUpstreams([]*upstream.Upstream{fast, slow})

	// Auto picks the fastest.
	s.Reselect()
	if got := s.Current(); got != fast {
		t.Fatalf("auto: expected fast active, got %v", got)
	}

	// Pin the slow one: it must become and stay active despite being slower.
	changed, _, to, ok := s.Pin("slow")
	if !ok || !changed || to != slow {
		t.Fatalf("pin: ok=%v changed=%v to=%v", ok, changed, to)
	}
	s.Reselect()
	if got := s.Current(); got != slow {
		t.Fatalf("pin: expected slow active, got %v", got)
	}
	if s.PinnedName() != "slow" {
		t.Fatalf("pin: expected PinnedName slow, got %q", s.PinnedName())
	}
}

func TestPinUnknownUpstream(t *testing.T) {
	s := New(0.3)
	s.SetUpstreams([]*upstream.Upstream{healthyUpstream("a", 10)})
	if _, _, _, ok := s.Pin("missing"); ok {
		t.Fatal("pin of unknown upstream should fail")
	}
	if s.PinnedName() != "" {
		t.Fatalf("expected no pin, got %q", s.PinnedName())
	}
}

func TestUnpinReturnsToAuto(t *testing.T) {
	fast := healthyUpstream("fast", 10)
	slow := healthyUpstream("slow", 500)
	s := New(0.3)
	s.SetUpstreams([]*upstream.Upstream{fast, slow})
	s.Pin("slow")

	changed, _, to := s.Unpin()
	if !changed || to != fast {
		t.Fatalf("unpin: changed=%v to=%v", changed, to)
	}
	if s.PinnedName() != "" {
		t.Fatalf("unpin: expected no pin, got %q", s.PinnedName())
	}
}

func TestPinnedUnhealthyFallsBackButKeepsPin(t *testing.T) {
	fast := healthyUpstream("fast", 10)
	slow := healthyUpstream("slow", 500)
	s := New(0.3)
	s.SetUpstreams([]*upstream.Upstream{fast, slow})
	s.Pin("slow")

	// Slow goes down: traffic must fall back to fast, but the pin stays.
	slow.Report(0, 1, 0, "boom")
	s.Reselect()
	if got := s.Current(); got != fast {
		t.Fatalf("fallback: expected fast active, got %v", got)
	}
	if s.PinnedName() != "slow" {
		t.Fatalf("fallback: pin should stay, got %q", s.PinnedName())
	}

	// Slow recovers: it must be reselected automatically.
	slow.Report(1, 1, 500*time.Millisecond, "")
	s.Reselect()
	if got := s.Current(); got != slow {
		t.Fatalf("recover: expected slow active, got %v", got)
	}
}

func TestPinnedRemovedOnReloadClearsPin(t *testing.T) {
	fast := healthyUpstream("fast", 10)
	slow := healthyUpstream("slow", 500)
	s := New(0.3)
	s.SetUpstreams([]*upstream.Upstream{fast, slow})
	s.Pin("slow")

	// Reload without the pinned upstream: pin is dropped, auto resumes.
	s.SetUpstreams([]*upstream.Upstream{fast})
	s.Reselect()
	if s.PinnedName() != "" {
		t.Fatalf("expected pin cleared after removal, got %q", s.PinnedName())
	}
	if got := s.Current(); got != fast {
		t.Fatalf("expected fast active, got %v", got)
	}
}
