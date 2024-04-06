package retry

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

// TODO (dottedmag): Some of these tests make assumptions about implementation.
// It would be better to have internal "delayer" interface stubbed by tests.

func TestInvalidConfig(t *testing.T) {
	s := time.Second
	for _, config := range []Config{
		{},
		{Delay: s, Scale: 0.9},
		{Delay: s, Scale: -0.1},
		{Delay: s, Jitter: -0.1},
		{Delay: s, Jitter: 1.1},
	} {
		t.Run(fmt.Sprint(config), func(t *testing.T) {
			var fnCalled bool
			err := Do(context.Background(), config, func(ctx context.Context) error {
				fnCalled = true
				return nil
			})
			if fnCalled {
				t.Fatalf("fn called on invalid config")
			}
			if err == nil {
				t.Fatalf("nil returned on invalid config")
			}
		})
	}
}

func timeAfterCancelOn100Hours(cancel func()) func(time.Duration) <-chan time.Time {
	return func(d time.Duration) <-chan time.Time {
		ch := time.After(d)
		if d == 100*time.Hour {
			// Cancel the context after returning from this function.
			// 1ms is plenty of time.
			go func() {
				time.Sleep(1 * time.Millisecond)
				cancel()
			}()
		}
		return ch
	}
}

func TestCancel(t *testing.T) {
	t.Run("while in fn", func(t *testing.T) {
		ctx, done := context.WithCancel(context.Background())
		defer done()

		err := Do(ctx, Config{Delay: time.Nanosecond}, func(ctx context.Context) error {
			done()
			return ctx.Err()
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do was supposed to return 'canceled' error, returned %v", err)
		}
	})
	t.Run("while in fn that returns ErrRetry", func(t *testing.T) {
		ctx, done := context.WithCancel(context.Background())
		defer done()

		err := Do(ctx, Config{Delay: time.Nanosecond}, func(ctx context.Context) error {
			done()
			return ErrRetry{errors.New("do it again")}
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do was supposed to return 'canceled' error, returned %v", err)
		}
	})
	t.Run("while in fn that returns no error", func(t *testing.T) {
		ctx, done := context.WithCancel(context.Background())
		defer done()

		err := Do(ctx, Config{Delay: time.Nanosecond}, func(ctx context.Context) error {
			done()
			return nil
		})
		if err != nil {
			t.Fatalf("Do was supposed to return successfully, returned %v", err)
		}
	})
	t.Run("while in pre delay", func(t *testing.T) {
		ctx, done := context.WithCancel(context.Background())
		defer done()

		timeAfter := timeAfterCancelOn100Hours(done)

		var fnCalled bool
		err := Do(ctx, Config{PreDelay: 100 * time.Hour, Delay: time.Nanosecond, timeAfter: timeAfter}, func(ctx context.Context) error {
			fnCalled = true
			return nil
		})
		if fnCalled {
			t.Fatalf("fn called when context is cancelled is pre-delay")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do was supposed to return 'canceled' error, returned %v", err)
		}
	})
	t.Run("while in delay", func(t *testing.T) {
		ctx, done := context.WithCancel(context.Background())
		defer done()

		timeAfter := timeAfterCancelOn100Hours(done)

		var fnCalled int
		// Make sure no jitter is added, or timeAfter stub won't be triggered
		err := Do(ctx, Config{Delay: 100 * time.Hour, Jitter: NoJitter, timeAfter: timeAfter}, func(ctx context.Context) error {
			fnCalled++
			return ErrRetry{errors.New("do it again")}
		})

		if fnCalled != 1 {
			t.Fatalf("fn was supposed to be called once, called %d times", fnCalled)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do was supposed to return 'canceled' error, returned %v", err)
		}
	})
}

func TestJitter(t *testing.T) {
	var jitters []time.Duration

	timeAfter := func(t time.Duration) <-chan time.Time {
		jitters = append(jitters, t)
		return time.After(0)
	}

	var fnCalled int
	err := Do(context.Background(), Config{Delay: time.Second, Jitter: 0.5, timeAfter: timeAfter}, func(ctx context.Context) error {
		if fnCalled == 1000 {
			return nil
		}
		fnCalled++
		return ErrRetry{errors.New("do it again")}
	})
	if err != nil {
		t.Fatalf("Do was supposed to return successfully")
	}

	var under06, over14 int
	for _, jitter := range jitters {
		if jitter < 500*time.Millisecond || jitter > 1500*time.Millisecond {
			t.Errorf("Delays were supposed to be 1s+-0.5s, got %v", jitter)
		}
		if jitter < 600*time.Millisecond {
			under06++
		}
		if jitter > 1400*time.Millisecond {
			over14++
		}
	}
	if under06 == 0 {
		t.Errorf("Delays were supposed to be 1s+-0.5s, got no [0.5..0.6]s delays")
	}
	if over14 == 0 {
		t.Errorf("Delays were supposed to be 1s+-0.5s, got no [1.4..1.5]s delays")
	}
}

func TestDelays(t *testing.T) {
	var delays []time.Duration
	timeAfter := func(t time.Duration) <-chan time.Time {
		delays = append(delays, t)
		return time.After(0)
	}

	cfg := Config{
		PreDelay:  100 * time.Millisecond,
		Delay:     2 * time.Second,
		Scale:     2,
		MaxDelay:  10 * time.Second,
		Jitter:    NoJitter,
		timeAfter: timeAfter,
	}

	var fnCalled int
	err := Do(context.Background(), cfg, func(ctx context.Context) error {
		fnCalled++
		if fnCalled == 6 {
			return ErrRestart{errors.New("restart")}
		}
		if fnCalled == 8 {
			return nil
		}
		return ErrRetry{errors.New("do it again")}
	})
	if err != nil {
		t.Fatalf("Do was supposed to return successfully")
	}

	expectedDelays := []time.Duration{
		100 * time.Millisecond, // pre-delay
		2 * time.Second,        // first delay
		4 * time.Second,        // scaled delay
		8 * time.Second,        // scaled delay
		10 * time.Second,       // bumped into maximum delay
		10 * time.Second,       // no longer increased
		2 * time.Second,        // delay has been reset by ErrRestart
		4 * time.Second,        // scaled again
	}

	if slices.Compare(delays, expectedDelays) != 0 {
		t.Errorf("Delays were supposed to be %v, got %v", expectedDelays, delays)
	}
}

func TestResetTimeout(t *testing.T) {
	// TODO: this test will fail if fn() actually takes more than one millisecond.
	// Do we care?
	cfg := Config{Timeout: time.Millisecond, Delay: 50 * time.Microsecond}
	var fnCalled int
	err := Do(context.Background(), cfg, func(ctx context.Context) error {
		fnCalled++
		if fnCalled == 100 {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrRestart{errors.New("restart")}
	})
	if err != nil {
		t.Fatalf("Do was supposed to return successfully, returned %v", err)
	}
}

func TestTimeoutPreDelay(t *testing.T) {
	cfg := Config{Delay: 100 * time.Hour, Timeout: time.Microsecond, PreDelay: 100 * time.Hour}
	var fnCalled bool
	err := Do(context.Background(), cfg, func(ctx context.Context) error {
		fnCalled = true
		return nil
	})
	if fnCalled {
		t.Fatalf("Do was supposed to return before calling the function")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Do was supposed to return 'dealine exceeded', returned %v", err)
	}
}

func TestTimeoutInFn(t *testing.T) {
	cfg := Config{Delay: 100 * time.Hour, Timeout: time.Microsecond}
	var fnCalled int
	err := Do(context.Background(), cfg, func(ctx context.Context) error {
		fnCalled++
		<-ctx.Done()
		return ctx.Err()
	})
	if fnCalled != 1 {
		t.Fatalf("Do was supposed to call fn once")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Do was supposed to return 'dealine exceeded', returned %v", err)
	}
}

func TestTimeoutDelay(t *testing.T) {
	cfg := Config{Delay: 100 * time.Hour, Timeout: time.Microsecond}
	var fnCalled int
	err := Do(context.Background(), cfg, func(ctx context.Context) error {
		fnCalled++
		return ErrRetry{errors.New("do it again")}
	})
	if fnCalled != 1 {
		t.Fatalf("Do was supposed to call function once and then return")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Do was supposed to return 'dealine exceeded', returned %v", err)
	}
}
