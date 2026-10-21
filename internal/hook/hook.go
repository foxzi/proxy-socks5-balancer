// Package hook runs a user command when the active upstream changes.
// The command is optional and runs as the service user (unprivileged),
// so it must not require root. Details are passed via environment variables.
package hook

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"time"
)

// Config is the hook command and its timeout (swappable on reload).
type Config struct {
	Command string
	Timeout time.Duration
}

// Event describes an active-upstream change.
type Event struct {
	OldName, OldAddr string
	NewName, NewAddr string
	NewLatencyMS     int64
}

// Runner executes the configured command for each switch event.
type Runner struct {
	cfg atomic.Pointer[Config]
}

func New(c Config) *Runner {
	r := &Runner{}
	r.cfg.Store(&c)
	return r
}

// Set swaps the hook config (used on config reload).
func (r *Runner) Set(c Config) { r.cfg.Store(&c) }

// Fire runs the command in the background with the event exposed as env vars.
// It is a no-op when no command is configured.
func (r *Runner) Fire(ev Event) {
	c := r.cfg.Load()
	if c.Command == "" {
		return
	}
	go r.run(c, ev)
}

func (r *Runner) run(c *Config, ev Event) {
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", c.Command)
	cmd.Env = append([]string{
		"BALANCER_OLD=" + orNone(ev.OldName),
		"BALANCER_OLD_ADDR=" + ev.OldAddr,
		"BALANCER_NEW=" + orNone(ev.NewName),
		"BALANCER_NEW_ADDR=" + ev.NewAddr,
		"BALANCER_NEW_LATENCY_MS=" + strconv.FormatInt(ev.NewLatencyMS, 10),
	}, os.Environ()...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("on_switch hook failed",
			"error", err, "output", trim(out))
		return
	}
	slog.Debug("on_switch hook ran", "output", trim(out))
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func trim(b []byte) string {
	const max = 500
	if len(b) > max {
		b = b[:max]
	}
	return string(b)
}
