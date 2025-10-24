package workflow

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

var (
	ErrTransitionRejected = errors.New("transition rejected")
	ErrNotFound           = errors.New("not found")
	ErrPermissionDenied   = errors.New("permission denied")
)

func TestAllowError(t *testing.T) {
	t.Run("bypasses error and continues workflow", func(t *testing.T) {
		called := make([]string, 0)

		step1 := func(ctx context.Context, input string) (string, error) {
			called = append(called, "step1")
			return "", ErrTransitionRejected
		}

		step2 := func(ctx context.Context, input string) (string, error) {
			called = append(called, "step2")
			return "final: " + input, nil
		}

		wf := New().
			Step("step1", TypedStep(step1),
				AllowError(
					func(err error) bool { return errors.Is(err, ErrTransitionRejected) },
					func(ctx context.Context, input string, err error) string {
						return input // Return original input
					},
				)).
			Step("step2", TypedStep(step2))

		result, err := ExecuteTyped[string](context.Background(), wf, "test")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if result != "final: test" {
			t.Errorf("expected 'final: test', got %q", result)
		}

		if len(called) != 2 {
			t.Errorf("expected 2 steps called, got %d", len(called))
		}
	})

	t.Run("only allows matching errors", func(t *testing.T) {
		step1 := func(ctx context.Context, input string) (string, error) {
			return "", ErrPermissionDenied
		}

		wf := New().
			Step("step1", TypedStep(step1),
				AllowError(
					func(err error) bool { return errors.Is(err, ErrTransitionRejected) },
					func(ctx context.Context, input string, err error) string {
						return input
					},
				))

		_, err := ExecuteTyped[string](context.Background(), wf, "test")
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if !errors.Is(err, ErrPermissionDenied) {
			t.Errorf("expected ErrPermissionDenied, got %v", err)
		}
	})

	t.Run("fallback receives correct input and error", func(t *testing.T) {
		var capturedInput string
		var capturedErr error

		step1 := func(ctx context.Context, input string) (int, error) {
			return 0, ErrNotFound
		}

		wf := New().
			Step("step1", TypedStep(step1),
				AllowError(
					func(err error) bool { return true },
					func(ctx context.Context, input string, err error) int {
						capturedInput = input
						capturedErr = err
						return 42 // Fallback value
					},
				))

		result, err := ExecuteTyped[int](context.Background(), wf, "myinput")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if result != 42 {
			t.Errorf("expected 42, got %d", result)
		}

		if capturedInput != "myinput" {
			t.Errorf("expected 'myinput', got %q", capturedInput)
		}

		if !errors.Is(capturedErr, ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", capturedErr)
		}
	})

	t.Run("does not trigger retry when error is allowed", func(t *testing.T) {
		attemptCount := 0

		step1 := func(ctx context.Context, input string) (string, error) {
			attemptCount++
			return "", ErrTransitionRejected
		}

		wf := New().
			WithRetry(RetryConfig{
				MaxAttempts:  5,
				InitialDelay: 1 * time.Millisecond,
				MaxDelay:     10 * time.Millisecond,
				Multiplier:   2.0,
			}).
			Step("step1", TypedStep(step1),
				AllowError(
					func(err error) bool { return errors.Is(err, ErrTransitionRejected) },
					func(ctx context.Context, input string, err error) string {
						return "fallback"
					},
				))

		result, err := ExecuteTyped[string](context.Background(), wf, "test")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if result != "fallback" {
			t.Errorf("expected 'fallback', got %q", result)
		}

		// Should only execute once, not retry
		if attemptCount != 1 {
			t.Errorf("expected 1 attempt, got %d", attemptCount)
		}
	})

	t.Run("retries when error is not allowed", func(t *testing.T) {
		attemptCount := 0

		step1 := func(ctx context.Context, input string) (string, error) {
			attemptCount++
			return "", ErrPermissionDenied
		}

		wf := New().
			WithRetry(RetryConfig{
				MaxAttempts:  3,
				InitialDelay: 1 * time.Millisecond,
				MaxDelay:     10 * time.Millisecond,
				Multiplier:   2.0,
			}).
			Step("step1", TypedStep(step1),
				AllowError(
					func(err error) bool { return errors.Is(err, ErrTransitionRejected) },
					func(ctx context.Context, input string, err error) string {
						return "fallback"
					},
				))

		_, err := ExecuteTyped[string](context.Background(), wf, "test")
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		// Should retry all attempts
		if attemptCount != 3 {
			t.Errorf("expected 3 attempts, got %d", attemptCount)
		}
	})

	t.Run("works with complex types", func(t *testing.T) {
		type User struct {
			ID   int
			Name string
		}

		step1 := func(ctx context.Context, input User) (User, error) {
			return User{}, ErrNotFound
		}

		step2 := func(ctx context.Context, input User) (User, error) {
			input.Name = "updated"
			return input, nil
		}

		wf := New().
			Step("step1", TypedStep(step1),
				AllowError(
					func(err error) bool { return errors.Is(err, ErrNotFound) },
					func(ctx context.Context, input User, err error) User {
						// Return input with default values
						input.ID = 999
						return input
					},
				)).
			Step("step2", TypedStep(step2))

		input := User{ID: 1, Name: "test"}
		result, err := ExecuteTyped[User](context.Background(), wf, input)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if result.ID != 999 {
			t.Errorf("expected ID 999, got %d", result.ID)
		}

		if result.Name != "updated" {
			t.Errorf("expected name 'updated', got %q", result.Name)
		}
	})

	t.Run("fallback can access context", func(t *testing.T) {
		type contextKey string
		key := contextKey("testkey")

		step1 := func(ctx context.Context, input string) (string, error) {
			return "", errors.New("test error")
		}

		wf := New().
			Step("step1", TypedStep(step1),
				AllowError(
					func(err error) bool { return true },
					func(ctx context.Context, input string, err error) string {
						value := ctx.Value(key)
						if value != nil {
							return value.(string)
						}
						return "default"
					},
				))

		ctx := context.WithValue(context.Background(), key, "context-value")
		result, err := ExecuteTyped[string](ctx, wf, "test")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if result != "context-value" {
			t.Errorf("expected 'context-value', got %q", result)
		}
	})

	t.Run("multiple steps with different AllowError conditions", func(t *testing.T) {
		step1 := func(ctx context.Context, input string) (string, error) {
			return "", ErrTransitionRejected
		}

		step2 := func(ctx context.Context, input string) (string, error) {
			return "", ErrNotFound
		}

		step3 := func(ctx context.Context, input string) (string, error) {
			return "final: " + input, nil
		}

		wf := New().
			Step("step1", TypedStep(step1),
				AllowError(
					func(err error) bool { return errors.Is(err, ErrTransitionRejected) },
					func(ctx context.Context, input string, err error) string {
						return input + "-1"
					},
				)).
			Step("step2", TypedStep(step2),
				AllowError(
					func(err error) bool { return errors.Is(err, ErrNotFound) },
					func(ctx context.Context, input string, err error) string {
						return input + "-2"
					},
				)).
			Step("step3", TypedStep(step3))

		result, err := ExecuteTyped[string](context.Background(), wf, "test")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if result != "final: test-1-2" {
			t.Errorf("expected 'final: test-1-2', got %q", result)
		}
	})

	t.Run("works with step-level retry override", func(t *testing.T) {
		attemptCount := 0

		step1 := func(ctx context.Context, input string) (string, error) {
			attemptCount++
			return "", ErrPermissionDenied
		}

		wf := New().
			WithRetry(RetryConfig{
				MaxAttempts:  10,
				InitialDelay: 1 * time.Millisecond,
				MaxDelay:     10 * time.Millisecond,
				Multiplier:   2.0,
			}).
			Step("step1", TypedStep(step1),
				WithRetry(RetryConfig{
					MaxAttempts:  2,
					InitialDelay: 1 * time.Millisecond,
					MaxDelay:     10 * time.Millisecond,
					Multiplier:   2.0,
				}),
				AllowError(
					func(err error) bool { return errors.Is(err, ErrTransitionRejected) },
					func(ctx context.Context, input string, err error) string {
						return "fallback"
					},
				))

		_, err := ExecuteTyped[string](context.Background(), wf, "test")
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		// Should use step-level retry (2), not workflow-level (10)
		if attemptCount != 2 {
			t.Errorf("expected 2 attempts, got %d", attemptCount)
		}
	})

	t.Run("works with NoRetry", func(t *testing.T) {
		attemptCount := 0

		step1 := func(ctx context.Context, input string) (string, error) {
			attemptCount++
			return "", ErrPermissionDenied
		}

		wf := New().
			WithRetry(RetryConfig{
				MaxAttempts:  5,
				InitialDelay: 1 * time.Millisecond,
				MaxDelay:     10 * time.Millisecond,
				Multiplier:   2.0,
			}).
			Step("step1", TypedStep(step1),
				NoRetry(),
				AllowError(
					func(err error) bool { return errors.Is(err, ErrTransitionRejected) },
					func(ctx context.Context, input string, err error) string {
						return "fallback"
					},
				))

		_, err := ExecuteTyped[string](context.Background(), wf, "test")
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		// Should not retry
		if attemptCount != 1 {
			t.Errorf("expected 1 attempt, got %d", attemptCount)
		}
	})
}

func TestAllowError_ObserverIntegration(t *testing.T) {
	t.Run("notifies observer when error is allowed", func(t *testing.T) {
		obs := &testObserver{}

		step1 := func(ctx context.Context, input string) (string, error) {
			return "", ErrTransitionRejected
		}

		wf := New().
			WithObserver(obs).
			Step("step1", TypedStep(step1),
				AllowError(
					func(err error) bool { return errors.Is(err, ErrTransitionRejected) },
					func(ctx context.Context, input string, err error) string {
						return "fallback"
					},
				))

		_, err := ExecuteTyped[string](context.Background(), wf, "test")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if obs.errorAllowedCount != 1 {
			t.Errorf("expected 1 error allowed notification, got %d", obs.errorAllowedCount)
		}

		if !errors.Is(obs.lastAllowedError, ErrTransitionRejected) {
			t.Errorf("expected ErrTransitionRejected, got %v", obs.lastAllowedError)
		}

		if obs.lastAllowedStepName != "step1" {
			t.Errorf("expected step name 'step1', got %q", obs.lastAllowedStepName)
		}
	})

	t.Run("does not notify when error is not allowed", func(t *testing.T) {
		obs := &testObserver{}

		step1 := func(ctx context.Context, input string) (string, error) {
			return "", ErrPermissionDenied
		}

		wf := New().
			WithObserver(obs).
			Step("step1", TypedStep(step1),
				AllowError(
					func(err error) bool { return errors.Is(err, ErrTransitionRejected) },
					func(ctx context.Context, input string, err error) string {
						return "fallback"
					},
				))

		_, err := ExecuteTyped[string](context.Background(), wf, "test")
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if obs.errorAllowedCount != 0 {
			t.Errorf("expected 0 error allowed notifications, got %d", obs.errorAllowedCount)
		}
	})

	t.Run("step completes successfully after error is allowed", func(t *testing.T) {
		obs := &testObserver{}

		step1 := func(ctx context.Context, input string) (string, error) {
			return "", ErrTransitionRejected
		}

		wf := New().
			WithObserver(obs).
			Step("step1", TypedStep(step1),
				AllowError(
					func(err error) bool { return errors.Is(err, ErrTransitionRejected) },
					func(ctx context.Context, input string, err error) string {
						return "fallback"
					},
				))

		_, err := ExecuteTyped[string](context.Background(), wf, "test")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if obs.stepCompleteCount != 1 {
			t.Errorf("expected 1 step complete notification, got %d", obs.stepCompleteCount)
		}

		// Step should complete without error (nil) because error was allowed
		if obs.lastCompleteError != nil {
			t.Errorf("expected nil error in step complete, got %v", obs.lastCompleteError)
		}
	})
}

// testObserver implements Observer for testing.
type testObserver struct {
	NoopObserver
	errorAllowedCount   int
	lastAllowedError    error
	lastAllowedStepName StepName
	stepCompleteCount   int
	lastCompleteError   error
}

func (o *testObserver) OnStepErrorAllowed(ctx context.Context, stepName StepName, err error) {
	o.errorAllowedCount++
	o.lastAllowedError = err
	o.lastAllowedStepName = stepName
}

func (o *testObserver) OnStepComplete(ctx context.Context, stepName StepName, duration time.Duration, err error) {
	o.stepCompleteCount++
	o.lastCompleteError = err
}

func TestAllowError_FSMUseCase(t *testing.T) {
	t.Run("FSM transition with fallback pattern", func(t *testing.T) {
		type State struct {
			Name string
			Path []string
		}

		tryPrimaryTransition := func(ctx context.Context, state State) (State, error) {
			return State{}, fmt.Errorf("primary transition failed: %w", ErrTransitionRejected)
		}

		tryAlternativeTransition := func(ctx context.Context, state State) (State, error) {
			state.Name = "alternative"
			state.Path = append(state.Path, "alternative")
			return state, nil
		}

		finalizeState := func(ctx context.Context, state State) (State, error) {
			state.Path = append(state.Path, "finalized")
			return state, nil
		}

		wf := New().
			WithID(WorkflowID("fsm-workflow")).
			Step("try_primary", TypedStep(tryPrimaryTransition),
				AllowError(
					func(err error) bool { return errors.Is(err, ErrTransitionRejected) },
					func(ctx context.Context, state State, err error) State {
						// Log that primary failed, return input state
						state.Path = append(state.Path, "primary_rejected")
						return state
					},
				)).
			Step("try_alternative", TypedStep(tryAlternativeTransition)).
			Step("finalize", TypedStep(finalizeState))

		input := State{Name: "initial", Path: []string{"start"}}
		result, err := ExecuteTyped[State](context.Background(), wf, input)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		expectedPath := []string{"start", "primary_rejected", "alternative", "finalized"}
		if len(result.Path) != len(expectedPath) {
			t.Fatalf("expected path length %d, got %d", len(expectedPath), len(result.Path))
		}

		for i, step := range expectedPath {
			if result.Path[i] != step {
				t.Errorf("path[%d]: expected %q, got %q", i, step, result.Path[i])
			}
		}

		if result.Name != "alternative" {
			t.Errorf("expected name 'alternative', got %q", result.Name)
		}
	})
}
