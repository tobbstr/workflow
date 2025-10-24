# Workflow - Lightweight Workflow Library for Go Microservices

A type-safe, fluent workflow library designed for HTTP and gRPC endpoint implementations in Go. Build complex, maintainable request-response workflows with built-in retry logic, parallel execution, conditional branching, and observability hooks.

[![Go Version](https://img.shields.io/badge/go-1.24+-blue.svg)](https://go.dev)
[![Coverage](https://img.shields.io/badge/coverage-91%25-brightgreen.svg)](.)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

## Features

- ⚡️ **Type-Safe**: Compile-time and runtime type safety for workflow steps
- 🔄 **Retry Logic**: Configurable exponential backoff with smart error classification
- 🛡️ **Error Handling**: Allow specific errors to continue workflow execution with fallback values
- ⚙️ **Parallel Execution**: Run independent steps concurrently with flexible strategies
- 🔀 **Conditional Branching**: Route workflows based on runtime conditions
- 🏗️ **Composition**: Build complex, nested workflows with unlimited depth
- 📊 **Observability**: Opt-in hooks for metrics, logging, and tracing
- 🎯 **Context-Aware**: Full `context.Context` support for cancellation and deadlines
- 🧩 **Fluent API**: Clean, readable workflow definitions

## Installation

```bash
go get github.com/tobbstr/workflow
```

## Quick Start

**Important**: Workflow construction is expensive and should happen during service initialization, not inside endpoint handlers. Build your workflows once and reuse them across requests.

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "time"

    "github.com/tobbstr/workflow"
)

type User struct {
    ID    int    `json:"id"`
    Email string `json:"email"`
}

type CreateUserRequest struct {
    Email string `json:"email"`
}

// UserController constructs the workflow once during initialization
type UserController struct {
    createUserWorkflow *workflow.Workflow
}

func NewUserController() *UserController {
    // Build the workflow during service initialization (expensive operation)
    wf := workflow.New().
        WithID(workflow.WorkflowID("create-user")).
        WithRetry(workflow.RetryConfig{
            MaxAttempts:  3,
            InitialDelay: 100 * time.Millisecond,
            MaxDelay:     5 * time.Second,
            Multiplier:   2.0,
        }).
        Step("validate", workflow.TypedStep(validateEmail)).
        Step("create", workflow.TypedStep(createUser)).
        Step("notify", workflow.TypedStep(sendNotification))

    return &UserController{createUserWorkflow: wf}
}

// CreateUser is an HTTP handler that reuses the pre-built workflow
func (c *UserController) CreateUser(w http.ResponseWriter, r *http.Request) {
    var req CreateUserRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid request", http.StatusBadRequest)
        return
    }

    // Execute the pre-built workflow (fast operation)
    user, err := workflow.ExecuteTyped[User](r.Context(), c.createUserWorkflow, req.Email)
    if err != nil {
        log.Printf("creating user: %v", err)
        http.Error(w, "failed to create user", http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(user)
}

func main() {
    // Initialize controller with pre-built workflow
    userController := NewUserController()

    http.HandleFunc("/users", userController.CreateUser)
    log.Fatal(http.ListenAndServe(":8080", nil))
}

func validateEmail(ctx context.Context, email string) (string, error) {
    if email == "" {
        return "", fmt.Errorf("email is required")
    }
    return email, nil
}

func createUser(ctx context.Context, email string) (User, error) {
    // Simulate database call
    return User{ID: 123, Email: email}, nil
}

func sendNotification(ctx context.Context, user User) (User, error) {
    // Simulate notification service call
    log.Printf("Sending notification to user %d", user.ID)
    return user, nil
}
```

## Core Concepts

### Type-Safe Steps

Steps are the building blocks of workflows. Each step has typed inputs and outputs:

```go
// TypedStep creates an unnamed step
step := workflow.TypedStep(func(ctx context.Context, input int) (string, error) {
    return fmt.Sprintf("result: %d", input), nil
})

// NamedTypedStep creates a step with a default name
authStep := workflow.NamedTypedStep("authenticate", func(ctx context.Context, token string) (User, error) {
    // Authentication logic
    return user, nil
})
```

### Retry Policies

Configure retry behavior at the workflow or step level:

```go
// Standard gRPC retry policy
grpcRetry := workflow.RetryConfig{
    MaxAttempts:  5,
    InitialDelay: 100 * time.Millisecond,
    MaxDelay:     2 * time.Second,
    Multiplier:   2.0,
    Condition: workflow.RetryOnGRPCCodes(
        codes.Unavailable,
        codes.ResourceExhausted,
        codes.DeadlineExceeded,
    ),
}

wf := workflow.New().
    WithRetry(grpcRetry).  // Default for all steps
    Step("api_call", workflow.TypedStep(callAPI),
        workflow.WithRetry(customRetry)).  // Override for this step
    Step("database", workflow.TypedStep(saveDB),
        workflow.NoRetry())  // Explicitly disable retry
```

### Error Handling with AllowError

Allow specific errors to continue workflow execution with custom fallback values. This is useful for non-critical operations or FSM transitions where alternative paths exist:

```go
var ErrTransitionRejected = errors.New("transition rejected")

type State struct {
    Name string
    Path []string
}

wf := workflow.New().
    // Try primary transition, continue with input if rejected
    Step("try_primary", workflow.TypedStep(tryPrimaryTransition),
        workflow.AllowError(
            func(err error) bool { return errors.Is(err, ErrTransitionRejected) },
            func(ctx context.Context, state State, err error) State {
                // Log rejection, return input state for alternative path
                log.Printf("Primary transition rejected: %v", err)
                state.Path = append(state.Path, "primary_rejected")
                return state
            },
        )).
    // Try alternative transition
    Step("try_alternative", workflow.TypedStep(tryAlternativeTransition))
```

**Key Points:**
- AllowError check happens **before** any retry logic
- If condition returns true, the fallback function provides the value to pass to the next step
- If condition returns false, normal error handling (retry/fail) applies
- Fallback function has access to context, input, and error for flexible recovery
- Observer notifications include `OnStepErrorAllowed` for bypassed errors

### Parallel Execution

Execute independent steps concurrently:

```go
// Returns []UserProfile
wf.Step("fetch", workflow.Parallel[UserContext, UserProfile](
    workflow.ParallelConfig{Strategy: workflow.FirstSuccess},
    fetchFromPrimaryDB,
    fetchFromReplicaDB,
    fetchFromCache,
))

// Returns single UserProfile using merger
wf.Step("fetch", workflow.ParallelMerge[UserContext, UserProfile](
    workflow.ParallelConfig{Strategy: workflow.AllMustSucceed},
    workflow.FirstResult[UserProfile](),
    fetchFromPrimaryDB,
    fetchFromReplicaDB,
))
```

### Conditional Branching

Route workflows based on runtime conditions:

```go
wf.Step("route", workflow.If[Request, Response](
    func(r Request) bool { return r.IsPremium },
    workflow.TypedStep(processPremiumUser),  // then branch
    workflow.TypedStep(processStandardUser), // else branch
))
```

### Workflow Composition

Build complex, nested workflows:

```go
// Inline composition
complexStep := workflow.Compose[Input, Output](
    workflow.TypedStep(step1),
    workflow.TypedStep(step2),
    workflow.TypedStep(step3),
)

// Sub-workflow composition
subWorkflow := workflow.New().
    WithID(workflow.WorkflowID("payment")).
    Step("validate", workflow.TypedStep(validatePayment)).
    Step("process", workflow.TypedStep(processPayment))

mainWorkflow := workflow.New().
    Step("prepare", workflow.TypedStep(prepareOrder)).
    Step(workflow.Auto, subWorkflow.AsStep()).  // Uses "payment" as step name
    Step("complete", workflow.TypedStep(completeOrder))
```

### Observability

Add custom metrics, logging, or tracing:

```go
type metricsObserver struct {
    metrics MetricsClient
}

func (m *metricsObserver) OnStepComplete(ctx context.Context, stepName workflow.StepName, duration time.Duration, err error) {
    tags := map[string]string{"step": string(stepName)}
    if err != nil {
        tags["status"] = "error"
    } else {
        tags["status"] = "success"
    }
    m.metrics.Timing("workflow.step.duration", duration, tags)
}

// Implement other Observer methods...

wf := workflow.New().
    WithObserver(&metricsObserver{metrics: metricsClient}).
    Step("step1", workflow.TypedStep(step1Fn))
```

## Advanced Examples

### FSM Transitions with Error Handling

Handle finite state machine transitions with fallback paths when transitions are rejected:

```go
type State struct {
    Name   string
    Status string
    Path   []string
}

var ErrTransitionRejected = errors.New("transition rejected")

func tryPrimaryTransition(ctx context.Context, state State) (State, error) {
    // Try to transition to primary state
    if state.Status == "pending" {
        return State{}, fmt.Errorf("primary path: %w", ErrTransitionRejected)
    }
    state.Status = "primary_complete"
    state.Path = append(state.Path, "primary")
    return state, nil
}

func tryAlternativeTransition(ctx context.Context, state State) (State, error) {
    state.Status = "alternative_complete"
    state.Path = append(state.Path, "alternative")
    return state, nil
}

func finalizeState(ctx context.Context, state State) (State, error) {
    state.Path = append(state.Path, "finalized")
    return state, nil
}

// Build workflow with AllowError for graceful fallback
wf := workflow.New().
    WithID(workflow.WorkflowID("fsm-workflow")).
    Step("try_primary", workflow.TypedStep(tryPrimaryTransition),
        workflow.AllowError(
            func(err error) bool { return errors.Is(err, ErrTransitionRejected) },
            func(ctx context.Context, state State, err error) State {
                // Primary rejected, mark in path and continue
                log.Printf("Primary transition rejected: %v", err)
                state.Path = append(state.Path, "primary_rejected")
                return state
            },
        )).
    Step("try_alternative", workflow.TypedStep(tryAlternativeTransition)).
    Step("finalize", workflow.TypedStep(finalizeState))

// Execute workflow - will follow alternative path if primary is rejected
result, err := workflow.ExecuteTyped[State](ctx, wf, State{
    Name:   "initial",
    Status: "pending",
    Path:   []string{"start"},
})
// Result path: ["start", "primary_rejected", "alternative", "finalized"]
```

### Reusable Retry Policies

```go
var (
    // Standard gRPC retry
    StandardGRPCRetry = workflow.RetryConfig{
        MaxAttempts:  5,
        InitialDelay: 100 * time.Millisecond,
        MaxDelay:     2 * time.Second,
        Multiplier:   2.0,
        Condition: workflow.RetryOnGRPCCodes(
            codes.Unavailable,
            codes.ResourceExhausted,
            codes.DeadlineExceeded,
        ),
    }
    
    // Network-only retry
    NetworkOnlyRetry = workflow.RetryConfig{
        MaxAttempts:  3,
        InitialDelay: 50 * time.Millisecond,
        MaxDelay:     1 * time.Second,
        Multiplier:   2.0,
        Condition:    workflow.RetryOnNetworkErrors(),
    }
)

// Use across multiple workflows
wf := workflow.New().
    Step("fetch_user", workflow.TypedStep(fetchUser),
        workflow.WithRetry(StandardGRPCRetry)).
    Step("query_db", workflow.TypedStep(queryDB),
        workflow.WithRetry(NetworkOnlyRetry))
```

### Tree-Like Workflows

```go
// Shipping decision workflow (nested 3 levels)
shippingDecision := workflow.New().
    WithID(workflow.WorkflowID("shipping")).
    Step("route", workflow.If[Order, Fulfillment](
        func(o Order) bool { return o.IsExpress },
        workflow.Compose[Order, Fulfillment](
            workflow.TypedStep(fastTrack),
            workflow.TypedStep(priorityNotify),
        ),
        workflow.TypedStep(standardShipping),
    ))

// Premium workflow using shipping decision
premiumFlow := workflow.New().
    WithID(workflow.WorkflowID("premium")).
    Step(workflow.Auto, shippingDecision.AsStep()).
    Step("discount", workflow.TypedStep(applyDiscount))

// Main workflow
mainWorkflow := workflow.New().
    Step("validate", workflow.TypedStep(validateOrder)).
    Step("process", workflow.If[Order, Result](
        func(o Order) bool { return o.IsPremium },
        premiumFlow.AsStep(),
        workflow.TypedStep(standardProcess),
    )).
    Step("record", workflow.TypedStep(recordOrder))
```

### Combining Parallel and Conditional

```go
wf := workflow.New().
    // Fetch data from multiple sources in parallel
    Step("fetch", workflow.Parallel[Request, Data](
        workflow.ParallelConfig{Strategy: workflow.AtLeastN, MinSuccessCount: 2},
        fetchFromAPI1,
        fetchFromAPI2,
        fetchFromAPI3,
    )).
    // Conditionally process based on result count
    Step("process", workflow.If[[]Data, Result](
        func(results []Data) bool { return len(results) >= 2 },
        workflow.TypedStep(processMultipleSources),
        workflow.TypedStep(processSingleSource),
    ))
```

### AllowError with Retry and Observability

```go
var ErrCacheUnavailable = errors.New("cache unavailable")

type metricsObserver struct {
    metrics MetricsClient
}

func (m *metricsObserver) OnStepErrorAllowed(ctx context.Context, stepName StepName, err error) {
    m.metrics.Increment("workflow.error_allowed", map[string]string{
        "step":  string(stepName),
        "error": err.Error(),
    })
}

wf := workflow.New().
    WithObserver(&metricsObserver{metrics: client}).
    WithRetry(workflow.RetryConfig{
        MaxAttempts:  3,
        InitialDelay: 100 * time.Millisecond,
        MaxDelay:     1 * time.Second,
        Multiplier:   2.0,
    }).
    // Try to get data from cache, allow cache miss
    Step("get_cache", workflow.TypedStep(getFromCache),
        workflow.AllowError(
            func(err error) bool { return errors.Is(err, ErrCacheUnavailable) },
            func(ctx context.Context, key string, err error) Data {
                // Cache miss is acceptable, return empty data
                return Data{CacheHit: false}
            },
        )).
    // Fetch from DB if cache missed (retries on network errors)
    Step("get_db", workflow.TypedStep(getFromDB))

// If cache fails with ErrCacheUnavailable:
// - No retry (error is allowed)
// - Fallback returns empty Data
// - Workflow continues to DB step
// - DB step will retry on network errors
```

## Testing

Run the test suite:

```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run with race detection
go test -race ./...

# Run verbose
go test -v ./...
```

Current test coverage: **91.2%**

## Design Philosophy

- **Lightweight**: No saga-style complexity, designed for request-response cycles
- **Type-Safe**: Catch errors at compile-time and runtime
- **Composable**: Build complex workflows from simple, reusable components
- **Observable**: Opt-in metrics and tracing without compromising simplicity
- **Idiomatic**: Follows Go best practices and microservices patterns

## API Documentation

All exported types and functions have comprehensive godoc comments with examples. View the full API documentation:

```bash
go doc github.com/tobbstr/workflow
```

Or browse online at: [pkg.go.dev/github.com/tobbstr/workflow](https://pkg.go.dev/github.com/tobbstr/workflow)

## Non-Goals

The following are explicitly out of scope:

- Persistent state (all state is in-memory only)
- Saga pattern (no distributed transactions or compensation)
- Async/long-running workflows (only synchronous, short-lived workflows)
- Workflow versioning or migration
- Built-in circuit breakers or rate limiting

## Contributing

Contributions are welcome! Please ensure:

1. All tests pass: `go test ./...`
2. Code is formatted: `go fmt ./...`
3. Linter passes: `golangci-lint run`
4. Test coverage >= 90%

## License

MIT License - see [LICENSE](LICENSE) file for details.

## Acknowledgments

Inspired by modern workflow patterns in microservices architecture, with a focus on simplicity, type safety, and developer experience.
