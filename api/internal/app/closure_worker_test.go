package app

import "testing"

// With the closures feature off the service is nil and the worker must finish at
// once: returning nil tells the supervisor the work is done, so it is not
// restarted in a loop.
func TestClosureWorkerIsANoOpWhenTheFeatureIsOff(t *testing.T) {
	if err := RunClosureWorker(nil)(t.Context()); err != nil {
		t.Fatalf("worker = %v, want nil", err)
	}
}
