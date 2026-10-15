package realtime

import "sync/atomic"

// Metrics counts what the realtime path does. It is a placeholder for the
// observability wiring of Task 8: the counters exist from the first publish so
// the tests can assert on them, and a nil *Metrics is safe everywhere.
type Metrics struct {
	publishErrors atomic.Int64
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
