package worker

import (
	"context"
	"testing"
	"time"
)

func TestFanOut_AllTasksProcessed(t *testing.T) {
	tasks := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	var handler = func(ctx context.Context, x int) int {
		return x * 3
	}

	result := FanOut(context.Background(), tasks, handler)

	if len(result) != 10 {
		t.Errorf("got %d, want %d", len(result), 10)
	}

	var sum int

	for _, r := range result {
		sum += r
	}

	if sum != 165 {
		t.Errorf("got %d, want %d", sum, 165)
	}

}

func TestFanOut_ContextCancel(t *testing.T) {
	tasks := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	var handler = func(ctx context.Context, x int) int {
		select {
		case <-time.After(1 * time.Second):
			return x * 3
		case <-ctx.Done():
			return 0 // ← завершаемся рано
		}

	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	result := FanOut(ctx, tasks, handler)

	if len(result) != len(tasks) {
		t.Errorf("got %d, want < %d", len(result), len(tasks))
	}

	var sum int
	for _, r := range result {
		sum += r
	}

	if sum != 0 {
		t.Errorf("got %d, want %d", sum, 0)
	}

}
