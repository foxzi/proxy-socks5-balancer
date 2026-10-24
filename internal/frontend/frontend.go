package frontend

import (
	"context"
	"io"
	"log/slog"
	"net"
	"time"
)

// DialFunc dials the target through the currently selected upstream.
// It must return an error when no upstream is available.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// pipe copies data in both directions until either side closes and returns
// the bytes copied from a to b and from b to a.
func pipe(a, b net.Conn) (aToB, bToA int64) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn, n *int64) {
		*n, _ = io.Copy(dst, src)
		// Unblock the other direction on half-close.
		if c, ok := dst.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		}
		done <- struct{}{}
	}
	go cp(b, a, &aToB)
	go cp(a, b, &bToA)
	<-done
	<-done
	a.Close()
	b.Close()
	return aToB, bToA
}

// upstreamName returns the upstream carrying conn, if the dialer tagged it.
func upstreamName(c net.Conn) string {
	if n, ok := c.(interface{ UpstreamName() string }); ok {
		return n.UpstreamName()
	}
	return ""
}

// access is one finished proxied connection, logged when access logging is on.
type access struct {
	proto, client, target, upstream string
	sent, received                  int64 // bytes client->target, target->client
	start                           time.Time
}

func (a access) log() {
	slog.Info("access", "proto", a.proto, "client", a.client, "target", a.target,
		"upstream", a.upstream, "sent", a.sent, "received", a.received,
		"duration_ms", time.Since(a.start).Milliseconds())
}

// dialTimeout wraps a DialFunc with a dial deadline.
func dialTimeout(dial DialFunc, network, addr string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return dial(ctx, network, addr)
}
