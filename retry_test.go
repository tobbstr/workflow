package workflow

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRetryConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  RetryConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: RetryConfig{
				MaxAttempts:  3,
				InitialDelay: 100 * time.Millisecond,
				MaxDelay:     5 * time.Second,
				Multiplier:   2.0,
			},
			wantErr: false,
		},
		{
			name: "MaxAttempts less than 1",
			config: RetryConfig{
				MaxAttempts:  0,
				InitialDelay: 100 * time.Millisecond,
				MaxDelay:     5 * time.Second,
				Multiplier:   2.0,
			},
			wantErr: true,
		},
		{
			name: "InitialDelay is zero",
			config: RetryConfig{
				MaxAttempts:  3,
				InitialDelay: 0,
				MaxDelay:     5 * time.Second,
				Multiplier:   2.0,
			},
			wantErr: true,
		},
		{
			name: "MaxDelay less than InitialDelay",
			config: RetryConfig{
				MaxAttempts:  3,
				InitialDelay: 5 * time.Second,
				MaxDelay:     100 * time.Millisecond,
				Multiplier:   2.0,
			},
			wantErr: true,
		},
		{
			name: "Multiplier less than 1.0",
			config: RetryConfig{
				MaxAttempts:  3,
				InitialDelay: 100 * time.Millisecond,
				MaxDelay:     5 * time.Second,
				Multiplier:   0.5,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWorkflow_WithRetry(t *testing.T) {
	t.Run("sets default retry config", func(t *testing.T) {
		config := RetryConfig{
			MaxAttempts:  3,
			InitialDelay: 100 * time.Millisecond,
			MaxDelay:     5 * time.Second,
			Multiplier:   2.0,
		}

		wf := New().WithRetry(config)

		if wf.defaultRetry == nil {
			t.Fatal("expected default retry to be set")
		}

		if wf.defaultRetry.MaxAttempts != 3 {
			t.Errorf("expected MaxAttempts=3, got %d", wf.defaultRetry.MaxAttempts)
		}
	})

	t.Run("panics with invalid config", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic")
			}
		}()

		New().WithRetry(RetryConfig{
			MaxAttempts:  0, // Invalid
			InitialDelay: 100 * time.Millisecond,
			MaxDelay:     5 * time.Second,
			Multiplier:   2.0,
		})
	})
}

func TestStepOption_WithRetry(t *testing.T) {
	t.Run("applies step-level retry config", func(t *testing.T) {
		workflowRetry := RetryConfig{
			MaxAttempts:  3,
			InitialDelay: 100 * time.Millisecond,
			MaxDelay:     5 * time.Second,
			Multiplier:   2.0,
		}

		stepRetry := RetryConfig{
			MaxAttempts:  5,
			InitialDelay: 50 * time.Millisecond,
			MaxDelay:     2 * time.Second,
			Multiplier:   1.5,
		}

		wf := New().
			WithRetry(workflowRetry).
			Step("step1", TypedStep(func(ctx context.Context, input string) (string, error) {
				return input, nil
			}), WithRetry(stepRetry))

		// Step should have override config
		if wf.steps[0].retry.MaxAttempts != 5 {
			t.Errorf("expected MaxAttempts=5, got %d", wf.steps[0].retry.MaxAttempts)
		}
	})

	t.Run("panics with invalid step config", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic")
			}
		}()

		WithRetry(RetryConfig{
			MaxAttempts:  0, // Invalid
			InitialDelay: 100 * time.Millisecond,
			MaxDelay:     5 * time.Second,
			Multiplier:   2.0,
		})
	})
}

func TestStepOption_NoRetry(t *testing.T) {
	t.Run("disables retry for specific step", func(t *testing.T) {
		workflowRetry := RetryConfig{
			MaxAttempts:  3,
			InitialDelay: 100 * time.Millisecond,
			MaxDelay:     5 * time.Second,
			Multiplier:   2.0,
		}

		wf := New().
			WithRetry(workflowRetry).
			Step("step1", TypedStep(func(ctx context.Context, input string) (string, error) {
				return input, nil
			})).
			Step("step2", TypedStep(func(ctx context.Context, input string) (string, error) {
				return input, nil
			}), NoRetry())

		// First step should have workflow retry
		if wf.steps[0].retry == nil {
			t.Error("expected step1 to have retry config")
		}

		// Second step should have no retry
		if wf.steps[1].retry != nil {
			t.Error("expected step2 to have no retry config")
		}
	})
}

func TestExecuteWithRetry(t *testing.T) {
	t.Run("succeeds on first attempt", func(t *testing.T) {
		config := &RetryConfig{
			MaxAttempts:  3,
			InitialDelay: 10 * time.Millisecond,
			MaxDelay:     100 * time.Millisecond,
			Multiplier:   2.0,
		}

		attempts := 0
		err := executeWithRetry(context.Background(), config, func() error {
			attempts++
			return nil
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if attempts != 1 {
			t.Errorf("expected 1 attempt, got %d", attempts)
		}
	})

	t.Run("retries on failure", func(t *testing.T) {
		config := &RetryConfig{
			MaxAttempts:  3,
			InitialDelay: 10 * time.Millisecond,
			MaxDelay:     100 * time.Millisecond,
			Multiplier:   2.0,
		}

		attempts := 0
		err := executeWithRetry(context.Background(), config, func() error {
			attempts++
			if attempts < 3 {
				return errors.New("temporary failure")
			}
			return nil
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if attempts != 3 {
			t.Errorf("expected 3 attempts, got %d", attempts)
		}
	})

	t.Run("returns error after max attempts", func(t *testing.T) {
		config := &RetryConfig{
			MaxAttempts:  3,
			InitialDelay: 10 * time.Millisecond,
			MaxDelay:     100 * time.Millisecond,
			Multiplier:   2.0,
		}

		expectedErr := errors.New("persistent failure")
		attempts := 0

		err := executeWithRetry(context.Background(), config, func() error {
			attempts++
			return expectedErr
		})

		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if attempts != 3 {
			t.Errorf("expected 3 attempts, got %d", attempts)
		}

		if !errors.Is(err, expectedErr) {
			t.Errorf("error chain should include original error")
		}
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		config := &RetryConfig{
			MaxAttempts:  10,
			InitialDelay: 100 * time.Millisecond,
			MaxDelay:     1 * time.Second,
			Multiplier:   2.0,
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		attempts := 0
		err := executeWithRetry(ctx, config, func() error {
			attempts++
			return errors.New("failure")
		})

		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled error, got %v", err)
		}

		// Should fail fast on context cancellation
		if attempts > 1 {
			t.Errorf("expected 1 attempt max, got %d", attempts)
		}
	})

	t.Run("works with nil config", func(t *testing.T) {
		attempts := 0
		err := executeWithRetry(context.Background(), nil, func() error {
			attempts++
			if attempts == 1 {
				return errors.New("failure")
			}
			return nil
		})

		// With nil config, should not retry
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if attempts != 1 {
			t.Errorf("expected 1 attempt (no retry), got %d", attempts)
		}
	})

	t.Run("respects retry condition", func(t *testing.T) {
		config := &RetryConfig{
			MaxAttempts:  3,
			InitialDelay: 10 * time.Millisecond,
			MaxDelay:     100 * time.Millisecond,
			Multiplier:   2.0,
			Condition: func(err error) bool {
				return errors.Is(err, context.DeadlineExceeded)
			},
		}

		// Non-retryable error
		attempts := 0
		err := executeWithRetry(context.Background(), config, func() error {
			attempts++
			return errors.New("permanent error")
		})

		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if attempts != 1 {
			t.Errorf("expected 1 attempt (no retry for non-retryable error), got %d", attempts)
		}
	})
}

func TestRetryConditions(t *testing.T) {
	t.Run("RetryOnAny", func(t *testing.T) {
		condition := RetryOnAny()

		if !condition(errors.New("any error")) {
			t.Error("RetryOnAny should return true for any error")
		}

		if !condition(context.Canceled) {
			t.Error("RetryOnAny should return true for context.Canceled")
		}
	})

	t.Run("RetryOnGRPCCodes", func(t *testing.T) {
		condition := RetryOnGRPCCodes(codes.Unavailable, codes.ResourceExhausted)

		// Retryable codes
		if !condition(status.Error(codes.Unavailable, "service unavailable")) {
			t.Error("should retry on Unavailable")
		}

		if !condition(status.Error(codes.ResourceExhausted, "rate limited")) {
			t.Error("should retry on ResourceExhausted")
		}

		// Non-retryable codes
		if condition(status.Error(codes.InvalidArgument, "bad request")) {
			t.Error("should not retry on InvalidArgument")
		}

		if condition(status.Error(codes.NotFound, "not found")) {
			t.Error("should not retry on NotFound")
		}

		// Non-gRPC error
		if condition(errors.New("regular error")) {
			t.Error("should not retry on non-gRPC error")
		}
	})

	t.Run("RetryOnNetworkErrors", func(t *testing.T) {
		condition := RetryOnNetworkErrors()

		// Network timeout error
		netErr := &net.DNSError{
			Err:         "timeout",
			IsTimeout:   true,
			IsTemporary: true,
		}
		if !condition(netErr) {
			t.Error("should retry on network timeout")
		}

		// Regular error
		if condition(errors.New("regular error")) {
			t.Error("should not retry on non-network error")
		}
	})

	t.Run("RetryOnContextErrors", func(t *testing.T) {
		condition := RetryOnContextErrors()

		if !condition(context.Canceled) {
			t.Error("should retry on context.Canceled")
		}

		if !condition(context.DeadlineExceeded) {
			t.Error("should retry on context.DeadlineExceeded")
		}

		if condition(errors.New("regular error")) {
			t.Error("should not retry on non-context error")
		}
	})

	t.Run("CombineConditions OR logic", func(t *testing.T) {
		condition := CombineConditions(
			RetryOnGRPCCodes(codes.Unavailable),
			RetryOnContextErrors(),
		)

		if !condition(status.Error(codes.Unavailable, "unavailable")) {
			t.Error("should retry on Unavailable")
		}

		if !condition(context.Canceled) {
			t.Error("should retry on context.Canceled")
		}

		if condition(errors.New("regular error")) {
			t.Error("should not retry on non-matching error")
		}
	})

	t.Run("AllConditions AND logic", func(t *testing.T) {
		// This is a contrived example - checking if error is both network and gRPC
		condition := AllConditions(
			func(err error) bool { return errors.Is(err, context.DeadlineExceeded) },
			func(err error) bool { return err.Error() == "context deadline exceeded" },
		)

		if !condition(context.DeadlineExceeded) {
			t.Error("should match when all conditions are true")
		}

		if condition(context.Canceled) {
			t.Error("should not match when any condition is false")
		}
	})

	t.Run("Not inverts condition", func(t *testing.T) {
		condition := Not(RetryOnContextErrors())

		if condition(context.Canceled) {
			t.Error("should not retry on context.Canceled (inverted)")
		}

		if !condition(errors.New("regular error")) {
			t.Error("should retry on non-context error (inverted)")
		}
	})
}

func TestWorkflow_RetryIntegration(t *testing.T) {
	t.Run("workflow with default retry", func(t *testing.T) {
		attempts := 0

		wf := New().
			WithRetry(RetryConfig{
				MaxAttempts:  3,
				InitialDelay: 10 * time.Millisecond,
				MaxDelay:     100 * time.Millisecond,
				Multiplier:   2.0,
			}).
			Step("step1", TypedStep(func(ctx context.Context, input int) (int, error) {
				attempts++
				if attempts < 2 {
					return 0, errors.New("temporary failure")
				}
				return input * 2, nil
			}))

		ctx := context.Background()
		result, err := ExecuteTyped[int](ctx, wf, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result != 10 {
			t.Errorf("expected 10, got %d", result)
		}

		if attempts != 2 {
			t.Errorf("expected 2 attempts, got %d", attempts)
		}
	})

	t.Run("step-level override", func(t *testing.T) {
		step1Attempts := 0
		step2Attempts := 0

		wf := New().
			WithRetry(RetryConfig{
				MaxAttempts:  2,
				InitialDelay: 10 * time.Millisecond,
				MaxDelay:     100 * time.Millisecond,
				Multiplier:   2.0,
			}).
			Step("step1", TypedStep(func(ctx context.Context, input int) (int, error) {
				step1Attempts++
				if step1Attempts < 2 {
					return 0, errors.New("temporary failure")
				}
				return input * 2, nil
			})).
			Step("step2", TypedStep(func(ctx context.Context, input int) (int, error) {
				step2Attempts++
				if step2Attempts < 3 {
					return 0, errors.New("temporary failure")
				}
				return input + 1, nil
			}), WithRetry(RetryConfig{
				MaxAttempts:  5, // Override with more attempts
				InitialDelay: 10 * time.Millisecond,
				MaxDelay:     100 * time.Millisecond,
				Multiplier:   2.0,
			}))

		ctx := context.Background()
		result, err := ExecuteTyped[int](ctx, wf, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result != 11 { // (5 * 2) + 1
			t.Errorf("expected 11, got %d", result)
		}

		if step1Attempts != 2 {
			t.Errorf("expected 2 attempts for step1, got %d", step1Attempts)
		}

		if step2Attempts != 3 {
			t.Errorf("expected 3 attempts for step2, got %d", step2Attempts)
		}
	})

	t.Run("NoRetry disables retry", func(t *testing.T) {
		attempts := 0

		wf := New().
			WithRetry(RetryConfig{
				MaxAttempts:  3,
				InitialDelay: 10 * time.Millisecond,
				MaxDelay:     100 * time.Millisecond,
				Multiplier:   2.0,
			}).
			Step("step1", TypedStep(func(ctx context.Context, input int) (int, error) {
				attempts++
				return 0, errors.New("failure")
			}), NoRetry())

		ctx := context.Background()
		_, err := ExecuteTyped[int](ctx, wf, 5)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if attempts != 1 {
			t.Errorf("expected 1 attempt (no retry), got %d", attempts)
		}
	})

	t.Run("retry with condition", func(t *testing.T) {
		attempts := 0

		wf := New().
			WithRetry(RetryConfig{
				MaxAttempts:  3,
				InitialDelay: 10 * time.Millisecond,
				MaxDelay:     100 * time.Millisecond,
				Multiplier:   2.0,
				Condition:    RetryOnGRPCCodes(codes.Unavailable),
			}).
			Step("step1", TypedStep(func(ctx context.Context, input int) (int, error) {
				attempts++
				if attempts == 1 {
					return 0, status.Error(codes.Unavailable, "service unavailable")
				}
				return input * 2, nil
			}))

		ctx := context.Background()
		result, err := ExecuteTyped[int](ctx, wf, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result != 10 {
			t.Errorf("expected 10, got %d", result)
		}

		if attempts != 2 {
			t.Errorf("expected 2 attempts, got %d", attempts)
		}
	})

	t.Run("non-retryable error fails immediately", func(t *testing.T) {
		attempts := 0

		wf := New().
			WithRetry(RetryConfig{
				MaxAttempts:  3,
				InitialDelay: 10 * time.Millisecond,
				MaxDelay:     100 * time.Millisecond,
				Multiplier:   2.0,
				Condition:    RetryOnGRPCCodes(codes.Unavailable),
			}).
			Step("step1", TypedStep(func(ctx context.Context, input int) (int, error) {
				attempts++
				// Return non-retryable error
				return 0, status.Error(codes.InvalidArgument, "bad request")
			}))

		ctx := context.Background()
		_, err := ExecuteTyped[int](ctx, wf, 5)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if attempts != 1 {
			t.Errorf("expected 1 attempt (non-retryable), got %d", attempts)
		}
	})
}

func TestCalculateDelay(t *testing.T) {
	config := &RetryConfig{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     5 * time.Second,
		Multiplier:   2.0,
	}

	t.Run("exponential backoff", func(t *testing.T) {
		// Attempt 1: ~100ms
		delay1 := calculateDelay(config, 1)
		if delay1 < 75*time.Millisecond || delay1 > 125*time.Millisecond {
			t.Errorf("attempt 1: expected ~100ms, got %v", delay1)
		}

		// Attempt 2: ~200ms
		delay2 := calculateDelay(config, 2)
		if delay2 < 150*time.Millisecond || delay2 > 250*time.Millisecond {
			t.Errorf("attempt 2: expected ~200ms, got %v", delay2)
		}

		// Attempt 3: ~400ms
		delay3 := calculateDelay(config, 3)
		if delay3 < 300*time.Millisecond || delay3 > 500*time.Millisecond {
			t.Errorf("attempt 3: expected ~400ms, got %v", delay3)
		}
	})

	t.Run("caps at MaxDelay", func(t *testing.T) {
		// After many attempts, should cap at MaxDelay
		delay := calculateDelay(config, 20)
		if delay > config.MaxDelay+config.MaxDelay/4 { // Allow for jitter
			t.Errorf("delay should be capped at ~MaxDelay, got %v", delay)
		}
	})
}

// Custom error type for testing HTTP status
type httpError struct {
	statusCode int
	message    string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.statusCode, e.message)
}

func (e *httpError) HTTPStatus() int {
	return e.statusCode
}

func TestRetryOnHTTPStatus(t *testing.T) {
	condition := RetryOnHTTPStatus(429, 503, 504)

	t.Run("retries on matching status", func(t *testing.T) {
		if !condition(&httpError{statusCode: 429, message: "too many requests"}) {
			t.Error("should retry on 429")
		}

		if !condition(&httpError{statusCode: 503, message: "service unavailable"}) {
			t.Error("should retry on 503")
		}

		if !condition(&httpError{statusCode: 504, message: "gateway timeout"}) {
			t.Error("should retry on 504")
		}
	})

	t.Run("does not retry on non-matching status", func(t *testing.T) {
		if condition(&httpError{statusCode: 400, message: "bad request"}) {
			t.Error("should not retry on 400")
		}

		if condition(&httpError{statusCode: 404, message: "not found"}) {
			t.Error("should not retry on 404")
		}
	})

	t.Run("does not retry on non-HTTP error", func(t *testing.T) {
		if condition(errors.New("regular error")) {
			t.Error("should not retry on non-HTTP error")
		}
	})
}
