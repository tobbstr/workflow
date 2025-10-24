package workflow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// mockObserver tracks all observer method calls for testing.
type mockObserver struct {
	mu                sync.Mutex
	workflowStarts    []WorkflowID
	workflowCompletes []workflowCompleteEvent
	stepStarts        []StepName
	stepCompletes     []stepCompleteEvent
	stepRetries       []stepRetryEvent
	stepErrorsAllowed []stepErrorAllowedEvent
	shouldPanic       bool
	panicOnMethod     string
}

type workflowCompleteEvent struct {
	workflowID WorkflowID
	duration   time.Duration
	err        error
}

type stepCompleteEvent struct {
	stepName StepName
	duration time.Duration
	err      error
}

type stepRetryEvent struct {
	stepName StepName
	attempt  int
	err      error
}

type stepErrorAllowedEvent struct {
	stepName StepName
	err      error
}

func (m *mockObserver) OnWorkflowStart(ctx context.Context, workflowID WorkflowID) {
	if m.shouldPanic && m.panicOnMethod == "OnWorkflowStart" {
		panic("observer panic")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workflowStarts = append(m.workflowStarts, workflowID)
}

func (m *mockObserver) OnWorkflowComplete(ctx context.Context, workflowID WorkflowID, duration time.Duration, err error) {
	if m.shouldPanic && m.panicOnMethod == "OnWorkflowComplete" {
		panic("observer panic")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workflowCompletes = append(m.workflowCompletes, workflowCompleteEvent{
		workflowID: workflowID,
		duration:   duration,
		err:        err,
	})
}

func (m *mockObserver) OnStepStart(ctx context.Context, stepName StepName) {
	if m.shouldPanic && m.panicOnMethod == "OnStepStart" {
		panic("observer panic")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stepStarts = append(m.stepStarts, stepName)
}

func (m *mockObserver) OnStepComplete(ctx context.Context, stepName StepName, duration time.Duration, err error) {
	if m.shouldPanic && m.panicOnMethod == "OnStepComplete" {
		panic("observer panic")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stepCompletes = append(m.stepCompletes, stepCompleteEvent{
		stepName: stepName,
		duration: duration,
		err:      err,
	})
}

func (m *mockObserver) OnStepRetry(ctx context.Context, stepName StepName, attempt int, err error) {
	if m.shouldPanic && m.panicOnMethod == "OnStepRetry" {
		panic("observer panic")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stepRetries = append(m.stepRetries, stepRetryEvent{
		stepName: stepName,
		attempt:  attempt,
		err:      err,
	})
}

func (m *mockObserver) OnStepErrorAllowed(ctx context.Context, stepName StepName, err error) {
	if m.shouldPanic && m.panicOnMethod == "OnStepErrorAllowed" {
		panic("observer panic")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stepErrorsAllowed = append(m.stepErrorsAllowed, stepErrorAllowedEvent{
		stepName: stepName,
		err:      err,
	})
}

func TestNoopObserver(t *testing.T) {
	t.Run("implements Observer interface", func(t *testing.T) {
		var _ Observer = (*NoopObserver)(nil)
	})

	t.Run("can be called without panic", func(t *testing.T) {
		obs := &NoopObserver{}
		ctx := context.Background()

		obs.OnWorkflowStart(ctx, "test")
		obs.OnWorkflowComplete(ctx, "test", time.Second, nil)
		obs.OnStepStart(ctx, "step")
		obs.OnStepComplete(ctx, "step", time.Second, nil)
		obs.OnStepRetry(ctx, "step", 1, errors.New("error"))
	})
}

func TestWorkflow_WithObserver(t *testing.T) {
	t.Run("adds single observer", func(t *testing.T) {
		obs := &mockObserver{}
		wf := New().
			WithObserver(obs).
			Step("step1", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input, nil
			}))

		if len(wf.observers) != 1 {
			t.Errorf("expected 1 observer, got %d", len(wf.observers))
		}
	})

	t.Run("adds multiple observers", func(t *testing.T) {
		obs1 := &mockObserver{}
		obs2 := &mockObserver{}
		wf := New().
			WithObserver(obs1).
			WithObserver(obs2).
			Step("step1", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input, nil
			}))

		if len(wf.observers) != 2 {
			t.Errorf("expected 2 observers, got %d", len(wf.observers))
		}
	})
}

func TestWorkflow_ObserverIntegration(t *testing.T) {
	t.Run("notifies on workflow start and complete", func(t *testing.T) {
		obs := &mockObserver{}
		wf := New().
			WithID(WorkflowID("test-workflow")).
			WithObserver(obs).
			Step("step1", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			}))

		ctx := context.Background()
		_, err := ExecuteTyped[int](ctx, wf, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		obs.mu.Lock()
		defer obs.mu.Unlock()

		if len(obs.workflowStarts) != 1 {
			t.Errorf("expected 1 workflow start, got %d", len(obs.workflowStarts))
		}

		if obs.workflowStarts[0] != "test-workflow" {
			t.Errorf("expected workflow ID 'test-workflow', got %q", obs.workflowStarts[0])
		}

		if len(obs.workflowCompletes) != 1 {
			t.Errorf("expected 1 workflow complete, got %d", len(obs.workflowCompletes))
		}

		if obs.workflowCompletes[0].err != nil {
			t.Errorf("expected no error, got %v", obs.workflowCompletes[0].err)
		}

		if obs.workflowCompletes[0].duration <= 0 {
			t.Error("expected positive duration")
		}
	})

	t.Run("notifies on step start and complete", func(t *testing.T) {
		obs := &mockObserver{}
		wf := New().
			WithObserver(obs).
			Step("step1", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			})).
			Step("step2", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input + 1, nil
			}))

		ctx := context.Background()
		_, err := ExecuteTyped[int](ctx, wf, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		obs.mu.Lock()
		defer obs.mu.Unlock()

		if len(obs.stepStarts) != 2 {
			t.Errorf("expected 2 step starts, got %d", len(obs.stepStarts))
		}

		if obs.stepStarts[0] != "step1" || obs.stepStarts[1] != "step2" {
			t.Errorf("unexpected step names: %v", obs.stepStarts)
		}

		if len(obs.stepCompletes) != 2 {
			t.Errorf("expected 2 step completes, got %d", len(obs.stepCompletes))
		}

		for i, complete := range obs.stepCompletes {
			if complete.err != nil {
				t.Errorf("step %d: expected no error, got %v", i, complete.err)
			}
			if complete.duration <= 0 {
				t.Errorf("step %d: expected positive duration", i)
			}
		}
	})

	t.Run("notifies on step retry", func(t *testing.T) {
		obs := &mockObserver{}
		attemptCount := 0

		wf := New().
			WithObserver(obs).
			WithRetry(RetryConfig{
				MaxAttempts:  3,
				InitialDelay: 10 * time.Millisecond,
				MaxDelay:     100 * time.Millisecond,
				Multiplier:   2.0,
			}).
			Step("retry_step", TypedStep(func(ctx context.Context, input int) (int, error) {
				attemptCount++
				if attemptCount < 3 {
					return 0, errors.New("temporary failure")
				}
				return input * 2, nil
			}))

		ctx := context.Background()
		_, err := ExecuteTyped[int](ctx, wf, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		obs.mu.Lock()
		defer obs.mu.Unlock()

		// Should have 2 retries (attempts 1 and 2 failed, attempt 3 succeeded)
		if len(obs.stepRetries) != 2 {
			t.Errorf("expected 2 retries, got %d", len(obs.stepRetries))
		}

		for i, retry := range obs.stepRetries {
			if retry.stepName != "retry_step" {
				t.Errorf("retry %d: expected step name 'retry_step', got %q", i, retry.stepName)
			}
			if retry.attempt != i+1 {
				t.Errorf("retry %d: expected attempt %d, got %d", i, i+1, retry.attempt)
			}
			if retry.err == nil {
				t.Errorf("retry %d: expected error, got nil", i)
			}
		}
	})

	t.Run("notifies on workflow error", func(t *testing.T) {
		obs := &mockObserver{}
		expectedErr := errors.New("step error")

		wf := New().
			WithObserver(obs).
			Step("failing_step", TypedStep(func(ctx context.Context, input int) (int, error) {
				return 0, expectedErr
			}))

		ctx := context.Background()
		_, err := ExecuteTyped[int](ctx, wf, 5)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		obs.mu.Lock()
		defer obs.mu.Unlock()

		if len(obs.workflowCompletes) != 1 {
			t.Errorf("expected 1 workflow complete, got %d", len(obs.workflowCompletes))
		}

		if obs.workflowCompletes[0].err == nil {
			t.Error("expected workflow complete with error")
		}

		if len(obs.stepCompletes) != 1 {
			t.Errorf("expected 1 step complete, got %d", len(obs.stepCompletes))
		}

		if !errors.Is(obs.stepCompletes[0].err, expectedErr) {
			t.Errorf("expected step error, got %v", obs.stepCompletes[0].err)
		}
	})

	t.Run("multiple observers receive all events", func(t *testing.T) {
		obs1 := &mockObserver{}
		obs2 := &mockObserver{}

		wf := New().
			WithObserver(obs1).
			WithObserver(obs2).
			Step("step1", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			}))

		ctx := context.Background()
		_, err := ExecuteTyped[int](ctx, wf, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for i, obs := range []*mockObserver{obs1, obs2} {
			obs.mu.Lock()
			if len(obs.workflowStarts) != 1 {
				t.Errorf("observer %d: expected 1 workflow start, got %d", i, len(obs.workflowStarts))
			}
			if len(obs.workflowCompletes) != 1 {
				t.Errorf("observer %d: expected 1 workflow complete, got %d", i, len(obs.workflowCompletes))
			}
			if len(obs.stepStarts) != 1 {
				t.Errorf("observer %d: expected 1 step start, got %d", i, len(obs.stepStarts))
			}
			if len(obs.stepCompletes) != 1 {
				t.Errorf("observer %d: expected 1 step complete, got %d", i, len(obs.stepCompletes))
			}
			obs.mu.Unlock()
		}
	})

	t.Run("observer panic doesn't break workflow execution", func(t *testing.T) {
		panicObs := &mockObserver{shouldPanic: true, panicOnMethod: "OnStepStart"}
		normalObs := &mockObserver{}

		wf := New().
			WithObserver(panicObs).
			WithObserver(normalObs).
			Step("step1", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input * 2, nil
			}))

		ctx := context.Background()
		result, err := ExecuteTyped[int](ctx, wf, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result != 10 {
			t.Errorf("expected 10, got %d", result)
		}

		// Normal observer should still receive events
		normalObs.mu.Lock()
		defer normalObs.mu.Unlock()

		if len(normalObs.stepStarts) != 1 {
			t.Errorf("expected normal observer to receive step start")
		}
	})

	t.Run("auto-generated workflow ID is observed", func(t *testing.T) {
		obs := &mockObserver{}
		wf := New().
			WithObserver(obs).
			Step("step1", TypedStep(func(ctx context.Context, input int) (int, error) {
				return input, nil
			}))

		ctx := context.Background()
		_, err := ExecuteTyped[int](ctx, wf, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		obs.mu.Lock()
		defer obs.mu.Unlock()

		if len(obs.workflowStarts) != 1 {
			t.Fatalf("expected 1 workflow start")
		}

		// Should have auto-generated ID (UUID format)
		if obs.workflowStarts[0] == "" {
			t.Error("expected non-empty workflow ID")
		}
	})
}
