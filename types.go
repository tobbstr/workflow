// Package workflow provides a lightweight workflow library for microservices,
// primarily designed for use in HTTP and gRPC endpoint implementations.
//
// The library provides a fluent API for defining synchronous, short-lived workflows
// with support for conditional branching, parallel execution, and configurable retry policies.
//
// Example usage:
//
//	wf := workflow.New().
//		Step("validate", workflow.TypedStep(validateInput)).
//		Step("process", workflow.TypedStep(processData)).
//		Step("save", workflow.TypedStep(saveResult))
//
//	result, err := workflow.ExecuteTyped[Result](ctx, wf, input)
package workflow

// WorkflowID identifies a workflow for observability purposes.
// It can be explicitly set via WithID() or auto-generated during execution.
//
// Example:
//
//	wf := workflow.New().
//		WithID(WorkflowID("order-processing"))
type WorkflowID string

// StepName identifies a step within a workflow.
// Steps can have optional default names via Step.Name(), with explicit control via workflow.
//
// Example:
//
//	// Explicit naming
//	wf.Step("validate", workflow.TypedStep(validateFn))
//
//	// Auto: inherit from step's default name
//	wf.Step(workflow.Auto, namedStep)
type StepName string

const (
	// Auto uses the step's default name from Step.Name().
	// Panics if the step has no default name.
	//
	// Example:
	//
	//	authStep := workflow.NamedTypedStep("authenticate", authFn)
	//	wf.Step(workflow.Auto, authStep) // Uses "authenticate"
	Auto StepName = ""
)
