package worker

import (
	"context"
	"testing"
	"time"
)

func TestPool_BasicFlow(t *testing.T) {
	testPool := NewPool(3, 10, func(ctx context.Context, x int) int {
		return x * 2
	})
	testPool.Start(context.Background())
	numJobs := 5
	for i := 1; i <= numJobs; i++ {
		testPool.Submit(i)
	}

	testPool.Close()

	result := make([]int, 0)

	for res := range testPool.Results() {
		result = append(result, res)
	}

	if len(result) != numJobs {
		t.Errorf("got %d results, want %d", len(result), numJobs)
	}

	var sum int
	for _, num := range result {
		sum += num
	}

	if sum != 30 {
		t.Errorf("got sum %d, want %d", len(result), 30)
	}
}

func TestPool_ContextCancel(t *testing.T) {
	numJobs := 50

	pool := NewPool(2, 10, func(ctx context.Context, x int) int {
		time.Sleep(100 * time.Millisecond)
		return x * 2
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool.Start(ctx)

	go func() {
		for i := 0; i < 1000; i++ {
			select {
			case <-ctx.Done():
				return
			default:
				pool.Submit(i)
			}
		}
	}()
	time.Sleep(1 * time.Second)

	cancel()

	var results []int

	for res := range pool.Results() {
		results = append(results, res)
	}

	if len(results) >= numJobs {
		t.Errorf("expected fewer than %d results, got %d", numJobs, len(results))
	}

	t.Logf("processed %d out of %d", len(results), numJobs)

}
