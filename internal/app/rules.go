package app

import (
	"runtime"
	"time"

	"awesome-reminder/internal/core"
	"awesome-reminder/internal/scheduler"
	"awesome-reminder/internal/settings"
)

// appRules are the scheduler rules from the current settings, local time.
// The scheduler and phone push both use them: a push's sequence ID is paired
// with the alert card of the same occurrence, so the two must agree.
func appRules(c *core.Core) scheduler.Rules {
	s, err := c.Settings()
	if err != nil {
		s = settings.Defaults(runtime.GOOS)
		s.LeadTimes = nil // as before: no default leads while settings can't be read
	}
	return core.SchedulerRules(s, time.Local)
}
