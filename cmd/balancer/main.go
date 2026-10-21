package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"proxy-socks5-balancer/internal/checker"
	"proxy-socks5-balancer/internal/config"
	"proxy-socks5-balancer/internal/frontend"
	"proxy-socks5-balancer/internal/selector"
	"proxy-socks5-balancer/internal/status"
	"proxy-socks5-balancer/internal/upstream"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	sel := selector.New(cfg.Check.SwitchMargin)
	sel.SetUpstreams(buildUpstreams(cfg))

	chk := checker.New(sel, checkSettings(cfg))

	// Dial function used by both frontends: route through the active upstream.
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		cur := sel.Current()
		if cur == nil {
			return nil, fmt.Errorf("no healthy upstream available")
		}
		return cur.DialContext(ctx, network, addr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go chk.Run(ctx)

	socks := &frontend.SOCKS5{Addr: cfg.Listen.SOCKS5, Dial: dial}
	httpProxy := &frontend.HTTPProxy{Addr: cfg.Listen.HTTP, Dial: dial}
	statusSrv := &status.Server{Addr: cfg.Listen.Status, Sel: sel}
	statusSrv.Listen.SOCKS5 = cfg.Listen.SOCKS5
	statusSrv.Listen.HTTP = cfg.Listen.HTTP

	go serve("socks5", func() error { return socks.ListenAndServe(ctx) }, stop)
	go serve("http", func() error { return httpProxy.ListenAndServe(ctx) }, stop)
	go serve("status", func() error { return statusSrv.ListenAndServe(ctx) }, stop)

	go handleReload(ctx, *cfgPath, sel, chk)

	log.Printf("balancer started with %d upstreams", len(cfg.Upstreams))
	<-ctx.Done()
	log.Printf("shutting down")
}

func buildUpstreams(cfg *config.Config) []*upstream.Upstream {
	ups := make([]*upstream.Upstream, 0, len(cfg.Upstreams))
	for _, u := range cfg.Upstreams {
		ups = append(ups, upstream.New(u.Name, u.SOCKS5))
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
		log.Printf("%s frontend error: %v", name, err)
		stop()
	}
}

// handleReload reloads the config on SIGHUP and swaps upstreams/settings live.
func handleReload(ctx context.Context, path string, sel *selector.Selector, chk *checker.Checker) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			cfg, err := config.Load(path)
			if err != nil {
				log.Printf("reload failed: %v", err)
				continue
			}
			sel.SetMargin(cfg.Check.SwitchMargin)
			sel.SetUpstreams(buildUpstreams(cfg))
			chk.SetSettings(checkSettings(cfg))
			log.Printf("reloaded config: %d upstreams", len(cfg.Upstreams))
		}
	}
}
