package frontend

import (
	"context"
	"io"
	"net"
	"time"
)

// DialFunc dials the target through the currently selected upstream.
// It must return an error when no upstream is available.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// pipe copies data in both directions until either side closes.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		io.Copy(dst, src)
		// Unblock the other direction on half-close.
		if c, ok := dst.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	<-done
	a.Close()
	b.Close()
}

// dialTimeout wraps a DialFunc with a dial deadline.
func dialTimeout(dial DialFunc, network, addr string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return dial(ctx, network, addr)
}
