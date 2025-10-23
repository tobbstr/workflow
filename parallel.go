package workflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

// ParallelStrategy determines how parallel step results are evaluated.
type ParallelStrategy int

const (
	// AllMustSucceed requires all parallel steps to succeed.
	// If any step fails, the entire parallel execution fails.
	AllMustSucceed ParallelStrategy = iota

	// AtLeastN requires at least MinSuccessCount steps to succeed.
	// Fails if fewer than MinSuccessCount steps succeed.
	AtLeastN

	// FirstSuccess returns as soon as the first step succeeds.
	// Cancels remaining steps and returns the first successful result.
	FirstSuccess
)

// ParallelConfig configures parallel step execution behavior.
//
// Example:
//
//	config := workflow.ParallelConfig{
//		Strategy:        workflow.AllMustSucceed,
//		MinSuccessCount: 0, // Not used for AllMustSucceed
//	}
type ParallelConfig struct {
	// Strategy determines how results are evaluated.
	Strategy ParallelStrategy

	// MinSuccessCount is the minimum number of successful steps required.
	// Only used with AtLeastN strategy.
	MinSuccessCount int
}

// Validate checks if the ParallelConfig is valid.
func (c ParallelConfig) Validate() error {
	if c.Strategy == AtLeastN && c.MinSuccessCount < 1 {
		return fmt.Errorf("MinSuccessCount must be >= 1 for AtLeastN strategy, got %d", c.MinSuccessCount)
	}
	return nil
}

// parallelStep executes multiple functions concurrently and returns all results as a slice.
type parallelStep[In, Out any] struct {
	config ParallelConfig
	steps  []func(context.Context, In) (Out, error)
}

// parallelResult holds the result of a single parallel step execution.
type parallelResult[Out any] struct {
	index  int
	value  Out
	err    error
	hasErr bool
}

// Name returns an empty string (parallel steps don't have default names).
func (s *parallelStep[In, Out]) Name() StepName {
	return ""
}

// Execute runs all steps concurrently and returns results based on the strategy.
func (s *parallelStep[In, Out]) Execute(ctx context.Context, input any) (any, error) {
	typedInput, ok := input.(In)
	if !ok {
		var zero In
		return nil, &TypeMismatchError{
			Expected: reflect.TypeOf(zero),
			Got:      reflect.TypeOf(input),
			Context:  "parallel step input",
		}
	}

	return s.executeParallel(ctx, typedInput)
}

// InputType returns the reflect.Type of the input.
func (s *parallelStep[In, Out]) InputType() reflect.Type {
	var zero In
	return reflect.TypeOf(zero)
}

// OutputType returns the reflect.Type of the slice output.
func (s *parallelStep[In, Out]) OutputType() reflect.Type {
	var zero []Out
	return reflect.TypeOf(zero)
}

// executeParallel runs all steps concurrently and collects results.
func (s *parallelStep[In, Out]) executeParallel(ctx context.Context, input In) ([]Out, error) {
	numSteps := len(s.steps)
	if numSteps == 0 {
		return []Out{}, nil
	}

	// Create cancellable context for parallel execution
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Channels for results and errors
	resultCh := make(chan parallelResult[Out], numSteps)
	var wg sync.WaitGroup

	// Start all parallel steps
	for i, step := range s.steps {
		wg.Add(1)
		go func(index int, stepFn func(context.Context, In) (Out, error)) {
			defer wg.Done()

			value, err := stepFn(ctx, input)
			resultCh <- parallelResult[Out]{
				index:  index,
				value:  value,
				err:    err,
				hasErr: err != nil,
			}
		}(i, step)
	}

	// Wait for all goroutines to complete
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results based on strategy
	return s.collectResults(ctx, resultCh, numSteps, cancel)
}

// collectResults gathers results based on the configured strategy.
func (s *parallelStep[In, Out]) collectResults(
	ctx context.Context,
	resultCh <-chan parallelResult[Out],
	numSteps int,
	cancel context.CancelFunc,
) ([]Out, error) {
	results := make([]Out, numSteps)
	errors := make([]error, 0)
	successCount := 0
	received := 0

	for res := range resultCh {
		received++

		if res.hasErr {
			errors = append(errors, fmt.Errorf("step %d: %w", res.index, res.err))

			// For AllMustSucceed, fail fast on first error
			if s.config.Strategy == AllMustSucceed {
				cancel() // Cancel remaining steps
				// Continue collecting to drain channel
			}
		} else {
			results[res.index] = res.value
			successCount++

			// For FirstSuccess, return immediately on first success
			if s.config.Strategy == FirstSuccess {
				cancel() // Cancel remaining steps
				return []Out{res.value}, nil
			}
		}

		// Check if we've received all results
		if received == numSteps {
			break
		}
	}

	// Evaluate based on strategy
	switch s.config.Strategy {
	case AllMustSucceed:
		if len(errors) > 0 {
			return nil, &ParallelExecutionError{
				TotalSteps:   numSteps,
				SuccessCount: successCount,
				FailureCount: len(errors),
				Errors:       errors,
			}
		}
		return results, nil

	case AtLeastN:
		if successCount < s.config.MinSuccessCount {
			return nil, &ParallelExecutionError{
				TotalSteps:   numSteps,
				SuccessCount: successCount,
				FailureCount: len(errors),
				Errors:       errors,
				Message: fmt.Sprintf("required %d successes, got %d",
					s.config.MinSuccessCount, successCount),
			}
		}
		// Return only successful results
		successResults := make([]Out, 0, successCount)
		for i := 0; i < numSteps; i++ {
			// Check if this index had an error
			hasError := false
			for _, err := range errors {
				if fmt.Sprintf("step %d:", i) == err.Error()[:len(fmt.Sprintf("step %d:", i))] {
					hasError = true
					break
				}
			}
			if !hasError {
				successResults = append(successResults, results[i])
			}
		}
		return successResults, nil

	case FirstSuccess:
		// Should have returned earlier, but handle just in case
		if successCount == 0 {
			return nil, &ParallelExecutionError{
				TotalSteps:   numSteps,
				SuccessCount: 0,
				FailureCount: len(errors),
				Errors:       errors,
				Message:      "no successful steps",
			}
		}
		// Return first successful result
		for i := 0; i < numSteps; i++ {
			hasError := false
			for _, err := range errors {
				if fmt.Sprintf("step %d:", i) == err.Error()[:len(fmt.Sprintf("step %d:", i))] {
					hasError = true
					break
				}
			}
			if !hasError {
				return []Out{results[i]}, nil
			}
		}

	default:
		return nil, fmt.Errorf("unknown parallel strategy: %d", s.config.Strategy)
	}

	return results, nil
}

// Parallel creates a step that executes multiple functions concurrently.
// Returns a slice of results ([]Out) that can be processed by subsequent steps.
//
// Example:
//
//	wf := workflow.New().
//		Step("fetch_parallel", workflow.Parallel[UserContext, UserProfile](
//			workflow.ParallelConfig{Strategy: workflow.FirstSuccess},
//			fetchFromPrimaryDB,
//			fetchFromReplicaDB,
//			fetchFromCache,
//		)).
//		Step("select", workflow.TypedStep(func(ctx context.Context, profiles []UserProfile) (UserProfile, error) {
//			return profiles[0], nil
//		}))
func Parallel[In, Out any](
	config ParallelConfig,
	steps ...func(context.Context, In) (Out, error),
) Step {
	if err := config.Validate(); err != nil {
		panic(fmt.Errorf("invalid parallel config: %w", err))
	}

	return &parallelStep[In, Out]{
		config: config,
		steps:  steps,
	}
}

// parallelMergeStep executes multiple functions concurrently and merges results.
type parallelMergeStep[In, Out any] struct {
	config ParallelConfig
	merger func([]Out) (Out, error)
	steps  []func(context.Context, In) (Out, error)
}

// Name returns an empty string (parallel merge steps don't have default names).
func (s *parallelMergeStep[In, Out]) Name() StepName {
	return ""
}

// Execute runs all steps concurrently, collects results, and merges them.
func (s *parallelMergeStep[In, Out]) Execute(ctx context.Context, input any) (any, error) {
	typedInput, ok := input.(In)
	if !ok {
		var zero In
		return nil, &TypeMismatchError{
			Expected: reflect.TypeOf(zero),
			Got:      reflect.TypeOf(input),
			Context:  "parallel merge step input",
		}
	}

	// Use embedded parallelStep to execute
	ps := &parallelStep[In, Out]{
		config: s.config,
		steps:  s.steps,
	}

	results, err := ps.executeParallel(ctx, typedInput)
	if err != nil {
		var zero Out
		return zero, err
	}

	// Merge results
	merged, err := s.merger(results)
	if err != nil {
		var zero Out
		return zero, fmt.Errorf("merging results: %w", err)
	}

	return merged, nil
}

// InputType returns the reflect.Type of the input.
func (s *parallelMergeStep[In, Out]) InputType() reflect.Type {
	var zero In
	return reflect.TypeOf(zero)
}

// OutputType returns the reflect.Type of the merged output.
func (s *parallelMergeStep[In, Out]) OutputType() reflect.Type {
	var zero Out
	return reflect.TypeOf(zero)
}

// ParallelMerge creates a step that executes multiple functions concurrently and merges results.
// Returns a single merged result (Out) using the provided merger function.
//
// Example:
//
//	wf := workflow.New().
//		Step("fetch_parallel", workflow.ParallelMerge[UserContext, UserProfile](
//			workflow.ParallelConfig{Strategy: workflow.FirstSuccess},
//			workflow.FirstResult[UserProfile](),
//			fetchFromPrimaryDB,
//			fetchFromReplicaDB,
//			fetchFromCache,
//		)).
//		Step("enrich", workflow.TypedStep(enrichProfile))
func ParallelMerge[In, Out any](
	config ParallelConfig,
	merger func([]Out) (Out, error),
	steps ...func(context.Context, In) (Out, error),
) Step {
	if err := config.Validate(); err != nil {
		panic(fmt.Errorf("invalid parallel config: %w", err))
	}

	if merger == nil {
		panic(errors.New("merger function cannot be nil"))
	}

	return &parallelMergeStep[In, Out]{
		config: config,
		merger: merger,
		steps:  steps,
	}
}

// Built-in merger functions

// FirstResult returns a merger that selects the first result from the slice.
//
// Example:
//
//	workflow.ParallelMerge[Input, Output](
//		config,
//		workflow.FirstResult[Output](),
//		step1, step2, step3,
//	)
func FirstResult[T any]() func([]T) (T, error) {
	return func(results []T) (T, error) {
		var zero T
		if len(results) == 0 {
			return zero, errors.New("no results to select from")
		}
		return results[0], nil
	}
}

// LastResult returns a merger that selects the last result from the slice.
//
// Example:
//
//	workflow.ParallelMerge[Input, Output](
//		config,
//		workflow.LastResult[Output](),
//		step1, step2, step3,
//	)
func LastResult[T any]() func([]T) (T, error) {
	return func(results []T) (T, error) {
		var zero T
		if len(results) == 0 {
			return zero, errors.New("no results to select from")
		}
		return results[len(results)-1], nil
	}
}

// AllResults returns a merger that passes through all results as a slice.
// Useful when you want ParallelMerge to return []T explicitly.
//
// Example:
//
//	workflow.ParallelMerge[Input, []Output](
//		config,
//		workflow.AllResults[Output](),
//		step1, step2, step3,
//	)
func AllResults[T any]() func([]T) ([]T, error) {
	return func(results []T) ([]T, error) {
		return results, nil
	}
}

// ParallelExecutionError is returned when parallel execution fails.
type ParallelExecutionError struct {
	TotalSteps   int
	SuccessCount int
	FailureCount int
	Errors       []error
	Message      string
}

// Error returns the error message.
func (e *ParallelExecutionError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("parallel execution failed: %s (total: %d, success: %d, failures: %d)",
			e.Message, e.TotalSteps, e.SuccessCount, e.FailureCount)
	}
	return fmt.Sprintf("parallel execution failed: %d of %d steps failed",
		e.FailureCount, e.TotalSteps)
}

// Unwrap returns the underlying errors.
func (e *ParallelExecutionError) Unwrap() []error {
	return e.Errors
}
