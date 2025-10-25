package workflow

import (
	"context"
	"errors"
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

// workflowStep wraps a Step with its configured name, retry policy, and error handling.
type workflowStep struct {
	step       Step
	name       StepName
	retry      *RetryConfig
	allowError *allowErrorConfig
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
		steps: make([]*workflowStep, 0, 4),
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
		step:       step,
		name:       actualName,
		retry:      stepRetry,
		allowError: config.allowError,
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

// executeStepWithRetry executes a step with error allowance and retry logic.
// AllowError check happens BEFORE retry logic.
func (w *Workflow) executeStepWithRetry(
	ctx context.Context,
	ws *workflowStep,
	input any,
	output *any,
	stepErr *error,
) error {
	// Execute the step first
	*output, *stepErr = ws.step.Execute(ctx, input)

	// Return early if no error
	if *stepErr == nil {
		return nil
	}

	// Check AllowError condition BEFORE retry logic
	if ws.allowError != nil {
		allowed := w.checkAllowedError(ctx, ws, input, *stepErr, output)
		if allowed {
			// Error was allowed and fallback was applied
			w.notifyStepErrorAllowed(ctx, ws.name, *stepErr)
			return nil
		}
	}

	// Error not allowed, proceed with retry logic if configured
	if ws.retry == nil {
		return *stepErr
	}

	// Execute retry attempts (starting from attempt 2, since we already executed once)
	var lastErr error = *stepErr
	for attempt := 2; attempt <= ws.retry.MaxAttempts; attempt++ {
		// Check if error is retryable
		if !ws.retry.shouldRetry(lastErr) {
			return lastErr
		}

		// Notify retry
		w.notifyStepRetry(ctx, ws.name, attempt-1, lastErr)

		// Calculate delay with exponential backoff and jitter
		delay := calculateDelay(ws.retry, attempt-1)

		// Sleep with context awareness
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during retry: %w", ctx.Err())
		}

		// Check context cancellation before retry
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled: %w", err)
		}

		*output, *stepErr = ws.step.Execute(ctx, input)
		lastErr = *stepErr

		if lastErr == nil {
			return nil
		}
	}

	return fmt.Errorf("max retry attempts (%d) exceeded: %w", ws.retry.MaxAttempts, lastErr)
}

// checkAllowedError checks if an error is allowed and applies the fallback if so.
// Returns true if the error was allowed and fallback was applied successfully.
func (w *Workflow) checkAllowedError(
	ctx context.Context,
	ws *workflowStep,
	input any,
	err error,
	output *any,
) bool {
	// Type-assert the checkError function
	checkError, ok := ws.allowError.checkError.(func(error) bool)
	if !ok {
		// Invalid configuration, treat as not allowed
		return false
	}

	// Check if the error should be allowed
	if !checkError(err) {
		return false
	}

	// Error is allowed, handle based on configuration
	if ws.allowError.passthrough {
		// Simple passthrough - no reflection needed
		*output = input
		return true
	}

	// Complex fallback - use reflection
	fallbackValue := reflect.ValueOf(ws.allowError.fallback)
	if !fallbackValue.IsValid() || fallbackValue.Kind() != reflect.Func {
		return false
	}

	// Call fallback(ctx, input, err)
	inputValue := reflect.ValueOf(input)
	errValue := reflect.ValueOf(err)
	ctxValue := reflect.ValueOf(ctx)

	results := fallbackValue.Call([]reflect.Value{ctxValue, inputValue, errValue})
	if len(results) != 1 {
		return false
	}

	*output = results[0].Interface()
	return true
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
	retry      *RetryConfig
	noRetry    bool
	allowError *allowErrorConfig
}

// StepOption is a functional option for configuring step behavior.
type StepOption func(*stepConfig)

// allowErrorConfig holds the configuration for allowing specific errors to be bypassed.
type allowErrorConfig struct {
	checkError any // func(error) bool
	fallback   any // func(context.Context, In, error) Out
	// Simple passthrough mode - when true, just pass through the input without calling fallback
	passthrough bool
}

// AllowErrorWithFallback returns a StepOption that allows the workflow to continue when specific
// errors occur, using a custom fallback function to provide the value for the next step.
// If the checkError function returns true for an error, the fallback function is called.
// This check happens BEFORE any retry logic.
//
// Use this when you need custom fallback logic, logging, or transformations.
// For simple passthrough of the input value, see AllowErrors.
//
// Example (FSM transition with fallback):
//
//	wf.Step("try_primary_transition", workflow.TypedStep(tryTransition),
//		workflow.AllowErrorWithFallback(
//			func(err error) bool { return errors.Is(err, ErrTransitionRejected) },
//			func(ctx context.Context, input State, err error) State {
//				// Log error, return modified state
//				log.Printf("Primary transition rejected: %v", err)
//				input.Retries++
//				return input
//			},
//		)).
//	Step("try_alternative_transition", workflow.TypedStep(tryAlternativeTransition))
func AllowErrorWithFallback[In, Out any](
	checkError func(error) bool,
	fallback func(context.Context, In, error) Out,
) StepOption {
	return func(sc *stepConfig) {
		sc.allowError = &allowErrorConfig{
			checkError: checkError,
			fallback:   fallback,
		}
	}
}

// AllowErrors returns a StepOption that allows the workflow to continue when any of the specified
// errors occur, passing through the input value unchanged. This is a simplified version of
// AllowErrorWithFallback for the common case where you just want to continue with the original input.
// This check happens BEFORE any retry logic.
//
// Use this when:
//   - The step's input and output types are the same
//   - You want to passthrough the original input unchanged
//   - You just need to match specific sentinel errors
//
// For custom fallback logic or logging, use AllowErrorWithFallback instead.
//
// Example (simple FSM transition):
//
//	wf.Step("try_primary_transition", workflow.TypedStep(tryTransition),
//		workflow.AllowErrors[State](ErrTransitionRejected)).
//	Step("try_alternative_transition", workflow.TypedStep(tryAlternativeTransition))
//
// Example (multiple errors):
//
//	wf.Step("optional_step", workflow.TypedStep(tryOptional),
//		workflow.AllowErrors[State](ErrNotFound, ErrTimeout, ErrUnavailable))
func AllowErrors[T any](targetErrs ...error) StepOption {
	return func(sc *stepConfig) {
		sc.allowError = &allowErrorConfig{
			checkError: func(err error) bool {
				for _, target := range targetErrs {
					if errors.Is(err, target) {
						return true
					}
				}
				return false
			},
			passthrough: true, // Use optimized passthrough mode
		}
	}
}

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
