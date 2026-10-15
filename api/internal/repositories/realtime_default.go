package repositories

import "sync/atomic"

// A process that writes a transition must be able to announce it. The server
// does, and so do the sweep, the reconciler, the dunner and the BYOK jobs, each of
// which builds its own repositories (there are dozens of construction sites). So
// the realtime wiring is set once, at the start of the process, and every
// constructor reads it. The zero value is off: a process that never calls
// UseRealtime (REALTIME_ENABLED=false, every test) writes no change and
// publishes nothing.
var currentRT atomic.Pointer[Realtime]

// UseRealtime sets the wiring every repository constructed afterwards uses. Call
// it before building repositories.
func UseRealtime(rt Realtime) { currentRT.Store(&rt) }

func realtimeDefault() Realtime {
	if rt := currentRT.Load(); rt != nil {
		return *rt
	}
	return Realtime{}
}
