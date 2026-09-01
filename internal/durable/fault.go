package durable

import "sync"

// FaultInjector is a process-local hook used by conformance tests to model a
// crash or I/O failure at a named durability boundary. Production callers do
// not install one; the hook is deliberately synchronous and fail-closed.
type FaultInjector func(point string) error

var faultState struct {
	sync.RWMutex
	fn FaultInjector
}

// SetFaultInjector installs fn and returns a restore function. It is safe to
// use from tests that run serially; callers should restore it with defer.
func SetFaultInjector(fn FaultInjector) func() {
	faultState.Lock()
	previous := faultState.fn
	faultState.fn = fn
	faultState.Unlock()
	return func() {
		faultState.Lock()
		faultState.fn = previous
		faultState.Unlock()
	}
}

// Inject invokes the current test hook, if any.
func Inject(point string) error {
	faultState.RLock()
	fn := faultState.fn
	faultState.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(point)
}
