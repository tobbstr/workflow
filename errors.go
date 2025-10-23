package workflow

import "errors"

var (
	// ErrTypeMismatch is returned when step input/output types don't match.
	ErrTypeMismatch = errors.New("type mismatch between steps")

	// ErrInvalidStepName is returned when attempting to use Auto with an unnamed step.
	ErrInvalidStepName = errors.New("cannot use Auto with unnamed step")

	// ErrNoSteps is returned when attempting to execute a workflow with no steps.
	ErrNoSteps = errors.New("workflow has no steps")
)
