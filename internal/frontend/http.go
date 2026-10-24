package frontend

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"proxy-socks5-balancer/internal/metrics"
)

// HTTPProxy is an inbound HTTP proxy supporting CONNECT (tunnel) and plain
// HTTP forwarding. All traffic goes through the selected upstream.
type HTTPProxy struct {
	Addr      string
	Dial      DialFunc
	Metrics   *metrics.Metrics
	AccessLog bool // log every finished request/tunnel at info level
}

func (p *HTTPProxy) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:    p.Addr,
		Handler: p,
	}
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", p.Addr)
	if err != nil {
		return err
	}
	slog.Info("http frontend listening", "addr", p.Addr)
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()
	err = srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (p *HTTPProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer p.Metrics.HTTPOpen()()
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

// handleConnect tunnels raw bytes through the upstream (used for HTTPS).
func (p *HTTPProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	up, err := dialTimeout(p.Dial, "tcp", r.Host, 15*time.Second)
	if err != nil {
		p.Metrics.DialError()
		slog.Warn("http connect dial failed", "client", r.RemoteAddr, "target", r.Host, "err", err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		up.Close()
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		up.Close()
		return
	}
	client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	sent, received := pipe(client, up)
	if p.AccessLog {
		access{proto: "http-connect", client: r.RemoteAddr, target: r.Host,
			upstream: upstreamName(up), sent: sent, received: received, start: start}.log()
	}
}

// handleHTTP forwards a plain HTTP request through the upstream.
func (p *HTTPProxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var upName string
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := p.Dial(ctx, network, addr)
			if err == nil {
				upName = upstreamName(c)
			}
			return c, err
		},
		DisableKeepAlives: true,
	}
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	removeHopHeaders(outReq.Header)

	resp, err := transport.RoundTrip(outReq)
	if err != nil {
		p.Metrics.DialError()
		slog.Warn("http forward failed", "client", r.RemoteAddr, "target", r.Host, "err", err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	removeHopHeaders(resp.Header)
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	received, _ := io.Copy(w, resp.Body)
	if p.AccessLog {
		slog.Info("access", "proto", "http", "client", r.RemoteAddr, "method", r.Method,
			"target", r.URL.String(), "upstream", upName, "status", resp.StatusCode,
			"received", received, "duration_ms", time.Since(start).Milliseconds())
	}
}

// hopByHop headers must not be forwarded by a proxy.
var hopByHop = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func removeHopHeaders(h http.Header) {
	for _, k := range hopByHop {
		h.Del(k)
	}
}
