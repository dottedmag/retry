package retry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"time"
)

// NoJitter is a jitter value that disables jitter
const NoJitter = -1

// Config configures the retry
type Config struct {
	// Delay is a delay between attempts. It is scaled by Scale for each
	// consecutive attempt until it reaches MaxDelay
	//
	// This field is required.
	Delay time.Duration

	// Scale is a exponential scale for delay.
	//
	// Defaults to 1 (no scaling, constant delay), can't be less than 1.
	Scale float64

	// Jitter is the amount of jitter to add to the delay.
	//
	// Defaults to 0.125 (12.5%), and has to be within [0,1].
	// To disable jitter, set this field to NoJitter.
	Jitter float64

	// PreDelay is optional delay before first try.
	//
	// Defaults to 0.
	PreDelay time.Duration

	// MaxDelay is a cap on delay scaling.
	//
	// Defaults to no maximum.
	MaxDelay time.Duration

	// Timeout is a maximum total time to retry.
	//
	// If timeout is reached then the context passed to the called function
	// will be called, and retry won't be attempted when the function returns.
	//
	// Note that if called function should handle context cancellation
	// for aborting the operation by timeout.
	//
	// Defaults to no timeout.
	Timeout time.Duration

	// Logger is a logger for retries
	//
	// This package logs retriable errors returned by an invoked function.
	// It omits logging identical subsequent errors.
	//
	// Defaults to slog.Default. Set to NoLog to disable logging.
	Logger *slog.Logger

	// LogLevel is a log level for retries
	//
	// Defaults to slog.Debug.
	LogLevel slog.Level

	// Override time.After, only for tests
	timeAfter func(d time.Duration) <-chan time.Time
}

// ErrRetry signals the retry attempt
type ErrRetry struct {
	err error
}

func (e ErrRetry) Error() string {
	return e.err.Error()
}

func (e ErrRetry) Unwrap() error {
	return e.err
}

// Retriable wraps the error in ErrRetry if it is not nil
//
// Typical usage is to wrap a potential error known to be retriable.
func Retriable(err error) error {
	if err == nil {
		return nil
	}
	return ErrRetry{err}
}

// ErrRestart signals the restart of retry attempts, resetting both delay and timeout
type ErrRestart struct {
	err error
}

func (e ErrRestart) Error() string {
	return e.err.Error()
}

func (e ErrRestart) Unwrap() error {
	return e.err
}

// Restartable wraps the error in ErrRestart if it is not nil
//
// Typical usage is to wrap a potential error known to require a restart.
func Restartable(err error) error {
	if err == nil {
		return nil
	}
	return ErrRestart{err}
}

// Do runs fn with retries controlled by config
//
// fn triggers a retry by returning ErrRetry or ErrRestart.
// Any other return value ends the retry and is returned
// to the caller.
//
// Context passed to fn is valid only during one attempt,
// and may or may not be canceled afterwards.
func Do(ctx context.Context, cfg Config, fn func(ctx context.Context) error) error {
	// This code modifiers cfg, so it is passed by value

	if cfg.Delay == 0 {
		return fmt.Errorf("no delay is specified")
	}

	if cfg.Scale == 0 {
		cfg.Scale = 1
	}
	if cfg.Scale != 0 && cfg.Scale < 1 {
		return fmt.Errorf("scale can't be less than 1")
	}

	switch cfg.Jitter {
	case NoJitter:
		cfg.Jitter = 0
	case 0:
		cfg.Jitter = 0.125
	}
	if cfg.Jitter < 0 || cfg.Jitter > 1 {
		return fmt.Errorf("jitter has to be within [0,1]")
	}

	if cfg.MaxDelay == 0 {
		cfg.MaxDelay = 1<<63 - 1 // time.go:maxDuration
	}

	if cfg.timeAfter == nil {
		cfg.timeAfter = time.After
	}

	var innerCtx context.Context
	var innerCtxDone func()
	defer func() {
		// This function will be nil if there is no timeout specified in the config
		if innerCtxDone != nil {
			innerCtxDone()
		}
	}()

	if cfg.Timeout == 0 {
		innerCtx = ctx
	} else {
		innerCtx, innerCtxDone = context.WithTimeout(ctx, cfg.Timeout)
	}

	if cfg.PreDelay > 0 {
		select {
		case <-cfg.timeAfter(cfg.PreDelay): // Doesn't leak since Go 1.23, https://github.com/golang/go/issues/8898
		case <-innerCtx.Done():
			return innerCtx.Err()
		}
	}

	delay := cfg.Delay
	for {
		err := fn(innerCtx)

		var errRetry ErrRetry
		doRetry := errors.As(err, &errRetry)
		var errRestart ErrRestart
		doRestart := errors.As(err, &errRestart)

		if err == nil || (!doRetry && !doRestart) {
			return err
		}

		if doRestart {
			delay = cfg.Delay
			if cfg.Timeout != 0 {
				innerCtxDone() // close the previous context
				innerCtx, innerCtxDone = context.WithTimeout(ctx, cfg.Timeout)
				_ = innerCtxDone // ignore false positive from lostcancel vet check
			}
		}

		jitteredDelay := time.Duration(float64(delay) * (1 + 2*rand.Float64()*cfg.Jitter - cfg.Jitter))

		select {
		case <-cfg.timeAfter(jitteredDelay): // Doesn't leak since Go 1.23, https://github.com/golang/go/issues/8898
		case <-innerCtx.Done():
			return innerCtx.Err()
		}

		delay = time.Duration(float64(delay) * cfg.Scale)
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}
	}
}

// Do1 is a version of Do with one return value
func Do1[T any](ctx context.Context, cfg Config, fn func(ctx context.Context) (T, error)) (T, error) {
	var ret T
	err := Do(ctx, cfg, func(ctx context.Context) error {
		var err error
		ret, err = fn(ctx)
		return err
	})
	return ret, err
}
