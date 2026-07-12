package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrTimeout = errors.New("messaging: request timed out")

func CallWithTimeout(ctx context.Context, timeout time.Duration, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := fn(ctx); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("%w: %v", ErrTimeout, err)
		}
		return err
	}
	return nil
}

func CallWithRetry(ctx context.Context, fn func(context.Context) error) error {
	const maxAttempts = 3
	for i := 0; i < maxAttempts; i++ {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrTimeout) {
			return err
		}
	}
	return fmt.Errorf("%w: all %d attempts timed out", ErrTimeout, maxAttempts)
}

func CallWithTimeoutAndRetry(ctx context.Context, timeout time.Duration, fn func(context.Context) error) error {
	return CallWithRetry(ctx, func(ctx context.Context) error {
		return CallWithTimeout(ctx, timeout, fn)
	})
}
