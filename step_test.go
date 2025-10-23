package workflow

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestTypedStep(t *testing.T) {
	t.Run("creates unnamed step", func(t *testing.T) {
		step := TypedStep(func(ctx context.Context, input string) (int, error) {
			return len(input), nil
		})

		if step.Name() != "" {
			t.Errorf("expected empty name, got %q", step.Name())
		}
	})

	t.Run("executes with correct types", func(t *testing.T) {
		step := TypedStep(func(ctx context.Context, input string) (int, error) {
			return len(input), nil
		})

		ctx := context.Background()
		result, err := step.Execute(ctx, "hello")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result != 5 {
			t.Errorf("expected 5, got %v", result)
		}
	})

	t.Run("returns error from function", func(t *testing.T) {
		expectedErr := errors.New("test error")
		step := TypedStep(func(ctx context.Context, input string) (int, error) {
			return 0, expectedErr
		})

		ctx := context.Background()
		_, err := step.Execute(ctx, "hello")
		if err != expectedErr {
			t.Errorf("expected %v, got %v", expectedErr, err)
		}
	})

	t.Run("fails with wrong input type", func(t *testing.T) {
		step := TypedStep(func(ctx context.Context, input string) (int, error) {
			return len(input), nil
		})

		ctx := context.Background()
		_, err := step.Execute(ctx, 123) // Wrong type
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var typeMismatchErr *TypeMismatchError
		if !errors.As(err, &typeMismatchErr) {
			t.Errorf("expected TypeMismatchError, got %T", err)
		}
	})

	t.Run("reports correct input type", func(t *testing.T) {
		step := TypedStep(func(ctx context.Context, input string) (int, error) {
			return len(input), nil
		})

		expectedType := reflect.TypeOf("")
		if step.InputType() != expectedType {
			t.Errorf("expected %v, got %v", expectedType, step.InputType())
		}
	})

	t.Run("reports correct output type", func(t *testing.T) {
		step := TypedStep(func(ctx context.Context, input string) (int, error) {
			return len(input), nil
		})

		expectedType := reflect.TypeOf(0)
		if step.OutputType() != expectedType {
			t.Errorf("expected %v, got %v", expectedType, step.OutputType())
		}
	})
}

func TestNamedTypedStep(t *testing.T) {
	t.Run("creates step with default name", func(t *testing.T) {
		step := NamedTypedStep("validate", func(ctx context.Context, input string) (int, error) {
			return len(input), nil
		})

		if step.Name() != "validate" {
			t.Errorf("expected 'validate', got %q", step.Name())
		}
	})

	t.Run("executes with correct types", func(t *testing.T) {
		step := NamedTypedStep("process", func(ctx context.Context, input string) (int, error) {
			return len(input) * 2, nil
		})

		ctx := context.Background()
		result, err := step.Execute(ctx, "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result != 8 {
			t.Errorf("expected 8, got %v", result)
		}
	})

	t.Run("reports correct types", func(t *testing.T) {
		step := NamedTypedStep("convert", func(ctx context.Context, input int) (string, error) {
			return "result", nil
		})

		expectedInputType := reflect.TypeOf(0)
		expectedOutputType := reflect.TypeOf("")

		if step.InputType() != expectedInputType {
			t.Errorf("input type: expected %v, got %v", expectedInputType, step.InputType())
		}

		if step.OutputType() != expectedOutputType {
			t.Errorf("output type: expected %v, got %v", expectedOutputType, step.OutputType())
		}
	})
}
