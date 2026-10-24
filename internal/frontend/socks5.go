package frontend

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"time"

	"proxy-socks5-balancer/internal/metrics"
)

// SOCKS5 is a minimal inbound SOCKS5 server (no auth, CONNECT only).
// It forwards every accepted connection through the selected upstream.
type SOCKS5 struct {
	Addr      string
	Dial      DialFunc
	Metrics   *metrics.Metrics
	AccessLog bool // log every finished connection at info level
}

func (s *SOCKS5) ListenAndServe(ctx context.Context) error {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", s.Addr)
	if err != nil {
		return err
	}
	slog.Info("socks5 frontend listening", "addr", s.Addr)
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		go s.handle(conn)
	}
}

func (s *SOCKS5) handle(conn net.Conn) {
	defer conn.Close()
	defer s.Metrics.SocksOpen()()
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	if err := s.handshake(conn); err != nil {
		return
	}
	target, err := s.readRequest(conn)
	if err != nil {
		return
	}

	start := time.Now()
	up, err := dialTimeout(s.Dial, "tcp", target, 15*time.Second)
	if err != nil {
		s.Metrics.DialError()
		// reply: general failure
		conn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		slog.Warn("socks5 dial failed", "client", conn.RemoteAddr().String(), "target", target, "err", err)
		return
	}
	defer up.Close()

	// reply: success (bound address is ignored by clients)
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}

	conn.SetDeadline(time.Time{})
	sent, received := pipe(conn, up)
	if s.AccessLog {
		access{proto: "socks5", client: conn.RemoteAddr().String(), target: target,
			upstream: upstreamName(up), sent: sent, received: received, start: start}.log()
	}
}

// handshake handles the SOCKS5 method negotiation (no-auth only).
func (s *SOCKS5) handshake(conn net.Conn) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != 0x05 {
		return fmt.Errorf("not socks5")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	// Select "no authentication required".
	_, err := conn.Write([]byte{0x05, 0x00})
	return err
}

// readRequest parses a CONNECT request and returns "host:port".
func (s *SOCKS5) readRequest(conn net.Conn) (string, error) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return "", err
	}
	// head[1] = command; 0x01 = CONNECT
	if head[1] != 0x01 {
		conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // command not supported
		return "", fmt.Errorf("unsupported command %d", head[1])
	}

	var host string
	switch head[3] { // address type
	case 0x01: // IPv4
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return "", err
		}
		host = net.IP(buf).String()
	case 0x03: // domain
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return "", err
		}
		buf := make([]byte, int(l[0]))
		if _, err := io.ReadFull(conn, buf); err != nil {
			return "", err
		}
		host = string(buf)
	case 0x04: // IPv6
		buf := make([]byte, 16)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return "", err
		}
		host = net.IP(buf).String()
	default:
		return "", fmt.Errorf("unsupported address type %d", head[3])
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(portBuf)
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}
