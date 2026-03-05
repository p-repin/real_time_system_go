package worker

import (
	"context"
	"sync"
)

func Stage[T any, R any](ctx context.Context, in <-chan T, handler func(context.Context, T) R) <-chan R {
	out := make(chan R)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case r, ok := <-in:
				if !ok {
					return
				}
				select {
				case <-ctx.Done():
					return
				case out <- handler(ctx, r):
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}
