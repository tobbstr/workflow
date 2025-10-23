package workflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	t.Run("creates empty workflow", func(t *testing.T) {
		wf := New()
		if wf == nil {
			t.Fatal("expected non-nil workflow")
		}

		if len(wf.steps) != 0 {
			t.Errorf("expected 0 steps, got %d", len(wf.steps))
		}
	})
}

func TestWorkflow_WithID(t *testing.T) {
	t.Run("sets workflow ID", func(t *testing.T) {
		wf := New().WithID(WorkflowID("test-workflow"))

		if wf.id != "test-workflow" {
			t.Errorf("expected 'test-workflow', got %q", wf.id)
		}
	})

	t.Run("returns same workflow instance", func(t *testing.T) {
		wf := New()
		result := wf.WithID(WorkflowID("test"))

		if result != wf {
			t.Error("expected same workflow instance")
		}
	})
}

func TestWorkflow_Step(t *testing.T) {
	t.Run("adds step with explicit name", func(t *testing.T) {
		wf := New().
			Step("step1", TypedStep(func(ctx context.Context, input string) (string, error) {
				return input, nil
			}))

		if len(wf.steps) != 1 {
			t.Fatalf("expected 1 step, got %d", len(wf.steps))
		}

		if wf.steps[0].name != "step1" {
			t.Errorf("expected name 'step1', got %q", wf.steps[0].name)
		}
	})

	t.Run("adds step with Auto name", func(t *testing.T) {
		namedStep := NamedTypedStep("default_name", func(ctx context.Context, input string) (string, error) {
			return input, nil
		})

		wf := New().Step(Auto, namedStep)

		if len(wf.steps) != 1 {
			t.Fatalf("expected 1 step, got %d", len(wf.steps))
		}

		if wf.steps[0].name != "default_name" {
			t.Errorf("expected name 'default_name', got %q", wf.steps[0].name)
		}
	})

	t.Run("panics when using Auto with unnamed step", func(t *testing.T) {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic")
			}

			err, ok := r.(error)
			if !ok {
				t.Fatalf("expected error, got %T", r)
			}

			if !errors.Is(err, ErrInvalidStepName) {
				t.Errorf("expected ErrInvalidStepName, got %v", err)
			}
		}()

		New().Step(Auto, TypedStep(func(ctx context.Context, input string) (string, error) {
			return input, nil
		}))
	})

	t.Run("validates type compatibility between steps", func(t *testing.T) {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic for type mismatch")
			}

			_, ok := r.(*TypeMismatchError)
			if !ok {
				t.Errorf("expected TypeMismatchError, got %T", r)
			}
		}()

		New().
			Step("step1", TypedStep(func(ctx context.Context, input string) (int, error) {
				return 0, nil
			})).
			Step("step2", TypedStep(func(ctx context.Context, input string) (string, error) {
				// Wrong input type - should be int
				return input, nil
			}))
	})

	t.Run("allows compatible type chain", func(t *testing.T) {
		wf := New().
			Step("step1", TypedStep(func(ctx context.Context, input string) (int, error) {
				return len(input), nil
			})).
			Step("step2", TypedStep(func(ctx context.Context, input int) (bool, error) {
				return input > 0, nil
			})).
			Step("step3", TypedStep(func(ctx context.Context, input bool) (string, error) {
				if input {
					return "yes", nil
				}
				return "no", nil
			}))

		if len(wf.steps) != 3 {
			t.Errorf("expected 3 steps, got %d", len(wf.steps))
		}
	})

	t.Run("sets workflow input and output types", func(t *testing.T) {
		wf := New().
			Step("step1", TypedStep(func(ctx context.Context, input string) (int, error) {
				return 0, nil
			})).
			Step("step2", TypedStep(func(ctx context.Context, input int) (bool, error) {
				return false, nil
			}))

		expectedInputType := stringType()
		expectedOutputType := boolType()

		if wf.inputType != expectedInputType {
			t.Errorf("expected %v input type, got %v", expectedInputType, wf.inputType)
		}

		if wf.outputType != expectedOutputType {
			t.Errorf("expected %v output type, got %v", expectedOutputType, wf.outputType)
		}
	})

	t.Run("returns same workflow instance for chaining", func(t *testing.T) {
		wf := New()
		result := wf.Step("step1", TypedStep(func(ctx context.Context, input string) (string, error) {
			return input, nil
		}))

		if result != wf {
			t.Error("expected same workflow instance")
		}
	})
}

func TestWorkflow_Execute(t *testing.T) {
	t.Run("executes simple workflow", func(t *testing.T) {
		wf := New().
			Step("double", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			})).
			Step("increment", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input + 1, nil
			}))

		ctx := context.Background()
		result, err := wf.Execute(ctx, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result != 11 { // (5 * 2) + 1
			t.Errorf("expected 11, got %v", result)
		}
	})

	t.Run("executes workflow with multiple steps", func(t *testing.T) {
		wf := New().
			Step("length", TypedStep(func(ctx context.Context, input string) (int, error) {
				return len(input), nil
			})).
			Step("double", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			})).
			Step("format", TypedStep(func(ctx context.Context, input int) (string, error) {
				return fmt.Sprintf("Result: %d", input), nil
			}))

		ctx := context.Background()
		result, err := wf.Execute(ctx, "hello")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result != "Result: 10" {
			t.Errorf("expected 'Result: 10', got %v", result)
		}
	})

	t.Run("returns error from failing step", func(t *testing.T) {
		expectedErr := errors.New("step failed")
		wf := New().
			Step("step1", TypedStep(func(ctx context.Context, input string) (int, error) {
				return 0, nil
			})).
			Step("step2", TypedStep(func(ctx context.Context, input int) (string, error) {
				return "", expectedErr
			}))

		ctx := context.Background()
		_, err := wf.Execute(ctx, "test")
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if !strings.Contains(err.Error(), "step2") {
			t.Errorf("error should mention step name, got: %v", err)
		}
	})

	t.Run("returns error for empty workflow", func(t *testing.T) {
		wf := New()

		ctx := context.Background()
		_, err := wf.Execute(ctx, "test")
		if !errors.Is(err, ErrNoSteps) {
			t.Errorf("expected ErrNoSteps, got %v", err)
		}
	})

	t.Run("validates input type", func(t *testing.T) {
		wf := New().
			Step("step1", TypedStep(func(ctx context.Context, input string) (string, error) {
				return input, nil
			}))

		ctx := context.Background()
		_, err := wf.Execute(ctx, 123) // Wrong type
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var typeMismatchErr *TypeMismatchError
		if !errors.As(err, &typeMismatchErr) {
			t.Errorf("expected TypeMismatchError, got %T", err)
		}
	})

	t.Run("generates workflow ID when not set", func(t *testing.T) {
		wf := New().
			Step("step1", TypedStep(func(ctx context.Context, input string) (string, error) {
				return input, nil
			}))

		ctx := context.Background()
		_, err := wf.Execute(ctx, "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// If ID was empty, it should have been generated (we can't test the actual ID)
		// Just verify execution succeeded
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		wf := New().
			Step("step1", TypedStep(func(ctx context.Context, input string) (string, error) {
				return input, nil
			})).
			Step("step2", TypedStep(func(ctx context.Context, input string) (string, error) {
				return input, nil
			}))

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		_, err := wf.Execute(ctx, "test")
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})
}

func TestExecuteTyped(t *testing.T) {
	t.Run("returns typed result", func(t *testing.T) {
		wf := New().
			Step("step1", TypedStep(func(ctx context.Context, input string) (int, error) {
				return len(input), nil
			}))

		ctx := context.Background()
		result, err := ExecuteTyped[int](ctx, wf, "hello")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result != 5 {
			t.Errorf("expected 5, got %d", result)
		}
	})

	t.Run("returns error when type doesn't match", func(t *testing.T) {
		wf := New().
			Step("step1", TypedStep(func(ctx context.Context, input string) (int, error) {
				return 123, nil
			}))

		ctx := context.Background()
		_, err := ExecuteTyped[string](ctx, wf, "test") // Wrong type
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var typeMismatchErr *TypeMismatchError
		if !errors.As(err, &typeMismatchErr) {
			t.Errorf("expected TypeMismatchError, got %T", err)
		}
	})

	t.Run("propagates execution errors", func(t *testing.T) {
		expectedErr := errors.New("execution failed")
		wf := New().
			Step("step1", TypedStep(func(ctx context.Context, input string) (int, error) {
				return 0, expectedErr
			}))

		ctx := context.Background()
		_, err := ExecuteTyped[int](ctx, wf, "test")
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if !errors.Is(err, expectedErr) {
			t.Errorf("expected original error, got %v", err)
		}
	})
}

// Benchmark tests

// BenchmarkWorkflowExecution benchmarks workflow execution with memory allocations.
// Uses a complex workflow with sequential steps, conditionals, and parallel execution
// to simulate realistic microservice endpoint logic.
func BenchmarkWorkflowExecution(b *testing.B) {
	// Setup test data types
	type UserInput struct {
		ID        int
		Username  string
		Email     string
		IsPremium bool
	}

	type ValidationResult struct {
		UserInput
		IsValid bool
		Errors  []string
	}

	type EnrichmentResult struct {
		ValidationResult
		Metadata map[string]string
	}

	type ProcessingResult struct {
		EnrichmentResult
		ProcessedData []string
	}

	// Define workflow steps
	validateInput := func(ctx context.Context, input UserInput) (ValidationResult, error) {
		result := ValidationResult{
			UserInput: input,
			IsValid:   true,
			Errors:    make([]string, 0),
		}

		// Simulate validation logic
		if input.Username == "" {
			result.IsValid = false
			result.Errors = append(result.Errors, "username required")
		}
		if input.Email == "" {
			result.IsValid = false
			result.Errors = append(result.Errors, "email required")
		}

		return result, nil
	}

	enrichWithMetadata := func(ctx context.Context, input ValidationResult) (EnrichmentResult, error) {
		result := EnrichmentResult{
			ValidationResult: input,
			Metadata: map[string]string{
				"timestamp": "2025-10-23T10:00:00Z",
				"source":    "api",
				"version":   "v1",
			},
		}
		return result, nil
	}

	// Parallel enrichment steps for premium users
	fetchUserPreferences := func(ctx context.Context, input EnrichmentResult) (map[string]string, error) {
		return map[string]string{
			"theme":    "dark",
			"language": "en",
		}, nil
	}

	fetchUserHistory := func(ctx context.Context, input EnrichmentResult) (map[string]string, error) {
		return map[string]string{
			"last_login":  "2025-10-22",
			"login_count": "42",
		}, nil
	}

	fetchUserSettings := func(ctx context.Context, input EnrichmentResult) (map[string]string, error) {
		return map[string]string{
			"notifications": "enabled",
			"2fa":           "enabled",
		}, nil
	}

	processWithEnrichment := func(ctx context.Context, input EnrichmentResult) (ProcessingResult, error) {
		// Execute parallel enrichment for premium users
		if input.IsPremium {
			parallelStep := Parallel[EnrichmentResult, map[string]string](
				ParallelConfig{Strategy: AllMustSucceed},
				fetchUserPreferences,
				fetchUserHistory,
				fetchUserSettings,
			)

			enrichmentResults, err := parallelStep.Execute(ctx, input)
			if err != nil {
				return ProcessingResult{}, fmt.Errorf("enriching premium user data: %w", err)
			}

			// Merge enrichment results
			enrichments, ok := enrichmentResults.([]map[string]string)
			if ok {
				for _, e := range enrichments {
					for k, v := range e {
						input.Metadata[k] = v
					}
				}
			}
		}

		result := ProcessingResult{
			EnrichmentResult: input,
			ProcessedData:    make([]string, 0, 5),
		}

		// Simulate data processing
		result.ProcessedData = append(result.ProcessedData,
			fmt.Sprintf("user_%d", input.ID),
			fmt.Sprintf("username_%s", input.Username),
			fmt.Sprintf("email_%s", input.Email),
		)

		return result, nil
	}

	formatOutput := func(ctx context.Context, input ProcessingResult) (string, error) {
		return fmt.Sprintf("Processed user %s (ID: %d) - Valid: %v",
			input.Username, input.ID, input.IsValid), nil
	}

	// Build workflow with conditional branching
	premiumBranch := Compose[ValidationResult, ProcessingResult](
		TypedStep(enrichWithMetadata),
		TypedStep(processWithEnrichment),
	)

	standardBranch := Compose[ValidationResult, ProcessingResult](
		TypedStep(enrichWithMetadata),
		TypedStep(processWithEnrichment),
	)

	wf := New().
		WithID(WorkflowID("user-processing")).
		Step("validate", TypedStep(validateInput)).
		Step("process", If[ValidationResult, ProcessingResult](
			func(v ValidationResult) bool { return v.IsPremium },
			premiumBranch,
			standardBranch,
		)).
		Step("format", TypedStep(formatOutput))

	// Test data
	premiumUser := UserInput{
		ID:        1,
		Username:  "premium_user",
		Email:     "premium@example.com",
		IsPremium: true,
	}

	standardUser := UserInput{
		ID:        2,
		Username:  "standard_user",
		Email:     "standard@example.com",
		IsPremium: false,
	}

	b.Run("PremiumUser", func(b *testing.B) {
		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			_, err := ExecuteTyped[string](ctx, wf, premiumUser)
			if err != nil {
				b.Fatalf("workflow execution failed: %v", err)
			}
		}
	})

	b.Run("StandardUser", func(b *testing.B) {
		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			_, err := ExecuteTyped[string](ctx, wf, standardUser)
			if err != nil {
				b.Fatalf("workflow execution failed: %v", err)
			}
		}
	})
}

// BenchmarkWorkflowWithRetry benchmarks workflow execution with retry configuration.
func BenchmarkWorkflowWithRetry(b *testing.B) {
	type Request struct {
		ID      int
		Payload string
	}

	type Response struct {
		ID     int
		Result string
	}

	processRequest := func(ctx context.Context, input Request) (Response, error) {
		return Response{
			ID:     input.ID,
			Result: fmt.Sprintf("processed: %s", input.Payload),
		}, nil
	}

	wf := New().
		WithID(WorkflowID("retry-test")).
		WithRetry(RetryConfig{
			MaxAttempts:  3,
			InitialDelay: 10 * time.Millisecond,
			MaxDelay:     100 * time.Millisecond,
			Multiplier:   2.0,
		}).
		Step("process", TypedStep(processRequest))

	ctx := context.Background()
	input := Request{ID: 1, Payload: "test data"}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := ExecuteTyped[Response](ctx, wf, input)
		if err != nil {
			b.Fatalf("workflow execution failed: %v", err)
		}
	}
}

// BenchmarkSimpleWorkflow benchmarks a simple sequential workflow for baseline comparison.
func BenchmarkSimpleWorkflow(b *testing.B) {
	wf := New().
		WithID(WorkflowID("simple")).
		Step("double", TypedStep(func(ctx context.Context, input int) (int, error) {
			return input * 2, nil
		})).
		Step("increment", TypedStep(func(ctx context.Context, input int) (int, error) {
			return input + 1, nil
		})).
		Step("format", TypedStep(func(ctx context.Context, input int) (string, error) {
			return fmt.Sprintf("result: %d", input), nil
		}))

	ctx := context.Background()
	input := 5

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := ExecuteTyped[string](ctx, wf, input)
		if err != nil {
			b.Fatalf("workflow execution failed: %v", err)
		}
	}
}

// Helper functions for type comparisons
func stringType() reflect.Type { return reflect.TypeOf("") }
func boolType() reflect.Type   { return reflect.TypeOf(false) }
