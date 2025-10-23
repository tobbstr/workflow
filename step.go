package workflow

import (
	"context"
	"reflect"
)

// Step represents a single step in a workflow with typed inputs and outputs.
// Steps can be created using TypedStep or NamedTypedStep functions.
//
// Example:
//
//	step := workflow.TypedStep(func(ctx context.Context, input string) (int, error) {
//		return len(input), nil
//	})
type Step interface {
	// Name returns the step's default name, or "" if the step has no default name.
	Name() StepName

	// Execute runs the step with the given input and returns the output.
	Execute(ctx context.Context, input any) (any, error)

	// InputType returns the reflect.Type of the step's input.
	InputType() reflect.Type

	// OutputType returns the reflect.Type of the step's output.
	OutputType() reflect.Type
}

// typedStep is an implementation of Step that wraps a typed function.
type typedStep[In, Out any] struct {
	name StepName
	fn   func(context.Context, In) (Out, error)
}

// Name returns the step's default name.
func (s *typedStep[In, Out]) Name() StepName {
	return s.name
}

// Execute runs the step function with type-safe conversion.
func (s *typedStep[In, Out]) Execute(ctx context.Context, input any) (any, error) {
	typedInput, ok := input.(In)
	if !ok {
		var zero In
		return nil, &TypeMismatchError{
			Expected: reflect.TypeOf(zero),
			Got:      reflect.TypeOf(input),
			Context:  "step input",
		}
	}

	return s.fn(ctx, typedInput)
}

// InputType returns the reflect.Type of the input.
func (s *typedStep[In, Out]) InputType() reflect.Type {
	var zero In
	return reflect.TypeOf(zero)
}

// OutputType returns the reflect.Type of the output.
func (s *typedStep[In, Out]) OutputType() reflect.Type {
	var zero Out
	return reflect.TypeOf(zero)
}

// TypedStep creates an unnamed step from a typed function.
// The step's Name() method will return "".
//
// Example:
//
//	validateStep := workflow.TypedStep(func(ctx context.Context, input string) (string, error) {
//		if input == "" {
//			return "", errors.New("input required")
//		}
//		return input, nil
//	})
//
//	wf := workflow.New().
//		Step("validate", validateStep)
func TypedStep[In, Out any](fn func(context.Context, In) (Out, error)) Step {
	return &typedStep[In, Out]{
		name: "",
		fn:   fn,
	}
}

// NamedTypedStep creates a step with a default name from a typed function.
// The step's Name() method will return the provided name.
//
// Example:
//
//	authStep := workflow.NamedTypedStep("authenticate", func(ctx context.Context, token string) (User, error) {
//		// Authentication logic
//		return user, nil
//	})
//
//	// Use with Auto to inherit the default name
//	wf := workflow.New().
//		Step(workflow.Auto, authStep) // Uses "authenticate"
//
//	// Or override with a custom name
//	wf := workflow.New().
//		Step("custom_auth", authStep) // Uses "custom_auth"
func NamedTypedStep[In, Out any](name StepName, fn func(context.Context, In) (Out, error)) Step {
	return &typedStep[In, Out]{
		name: name,
		fn:   fn,
	}
}
