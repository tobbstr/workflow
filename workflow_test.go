package workflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
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

// Helper functions for type comparisons
func stringType() reflect.Type { return reflect.TypeOf("") }
func boolType() reflect.Type   { return reflect.TypeOf(false) }
