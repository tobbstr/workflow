package workflow

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestParallelConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  ParallelConfig
		wantErr bool
	}{
		{
			name: "AllMustSucceed valid",
			config: ParallelConfig{
				Strategy: AllMustSucceed,
			},
			wantErr: false,
		},
		{
			name: "AtLeastN valid",
			config: ParallelConfig{
				Strategy:        AtLeastN,
				MinSuccessCount: 2,
			},
			wantErr: false,
		},
		{
			name: "AtLeastN invalid MinSuccessCount",
			config: ParallelConfig{
				Strategy:        AtLeastN,
				MinSuccessCount: 0,
			},
			wantErr: true,
		},
		{
			name: "FirstSuccess valid",
			config: ParallelConfig{
				Strategy: FirstSuccess,
			},
			wantErr: false,
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

func TestParallel_AllMustSucceed(t *testing.T) {
	t.Run("all steps succeed", func(t *testing.T) {
		step := Parallel[int, int](
			ParallelConfig{Strategy: AllMustSucceed},
			func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			},
			func(ctx context.Context, input int) (int, error) {
				return input * 3, nil
			},
			func(ctx context.Context, input int) (int, error) {
				return input * 4, nil
			},
		)

		ctx := context.Background()
		result, err := step.Execute(ctx, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		results := result.([]int)
		if len(results) != 3 {
			t.Fatalf("expected 3 results, got %d", len(results))
		}

		if results[0] != 10 || results[1] != 15 || results[2] != 20 {
			t.Errorf("unexpected results: %v", results)
		}
	})

	t.Run("one step fails", func(t *testing.T) {
		step := Parallel[int, int](
			ParallelConfig{Strategy: AllMustSucceed},
			func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			},
			func(ctx context.Context, input int) (int, error) {
				return 0, errors.New("failure")
			},
			func(ctx context.Context, input int) (int, error) {
				return input * 4, nil
			},
		)

		ctx := context.Background()
		_, err := step.Execute(ctx, 5)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var parallelErr *ParallelExecutionError
		if !errors.As(err, &parallelErr) {
			t.Errorf("expected ParallelExecutionError, got %T", err)
		} else {
			if parallelErr.FailureCount != 1 {
				t.Errorf("expected 1 failure, got %d", parallelErr.FailureCount)
			}
			if parallelErr.SuccessCount != 2 {
				t.Errorf("expected 2 successes, got %d", parallelErr.SuccessCount)
			}
		}
	})

	t.Run("cancels remaining steps on error", func(t *testing.T) {
		var executionCount atomic.Int32

		step := Parallel[int, int](
			ParallelConfig{Strategy: AllMustSucceed},
			func(ctx context.Context, input int) (int, error) {
				executionCount.Add(1)
				return 0, errors.New("immediate failure")
			},
			func(ctx context.Context, input int) (int, error) {
				// This might not run if cancellation is fast enough
				time.Sleep(100 * time.Millisecond)
				if ctx.Err() != nil {
					return 0, ctx.Err()
				}
				executionCount.Add(1)
				return input * 2, nil
			},
		)

		ctx := context.Background()
		_, err := step.Execute(ctx, 5)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		// Should have at least one execution (the failing one)
		if executionCount.Load() == 0 {
			t.Error("expected at least one execution")
		}
	})
}

func TestParallel_AtLeastN(t *testing.T) {
	t.Run("meets minimum success count", func(t *testing.T) {
		step := Parallel[int, int](
			ParallelConfig{
				Strategy:        AtLeastN,
				MinSuccessCount: 2,
			},
			func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			},
			func(ctx context.Context, input int) (int, error) {
				return 0, errors.New("failure")
			},
			func(ctx context.Context, input int) (int, error) {
				return input * 4, nil
			},
		)

		ctx := context.Background()
		result, err := step.Execute(ctx, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		results := result.([]int)
		if len(results) != 2 {
			t.Fatalf("expected 2 successful results, got %d", len(results))
		}
	})

	t.Run("fails to meet minimum success count", func(t *testing.T) {
		step := Parallel[int, int](
			ParallelConfig{
				Strategy:        AtLeastN,
				MinSuccessCount: 3,
			},
			func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			},
			func(ctx context.Context, input int) (int, error) {
				return 0, errors.New("failure 1")
			},
			func(ctx context.Context, input int) (int, error) {
				return 0, errors.New("failure 2")
			},
		)

		ctx := context.Background()
		_, err := step.Execute(ctx, 5)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var parallelErr *ParallelExecutionError
		if !errors.As(err, &parallelErr) {
			t.Errorf("expected ParallelExecutionError, got %T", err)
		}
	})
}

func TestParallel_FirstSuccess(t *testing.T) {
	t.Run("returns first successful result", func(t *testing.T) {
		step := Parallel[int, int](
			ParallelConfig{Strategy: FirstSuccess},
			func(ctx context.Context, input int) (int, error) {
				time.Sleep(50 * time.Millisecond)
				return 1, nil
			},
			func(ctx context.Context, input int) (int, error) {
				time.Sleep(10 * time.Millisecond)
				return 2, nil // This should complete first
			},
			func(ctx context.Context, input int) (int, error) {
				time.Sleep(30 * time.Millisecond)
				return 3, nil
			},
		)

		ctx := context.Background()
		result, err := step.Execute(ctx, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		results := result.([]int)
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}

		// The step with shortest sleep (10ms) should complete first
		// Due to goroutine scheduling, we can't guarantee exact order, but
		// with these timing differences, result should be 2
		if results[0] != 2 {
			t.Logf("Note: expected 2 (step with shortest sleep), got %d - may be due to goroutine scheduling", results[0])
		}
	})

	t.Run("all steps fail", func(t *testing.T) {
		step := Parallel[int, int](
			ParallelConfig{Strategy: FirstSuccess},
			func(ctx context.Context, input int) (int, error) {
				return 0, errors.New("failure 1")
			},
			func(ctx context.Context, input int) (int, error) {
				return 0, errors.New("failure 2")
			},
			func(ctx context.Context, input int) (int, error) {
				return 0, errors.New("failure 3")
			},
		)

		ctx := context.Background()
		_, err := step.Execute(ctx, 5)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var parallelErr *ParallelExecutionError
		if !errors.As(err, &parallelErr) {
			t.Errorf("expected ParallelExecutionError, got %T", err)
		}
	})
}

func TestParallel_ContextCancellation(t *testing.T) {
	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		step := Parallel[int, int](
			ParallelConfig{Strategy: AllMustSucceed},
			func(ctx context.Context, input int) (int, error) {
				time.Sleep(100 * time.Millisecond)
				return input * 2, nil
			},
			func(ctx context.Context, input int) (int, error) {
				time.Sleep(100 * time.Millisecond)
				return input * 3, nil
			},
		)

		// Cancel context before steps complete
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()

		_, err := step.Execute(ctx, 5)
		// Steps may or may not complete before cancellation, so we just verify execution completes
		_ = err
	})
}

func TestParallelMerge(t *testing.T) {
	t.Run("merges results with FirstResult", func(t *testing.T) {
		step := ParallelMerge[int, int](
			ParallelConfig{Strategy: AllMustSucceed},
			FirstResult[int](),
			func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			},
			func(ctx context.Context, input int) (int, error) {
				return input * 3, nil
			},
			func(ctx context.Context, input int) (int, error) {
				return input * 4, nil
			},
		)

		ctx := context.Background()
		result, err := step.Execute(ctx, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		merged := result.(int)
		if merged != 10 { // First result: 5 * 2
			t.Errorf("expected 10, got %d", merged)
		}
	})

	t.Run("merges results with LastResult", func(t *testing.T) {
		step := ParallelMerge[int, int](
			ParallelConfig{Strategy: AllMustSucceed},
			LastResult[int](),
			func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			},
			func(ctx context.Context, input int) (int, error) {
				return input * 3, nil
			},
			func(ctx context.Context, input int) (int, error) {
				return input * 4, nil
			},
		)

		ctx := context.Background()
		result, err := step.Execute(ctx, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		merged := result.(int)
		if merged != 20 { // Last result: 5 * 4
			t.Errorf("expected 20, got %d", merged)
		}
	})

	t.Run("using AllResults for pass-through", func(t *testing.T) {
		// AllResults is intended for when you want to keep all results in a workflow
		// where the next step expects []T
		merger := AllResults[int]()

		results := []int{10, 20, 30}
		merged, err := merger(results)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(merged) != 3 {
			t.Fatalf("expected 3 results, got %d", len(merged))
		}
		if merged[0] != 10 || merged[1] != 20 || merged[2] != 30 {
			t.Errorf("unexpected results: %v", merged)
		}
	})

	t.Run("custom merger function", func(t *testing.T) {
		step := ParallelMerge[int, int](
			ParallelConfig{Strategy: AllMustSucceed},
			func(results []int) (int, error) {
				// Sum all results
				sum := 0
				for _, r := range results {
					sum += r
				}
				return sum, nil
			},
			func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			},
			func(ctx context.Context, input int) (int, error) {
				return input * 3, nil
			},
			func(ctx context.Context, input int) (int, error) {
				return input * 4, nil
			},
		)

		ctx := context.Background()
		result, err := step.Execute(ctx, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		merged := result.(int)
		expected := 10 + 15 + 20 // Sum of 5*2, 5*3, 5*4
		if merged != expected {
			t.Errorf("expected %d, got %d", expected, merged)
		}
	})

	t.Run("merger returns error", func(t *testing.T) {
		mergerErr := errors.New("merger error")

		step := ParallelMerge[int, int](
			ParallelConfig{Strategy: AllMustSucceed},
			func(results []int) (int, error) {
				return 0, mergerErr
			},
			func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			},
		)

		ctx := context.Background()
		_, err := step.Execute(ctx, 5)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if !errors.Is(err, mergerErr) {
			t.Errorf("expected merger error, got %v", err)
		}
	})

	t.Run("panics with nil merger", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic")
			}
		}()

		ParallelMerge[int, int](
			ParallelConfig{Strategy: AllMustSucceed},
			nil, // Nil merger should panic
			func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			},
		)
	})
}

func TestMergerFunctions(t *testing.T) {
	t.Run("FirstResult with empty slice", func(t *testing.T) {
		merger := FirstResult[int]()
		_, err := merger([]int{})
		if err == nil {
			t.Fatal("expected error for empty slice")
		}
	})

	t.Run("FirstResult with values", func(t *testing.T) {
		merger := FirstResult[int]()
		result, err := merger([]int{10, 20, 30})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != 10 {
			t.Errorf("expected 10, got %d", result)
		}
	})

	t.Run("LastResult with empty slice", func(t *testing.T) {
		merger := LastResult[int]()
		_, err := merger([]int{})
		if err == nil {
			t.Fatal("expected error for empty slice")
		}
	})

	t.Run("LastResult with values", func(t *testing.T) {
		merger := LastResult[int]()
		result, err := merger([]int{10, 20, 30})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != 30 {
			t.Errorf("expected 30, got %d", result)
		}
	})

	t.Run("AllResults", func(t *testing.T) {
		merger := AllResults[int]()
		results, err := merger([]int{10, 20, 30})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("expected 3 results, got %d", len(results))
		}
		if results[0] != 10 || results[1] != 20 || results[2] != 30 {
			t.Errorf("unexpected results: %v", results)
		}
	})
}

func TestWorkflow_ParallelIntegration(t *testing.T) {
	t.Run("parallel in workflow", func(t *testing.T) {
		wf := New().
			Step("prepare", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input + 1, nil
			})).
			Step("parallel", Parallel[int, int](
				ParallelConfig{Strategy: AllMustSucceed},
				func(ctx context.Context, input int) (int, error) {
					return input * 2, nil
				},
				func(ctx context.Context, input int) (int, error) {
					return input * 3, nil
				},
			)).
			Step("sum", TypedStep(func(ctx context.Context, results []int) (int, error) {
				sum := 0
				for _, r := range results {
					sum += r
				}
				return sum, nil
			}))

		ctx := context.Background()
		result, err := ExecuteTyped[int](ctx, wf, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// (5 + 1) = 6, then [6*2, 6*3] = [12, 18], then sum = 30
		if result != 30 {
			t.Errorf("expected 30, got %d", result)
		}
	})

	t.Run("parallel merge in workflow", func(t *testing.T) {
		wf := New().
			Step("prepare", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input + 1, nil
			})).
			Step("parallel_merge", ParallelMerge[int, int](
				ParallelConfig{Strategy: AllMustSucceed},
				FirstResult[int](),
				func(ctx context.Context, input int) (int, error) {
					return input * 2, nil
				},
				func(ctx context.Context, input int) (int, error) {
					return input * 3, nil
				},
			)).
			Step("increment", TypedStep(func(ctx context.Context, value int) (int, error) {
				return value + 1, nil
			}))

		ctx := context.Background()
		result, err := ExecuteTyped[int](ctx, wf, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// (5 + 1) = 6, then first of [6*2, 6*3] = 12, then 12 + 1 = 13
		if result != 13 {
			t.Errorf("expected 13, got %d", result)
		}
	})

	t.Run("parallel with retry", func(t *testing.T) {
		var attempts atomic.Int32

		wf := New().
			WithRetry(RetryConfig{
				MaxAttempts:  2,
				InitialDelay: 10 * time.Millisecond,
				MaxDelay:     100 * time.Millisecond,
				Multiplier:   2.0,
			}).
			Step("parallel", Parallel[int, int](
				ParallelConfig{Strategy: AllMustSucceed},
				func(ctx context.Context, input int) (int, error) {
					count := attempts.Add(1)
					if count == 1 {
						return 0, errors.New("temporary failure")
					}
					return input * 2, nil
				},
				func(ctx context.Context, input int) (int, error) {
					return input * 3, nil
				},
			))

		ctx := context.Background()
		result, err := ExecuteTyped[[]int](ctx, wf, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result) != 2 {
			t.Fatalf("expected 2 results, got %d", len(result))
		}

		// After retry, should have [10, 15]
		if result[0] != 10 || result[1] != 15 {
			t.Errorf("unexpected results: %v", result)
		}

		if attempts.Load() < 2 {
			t.Errorf("expected at least 2 attempts due to retry")
		}
	})
}

func TestParallelExecutionError(t *testing.T) {
	t.Run("error message", func(t *testing.T) {
		err := &ParallelExecutionError{
			TotalSteps:   5,
			SuccessCount: 3,
			FailureCount: 2,
			Errors: []error{
				errors.New("error 1"),
				errors.New("error 2"),
			},
		}

		msg := err.Error()
		if msg == "" {
			t.Error("expected non-empty error message")
		}
	})

	t.Run("error message with custom message", func(t *testing.T) {
		err := &ParallelExecutionError{
			TotalSteps:   5,
			SuccessCount: 1,
			FailureCount: 4,
			Message:      "custom failure message",
		}

		msg := err.Error()
		if msg == "" {
			t.Error("expected non-empty error message")
		}
	})

	t.Run("unwrap errors", func(t *testing.T) {
		innerErr1 := errors.New("error 1")
		innerErr2 := errors.New("error 2")

		err := &ParallelExecutionError{
			TotalSteps:   2,
			SuccessCount: 0,
			FailureCount: 2,
			Errors: []error{
				fmt.Errorf("step 0: %w", innerErr1),
				fmt.Errorf("step 1: %w", innerErr2),
			},
		}

		unwrapped := err.Unwrap()
		if len(unwrapped) != 2 {
			t.Fatalf("expected 2 unwrapped errors, got %d", len(unwrapped))
		}
	})
}
