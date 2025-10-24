package workflow

import (
	"context"
	"time"
)

// Observer provides hooks for monitoring workflow and step execution.
// Implement this interface to add custom metrics, logging, or tracing.
//
// Example:
//
//	type metricsObserver struct {
//		metrics MetricsClient
//	}
//
//	func (m *metricsObserver) OnStepStart(ctx context.Context, stepName StepName) {
//		m.metrics.Increment("workflow.step.started", map[string]string{"step": string(stepName)})
//	}
//
//	func (m *metricsObserver) OnStepComplete(ctx context.Context, stepName StepName, duration time.Duration, err error) {
//		tags := map[string]string{"step": string(stepName)}
//		if err != nil {
//			tags["status"] = "error"
//		} else {
//			tags["status"] = "success"
//		}
//		m.metrics.Timing("workflow.step.duration", duration, tags)
//	}
type Observer interface {
	// OnWorkflowStart is called when workflow execution begins.
	OnWorkflowStart(ctx context.Context, workflowID WorkflowID)

	// OnWorkflowComplete is called when workflow execution completes (success or failure).
	OnWorkflowComplete(ctx context.Context, workflowID WorkflowID, duration time.Duration, err error)

	// OnStepStart is called when a step begins execution.
	OnStepStart(ctx context.Context, stepName StepName)

	// OnStepComplete is called when a step completes (success or failure).
	OnStepComplete(ctx context.Context, stepName StepName, duration time.Duration, err error)

	// OnStepRetry is called when a step is retried after a failure.
	OnStepRetry(ctx context.Context, stepName StepName, attempt int, err error)

	// OnStepErrorAllowed is called when a step error is allowed and bypassed using AllowError.
	OnStepErrorAllowed(ctx context.Context, stepName StepName, err error)
}

// NoopObserver is a default implementation of Observer that does nothing.
// Use this as a base for implementing partial observers.
//
// Example:
//
//	type myObserver struct {
//		workflow.NoopObserver
//	}
//
//	// Override only the methods you care about
//	func (m *myObserver) OnStepComplete(ctx context.Context, stepName StepName, duration time.Duration, err error) {
//		log.Printf("Step %s completed in %v", stepName, duration)
//	}
type NoopObserver struct{}

// OnWorkflowStart implements Observer.
func (n *NoopObserver) OnWorkflowStart(ctx context.Context, workflowID WorkflowID) {}

// OnWorkflowComplete implements Observer.
func (n *NoopObserver) OnWorkflowComplete(ctx context.Context, workflowID WorkflowID, duration time.Duration, err error) {
}

// OnStepStart implements Observer.
func (n *NoopObserver) OnStepStart(ctx context.Context, stepName StepName) {}

// OnStepComplete implements Observer.
func (n *NoopObserver) OnStepComplete(ctx context.Context, stepName StepName, duration time.Duration, err error) {
}

// OnStepRetry implements Observer.
func (n *NoopObserver) OnStepRetry(ctx context.Context, stepName StepName, attempt int, err error) {}

// OnStepErrorAllowed implements Observer.
func (n *NoopObserver) OnStepErrorAllowed(ctx context.Context, stepName StepName, err error) {}

// WithObserver adds an observer to the workflow.
// Multiple observers can be added and all will be notified of events.
//
// Example:
//
//	wf := workflow.New().
//		WithObserver(&metricsObserver{metrics: metricsClient}).
//		WithObserver(&loggingObserver{logger: logger}).
//		Step("step1", workflow.TypedStep(step1Fn))
func (w *Workflow) WithObserver(observer Observer) *Workflow {
	w.observers = append(w.observers, observer)
	return w
}

// notifyWorkflowStart notifies all observers that workflow execution started.
func (w *Workflow) notifyWorkflowStart(ctx context.Context) {
	for _, obs := range w.observers {
		// Catch panics from observers to prevent breaking workflow execution
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Observer panic is logged but doesn't break execution
				}
			}()
			obs.OnWorkflowStart(ctx, w.id)
		}()
	}
}

// notifyWorkflowComplete notifies all observers that workflow execution completed.
func (w *Workflow) notifyWorkflowComplete(ctx context.Context, duration time.Duration, err error) {
	for _, obs := range w.observers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Observer panic is logged but doesn't break execution
				}
			}()
			obs.OnWorkflowComplete(ctx, w.id, duration, err)
		}()
	}
}

// notifyStepStart notifies all observers that step execution started.
func (w *Workflow) notifyStepStart(ctx context.Context, stepName StepName) {
	for _, obs := range w.observers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Observer panic is logged but doesn't break execution
				}
			}()
			obs.OnStepStart(ctx, stepName)
		}()
	}
}

// notifyStepComplete notifies all observers that step execution completed.
func (w *Workflow) notifyStepComplete(ctx context.Context, stepName StepName, duration time.Duration, err error) {
	for _, obs := range w.observers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Observer panic is logged but doesn't break execution
				}
			}()
			obs.OnStepComplete(ctx, stepName, duration, err)
		}()
	}
}

// notifyStepRetry notifies all observers that a step is being retried.
func (w *Workflow) notifyStepRetry(ctx context.Context, stepName StepName, attempt int, err error) {
	for _, obs := range w.observers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Observer panic is logged but doesn't break execution
				}
			}()
			obs.OnStepRetry(ctx, stepName, attempt, err)
		}()
	}
}

// notifyStepErrorAllowed notifies all observers that a step error was allowed and bypassed.
func (w *Workflow) notifyStepErrorAllowed(ctx context.Context, stepName StepName, err error) {
	for _, obs := range w.observers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Observer panic is logged but doesn't break execution
				}
			}()
			obs.OnStepErrorAllowed(ctx, stepName, err)
		}()
	}
}
