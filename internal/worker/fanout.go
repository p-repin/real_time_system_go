package worker

import (
	"context"
	"sync"
)

func FanOut[T any, R any](ctx context.Context, tasks []T, handler func(context.Context, T) R) []R {
	resultSlice := make([]R, 0, len(tasks))

	results := make(chan R, len(tasks))

	var wg sync.WaitGroup

	for _, task := range tasks {
		wg.Add(1)
		go func(id T) {
			defer wg.Done()
			res := handler(ctx, id)

			results <- res

		}(task)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		resultSlice = append(resultSlice, r)
	}
	return resultSlice
}
