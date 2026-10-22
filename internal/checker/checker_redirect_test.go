package checker

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"proxy-socks5-balancer/internal/selector"
	"proxy-socks5-balancer/internal/upstream"
)

// startSOCKS5 runs a minimal no-auth SOCKS5 CONNECT proxy and returns its address.
func startSOCKS5(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSOCKS5(c)
		}
	}()
	return ln.Addr().String()
}

func handleSOCKS5(c net.Conn) {
	defer c.Close()

	// Greeting: VER NMETHODS METHODS...
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil || hdr[0] != 5 {
		return
	}
	if _, err := io.ReadFull(c, make([]byte, hdr[1])); err != nil {
		return
	}
	if _, err := c.Write([]byte{5, 0}); err != nil {
		return
	}

	// Request: VER CMD RSV ATYP
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil || req[0] != 5 || req[1] != 1 {
		return
	}
	var host string
	switch req[3] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3:
		l := make([]byte, 1)
		if _, err := io.ReadFull(c, l); err != nil {
			return
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = string(b)
	default:
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(c, pb); err != nil {
		return
	}
	port := int(pb[0])<<8 | int(pb[1])

	target, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer target.Close()
	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}

	done := make(chan struct{}, 2)
	go func() { io.Copy(target, c); done <- struct{}{} }()
	go func() { io.Copy(c, target); done <- struct{}{} }()
	<-done
}

func probeOnce(t *testing.T, targetURL string) upstream.Snapshot {
	t.Helper()
	u := upstream.New("t", startSOCKS5(t), upstream.Params{Weight: 1, UpThreshold: 1, DownThreshold: 1})
	sel := selector.New(0)
	sel.SetUpstreams([]*upstream.Upstream{u})
	s := Settings{Timeout: 5 * time.Second, Required: 1, URLs: []string{targetURL}}
	c := New(sel, s)
	c.probe(context.Background(), u, &s)
	return u.Snapshot()
}

func TestProbeRedirectCountsAsHealthy(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(origin.Close)

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", origin.URL)
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	snap := probeOnce(t, redirector.URL)
	if !snap.Healthy {
		t.Fatalf("upstream not healthy after 302 probe; lastErr=%q", snap.LastErr)
	}
	if snap.LastErr != "" {
		t.Errorf("unexpected LastErr %q", snap.LastErr)
	}
	if snap.OK != 1 || snap.Total != 1 {
		t.Errorf("ok/total = %d/%d, want 1/1", snap.OK, snap.Total)
	}
	if snap.Latency <= 0 {
		t.Errorf("latency = %v, want > 0", snap.Latency)
	}
}

func TestProbeClientErrorIsUnhealthy(t *testing.T) {
	notFound := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(notFound.Close)

	snap := probeOnce(t, notFound.URL)
	if snap.Healthy {
		t.Fatal("upstream healthy after 404 probe, want unhealthy")
	}
	if snap.LastErr == "" {
		t.Error("expected LastErr to be set for 404")
	}
}
