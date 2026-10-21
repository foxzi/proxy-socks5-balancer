// Package metrics holds process-wide counters exposed on the status port in
// Prometheus text format. It has no external dependencies: the exposition
// format is small and written by hand.
package metrics

import "sync/atomic"

// Metrics are the live runtime counters shared by the frontends and checker.
// The zero value is ready to use and all methods are safe for concurrent use.
type Metrics struct {
	Switches    atomic.Int64 // active upstream changes
	SocksConns  atomic.Int64 // total accepted SOCKS5 connections
	HTTPConns   atomic.Int64 // total accepted HTTP proxy connections
	SocksActive atomic.Int64 // currently open SOCKS5 connections
	HTTPActive  atomic.Int64 // currently open HTTP proxy connections
	DialErrors  atomic.Int64 // failed dials to the active upstream
}

// New returns an empty metric set.
func New() *Metrics { return &Metrics{} }

func (m *Metrics) nilSafe() bool { return m != nil }

// SocksOpen records a new SOCKS5 connection and returns a close func to call
// when it ends. A nil receiver is a no-op so wiring stays optional.
func (m *Metrics) SocksOpen() func() {
	if !m.nilSafe() {
		return func() {}
	}
	m.SocksConns.Add(1)
	m.SocksActive.Add(1)
	return func() { m.SocksActive.Add(-1) }
}

// HTTPOpen mirrors SocksOpen for the HTTP proxy frontend.
func (m *Metrics) HTTPOpen() func() {
	if !m.nilSafe() {
		return func() {}
	}
	m.HTTPConns.Add(1)
	m.HTTPActive.Add(1)
	return func() { m.HTTPActive.Add(-1) }
}

// DialError records a failed dial to the active upstream.
func (m *Metrics) DialError() {
	if !m.nilSafe() {
		return
	}
	m.DialErrors.Add(1)
}

// Switch records an active-upstream change.
func (m *Metrics) Switch() {
	if !m.nilSafe() {
		return
	}
	m.Switches.Add(1)
}
