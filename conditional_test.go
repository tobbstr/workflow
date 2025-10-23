package workflow

import (
	"context"
	"errors"
	"testing"
)

func TestIf(t *testing.T) {
	t.Run("executes then branch when condition is true", func(t *testing.T) {
		step := If[int, string](
			func(input int) bool { return input > 5 },
			TypedStep(func(ctx context.Context, input int) (string, error) {
				return "then", nil
			}),
			TypedStep(func(ctx context.Context, input int) (string, error) {
				return "else", nil
			}),
		)

		ctx := context.Background()
		result, err := step.Execute(ctx, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result != "then" {
			t.Errorf("expected 'then', got %v", result)
		}
	})

	t.Run("executes else branch when condition is false", func(t *testing.T) {
		step := If[int, string](
			func(input int) bool { return input > 5 },
			TypedStep(func(ctx context.Context, input int) (string, error) {
				return "then", nil
			}),
			TypedStep(func(ctx context.Context, input int) (string, error) {
				return "else", nil
			}),
		)

		ctx := context.Background()
		result, err := step.Execute(ctx, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result != "else" {
			t.Errorf("expected 'else', got %v", result)
		}
	})

	t.Run("propagates error from then branch", func(t *testing.T) {
		expectedErr := errors.New("then error")
		step := If[int, string](
			func(input int) bool { return true },
			TypedStep(func(ctx context.Context, input int) (string, error) {
				return "", expectedErr
			}),
			TypedStep(func(ctx context.Context, input int) (string, error) {
				return "else", nil
			}),
		)

		ctx := context.Background()
		_, err := step.Execute(ctx, 10)
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("propagates error from else branch", func(t *testing.T) {
		expectedErr := errors.New("else error")
		step := If[int, string](
			func(input int) bool { return false },
			TypedStep(func(ctx context.Context, input int) (string, error) {
				return "then", nil
			}),
			TypedStep(func(ctx context.Context, input int) (string, error) {
				return "", expectedErr
			}),
		)

		ctx := context.Background()
		_, err := step.Execute(ctx, 10)
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("panics with mismatched then input type", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic")
			}
		}()

		If[int, string](
			func(input int) bool { return true },
			TypedStep(func(ctx context.Context, input string) (string, error) {
				// Wrong input type
				return input, nil
			}),
			TypedStep(func(ctx context.Context, input int) (string, error) {
				return "else", nil
			}),
		)
	})

	t.Run("panics with mismatched else input type", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic")
			}
		}()

		If[int, string](
			func(input int) bool { return true },
			TypedStep(func(ctx context.Context, input int) (string, error) {
				return "then", nil
			}),
			TypedStep(func(ctx context.Context, input string) (string, error) {
				// Wrong input type
				return input, nil
			}),
		)
	})

	t.Run("panics with mismatched then output type", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic")
			}
		}()

		If[int, string](
			func(input int) bool { return true },
			TypedStep(func(ctx context.Context, input int) (int, error) {
				// Wrong output type
				return input, nil
			}),
			TypedStep(func(ctx context.Context, input int) (string, error) {
				return "else", nil
			}),
		)
	})

	t.Run("panics with mismatched else output type", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic")
			}
		}()

		If[int, string](
			func(input int) bool { return true },
			TypedStep(func(ctx context.Context, input int) (string, error) {
				return "then", nil
			}),
			TypedStep(func(ctx context.Context, input int) (int, error) {
				// Wrong output type
				return input, nil
			}),
		)
	})
}

func TestWorkflow_ConditionalIntegration(t *testing.T) {
	type Request struct {
		Value     int
		IsPremium bool
	}

	type Response struct {
		Result string
		Value  int
	}

	t.Run("conditional in workflow with then branch", func(t *testing.T) {
		wf := New().
			Step("conditional", If[Request, Response](
				func(r Request) bool { return r.IsPremium },
				TypedStep(func(ctx context.Context, r Request) (Response, error) {
					return Response{Result: "premium", Value: r.Value * 2}, nil
				}),
				TypedStep(func(ctx context.Context, r Request) (Response, error) {
					return Response{Result: "standard", Value: r.Value}, nil
				}),
			))

		ctx := context.Background()
		result, err := ExecuteTyped[Response](ctx, wf, Request{Value: 10, IsPremium: true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Result != "premium" || result.Value != 20 {
			t.Errorf("expected {premium, 20}, got {%s, %d}", result.Result, result.Value)
		}
	})

	t.Run("conditional in workflow with else branch", func(t *testing.T) {
		wf := New().
			Step("conditional", If[Request, Response](
				func(r Request) bool { return r.IsPremium },
				TypedStep(func(ctx context.Context, r Request) (Response, error) {
					return Response{Result: "premium", Value: r.Value * 2}, nil
				}),
				TypedStep(func(ctx context.Context, r Request) (Response, error) {
					return Response{Result: "standard", Value: r.Value}, nil
				}),
			))

		ctx := context.Background()
		result, err := ExecuteTyped[Response](ctx, wf, Request{Value: 10, IsPremium: false})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Result != "standard" || result.Value != 10 {
			t.Errorf("expected {standard, 10}, got {%s, %d}", result.Result, result.Value)
		}
	})

	t.Run("conditional with compose in branches", func(t *testing.T) {
		wf := New().
			Step("process", If[Request, Response](
				func(r Request) bool { return r.IsPremium },
				// Premium path with multiple steps
				Compose[Request, Response](
					TypedStep(func(ctx context.Context, r Request) (Request, error) {
						r.Value *= 2
						return r, nil
					}),
					TypedStep(func(ctx context.Context, r Request) (Response, error) {
						return Response{Result: "premium", Value: r.Value + 10}, nil
					}),
				),
				// Standard path with single step
				TypedStep(func(ctx context.Context, r Request) (Response, error) {
					return Response{Result: "standard", Value: r.Value}, nil
				}),
			))

		ctx := context.Background()
		result, err := ExecuteTyped[Response](ctx, wf, Request{Value: 5, IsPremium: true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// (5 * 2) + 10 = 20
		if result.Result != "premium" || result.Value != 20 {
			t.Errorf("expected {premium, 20}, got {%s, %d}", result.Result, result.Value)
		}
	})

	t.Run("nested conditionals", func(t *testing.T) {
		wf := New().
			Step("outer", If[Request, Response](
				func(r Request) bool { return r.Value > 10 },
				// High value branch
				If[Request, Response](
					func(r Request) bool { return r.IsPremium },
					TypedStep(func(ctx context.Context, r Request) (Response, error) {
						return Response{Result: "high-premium", Value: r.Value * 3}, nil
					}),
					TypedStep(func(ctx context.Context, r Request) (Response, error) {
						return Response{Result: "high-standard", Value: r.Value * 2}, nil
					}),
				),
				// Low value branch
				TypedStep(func(ctx context.Context, r Request) (Response, error) {
					return Response{Result: "low", Value: r.Value}, nil
				}),
			))

		tests := []struct {
			name     string
			input    Request
			expected Response
		}{
			{
				name:     "high value premium",
				input:    Request{Value: 15, IsPremium: true},
				expected: Response{Result: "high-premium", Value: 45},
			},
			{
				name:     "high value standard",
				input:    Request{Value: 15, IsPremium: false},
				expected: Response{Result: "high-standard", Value: 30},
			},
			{
				name:     "low value",
				input:    Request{Value: 5, IsPremium: true},
				expected: Response{Result: "low", Value: 5},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				ctx := context.Background()
				result, err := ExecuteTyped[Response](ctx, wf, tt.input)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if result.Result != tt.expected.Result || result.Value != tt.expected.Value {
					t.Errorf("expected %+v, got %+v", tt.expected, result)
				}
			})
		}
	})
}
