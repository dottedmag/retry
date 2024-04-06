# Go library to handle retries
[![Go Reference](https://pkg.go.dev/badge/github.com/dottedmag/retry.svg)](https://pkg.go.dev/github.com/dottedmag/retry)

Retry the function, with 1 second between calls with jitter, until it succeeds:

    cfg := retry.Config{Delay: time.Second}

    err = retry.Do(ctx, cfg, func(ctx context.Context) error {
        ...
        return Retriable(err)
    })

    val, err = retry.Do1(ctx, cfg, func(ctx context.Context) (Foo, error) {
        ...
        return Foo{}, Retriable(err)
    })

Retry is triggered by inner function returning `retry.ErrRetry` or `retry.ErrRestart`.

Other errors and `nil` stop the retries.

## Exponential backoff

    retry.Config{Delay: 1*time.Second, Scale: 1.5}

## Capped exponential backoff

    retry.Config{Delay: 1*time.Second, Scale: 1.5, MaxDelay: 10*time.Second}

## Resetting backoff

If a function returns `retry.ErrRestart` then the delay is reset to `Config.Delay`.

## Additional delay before first call

    retry.Config{PreDelay: 200*time.Millisecond, Delay: 1*time.Second}

## Jitter

    # Default, ±12.5%
    retry.Config{Delay: 1*time.Second}
    # ±50%
    retry.Config{Delay: 1*time.Second, Jitter: 0.5}
    # Disabled
    retry.Config{Delay: 1*time.Second, Jitter: retry.NoJitter}

## Timeout

Cancel the inner context, wait for the called function to return and do not retry if a timeout is reached:

    retry.Config{Delay: 1*time.Second, Timeout: 30*time.Second}

## Resetting timeout

If a function returns `retry.ErrRestart` then the timeout is reset to `Config.Timeout`.

## Legal

Copyright Mikhail Gusarov <dottedmag@dottedmag.net>.

Licensed under [Apache 2.0](LICENSE) license.
