package workflow

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestCompose(t *testing.T) {
	t.Run("composes steps with compatible types", func(t *testing.T) {
		step := Compose[int, string](
			TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			}),
			TypedStep(func(ctx context.Context, input int) (int, error) {
				return input + 5, nil
			}),
			TypedStep(func(ctx context.Context, input int) (string, error) {
				return "result", nil
			}),
		)

		ctx := context.Background()
		result, err := step.Execute(ctx, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result != "result" {
			t.Errorf("expected 'result', got %v", result)
		}
	})

	t.Run("executes steps in sequence", func(t *testing.T) {
		step := Compose[int, int](
			TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			}),
			TypedStep(func(ctx context.Context, input int) (int, error) {
				return input + 10, nil
			}),
			TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 3, nil
			}),
		)

		ctx := context.Background()
		result, err := step.Execute(ctx, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// ((5 * 2) + 10) * 3 = 60
		if result != 60 {
			t.Errorf("expected 60, got %v", result)
		}
	})

	t.Run("propagates error from step", func(t *testing.T) {
		expectedErr := errors.New("step error")
		step := Compose[int, int](
			TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			}),
			TypedStep(func(ctx context.Context, input int) (int, error) {
				return 0, expectedErr
			}),
			TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 3, nil
			}),
		)

		ctx := context.Background()
		_, err := step.Execute(ctx, 5)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error to wrap %v", expectedErr)
		}
	})

	t.Run("panics with no steps", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic")
			}
		}()

		Compose[int, int]()
	})

	t.Run("panics with incompatible first step input", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic")
			}
		}()

		Compose[int, int](
			TypedStep(func(ctx context.Context, input string) (int, error) {
				// Wrong input type
				return 0, nil
			}),
		)
	})

	t.Run("panics with incompatible last step output", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic")
			}
		}()

		Compose[int, string](
			TypedStep(func(ctx context.Context, input int) (int, error) {
				// Wrong output type - should be string
				return input, nil
			}),
		)
	})

	t.Run("panics with incompatible sequential types", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic")
			}
		}()

		Compose[int, string](
			TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			}),
			TypedStep(func(ctx context.Context, input string) (string, error) {
				// Wrong input type - should be int
				return input, nil
			}),
		)
	})

	t.Run("single step compose", func(t *testing.T) {
		step := Compose[int, string](
			TypedStep(func(ctx context.Context, input int) (string, error) {
				return "single", nil
			}),
		)

		ctx := context.Background()
		result, err := step.Execute(ctx, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result != "single" {
			t.Errorf("expected 'single', got %v", result)
		}
	})
}

func TestWorkflow_AsStep(t *testing.T) {
	t.Run("converts workflow to step", func(t *testing.T) {
		subWorkflow := New().
			WithID(WorkflowID("sub")).
			Step("double", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			})).
			Step("increment", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input + 1, nil
			}))

		mainWorkflow := New().
			Step("prepare", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input + 5, nil
			})).
			Step(Auto, subWorkflow.AsStep()).
			Step("finalize", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 10, nil
			}))

		ctx := context.Background()
		result, err := ExecuteTyped[int](ctx, mainWorkflow, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// ((3 + 5) * 2 + 1) * 10 = 170
		if result != 170 {
			t.Errorf("expected 170, got %d", result)
		}
	})

	t.Run("workflow ID becomes step name", func(t *testing.T) {
		subWorkflow := New().
			WithID(WorkflowID("my_sub_workflow")).
			Step("step1", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input, nil
			}))

		step := subWorkflow.AsStep()
		if step.Name() != "my_sub_workflow" {
			t.Errorf("expected 'my_sub_workflow', got %q", step.Name())
		}
	})

	t.Run("panics with empty workflow", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic")
			}
		}()

		New().AsStep()
	})

	t.Run("nested workflows multiple levels deep", func(t *testing.T) {
		// Level 3: innermost workflow
		level3 := New().
			WithID(WorkflowID("level3")).
			Step("multiply", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			}))

		// Level 2: middle workflow
		level2 := New().
			WithID(WorkflowID("level2")).
			Step("add", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input + 10, nil
			})).
			Step(Auto, level3.AsStep())

		// Level 1: outer workflow
		level1 := New().
			WithID(WorkflowID("level1")).
			Step("start", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input + 5, nil
			})).
			Step(Auto, level2.AsStep()).
			Step("finish", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input + 1, nil
			}))

		ctx := context.Background()
		result, err := ExecuteTyped[int](ctx, level1, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// ((10 + 5) + 10) * 2 + 1 = 51
		if result != 51 {
			t.Errorf("expected 51, got %d", result)
		}
	})
}

func TestWorkflow_CompositionIntegration(t *testing.T) {
	type Request struct {
		Value     int
		IsPremium bool
		IsExpress bool
	}

	type Intermediate struct {
		Value  int
		Status string
	}

	type Result struct {
		Value       int
		FinalStatus string
	}

	t.Run("complex tree-like workflow", func(t *testing.T) {
		// Sub-workflow for premium express processing
		premiumExpress := New().
			WithID(WorkflowID("premium_express")).
			Step("boost", TypedStep(func(ctx context.Context, r Request) (Intermediate, error) {
				return Intermediate{Value: r.Value * 5, Status: "express"}, nil
			})).
			Step("finalize", TypedStep(func(ctx context.Context, i Intermediate) (Result, error) {
				return Result{Value: i.Value + 100, FinalStatus: "premium-express"}, nil
			}))

		// Sub-workflow for premium standard processing
		premiumStandard := New().
			WithID(WorkflowID("premium_standard")).
			Step("boost", TypedStep(func(ctx context.Context, r Request) (Intermediate, error) {
				return Intermediate{Value: r.Value * 3, Status: "standard"}, nil
			})).
			Step("finalize", TypedStep(func(ctx context.Context, i Intermediate) (Result, error) {
				return Result{Value: i.Value + 50, FinalStatus: "premium-standard"}, nil
			}))

		// Premium workflow with conditional sub-workflows
		premiumFlow := New().
			WithID(WorkflowID("premium_flow")).
			Step("route", If[Request, Result](
				func(r Request) bool { return r.IsExpress },
				premiumExpress.AsStep(),
				premiumStandard.AsStep(),
			))

		// Standard workflow with simple processing
		standardFlow := Compose[Request, Result](
			TypedStep(func(ctx context.Context, r Request) (Intermediate, error) {
				return Intermediate{Value: r.Value, Status: "standard"}, nil
			}),
			TypedStep(func(ctx context.Context, i Intermediate) (Result, error) {
				return Result{Value: i.Value + 10, FinalStatus: "standard"}, nil
			}),
		)

		// Main workflow
		mainWorkflow := New().
			WithID(WorkflowID("main")).
			Step("validate", TypedStep(func(ctx context.Context, r Request) (Request, error) {
				if r.Value < 0 {
					return r, errors.New("invalid value")
				}
				return r, nil
			})).
			Step("process", If[Request, Result](
				func(r Request) bool { return r.IsPremium },
				premiumFlow.AsStep(),
				standardFlow,
			))

		tests := []struct {
			name     string
			input    Request
			expected Result
		}{
			{
				name:     "premium express",
				input:    Request{Value: 10, IsPremium: true, IsExpress: true},
				expected: Result{Value: 150, FinalStatus: "premium-express"}, // (10 * 5) + 100
			},
			{
				name:     "premium standard",
				input:    Request{Value: 10, IsPremium: true, IsExpress: false},
				expected: Result{Value: 80, FinalStatus: "premium-standard"}, // (10 * 3) + 50
			},
			{
				name:     "standard",
				input:    Request{Value: 10, IsPremium: false, IsExpress: false},
				expected: Result{Value: 20, FinalStatus: "standard"}, // 10 + 10
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				ctx := context.Background()
				result, err := ExecuteTyped[Result](ctx, mainWorkflow, tt.input)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if result.Value != tt.expected.Value || result.FinalStatus != tt.expected.FinalStatus {
					t.Errorf("expected %+v, got %+v", tt.expected, result)
				}
			})
		}
	})

	t.Run("compose with parallel and conditional", func(t *testing.T) {
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
			Step("select", If[[]int, int](
				func(results []int) bool { return len(results) > 1 },
				TypedStep(func(ctx context.Context, results []int) (int, error) {
					return results[0] + results[1], nil
				}),
				TypedStep(func(ctx context.Context, results []int) (int, error) {
					return 0, nil
				}),
			))

		ctx := context.Background()
		result, err := ExecuteTyped[int](ctx, wf, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// (5 + 1) = 6, then [6*2, 6*3] = [12, 18], then 12 + 18 = 30
		if result != 30 {
			t.Errorf("expected 30, got %d", result)
		}
	})
}

func TestCompositeStep_InputOutputTypes(t *testing.T) {
	t.Run("reports correct input and output types", func(t *testing.T) {
		step := Compose[int, string](
			TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			}),
			TypedStep(func(ctx context.Context, input int) (string, error) {
				return "result", nil
			}),
		)

		expectedInputType := reflect.TypeOf(0)
		expectedOutputType := reflect.TypeOf("")

		if step.InputType() != expectedInputType {
			t.Errorf("expected %v input type, got %v", expectedInputType, step.InputType())
		}

		if step.OutputType() != expectedOutputType {
			t.Errorf("expected %v output type, got %v", expectedOutputType, step.OutputType())
		}
	})
}

func TestWorkflowAsStep_InputOutputTypes(t *testing.T) {
	t.Run("reports correct input and output types", func(t *testing.T) {
		wf := New().
			Step("double", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			})).
			Step("format", TypedStep(func(ctx context.Context, input int) (string, error) {
				return "result", nil
			}))

		step := wf.AsStep()

		expectedInputType := reflect.TypeOf(0)
		expectedOutputType := reflect.TypeOf("")

		if step.InputType() != expectedInputType {
			t.Errorf("expected %v input type, got %v", expectedInputType, step.InputType())
		}

		if step.OutputType() != expectedOutputType {
			t.Errorf("expected %v output type, got %v", expectedOutputType, step.OutputType())
		}
	})
}
