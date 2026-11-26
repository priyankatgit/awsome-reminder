package scheduler

import (
	"context"
	"log"
	"time"

	"awesome-reminder/internal/model"
)

// Source is the storage the scheduler reads from and records notifications in.
type Source interface {
	ListOpen() ([]model.Item, error)
	WasNotified(string) (bool, error)
	MarkNotified(string, time.Time) error
}

const (
	checkInterval = 30 * time.Second
	jumpTolerance = 5 * time.Second
)

// Scheduler fires alerts using a single timer.
type Scheduler struct {
	src   Source
	leads func() []time.Duration
	fire  func([]Alert)
	now   func() time.Time
	kick  chan struct{}

	retryDelay time.Duration // re-plan delay after a Source error
}

// New creates a Scheduler.
func New(src Source, leads func() []time.Duration, fire func([]Alert), now func() time.Time) *Scheduler {
	return &Scheduler{src: src, leads: leads, fire: fire, now: now, kick: make(chan struct{}, 1), retryDelay: checkInterval}
}

// Kick requests a re-plan. It never blocks; concurrent kicks coalesce.
func (s *Scheduler) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Start launches the scheduling goroutine; it exits when ctx is done.
func (s *Scheduler) Start(ctx context.Context) {
	go s.run(ctx)
}

func (s *Scheduler) run(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	var target time.Time            // timer's target in s.now() terms; zero = none
	lastWall := time.Now().Round(0) // strip monotonic reading

	replan := func() {
		next := s.plan()
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		target = next
		if !next.IsZero() {
			d := next.Sub(s.now())
			if d < 0 {
				d = 0
			}
			timer.Reset(d)
		}
	}

	replan()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
			replan()
		case <-timer.C:
			target = time.Time{}
			replan()
		case <-ticker.C:
			wall := time.Now().Round(0)
			jumped := wallJumped(lastWall, wall, checkInterval)
			lastWall = wall
			if jumped || (!target.IsZero() && !target.After(s.now())) {
				replan()
			}
		}
	}
}

// wallJumped reports whether the wall clock moved by something other than the expected
// interval (forward or backward) beyond tolerance, e.g. after sleep/wake or a clock change.
// Both times must have monotonic readings stripped.
func wallJumped(last, wall time.Time, expected time.Duration) bool {
	d := wall.Sub(last) - expected
	return d > jumpTolerance || d < -jumpTolerance
}

// plan computes alerts, fires due ones, marks them notified, and returns the next fire time.
func (s *Scheduler) plan() time.Time {
	items, err := s.src.ListOpen()
	if err != nil {
		log.Printf("scheduler: list open: %v", err)
		return s.now().Add(s.retryDelay)
	}
	now := s.now()
	notified := func(key string) bool {
		ok, err := s.src.WasNotified(key)
		if err != nil {
			log.Printf("scheduler: was notified %q: %v", key, err)
			return true // avoid re-firing on a failing store
		}
		return ok
	}
	fireNow, covered, next := Plan(items, s.leads(), now, notified)
	if len(fireNow) > 0 {
		s.fire(fireNow)
	}
	for _, k := range covered {
		if err := s.src.MarkNotified(k, now); err != nil {
			log.Printf("scheduler: mark notified %q: %v", k, err)
		}
	}
	return next
}
