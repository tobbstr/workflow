package workflow

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RetryCondition determines if an error should trigger a retry.
// Return true to retry, false to fail immediately.
//
// Example:
//
//	condition := func(err error) bool {
//		var netErr net.Error
//		return errors.As(err, &netErr) && netErr.Timeout()
//	}
type RetryCondition func(error) bool

// RetryConfig defines exponential backoff parameters for retry behavior.
//
// Example:
//
//	config := workflow.RetryConfig{
//		MaxAttempts:  3,
//		InitialDelay: 100 * time.Millisecond,
//		MaxDelay:     5 * time.Second,
//		Multiplier:   2.0,
//		Condition:    workflow.RetryOnNetworkErrors(),
//	}
type RetryConfig struct {
	// MaxAttempts is the maximum number of attempts (including initial).
	// Must be >= 1.
	MaxAttempts int

	// InitialDelay is the delay before the first retry.
	// Must be > 0.
	InitialDelay time.Duration

	// MaxDelay is the maximum delay between retries.
	// Must be >= InitialDelay.
	MaxDelay time.Duration

	// Multiplier is the exponential backoff multiplier.
	// Must be >= 1.0.
	Multiplier float64

	// Condition determines which errors are retryable.
	// If nil, all errors are retried (default behavior).
	Condition RetryCondition
}

// Validate checks if the RetryConfig is valid.
// Returns an error if any parameter is invalid.
//
// Example:
//
//	if err := config.Validate(); err != nil {
//		return fmt.Errorf("invalid retry config: %w", err)
//	}
func (c RetryConfig) Validate() error {
	if c.MaxAttempts < 1 {
		return fmt.Errorf("MaxAttempts must be >= 1, got %d", c.MaxAttempts)
	}

	if c.InitialDelay <= 0 {
		return fmt.Errorf("InitialDelay must be > 0, got %v", c.InitialDelay)
	}

	if c.MaxDelay < c.InitialDelay {
		return fmt.Errorf("MaxDelay (%v) must be >= InitialDelay (%v)", c.MaxDelay, c.InitialDelay)
	}

	if c.Multiplier < 1.0 {
		return fmt.Errorf("Multiplier must be >= 1.0, got %f", c.Multiplier)
	}

	return nil
}

// shouldRetry determines if an error should trigger a retry based on the condition.
func (c RetryConfig) shouldRetry(err error) bool {
	if c.Condition == nil {
		return true // Default: retry all errors
	}
	return c.Condition(err)
}

// WithRetry sets the default retry policy for all workflow steps.
// Individual steps can override this with their own WithRetry() or NoRetry() options.
//
// Example:
//
//	wf := workflow.New().
//		WithRetry(workflow.RetryConfig{
//			MaxAttempts:  3,
//			InitialDelay: 100 * time.Millisecond,
//			MaxDelay:     5 * time.Second,
//			Multiplier:   2.0,
//		})
func (w *Workflow) WithRetry(config RetryConfig) *Workflow {
	if err := config.Validate(); err != nil {
		panic(fmt.Errorf("invalid retry config: %w", err))
	}
	w.defaultRetry = &config
	return w
}

// WithRetry returns a StepOption that overrides the workflow's default retry policy for a specific step.
//
// Example:
//
//	wf.Step("external_api", workflow.TypedStep(callAPI),
//		workflow.WithRetry(workflow.RetryConfig{
//			MaxAttempts:  5,
//			InitialDelay: 50 * time.Millisecond,
//			MaxDelay:     2 * time.Second,
//			Multiplier:   2.0,
//		}))
func WithRetry(config RetryConfig) StepOption {
	if err := config.Validate(); err != nil {
		panic(fmt.Errorf("invalid retry config: %w", err))
	}
	return func(sc *stepConfig) {
		sc.retry = &config
	}
}

// NoRetry returns a StepOption that explicitly disables retry for a specific step.
// Use this for non-idempotent operations that should not be retried.
//
// Example:
//
//	wf.Step("mutate_db", workflow.TypedStep(updateDatabase),
//		workflow.NoRetry())
func NoRetry() StepOption {
	return func(sc *stepConfig) {
		sc.noRetry = true
	}
}

// executeWithRetry executes a function with retry logic based on the config.
func executeWithRetry(ctx context.Context, config *RetryConfig, fn func() error) error {
	if config == nil {
		return fn()
	}

	var lastErr error
	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		// Check context cancellation before attempt
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled: %w", err)
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		// Check if error is retryable
		if !config.shouldRetry(lastErr) {
			return lastErr
		}

		// Don't sleep after the last attempt
		if attempt == config.MaxAttempts {
			break
		}

		// Calculate delay with exponential backoff and jitter
		delay := calculateDelay(config, attempt)

		// Sleep with context awareness
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during retry: %w", ctx.Err())
		}
	}

	return fmt.Errorf("max retry attempts (%d) exceeded: %w", config.MaxAttempts, lastErr)
}

// calculateDelay calculates the delay for a given attempt with exponential backoff and jitter.
func calculateDelay(config *RetryConfig, attempt int) time.Duration {
	// Calculate exponential backoff
	delay := float64(config.InitialDelay) * math.Pow(config.Multiplier, float64(attempt-1))

	// Cap at MaxDelay
	if delay > float64(config.MaxDelay) {
		delay = float64(config.MaxDelay)
	}

	// Add jitter (±25%) to prevent thundering herd
	jitter := delay * 0.25 * (rand.Float64()*2 - 1)
	finalDelay := time.Duration(delay + jitter)

	// Ensure delay is positive
	if finalDelay < 0 {
		finalDelay = config.InitialDelay
	}

	return finalDelay
}

// Built-in retry conditions

// RetryOnAny retries on all errors.
// This is the default behavior if Condition is nil.
//
// Example:
//
//	config := workflow.RetryConfig{
//		MaxAttempts:  3,
//		InitialDelay: 100 * time.Millisecond,
//		MaxDelay:     5 * time.Second,
//		Multiplier:   2.0,
//		Condition:    workflow.RetryOnAny(),
//	}
func RetryOnAny() RetryCondition {
	return func(err error) bool {
		return true
	}
}

// RetryOnGRPCCodes retries only if error is a gRPC status with one of the specified codes.
//
// Example:
//
//	condition := workflow.RetryOnGRPCCodes(
//		codes.Unavailable,
//		codes.ResourceExhausted,
//		codes.DeadlineExceeded,
//	)
func RetryOnGRPCCodes(retryableCodes ...codes.Code) RetryCondition {
	codeMap := make(map[codes.Code]bool)
	for _, code := range retryableCodes {
		codeMap[code] = true
	}

	return func(err error) bool {
		st, ok := status.FromError(err)
		if !ok {
			return false
		}
		return codeMap[st.Code()]
	}
}

// RetryOnHTTPStatus retries only if error contains an HTTP response with one of the specified status codes.
//
// Example:
//
//	condition := workflow.RetryOnHTTPStatus(
//		http.StatusTooManyRequests,    // 429
//		http.StatusServiceUnavailable,  // 503
//		http.StatusGatewayTimeout,      // 504
//	)
func RetryOnHTTPStatus(statuses ...int) RetryCondition {
	statusMap := make(map[int]bool)
	for _, status := range statuses {
		statusMap[status] = true
	}

	return func(err error) bool {
		// Check if error wraps an HTTP status
		type httpStatusError interface {
			HTTPStatus() int
		}

		var httpErr httpStatusError
		if errors.As(err, &httpErr) {
			return statusMap[httpErr.HTTPStatus()]
		}

		return false
	}
}

// RetryOnNetworkErrors retries on network and timeout errors.
//
// Example:
//
//	config := workflow.RetryConfig{
//		MaxAttempts:  3,
//		InitialDelay: 100 * time.Millisecond,
//		MaxDelay:     5 * time.Second,
//		Multiplier:   2.0,
//		Condition:    workflow.RetryOnNetworkErrors(),
//	}
func RetryOnNetworkErrors() RetryCondition {
	return func(err error) bool {
		// Check for network errors
		var netErr net.Error
		if errors.As(err, &netErr) {
			return true
		}

		// Check for DNS errors
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) {
			return true
		}

		// Check for operation errors
		var opErr *net.OpError
		if errors.As(err, &opErr) {
			return true
		}

		return false
	}
}

// RetryOnContextErrors retries on context cancellation or deadline exceeded errors.
// Note: Usually you DON'T want to retry context errors.
//
// Example:
//
//	// This is generally NOT recommended
//	condition := workflow.RetryOnContextErrors()
func RetryOnContextErrors() RetryCondition {
	return func(err error) bool {
		return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	}
}

// Condition combinators

// CombineConditions combines multiple conditions with OR logic.
// Returns true if ANY condition returns true.
//
// Example:
//
//	condition := workflow.CombineConditions(
//		workflow.RetryOnNetworkErrors(),
//		workflow.RetryOnGRPCCodes(codes.Unavailable),
//	)
func CombineConditions(conditions ...RetryCondition) RetryCondition {
	return func(err error) bool {
		for _, condition := range conditions {
			if condition(err) {
				return true
			}
		}
		return false
	}
}

// AllConditions combines multiple conditions with AND logic.
// Returns true only if ALL conditions return true.
//
// Example:
//
//	condition := workflow.AllConditions(
//		workflow.RetryOnNetworkErrors(),
//		workflow.Not(workflow.RetryOnContextErrors()),
//	)
func AllConditions(conditions ...RetryCondition) RetryCondition {
	return func(err error) bool {
		for _, condition := range conditions {
			if !condition(err) {
				return false
			}
		}
		return true
	}
}

// Not inverts a condition.
// Returns true if the condition returns false, and vice versa.
//
// Example:
//
//	// Retry on any error EXCEPT context errors
//	condition := workflow.Not(workflow.RetryOnContextErrors())
func Not(condition RetryCondition) RetryCondition {
	return func(err error) bool {
		return !condition(err)
	}
}
