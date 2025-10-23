package workflow

import (
	"context"
	"reflect"
)

// conditionalStep executes either thenStep or elseStep based on a condition.
type conditionalStep[In, Out any] struct {
	condition func(In) bool
	thenStep  Step
	elseStep  Step
}

// Name returns an empty string (conditional steps don't have default names).
func (s *conditionalStep[In, Out]) Name() StepName {
	return ""
}

// Execute evaluates the condition and executes the appropriate branch.
func (s *conditionalStep[In, Out]) Execute(ctx context.Context, input any) (any, error) {
	typedInput, ok := input.(In)
	if !ok {
		var zero In
		return nil, &TypeMismatchError{
			Expected: reflect.TypeOf(zero),
			Got:      reflect.TypeOf(input),
			Context:  "conditional step input",
		}
	}

	// Evaluate condition
	if s.condition(typedInput) {
		return s.thenStep.Execute(ctx, input)
	}
	return s.elseStep.Execute(ctx, input)
}

// InputType returns the reflect.Type of the input.
func (s *conditionalStep[In, Out]) InputType() reflect.Type {
	var zero In
	return reflect.TypeOf(zero)
}

// OutputType returns the reflect.Type of the output.
func (s *conditionalStep[In, Out]) OutputType() reflect.Type {
	var zero Out
	return reflect.TypeOf(zero)
}

// If creates a step that executes thenStep or elseStep based on a condition.
// Both branches must have the same input and output types.
//
// Example:
//
//	wf := workflow.New().
//		Step("route", workflow.If[CheckRequest, ProcessingResult](
//			func(r CheckRequest) bool { return r.IsPremium },
//			workflow.TypedStep(processPremiumUser),
//			workflow.TypedStep(processStandardUser),
//		))
func If[In, Out any](
	condition func(In) bool,
	thenStep Step,
	elseStep Step,
) Step {
	// Validate input types
	var zeroIn In
	expectedInputType := reflect.TypeOf(zeroIn)

	if thenStep.InputType() != expectedInputType {
		panic(&TypeMismatchError{
			Expected: expectedInputType,
			Got:      thenStep.InputType(),
			Context:  "If thenStep input type",
		})
	}

	if elseStep.InputType() != expectedInputType {
		panic(&TypeMismatchError{
			Expected: expectedInputType,
			Got:      elseStep.InputType(),
			Context:  "If elseStep input type",
		})
	}

	// Validate output types
	var zeroOut Out
	expectedOutputType := reflect.TypeOf(zeroOut)

	if thenStep.OutputType() != expectedOutputType {
		panic(&TypeMismatchError{
			Expected: expectedOutputType,
			Got:      thenStep.OutputType(),
			Context:  "If thenStep output type",
		})
	}

	if elseStep.OutputType() != expectedOutputType {
		panic(&TypeMismatchError{
			Expected: expectedOutputType,
			Got:      elseStep.OutputType(),
			Context:  "If elseStep output type",
		})
	}

	return &conditionalStep[In, Out]{
		condition: condition,
		thenStep:  thenStep,
		elseStep:  elseStep,
	}
}
