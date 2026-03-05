package resilience

import "context"

type Semaphore struct {
	sem chan struct{}
}

func NewSemaphore(maxConcurrent int) *Semaphore {
	return &Semaphore{sem: make(chan struct{}, maxConcurrent)}
}

func (s *Semaphore) Acquire(ctx context.Context) error {
	select {
	case s.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Semaphore) Release() {
	<-s.sem
}

func (s *Semaphore) TryAcquire() bool {
	select {
	case s.sem <- struct{}{}:
		return true
	default:
		return false
	}
}
