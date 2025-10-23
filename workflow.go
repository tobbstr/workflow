package workflow

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
)

// Workflow represents a sequence of steps that process data from input to output.
// Workflows are constructed using a fluent builder API.
//
// Example:
//
//	wf := workflow.New().
//		WithID(WorkflowID("user-registration")).
//		Step("validate", workflow.TypedStep(validateInput)).
//		Step("create_user", workflow.TypedStep(createUser))
//
//	result, err := workflow.ExecuteTyped[User](ctx, wf, input)
type Workflow struct {
	id           WorkflowID
	steps        []*workflowStep
	inputType    reflect.Type
	outputType   reflect.Type
	defaultRetry *RetryConfig
	observers    []Observer
}

// workflowStep wraps a Step with its configured name and retry policy.
type workflowStep struct {
	step  Step
	name  StepName
	retry *RetryConfig
}

// New creates a new empty workflow.
//
// Example:
//
//	wf := workflow.New().
//		Step("step1", workflow.TypedStep(fn1)).
//		Step("step2", workflow.TypedStep(fn2))
func New() *Workflow {
	return &Workflow{
		steps: make([]*workflowStep, 0),
	}
}

// WithID sets the workflow ID for observability purposes.
// If not set, a unique ID will be auto-generated during execution.
//
// Example:
//
//	wf := workflow.New().
//		WithID(WorkflowID("order-processing"))
func (w *Workflow) WithID(id WorkflowID) *Workflow {
	w.id = id
	return w
}

// Step adds a step to the workflow with the given name.
// If name is Auto, the step's default name from Step.Name() is used.
// Panics if name is Auto and the step has no default name.
// Panics if the step's input type doesn't match the previous step's output type.
//
// Example:
//
//	// Explicit naming
//	wf.Step("validate", workflow.TypedStep(validateFn))
//
//	// Auto: inherit from step's default name
//	namedStep := workflow.NamedTypedStep("process", processFn)
//	wf.Step(workflow.Auto, namedStep) // Uses "process"
func (w *Workflow) Step(name StepName, step Step, opts ...StepOption) *Workflow {
	// Handle Auto step naming
	actualName := name
	if name == Auto {
		if step.Name() == "" {
			panic(fmt.Errorf("%w: step has no default name", ErrInvalidStepName))
		}
		actualName = step.Name()
	}

	// Validate type compatibility with previous step
	if len(w.steps) > 0 {
		prevStep := w.steps[len(w.steps)-1]
		prevOutputType := prevStep.step.OutputType()
		currentInputType := step.InputType()

		if prevOutputType != currentInputType {
			panic(&TypeMismatchError{
				Expected: currentInputType,
				Got:      prevOutputType,
				Context:  fmt.Sprintf("step '%s' input type", actualName),
			})
		}
	} else {
		// First step - set workflow input type
		w.inputType = step.InputType()
	}

	// Last step - set workflow output type
	w.outputType = step.OutputType()

	// Apply options
	config := &stepConfig{}
	for _, opt := range opts {
		opt(config)
	}

	// Determine retry config for this step
	var stepRetry *RetryConfig
	if config.noRetry {
		// Explicitly disabled
		stepRetry = nil
	} else if config.retry != nil {
		// Step-level override
		stepRetry = config.retry
	} else {
		// Use workflow default (may be nil)
		stepRetry = w.defaultRetry
	}

	w.steps = append(w.steps, &workflowStep{
		step:  step,
		name:  actualName,
		retry: stepRetry,
	})

	return w
}

// Execute runs the workflow with the given input and returns the final output.
// The workflow ID is used for observability; if not set, a unique ID is auto-generated.
//
// Example:
//
//	result, err := wf.Execute(ctx, input)
//	if err != nil {
//		return fmt.Errorf("executing workflow: %w", err)
//	}
func (w *Workflow) Execute(ctx context.Context, input any) (any, error) {
	// Return early if workflow has no steps
	if len(w.steps) == 0 {
		return nil, ErrNoSteps
	}

	// Generate execution ID if not set
	executionID := w.id
	if executionID == "" {
		executionID = WorkflowID(uuid.New().String())
	}
	// Update workflow ID for this execution
	originalID := w.id
	w.id = executionID
	defer func() {
		w.id = originalID
	}()

	// Validate input type matches workflow's expected input type
	inputType := reflect.TypeOf(input)
	if w.inputType != inputType {
		return nil, &TypeMismatchError{
			Expected: w.inputType,
			Got:      inputType,
			Context:  "workflow input",
		}
	}

	// Notify workflow start
	startTime := time.Now()
	w.notifyWorkflowStart(ctx)

	// Track workflow completion
	var workflowErr error
	defer func() {
		duration := time.Since(startTime)
		w.notifyWorkflowComplete(ctx, duration, workflowErr)
	}()

	// Execute steps sequentially
	currentOutput := input
	for _, ws := range w.steps {
		// Check context cancellation
		if err := ctx.Err(); err != nil {
			workflowErr = fmt.Errorf("context cancelled before step '%s': %w", ws.name, err)
			return nil, workflowErr
		}

		// Execute step with retry and observer notifications
		var output any
		var stepErr error

		stepStartTime := time.Now()
		w.notifyStepStart(ctx, ws.name)

		executeErr := w.executeStepWithRetry(ctx, ws, currentOutput, &output, &stepErr)

		stepDuration := time.Since(stepStartTime)
		w.notifyStepComplete(ctx, ws.name, stepDuration, executeErr)

		if executeErr != nil {
			workflowErr = fmt.Errorf("executing step '%s': %w", ws.name, executeErr)
			return nil, workflowErr
		}

		currentOutput = output
	}

	return currentOutput, nil
}

// executeStepWithRetry executes a step with retry logic and observer notifications.
func (w *Workflow) executeStepWithRetry(
	ctx context.Context,
	ws *workflowStep,
	input any,
	output *any,
	stepErr *error,
) error {
	if ws.retry == nil {
		*output, *stepErr = ws.step.Execute(ctx, input)
		return *stepErr
	}

	// Execute with retry and notify on each retry attempt
	var lastErr error
	for attempt := 1; attempt <= ws.retry.MaxAttempts; attempt++ {
		// Check context cancellation
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled: %w", err)
		}

		*output, *stepErr = ws.step.Execute(ctx, input)
		lastErr = *stepErr

		if lastErr == nil {
			return nil
		}

		// Check if error is retryable
		if !ws.retry.shouldRetry(lastErr) {
			return lastErr
		}

		// Don't sleep or notify after the last attempt
		if attempt == ws.retry.MaxAttempts {
			break
		}

		// Notify retry
		w.notifyStepRetry(ctx, ws.name, attempt, lastErr)

		// Calculate delay with exponential backoff and jitter
		delay := calculateDelay(ws.retry, attempt)

		// Sleep with context awareness
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during retry: %w", ctx.Err())
		}
	}

	return fmt.Errorf("max retry attempts (%d) exceeded: %w", ws.retry.MaxAttempts, lastErr)
}

// ExecuteTyped is a type-safe helper that executes the workflow and returns a typed result.
// It validates that the workflow's output type matches the expected type parameter.
//
// Example:
//
//	type User struct {
//		ID   int
//		Name string
//	}
//
//	result, err := workflow.ExecuteTyped[User](ctx, wf, input)
//	if err != nil {
//		return fmt.Errorf("executing workflow: %w", err)
//	}
//	// result is of type User
func ExecuteTyped[Out any](ctx context.Context, w *Workflow, input any) (Out, error) {
	var zero Out

	result, err := w.Execute(ctx, input)
	if err != nil {
		return zero, err
	}

	typedResult, ok := result.(Out)
	if !ok {
		return zero, &TypeMismatchError{
			Expected: reflect.TypeOf(zero),
			Got:      reflect.TypeOf(result),
			Context:  "workflow output",
		}
	}

	return typedResult, nil
}

// stepConfig holds configuration options for a step.
type stepConfig struct {
	retry   *RetryConfig
	noRetry bool
}

// StepOption is a functional option for configuring step behavior.
type StepOption func(*stepConfig)

// TypeMismatchError is returned when there's a type mismatch between workflow steps.
type TypeMismatchError struct {
	Expected reflect.Type
	Got      reflect.Type
	Context  string
}

// Error returns the error message.
func (e *TypeMismatchError) Error() string {
	return fmt.Sprintf("%s: expected type %v, got %v", e.Context, e.Expected, e.Got)
}
