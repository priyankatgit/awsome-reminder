package cloudsync

// NextDelay exposes nextDelay to the external test package.
var NextDelay = nextDelay

// Failures reports the engine's consecutive failed cycles.
func Failures(e *Engine) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.failures
}
