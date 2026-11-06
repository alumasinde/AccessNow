package events

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type counting struct {
	calls atomic.Int32
	fail  bool
}

func (c *counting) Drain(context.Context) error {
	c.calls.Add(1)
	if c.fail {
		return errors.New("tenant 7: gate is closed")
	}
	return nil
}

func waitFor(t *testing.T, want int32, c *counting) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for c.calls.Load() < want {
		select {
		case <-deadline:
			t.Fatalf("drained %d times, want %d", c.calls.Load(), want)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestBacklogDrainsOnATick(t *testing.T) {
	c := &counting{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() { NewBacklog(c, 10*time.Millisecond).Run(ctx); close(done) }()

	waitFor(t, 2, c)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run() kept going after the context ended")
	}
}

func TestBacklogKeepsGoingAfterAFailedPass(t *testing.T) {
	// The rows are the record, so a pass that fails is retried rather than fatal: the loop
	// would otherwise stop delivering every tenant behind the one that broke.
	c := &counting{fail: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go NewBacklog(c, 10*time.Millisecond).Run(ctx)
	waitFor(t, 3, c)
}
