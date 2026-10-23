package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"proxy-socks5-balancer/internal/checker"
	"proxy-socks5-balancer/internal/config"
	"proxy-socks5-balancer/internal/frontend"
	"proxy-socks5-balancer/internal/hook"
	"proxy-socks5-balancer/internal/metrics"
	"proxy-socks5-balancer/internal/selector"
	"proxy-socks5-balancer/internal/status"
	"proxy-socks5-balancer/internal/upstream"
	"proxy-socks5-balancer/internal/version"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Version)
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		// Logging is not configured yet; use the default logger.
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}
	setupLogging(cfg.Log)

	sel := selector.New(cfg.Check.SwitchMargin)
	sel.SetUpstreams(buildUpstreams(cfg))

	mx := metrics.New()
	runner := hook.New(hookConfig(cfg))
	chk := checker.New(sel, checkSettings(cfg))
	onSwitch := func(from, to *upstream.Upstream) {
		mx.Switch()
		runner.Fire(switchEvent(from, to))
	}
	chk.OnSwitch = onSwitch

	// Dial function used by both frontends: route through the active upstream.
	// A dial failure pokes the checker to re-probe out of band, so a dead
	// upstream is detected without waiting for the next interval.
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		cur := sel.Current()
		if cur == nil {
			return nil, fmt.Errorf("no healthy upstream available")
		}
		conn, err := cur.DialContext(ctx, network, addr)
		if err != nil {
			chk.Trigger()
			return nil, err
		}
		return conn, nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go chk.Run(ctx)

	socks := &frontend.SOCKS5{Addr: cfg.Listen.SOCKS5, Dial: dial, Metrics: mx}
	httpProxy := &frontend.HTTPProxy{Addr: cfg.Listen.HTTP, Dial: dial, Metrics: mx}
	statusSrv := &status.Server{Addr: cfg.Listen.Status, Sel: sel, Metrics: mx}
	statusSrv.Listen.SOCKS5 = cfg.Listen.SOCKS5
	statusSrv.Listen.HTTP = cfg.Listen.HTTP
	statusSrv.OnSwitch = onSwitch

	go serve("socks5", func() error { return socks.ListenAndServe(ctx) }, stop)
	go serve("http", func() error { return httpProxy.ListenAndServe(ctx) }, stop)
	go serve("status", func() error { return statusSrv.ListenAndServe(ctx) }, stop)

	go handleReload(ctx, *cfgPath, sel, chk, runner)

	slog.Info("balancer started", "version", version.Version, "upstreams", len(cfg.Upstreams),
		"socks5", cfg.Listen.SOCKS5, "http", cfg.Listen.HTTP, "status", cfg.Listen.Status)
	<-ctx.Done()
	slog.Info("shutting down")
}

// setupLogging installs the global slog logger based on config. Output goes to
// stdout without timestamps (journald adds its own) in text or JSON format.
func setupLogging(c config.Log) {
	opts := &slog.HandlerOptions{Level: parseLevel(c.Level)}
	var h slog.Handler
	if c.Format == "json" {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(h))
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func hookConfig(cfg *config.Config) hook.Config {
	return hook.Config{
		Command: cfg.OnSwitch.Command,
		Timeout: cfg.OnSwitch.Timeout.Std(),
	}
}

func switchEvent(from, to *upstream.Upstream) hook.Event {
	ev := hook.Event{}
	if from != nil {
		ev.OldName, ev.OldAddr = from.Name, from.Addr
	}
	if to != nil {
		ev.NewName, ev.NewAddr = to.Name, to.Addr
		ev.NewLatencyMS = to.Latency().Milliseconds()
	}
	return ev
}

func buildUpstreams(cfg *config.Config) []*upstream.Upstream {
	ups := make([]*upstream.Upstream, 0, len(cfg.Upstreams))
	for _, u := range cfg.Upstreams {
		ups = append(ups, upstream.New(u.Name, u.SOCKS5, upstream.Params{
			Priority:      u.Priority,
			Weight:        u.Weight,
			UpThreshold:   cfg.Check.UpThreshold,
			DownThreshold: cfg.Check.DownThreshold,
			LatencyEWMA:   cfg.Check.LatencyEWMA,
		}))
	}
	return ups
}

func checkSettings(cfg *config.Config) checker.Settings {
	return checker.Settings{
		Interval: cfg.Check.Interval.Std(),
		Timeout:  cfg.Check.Timeout.Std(),
		Required: cfg.Check.Required,
		URLs:     cfg.Check.URLs,
	}
}

// serve runs a listener and aborts the whole process on fatal startup errors.
func serve(name string, run func() error, stop context.CancelFunc) {
	if err := run(); err != nil {
		slog.Error("frontend error", "frontend", name, "err", err)
		stop()
	}
}

// handleReload reloads the config on SIGHUP and swaps upstreams/settings live.
func handleReload(ctx context.Context, path string, sel *selector.Selector, chk *checker.Checker, runner *hook.Runner) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			cfg, err := config.Load(path)
			if err != nil {
				slog.Error("reload failed", "err", err)
				continue
			}
			sel.SetMargin(cfg.Check.SwitchMargin)
			sel.SetUpstreams(buildUpstreams(cfg))
			chk.SetSettings(checkSettings(cfg))
			runner.Set(hookConfig(cfg))
			slog.Info("config reloaded", "upstreams", len(cfg.Upstreams))
		}
	}
}
