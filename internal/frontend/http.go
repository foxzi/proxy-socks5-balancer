package frontend

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// HTTPProxy is an inbound HTTP proxy supporting CONNECT (tunnel) and plain
// HTTP forwarding. All traffic goes through the selected upstream.
type HTTPProxy struct {
	Addr string
	Dial DialFunc
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
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

// handleConnect tunnels raw bytes through the upstream (used for HTTPS).
func (p *HTTPProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	up, err := dialTimeout(p.Dial, "tcp", r.Host, 15*time.Second)
	if err != nil {
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
	pipe(client, up)
}

// handleHTTP forwards a plain HTTP request through the upstream.
func (p *HTTPProxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	transport := &http.Transport{
		DialContext:       p.Dial,
		DisableKeepAlives: true,
	}
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	removeHopHeaders(outReq.Header)

	resp, err := transport.RoundTrip(outReq)
	if err != nil {
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
	io.Copy(w, resp.Body)
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
