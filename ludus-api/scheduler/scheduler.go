package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type Job struct {
	Name     string
	Interval time.Duration
	Fn       func(ctx context.Context) error
}

type Scheduler struct {
	jobs    []Job
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	logger  *slog.Logger
	mu      sync.Mutex
	started bool
}

func New(logger *slog.Logger) *Scheduler {
	return &Scheduler{
		logger: logger,
	}
}

func (s *Scheduler) Register(job Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = append(s.jobs, job)
	if s.started {
		s.logger.Info(fmt.Sprintf("scheduler: late-registering job %q (interval: %s)", job.Name, job.Interval))
		s.wg.Add(1)
		go s.run(s.ctx, job)
	}
}

func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = true

	s.ctx, s.cancel = context.WithCancel(context.Background())

	for _, job := range s.jobs {
		s.wg.Add(1)
		go s.run(s.ctx, job)
	}
}

func (s *Scheduler) run(ctx context.Context, job Job) {
	defer s.wg.Done()
	s.logger.Info(fmt.Sprintf("scheduler: starting job %q (interval: %s)", job.Name, job.Interval))

	defer func() {
		if r := recover(); r != nil {
			s.logger.Error(fmt.Sprintf("scheduler: panic in job %q: %v", job.Name, r))
		}
	}()

	// Run immediately on first tick
	if err := s.runJob(ctx, job); err != nil {
		s.logger.Error(fmt.Sprintf("scheduler: job %q error: %v", job.Name, err))
	}

	ticker := time.NewTicker(job.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info(fmt.Sprintf("scheduler: stopping job %q", job.Name))
			return
		case <-ticker.C:
			if err := s.runJob(ctx, job); err != nil {
				s.logger.Error(fmt.Sprintf("scheduler: job %q error: %v", job.Name, err))
			}
		}
	}
}

func (s *Scheduler) runJob(ctx context.Context, job Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
			s.logger.Error(fmt.Sprintf("scheduler: panic in job %q: %v", job.Name, r))
		}
	}()
	return job.Fn(ctx)
}

func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}
