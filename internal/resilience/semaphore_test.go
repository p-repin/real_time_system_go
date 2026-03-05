package resilience

import (
	"context"
	"testing"
	"time"
)

func TestSemaphore_AcquireRelease(t *testing.T) {
	sem := NewSemaphore(2)

	for range 2 {
		if err := sem.Acquire(context.Background()); err != nil {
			t.Errorf("context down")
		}
	}

	res := sem.TryAcquire()

	if res == true {
		t.Errorf("got %t, want %t", res, false)
	}

	sem.Release()

	res = sem.TryAcquire()

	if res == false {
		t.Errorf("got %t, want %t", res, true)
	}
}

func TestSemaphore_ContextWithTimeout(t *testing.T) {
	sem := NewSemaphore(2)

	for range 2 {
		if err := sem.Acquire(context.Background()); err != nil {
			t.Errorf("context exspression")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	res := sem.Acquire(ctx)

	if err := res; err == nil {
		t.Errorf("got: %v, want %v", res, ctx.Err())
	}
}
