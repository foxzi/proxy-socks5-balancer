package status

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"proxy-socks5-balancer/internal/metrics"
	"proxy-socks5-balancer/internal/selector"
	"proxy-socks5-balancer/internal/upstream"
)

// Server serves a simple status page and a JSON endpoint.
type Server struct {
	Addr    string
	Sel     *selector.Selector
	Metrics *metrics.Metrics
	Listen  struct {
		SOCKS5 string
		HTTP   string
	}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handlePage)
	mux.HandleFunc("/status.json", s.handleJSON)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/healthz", s.handleHealthz)

	srv := &http.Server{Addr: s.Addr, Handler: mux}
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", s.Addr)
	if err != nil {
		return err
	}
	slog.Info("status page listening", "addr", s.Addr)
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

type row struct {
	upstream.Snapshot
	Active bool
}

func (s *Server) rows() []row {
	cur := s.Sel.Current()
	ups := s.Sel.Upstreams()
	out := make([]row, 0, len(ups))
	for _, u := range ups {
		out = append(out, row{Snapshot: u.Snapshot(), Active: u == cur})
	}
	return out
}

func (s *Server) handleJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(s.rows())
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data := struct {
		Rows   []row
		SOCKS5 string
		HTTP   string
		Now    string
	}{
		Rows:   s.rows(),
		SOCKS5: s.Listen.SOCKS5,
		HTTP:   s.Listen.HTTP,
		Now:    time.Now().Format("15:04:05"),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTmpl.Execute(w, data); err != nil {
		slog.Error("status template error", "err", err)
	}
}

// handleHealthz returns 200 when at least one upstream is healthy, else 503.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	healthy := 0
	for _, u := range s.Sel.Upstreams() {
		if u.Healthy() {
			healthy++
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if healthy == 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintln(w, "unhealthy: no upstream available")
		return
	}
	fmt.Fprintf(w, "ok: %d healthy upstream(s)\n", healthy)
}

// handleMetrics exposes counters and per-upstream gauges in Prometheus text
// format. Written by hand to avoid a client-library dependency.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var b strings.Builder

	m := s.Metrics
	writeCounter := func(name, help string, v int64) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, v)
	}
	if m != nil {
		writeCounter("balancer_switches_total", "Active upstream changes.", m.Switches.Load())
		writeCounter("balancer_frontend_dial_errors_total", "Failed dials to the active upstream.", m.DialErrors.Load())

		fmt.Fprintf(&b, "# HELP balancer_frontend_connections_total Accepted frontend connections.\n# TYPE balancer_frontend_connections_total counter\n")
		fmt.Fprintf(&b, "balancer_frontend_connections_total{frontend=\"socks5\"} %d\n", m.SocksConns.Load())
		fmt.Fprintf(&b, "balancer_frontend_connections_total{frontend=\"http\"} %d\n", m.HTTPConns.Load())

		fmt.Fprintf(&b, "# HELP balancer_frontend_active_connections Open frontend connections.\n# TYPE balancer_frontend_active_connections gauge\n")
		fmt.Fprintf(&b, "balancer_frontend_active_connections{frontend=\"socks5\"} %d\n", m.SocksActive.Load())
		fmt.Fprintf(&b, "balancer_frontend_active_connections{frontend=\"http\"} %d\n", m.HTTPActive.Load())
	}

	cur := s.Sel.Current()
	curName := ""
	if cur != nil {
		curName = cur.Name
	}
	ups := s.Sel.Upstreams()

	fmt.Fprintf(&b, "# HELP balancer_upstream_up Whether the upstream passed its last check (1) or not (0).\n# TYPE balancer_upstream_up gauge\n")
	for _, u := range ups {
		sn := u.Snapshot()
		fmt.Fprintf(&b, "balancer_upstream_up{name=%q} %d\n", sn.Name, b2i(sn.Healthy))
	}
	fmt.Fprintf(&b, "# HELP balancer_upstream_latency_ms Last measured average latency in milliseconds.\n# TYPE balancer_upstream_latency_ms gauge\n")
	for _, u := range ups {
		sn := u.Snapshot()
		fmt.Fprintf(&b, "balancer_upstream_latency_ms{name=%q} %d\n", sn.Name, sn.Latency.Milliseconds())
	}
	fmt.Fprintf(&b, "# HELP balancer_upstream_checks_total Total check cycles run for the upstream.\n# TYPE balancer_upstream_checks_total counter\n")
	for _, u := range ups {
		sn := u.Snapshot()
		fmt.Fprintf(&b, "balancer_upstream_checks_total{name=%q} %d\n", sn.Name, sn.CheckTotal)
	}
	fmt.Fprintf(&b, "# HELP balancer_upstream_check_failures_total Total failed check cycles for the upstream.\n# TYPE balancer_upstream_check_failures_total counter\n")
	for _, u := range ups {
		sn := u.Snapshot()
		fmt.Fprintf(&b, "balancer_upstream_check_failures_total{name=%q} %d\n", sn.Name, sn.CheckFailures)
	}
	fmt.Fprintf(&b, "# HELP balancer_active_upstream Whether the upstream is currently active (1) or not (0).\n# TYPE balancer_active_upstream gauge\n")
	for _, u := range ups {
		sn := u.Snapshot()
		fmt.Fprintf(&b, "balancer_active_upstream{name=%q} %d\n", sn.Name, b2i(sn.Name == curName))
	}

	w.Write([]byte(b.String()))
}

func b2i(ok bool) int {
	if ok {
		return 1
	}
	return 0
}

func ms(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	return fmt.Sprintf("%d ms", d.Milliseconds())
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return time.Since(t).Round(time.Second).String() + " ago"
}

var pageTmpl = template.Must(template.New("page").Funcs(template.FuncMap{
	"ms":  ms,
	"ago": ago,
}).Parse(pageHTML))

const pageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="5">
<title>proxy-socks5-balancer</title>
<style>
  body { font-family: system-ui, sans-serif; margin: 2rem; background: #0f1419; color: #e6e6e6; }
  h1 { font-size: 1.3rem; }
  .ports { color: #9aa5b1; margin-bottom: 1.2rem; font-size: .9rem; }
  .ports code { background:#1c2330; padding:.1rem .4rem; border-radius:4px; color:#7fd3ff; }
  table { border-collapse: collapse; width: 100%; max-width: 820px; }
  th, td { text-align: left; padding: .55rem .8rem; border-bottom: 1px solid #222b38; }
  th { color: #9aa5b1; font-weight: 600; font-size: .8rem; text-transform: uppercase; }
  .up { color: #3fb950; font-weight: 600; }
  .down { color: #f85149; font-weight: 600; }
  .active td { background: #16261b; }
  .badge { background:#238636; color:#fff; padding:.1rem .5rem; border-radius:10px; font-size:.75rem; }
  .err { color:#f0883e; font-size:.8rem; }
  footer { margin-top:1.2rem; color:#6b7684; font-size:.8rem; }
</style>
</head>
<body>
<h1>proxy-socks5-balancer</h1>
<div class="ports">
  SOCKS5: <code>{{.SOCKS5}}</code>&nbsp;&nbsp;HTTP: <code>{{.HTTP}}</code>
</div>
<table>
  <tr>
    <th>Active</th><th>Name</th><th>Address</th><th>Prio/Wt</th><th>Health</th>
    <th>Latency</th><th>OK/Total</th><th>Checked</th>
  </tr>
  {{range .Rows}}
  <tr class="{{if .Active}}active{{end}}">
    <td>{{if .Active}}<span class="badge">active</span>{{end}}</td>
    <td>{{.Name}}</td>
    <td>{{.Addr}}</td>
    <td>{{.Priority}}/{{.Weight}}</td>
    <td>{{if .Healthy}}<span class="up">up</span>{{else}}<span class="down">down</span>{{end}}</td>
    <td>{{ms .Latency}}</td>
    <td>{{.OK}}/{{.Total}}</td>
    <td>{{ago .CheckedAt}}{{if .LastErr}}<div class="err">{{.LastErr}}</div>{{end}}</td>
  </tr>
  {{end}}
</table>
<footer>auto-refresh 5s &middot; rendered {{.Now}} &middot; <a style="color:#7fd3ff" href="/status.json">status.json</a></footer>
</body>
</html>`
