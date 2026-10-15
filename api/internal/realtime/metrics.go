package realtime

import "sync/atomic"

// Metrics counts what the realtime path does. It is a placeholder for the
// observability wiring of Task 8: the counters exist from the first publish so
// the tests can assert on them, and a nil *Metrics is safe everywhere.
type Metrics struct {
	publishErrors atomic.Int64
	slowCloses    atomic.Int64
	active        atomic.Int64
	authFailures  atomic.Int64
}

func (m *Metrics) authFailure(string) {
	if m != nil {
		m.authFailures.Add(1)
	}
}

// AuthFailures is how many connections were refused at authorization.
func (m *Metrics) AuthFailures() int64 {
	if m == nil {
		return 0
	}
	return m.authFailures.Load()
}

func (m *Metrics) slowClose() {
	if m != nil {
		m.slowCloses.Add(1)
	}
}

func (m *Metrics) connected(string) {
	if m != nil {
		m.active.Add(1)
	}
}

func (m *Metrics) disconnected(string) {
	if m != nil {
		m.active.Add(-1)
	}
}

// SlowCloses is how many connections were closed for not keeping up.
func (m *Metrics) SlowCloses() int64 {
	if m == nil {
		return 0
	}
	return m.slowCloses.Load()
}

// Active is the connections currently registered.
func (m *Metrics) Active() int64 {
	if m == nil {
		return 0
	}
	return m.active.Load()
}

func (m *Metrics) publishError() {
	if m != nil {
		m.publishErrors.Add(1)
	}
}

// PublishErrors is how many changes could not be encoded for publishing.
func (m *Metrics) PublishErrors() int64 {
	if m == nil {
		return 0
	}
	return m.publishErrors.Load()
}
