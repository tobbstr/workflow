package workflow

import (
	"context"
	"fmt"
	"reflect"
)

// compositeStep executes a sequence of steps in order.
type compositeStep struct {
	steps      []Step
	inputType  reflect.Type
	outputType reflect.Type
}

// Name returns an empty string (composite steps don't have default names).
func (s *compositeStep) Name() StepName {
	return ""
}

// Execute runs all steps in sequence.
func (s *compositeStep) Execute(ctx context.Context, input any) (any, error) {
	if len(s.steps) == 0 {
		return input, nil
	}

	currentOutput := input
	for i, step := range s.steps {
		output, err := step.Execute(ctx, currentOutput)
		if err != nil {
			return nil, fmt.Errorf("composite step %d: %w", i, err)
		}
		currentOutput = output
	}

	return currentOutput, nil
}

// InputType returns the reflect.Type of the input.
func (s *compositeStep) InputType() reflect.Type {
	return s.inputType
}

// OutputType returns the reflect.Type of the output.
func (s *compositeStep) OutputType() reflect.Type {
	return s.outputType
}

// Compose creates a step from a sequence of steps.
// Useful for inline composition of simple branches in conditionals.
// Type compatibility is validated at construction time.
//
// Example:
//
//	// Compose multiple steps into a single step
//	complexStep := workflow.Compose[Input, Output](
//		workflow.TypedStep(step1),
//		workflow.TypedStep(step2),
//		workflow.TypedStep(step3),
//	)
//
//	// Use in conditional
//	workflow.If[Input, Output](
//		condition,
//		workflow.Compose[Input, Output](thenStep1, thenStep2),
//		workflow.Compose[Input, Output](elseStep1, elseStep2),
//	)
func Compose[In, Out any](steps ...Step) Step {
	if len(steps) == 0 {
		panic("Compose requires at least one step")
	}

	var zeroIn In
	var zeroOut Out
	expectedInputType := reflect.TypeOf(zeroIn)
	expectedOutputType := reflect.TypeOf(zeroOut)

	// Validate first step input type
	if steps[0].InputType() != expectedInputType {
		panic(&TypeMismatchError{
			Expected: expectedInputType,
			Got:      steps[0].InputType(),
			Context:  "Compose first step input type",
		})
	}

	// Validate sequential type compatibility
	for i := 0; i < len(steps)-1; i++ {
		currentOutput := steps[i].OutputType()
		nextInput := steps[i+1].InputType()

		if currentOutput != nextInput {
			panic(&TypeMismatchError{
				Expected: nextInput,
				Got:      currentOutput,
				Context:  fmt.Sprintf("Compose step %d output to step %d input", i, i+1),
			})
		}
	}

	// Validate last step output type
	if steps[len(steps)-1].OutputType() != expectedOutputType {
		panic(&TypeMismatchError{
			Expected: expectedOutputType,
			Got:      steps[len(steps)-1].OutputType(),
			Context:  "Compose last step output type",
		})
	}

	return &compositeStep{
		steps:      steps,
		inputType:  expectedInputType,
		outputType: expectedOutputType,
	}
}

// workflowStep represents a workflow wrapped as a step.
type workflowAsStep struct {
	workflow *Workflow
}

// Name returns the workflow's ID as the default step name.
func (s *workflowAsStep) Name() StepName {
	return StepName(s.workflow.id)
}

// Execute runs the workflow.
func (s *workflowAsStep) Execute(ctx context.Context, input any) (any, error) {
	return s.workflow.Execute(ctx, input)
}

// InputType returns the workflow's input type.
func (s *workflowAsStep) InputType() reflect.Type {
	return s.workflow.inputType
}

// OutputType returns the workflow's output type.
func (s *workflowAsStep) OutputType() reflect.Type {
	return s.workflow.outputType
}

// AsStep converts a workflow into a Step that can be used in other workflows.
// The workflow's ID is used as the default step name.
// Useful for complex reusable sub-workflows.
//
// Example:
//
//	// Define reusable sub-workflow
//	authWorkflow := workflow.New().
//		WithID(WorkflowID("authentication")).
//		Step("validate_token", workflow.TypedStep(validateToken)).
//		Step("load_user", workflow.TypedStep(loadUser))
//
//	// Use as step in main workflow
//	mainWorkflow := workflow.New().
//		Step(workflow.Auto, authWorkflow.AsStep()).  // Uses "authentication"
//		Step("process", workflow.TypedStep(processRequest))
func (w *Workflow) AsStep() Step {
	if len(w.steps) == 0 {
		panic("cannot convert empty workflow to step")
	}

	return &workflowAsStep{
		workflow: w,
	}
}
