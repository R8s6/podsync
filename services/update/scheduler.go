package update

import (
	"context"
	"fmt"
	"sync"

	"github.com/robfig/cron/v3"
	log "github.com/sirupsen/logrus"

	"github.com/mxpv/podsync/pkg/feed"
)

// Scheduler coalesces queued updates by feed. One worker performs downloads, so
// adding a show cannot start an unbounded number of concurrent downloads.
type Scheduler struct {
	cron    *cron.Cron
	mu      sync.Mutex
	pending []*feed.Config
	queued  map[string]bool
	wake    chan struct{}
}

func NewScheduler() *Scheduler {
	return &Scheduler{cron: cron.New(), queued: make(map[string]bool), wake: make(chan struct{}, 1)}
}

func feedSchedule(f *feed.Config) (cron.Schedule, error) {
	spec := f.CronSchedule
	if spec == "" {
		spec = fmt.Sprintf("@every %s", f.UpdatePeriod)
	}
	return cron.ParseStandard(spec)
}

func (s *Scheduler) Add(f *feed.Config) error {
	schedule, err := feedSchedule(f)
	if err != nil {
		return err
	}
	s.register(f, schedule, f.CronSchedule == "")
	return nil
}

func (s *Scheduler) register(f *feed.Config, schedule cron.Schedule, immediate bool) {
	s.cron.Schedule(schedule, cron.FuncJob(func() { s.enqueue(f) }))
	if immediate {
		s.enqueue(f)
	}
}

func (s *Scheduler) enqueue(f *feed.Config) {
	s.mu.Lock()
	if !s.queued[f.ID] {
		s.queued[f.ID] = true
		s.pending = append(s.pending, f)
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Scheduler) Run(ctx context.Context, update func(context.Context, *feed.Config) error) error {
	s.cron.Start()
	defer s.cron.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.wake:
		}
		for ctx.Err() == nil {
			s.mu.Lock()
			if len(s.pending) == 0 {
				s.mu.Unlock()
				break
			}
			f := s.pending[0]
			s.pending = s.pending[1:]
			s.mu.Unlock()
			if err := update(ctx, f); err != nil {
				log.WithError(err).WithField("feed_id", f.ID).Error("failed to update feed")
			}
			s.mu.Lock()
			delete(s.queued, f.ID)
			s.mu.Unlock()
		}
	}
}
