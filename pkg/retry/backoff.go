package retry

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"time"
)

// Config defines the configuration for backoff-retry mechanism
type Config struct {
	// MaxRetries is the maximum number of retries
	MaxRetries int
	// InitialInterval is the initial retry interval in milliseconds
	InitialInterval time.Duration
	// MaxInterval is the maximum retry interval in milliseconds
	MaxInterval time.Duration
	// Multiplier is the factor by which the retry interval increases
	Multiplier float64
	// RandomizationFactor is the randomization factor (0.0-1.0)
	RandomizationFactor float64
}

// DefaultConfig returns the default retry configuration
func DefaultConfig() Config {
	return Config{
		MaxRetries:          5,
		InitialInterval:     100 * time.Millisecond,
		MaxInterval:         10 * time.Second,
		Multiplier:          1.5,
		RandomizationFactor: 0.5,
	}
}

// nextBackoff calculates the next backoff interval
func (c *Config) nextBackoff(retry int) time.Duration {
	if retry >= c.MaxRetries {
		return 0
	}

	// Calculate next interval with exponential backoff
	backoff := float64(c.InitialInterval) * math.Pow(c.Multiplier, float64(retry))
	if backoff > float64(c.MaxInterval) {
		backoff = float64(c.MaxInterval)
	}

	// Apply randomization
	delta := c.RandomizationFactor * backoff
	minn := backoff - delta
	maxx := backoff + delta
	backoff = minn + (rand.Float64() * (maxx - minn)) //nolint:gosec

	return time.Duration(backoff)
}

// RetryableFunc represents a function that can be retried
type RetryableFunc func() error

// IsRetryable is a function that determines if an error should be retried
type IsRetryable func(error) bool

// DoWithCallbacks executes a retryable function with callbacks for metrics/logging
type Callbacks struct {
	OnRetryAttempt func(attempt int, err error, nextBackoff time.Duration)
	OnRetrySuccess func(attempt int)
	OnRetryFailure func(attempt int, err error)
}

// Do executes the given function with retries based on the provided config
func Do(ctx context.Context, fn RetryableFunc, isRetryable IsRetryable, cfg Config) error {
	return DoWithCallbacks(ctx, fn, isRetryable, cfg, Callbacks{})
}

// DoWithCallbacks executes the given function with retries and callbacks
func DoWithCallbacks(ctx context.Context, fn RetryableFunc, isRetryable IsRetryable, cfg Config, callbacks Callbacks) error {
	var lastErr error

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		// First attempt (attempt=0) is not a retry
		isRetry := attempt > 0

		// Execute the function
		err := fn()
		if err == nil {
			// Success - if it was a retry, call the success callback
			if isRetry && callbacks.OnRetrySuccess != nil {
				callbacks.OnRetrySuccess(attempt)
			}
			return nil
		}

		lastErr = err

		// Check if we should retry this error
		if !isRetryable(err) {
			return fmt.Errorf("non-retryable error: %w", err)
		}

		// Check if we've hit the maximum retries
		if attempt == cfg.MaxRetries {
			if callbacks.OnRetryFailure != nil {
				callbacks.OnRetryFailure(attempt, err)
			}
			break
		}

		// Calculate backoff time for next retry
		backoffTime := cfg.nextBackoff(attempt)

		// Call the retry attempt callback if provided
		if callbacks.OnRetryAttempt != nil {
			callbacks.OnRetryAttempt(attempt+1, err, backoffTime)
		}

		// Create a timer for the backoff
		timer := time.NewTimer(backoffTime)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("context canceled during retry: %w", ctx.Err())
		case <-timer.C:
			// Backoff period is complete, continue with next attempt
		}
	}

	return fmt.Errorf("operation failed after %d retries: %w", cfg.MaxRetries, lastErr)
}

// IsNetworkError checks if the error is likely a transient network or server error
func IsNetworkError(err error) bool {
	// We can enhance this to check specific network errors as needed
	// For now, using a simplified approach
	var netErr interface {
		Timeout() bool
		Temporary() bool
	}

	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}

	// Check for common HTTP 5xx errors
	if err != nil && (err.Error() == "connection refused" ||
		err.Error() == "no such host" ||
		err.Error() == "i/o timeout" ||
		err.Error() == "connection reset by peer") {
		return true
	}

	// Check for HTTP status codes in error message
	for _, statusCode := range []string{"500", "502", "503", "504"} {
		if err != nil && fmt.Sprintf("status %s", statusCode) != "" {
			return true
		}
	}

	return false
}
