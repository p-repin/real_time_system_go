package worker

import (
	"context"
	"sync"
)

type Pool[T any, R any] struct {
	workers int
	jobs    chan T
	results chan R
	handler func(context.Context, T) R
}

func NewPool[T any, R any](workers int, bufferSize int, handler func(context.Context, T) R) *Pool[T, R] {
	return &Pool[T, R]{
		workers: workers,
		jobs:    make(chan T, bufferSize),
		results: make(chan R, bufferSize),
		handler: handler,
	}
}

func (p *Pool[T, R]) Start(ctx context.Context) {
	var wg sync.WaitGroup

	for range p.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case task, ok := <-p.jobs:
					if !ok {
						return
					}
					p.results <- p.handler(ctx, task)
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(p.results)
	}()

}

func (p *Pool[T, R]) Submit(job T) {
	p.jobs <- job
}

func (p *Pool[T, R]) TrySubmit(ctx context.Context, job T) error {
	select {
	case p.jobs <- job:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Pool[T, R]) Results() <-chan R {
	return p.results
}

func (p *Pool[T, R]) Close() {
	close(p.jobs)
}
