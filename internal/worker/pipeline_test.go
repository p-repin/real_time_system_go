package worker

import (
	"context"
	"testing"
	"time"
)

func TestStageChain(t *testing.T) {
	in := make(chan int)

	double := Stage(context.Background(), in, func(ctx context.Context, x int) int {
		return x * 2
	})

	added := Stage(context.Background(), double, func(ctx context.Context, x int) int {
		return x + 10
	})

	go func() {
		for i := 1; i < 6; i++ {
			in <- i
		}
		close(in)
	}()

	expected := []int{12, 14, 16, 18, 20}
	var results []int

	for result := range added {
		results = append(results, result)
	}

	if len(results) != len(expected) {
		t.Errorf("got %d, want %d", len(results), len(expected))
	}

	for i := range results {
		if results[i] != expected[i] {
			t.Errorf("got %d, want %d", results[i], expected[i])
		}
	}

}

func TestStage_ContextCancel(t *testing.T) {

	in := make(chan int)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	go func() {
		defer close(in)
		for i := 1; i <= 100; i++ {
			select {
			case in <- i:
			case <-ctx.Done():
				return
			}
		}
	}()

	double := Stage(ctx, in, func(ctx context.Context, x int) int {
		select {
		case <-time.After(1 * time.Second):
			return x * 2
		case <-ctx.Done():
			return 0
		}
	})

	added := Stage(ctx, double, func(ctx context.Context, x int) int {
		select {
		case <-time.After(1 * time.Second):
			return x + 10
		case <-ctx.Done():
			return 0
		}
	})
	var results []int
	for result := range added {
		results = append(results, result)
	}

	var sum int

	for _, r := range results {
		sum += r
	}

	if sum != 0 {
		t.Errorf("got %d, want %d", sum, 0)
	}

}
