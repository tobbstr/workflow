# Workflow Library Implementation Plan

## Overview

A lightweight workflow library for microservices, primarily designed for use in HTTP and gRPC endpoint implementations. The library provides a fluent API for defining synchronous, short-lived workflows with support for conditional branching, parallel execution, and configurable retry policies.

## Goals

- **Lightweight**: No saga-style complexity, designed for request-response cycles
- **Type-safe**: Compile-time and runtime type safety for workflow steps
- **Flexible**: Support sequential, parallel, and conditional execution
- **Observable**: Opt-in metrics and tracing hooks
- **Idiomatic**: Follow Go best practices and microservices patterns

## Key Requirements

### Execution Model
- Synchronous execution (blocking until complete)
- Short-lived workflows (within HTTP/gRPC request timeout)
- Respect `context.Context` for cancellation and deadlines

### Control Flow
- Sequential step execution
- Conditional branching (if-then-else)
- Parallel execution with configurable success criteria
- Steps in parallel execution must be truly independent

### State Management
- In-memory state only
- Type-safe state passing between steps
- Each step has typed inputs and outputs

### Error Handling
- Retry with exponential backoff
- Configurable per-workflow (default) and per-step (override or opt-out)
- No compensation/rollback (keep it simple)

### Observability
- Opt-in metrics via hooks
- Opt-in tracing via hooks
- Explicit hooks in builder API

## Design Decisions

### Type Safety Approach: Option B (Type-Erased with Runtime Validation)

**Decision**: Use type-erased workflow with runtime type safety validation.

**Rationale**:
- Simpler API compared to fully generic approach
- Easier to implement parallel and conditional branching
- More flexible for dynamic workflow composition
- Performance difference is negligible (<0.001% in real microservices scenarios)
- Fail-fast validation during workflow construction catches type mismatches early
- Users can get type safety at execution with `ExecuteTyped[T]()` helper

**Implementation**:
```go
// Workflow without generic parameters
type Workflow struct {
    steps        []Step
    inputType    reflect.Type
    outputType   reflect.Type
    defaultRetry *RetryConfig
}

// Step interface with type information
type Step interface {
    Name() string
    Execute(ctx context.Context, input any) (any, error)
    InputType() reflect.Type
    OutputType() reflect.Type
}

// TypedStep wrapper
func TypedStep[In, Out any](fn func(context.Context, In) (Out, error)) Step {
    return &typedStep[In, Out]{fn: fn}
}

// Type-safe execution helper
func ExecuteTyped[Out any](ctx context.Context, w *Workflow, input any) (Out, error) {
    result, err := w.Execute(ctx, input)
    if err != nil {
        var zero Out
        return zero, err
    }
    
    typedResult, ok := result.(Out)
    if !ok {
        var zero Out
        return zero, fmt.Errorf("workflow output type mismatch: expected %T, got %T", 
            zero, result)
    }
    
    return typedResult, nil
}
```

### Retry Policy Design: Functional Options Pattern

**Decision**: Use functional options pattern with three-level retry configuration:
1. No workflow default (no retry by default)
2. Workflow-level default (applies to all steps)
3. Per-step override or opt-out

**Rationale**:
- Single `Step()` method keeps API clean
- Composable options allow future extensibility
- Explicit opt-out (`NoRetry()`) makes intent clear for non-idempotent operations
- Go-idiomatic pattern

**Configuration Hierarchy**:
```
Step Configuration Priority (highest to lowest):
1. Step-level NoRetry() - explicitly disable retry for this step
2. Step-level WithRetry(config) - override with custom config for this step
3. Workflow-level WithRetry(config) - default for all steps
4. No retry - if no configuration at any level
```

**API Design**:
```go
// RetryConfig defines exponential backoff parameters
type RetryConfig struct {
    MaxAttempts  int           // Maximum number of attempts (including initial)
    InitialDelay time.Duration // Initial delay before first retry
    MaxDelay     time.Duration // Maximum delay between retries
    Multiplier   float64       // Exponential backoff multiplier
}

// StepOption configures individual step behavior
type StepOption func(*stepConfig)

// WithRetry overrides workflow's retry policy for a specific step
func WithRetry(config RetryConfig) StepOption

// NoRetry explicitly disables retry for a specific step
func NoRetry() StepOption

// Usage examples:
wf := workflow.New().
    WithRetry(workflow.RetryConfig{MaxAttempts: 3, InitialDelay: 100*time.Millisecond}).
    Step("fetch_user", workflow.TypedStep(fetchUserFn)).  // Uses workflow default
    Step("check_perm", workflow.TypedStep(checkPermFn),
        workflow.WithRetry(workflow.RetryConfig{MaxAttempts: 5})).  // Override
    Step("mutate_db", workflow.TypedStep(mutateDBFn),
        workflow.NoRetry()).  // Opt out - DB mutations should be idempotent
    Step("publish_event", workflow.TypedStep(publishEventFn))  // Uses workflow default
```

**Validation**:
- Validate `RetryConfig` during workflow construction (fail-fast with panic)
- `MaxAttempts >= 1`
- `InitialDelay > 0`
- `Multiplier >= 1.0`
- `MaxDelay >= InitialDelay`
- `Condition` can be `nil` (means retry all errors)

### Retry Conditions: Reusable Error Classification

**Decision**: Add `RetryCondition` function to `RetryConfig` to determine which errors are retryable.

**Rationale**:
- Different error types require different retry strategies
- Transient errors (network issues, rate limits) should be retried
- Permanent errors (invalid input, not found) should not be retried
- gRPC and HTTP errors have well-defined retryable status codes
- Retry policies should be reusable across workflows and services

**Key Features**:
- `RetryCondition` is a simple function: `func(error) bool`
- If `Condition` is `nil`, retry all errors (default, backward compatible)
- Provide built-in conditions for common cases (gRPC codes, HTTP status)
- Composable with AND/OR/NOT logic for complex conditions
- Users can create custom conditions for domain-specific errors

**Usage Pattern**:
```go
// Define reusable gRPC retry policy
var DefaultGRPCRetry = workflow.RetryConfig{
    MaxAttempts:  5,
    InitialDelay: 100 * time.Millisecond,
    MaxDelay:     2 * time.Second,
    Multiplier:   2.0,
    Condition: workflow.RetryOnGRPCCodes(
        codes.Unavailable,
        codes.ResourceExhausted,
        codes.DeadlineExceeded,
        codes.Internal,
    ),
}

// Use across multiple steps
wf := workflow.New().
    Step("call_user_svc", workflow.TypedStep(callUserService),
        workflow.WithRetry(DefaultGRPCRetry)).
    Step("call_inventory_svc", workflow.TypedStep(callInventoryService),
        workflow.WithRetry(DefaultGRPCRetry))

// Combine conditions
complexCondition := workflow.CombineConditions(
    workflow.RetryOnNetworkErrors(),
    workflow.RetryOnGRPCCodes(codes.Unavailable),
)

// Custom condition
func retryOnBusinessError(err error) bool {
    var businessErr *MyRetryableError
    return errors.As(err, &businessErr) && businessErr.IsRetryable()
}
```

### Workflow Composition: Hybrid Approach (Compose + AsStep)

**Decision**: Provide both `Compose()` for inline composition and `AsStep()` for complex sub-workflows.

**Rationale**:
- Supports tree-like workflows with unlimited nesting depth
- Users can choose the right tool for the job:
  - `Compose()` for simple inline sequences (2-3 steps)
  - `AsStep()` for complex, reusable sub-workflows
- Natural fit for microservices patterns (modularity and reusability)
- Easy refactoring path (inline → extract to sub-workflow)
- Both patterns work seamlessly with all step types (parallel, conditional, retry)

**Usage Pattern**:
```go
// Simple inline composition with Compose()
workflow.If(condition,
    workflow.Compose[A, B](step1, step2, step3),  // Inline then branch
    workflow.Compose[A, B](step4, step5),         // Inline else branch
)

// Complex reusable sub-workflow with AsStep()
subWorkflow := workflow.New().
    Step("step1", workflow.TypedStep(fn1)).
    If("nested", condition, thenStep, elseStep).
    Step("step3", workflow.TypedStep(fn3))

mainWorkflow := workflow.New().
    Step("main1", workflow.TypedStep(mainFn1)).
    Step("sub", subWorkflow.AsStep()).  // Reusable sub-workflow
    Step("main2", workflow.TypedStep(mainFn2))

// Mixing both patterns
workflow.New().
    If("route", condition,
        subWorkflow.AsStep(),  // Complex branch as sub-workflow
        workflow.Compose[A, B](simple1, simple2),  // Simple branch inline
    )
```

**Implementation Details**:
- `Compose[In, Out]()` validates type compatibility of sequential steps
- `AsStep()` exposes workflow's input/output types via `InputType()` and `OutputType()`
- Both create Steps that work with the existing workflow builder API
- Type validation happens at construction time (fail-fast)
- Nested workflows integrate with observers (emit their own step events)

## Core Types and Interfaces

### Workflow
```go
type Workflow struct {
    steps        []*workflowStep
    inputType    reflect.Type
    outputType   reflect.Type
    defaultRetry *RetryConfig
    observers    []Observer
}

type workflowStep struct {
    step  Step
    name  string
    retry *RetryConfig
}

func New() *Workflow
func (w *Workflow) WithRetry(config RetryConfig) *Workflow
func (w *Workflow) Step(name string, step Step, opts ...StepOption) *Workflow
func (w *Workflow) Parallel(name string, config ParallelConfig, steps ...Step) *Workflow
func (w *Workflow) If(name string, condition Condition, thenStep, elseStep Step) *Workflow
func (w *Workflow) WithObserver(observer Observer) *Workflow
func (w *Workflow) Execute(ctx context.Context, input any) (any, error)
```

### Step
```go
type Step interface {
    Name() string
    Execute(ctx context.Context, input any) (any, error)
    InputType() reflect.Type
    OutputType() reflect.Type
}

func TypedStep[In, Out any](fn func(context.Context, In) (Out, error)) Step
```

### Parallel Execution
```go
type ParallelConfig struct {
    Strategy ParallelStrategy
    MinSuccessCount int  // For AtLeastN strategy
}

type ParallelStrategy int

const (
    AllMustSucceed ParallelStrategy = iota  // All parallel steps must succeed
    AtLeastN                                  // At least N steps must succeed
    FirstSuccess                              // First successful step wins
)

// Parallel step - all sub-steps must have same input and output types
func Parallel[In, Out any](config ParallelConfig, steps ...func(context.Context, In) (Out, error)) Step
```

### Conditional Execution
```go
type Condition func(input any) bool

// If step - both branches must have same input and output types
// Branches can be any Step (TypedStep, Compose, workflow.AsStep(), etc.)
func If[In, Out any](
    condition func(In) bool,
    thenStep Step,
    elseStep Step,
) Step
```

### Workflow Composition
```go
// Compose creates a Step from a sequence of steps
// Useful for inline composition of simple branches
func Compose[In, Out any](steps ...Step) Step

// AsStep converts a workflow into a Step
// Useful for complex reusable sub-workflows
func (w *Workflow) AsStep() Step
```

### Retry Configuration
```go
// RetryCondition determines if an error should trigger a retry
type RetryCondition func(error) bool

type RetryConfig struct {
    MaxAttempts  int
    InitialDelay time.Duration
    MaxDelay     time.Duration
    Multiplier   float64
    Condition    RetryCondition  // If nil, retry all errors (default)
}

func (c RetryConfig) Validate() error

type StepOption func(*stepConfig)

func WithRetry(config RetryConfig) StepOption
func NoRetry() StepOption
```

### Built-in Retry Conditions
```go
// RetryOnAny retries on all errors (default behavior if Condition is nil)
func RetryOnAny() RetryCondition

// RetryOnGRPCCodes retries only if error is a gRPC status with matching code
func RetryOnGRPCCodes(codes ...codes.Code) RetryCondition

// RetryOnHTTPStatus retries only if error contains matching HTTP status
func RetryOnHTTPStatus(statuses ...int) RetryCondition

// RetryOnNetworkErrors retries on network/timeout errors
func RetryOnNetworkErrors() RetryCondition

// RetryOnContextErrors retries on context deadline/cancelled (usually NOT wanted)
func RetryOnContextErrors() RetryCondition

// CombineConditions combines multiple conditions with OR logic
func CombineConditions(conditions ...RetryCondition) RetryCondition

// AllConditions combines multiple conditions with AND logic
func AllConditions(conditions ...RetryCondition) RetryCondition

// Not inverts a condition
func Not(condition RetryCondition) RetryCondition
```

### Observability
```go
type Observer interface {
    OnWorkflowStart(ctx context.Context, workflowID string)
    OnWorkflowComplete(ctx context.Context, workflowID string, duration time.Duration, err error)
    OnStepStart(ctx context.Context, stepName string)
    OnStepComplete(ctx context.Context, stepName string, duration time.Duration, err error)
    OnStepRetry(ctx context.Context, stepName string, attempt int, err error)
}

// NoopObserver provides default implementation
type NoopObserver struct{}
```

## Implementation Phases

**Note**: Phases should be implemented sequentially, as later phases depend on earlier ones. Phase 4 (Composition) depends on Phases 1-3 being complete, as it builds upon the basic workflow execution, retry logic, and parallel execution capabilities.

### Phase 1: Core Framework
**Goal**: Basic workflow execution with type safety

**Tasks**:
- [ ] Define core types (`Workflow`, `Step`, `stepConfig`)
- [ ] Implement `TypedStep[In, Out]` wrapper
- [ ] Implement `Step()` method with type validation
- [ ] Implement `Execute()` method
- [ ] Implement `ExecuteTyped[Out]()` helper
- [ ] Add basic error handling
- [ ] Unit tests for type safety validation

**Acceptance Criteria**:
- Can create workflow with sequential steps
- Type mismatches detected during construction (panic)
- Workflow executes and passes data between steps
- Context propagation works correctly

### Phase 2: Retry Logic
**Goal**: Exponential backoff retry with configurable policies and conditions

**Tasks**:
- [ ] Define `RetryConfig` struct with `Condition` field
- [ ] Define `RetryCondition` type
- [ ] Implement `RetryConfig.Validate()`
- [ ] Implement `WithRetry()` on workflow
- [ ] Implement `WithRetry()` and `NoRetry()` step options
- [ ] Implement retry executor with exponential backoff
- [ ] Implement retry condition evaluation (nil condition = retry all)
- [ ] Add jitter to prevent thundering herd
- [ ] Implement built-in retry conditions:
  - [ ] `RetryOnAny()`
  - [ ] `RetryOnGRPCCodes(codes ...codes.Code)`
  - [ ] `RetryOnHTTPStatus(statuses ...int)`
  - [ ] `RetryOnNetworkErrors()`
  - [ ] `RetryOnContextErrors()`
- [ ] Implement condition combinators:
  - [ ] `CombineConditions()` (OR logic)
  - [ ] `AllConditions()` (AND logic)
  - [ ] `Not()` (invert)
- [ ] Unit tests for retry logic
- [ ] Unit tests for configuration hierarchy
- [ ] Unit tests for each built-in condition
- [ ] Unit tests for condition combinators
- [ ] Unit tests for custom conditions

**Acceptance Criteria**:
- Workflow-level retry applies to all steps by default
- Step-level override works correctly
- Step-level opt-out works correctly
- Exponential backoff calculated correctly
- Context cancellation stops retries immediately
- Retry conditions correctly classify errors
- Non-retryable errors fail immediately (no retry)
- Built-in conditions work with gRPC and HTTP errors
- Custom conditions can be created and used

### Phase 3: Parallel Execution
**Goal**: Execute independent steps concurrently

**Tasks**:
- [ ] Define `ParallelConfig` and `ParallelStrategy`
- [ ] Implement `Parallel[In, Out]()` step constructor
- [ ] Implement parallel execution with goroutines
- [ ] Implement `AllMustSucceed` strategy
- [ ] Implement `AtLeastN` strategy
- [ ] Implement `FirstSuccess` strategy
- [ ] Add error aggregation for failures
- [ ] Unit tests for each strategy
- [ ] Unit tests for context cancellation

**Acceptance Criteria**:
- All parallel steps receive same input
- Steps execute concurrently
- Results collected according to strategy
- First error cancels other goroutines (for AllMustSucceed)
- Context cancellation propagates to all parallel steps

### Phase 4: Conditional Execution & Composition
**Goal**: Branch workflow based on runtime conditions and support nested workflows

**Tasks**:
- [ ] Define `Condition` type
- [ ] Implement `If[In, Out]()` step constructor (accepts Steps, not functions)
- [ ] Implement `Compose[In, Out]()` for functional composition
- [ ] Implement `AsStep()` method on Workflow
- [ ] Implement conditional evaluation
- [ ] Ensure type safety for branches (both single and composite)
- [ ] Unit tests for true branch
- [ ] Unit tests for false branch
- [ ] Unit tests for condition evaluation
- [ ] Unit tests for nested workflows (3+ levels deep)
- [ ] Unit tests for Compose with type validation

**Acceptance Criteria**:
- Condition evaluated with correct input
- Only selected branch executes
- Both branches type-checked at construction
- Compose() validates step type compatibility
- AsStep() exposes correct input/output types
- Nested workflows execute correctly (unlimited depth)
- Works with other step types (parallel, retry)
- Observers track nested workflow steps

### Phase 5: Observability
**Goal**: Opt-in hooks for metrics and tracing

**Tasks**:
- [ ] Define `Observer` interface
- [ ] Implement `NoopObserver`
- [ ] Implement `WithObserver()` method
- [ ] Add observer calls in workflow execution
- [ ] Add observer calls in step execution
- [ ] Add observer calls in retry logic
- [ ] Add observer calls in parallel execution
- [ ] Unit tests with mock observer
- [ ] Example observer implementations (metrics, logging)

**Acceptance Criteria**:
- Observer receives all lifecycle events
- Multiple observers can be registered
- Observer errors don't break workflow execution
- Timing measurements are accurate

### Phase 6: Documentation and Examples
**Goal**: Complete documentation and usage examples

**Tasks**:
- [ ] Write package documentation
- [ ] Write API documentation for all exported types
- [ ] Create example: Simple sequential workflow
- [ ] Create example: Workflow with retry policies and conditions
- [ ] Create example: Reusable retry policies and advanced conditions
- [ ] Create example: Parallel execution
- [ ] Create example: Conditional branching (simple)
- [ ] Create example: Tree-like workflow with nested branches (Compose + AsStep)
- [ ] Create example: Complete microservice handler with observability
- [ ] Create example: Custom observer for metrics
- [ ] Write README with quick start guide
- [ ] Write migration/upgrade guide (if applicable)

**Acceptance Criteria**:
- All exported types have godoc comments
- Examples compile and run successfully
- README provides clear getting started guide
- Examples cover all major features including composition and retry conditions
- Tree-like workflow example demonstrates 3+ levels of nesting
- Retry condition examples show gRPC, HTTP, and custom conditions

## Usage Examples

### Example 1: Simple Sequential Workflow
```go
type ValidateInput struct {
    Username string
    Email    string
}

type User struct {
    ID       int
    Username string
    Email    string
}

type Result struct {
    Success bool
    UserID  int
}

func HandleCreateUser(ctx context.Context, input ValidateInput) (Result, error) {
    wf := workflow.New().
        Step("validate", workflow.TypedStep(validateInput)).
        Step("fetch_user", workflow.TypedStep(fetchUser)).
        Step("create_user", workflow.TypedStep(createUser))
    
    return workflow.ExecuteTyped[Result](ctx, wf, input)
}

func validateInput(ctx context.Context, input ValidateInput) (ValidateInput, error) {
    if input.Username == "" {
        return ValidateInput{}, errors.New("username required")
    }
    return input, nil
}

func fetchUser(ctx context.Context, input ValidateInput) (User, error) {
    // Check if user exists
    return User{ID: 0, Username: input.Username, Email: input.Email}, nil
}

func createUser(ctx context.Context, user User) (Result, error) {
    // Create user in database
    return Result{Success: true, UserID: 123}, nil
}
```

### Example 2: Workflow with Retry Policies and Conditions
```go
func HandleOrderProcessing(ctx context.Context, order Order) (OrderResult, error) {
    // Default retry for transient failures - retry on any error
    defaultRetry := workflow.RetryConfig{
        MaxAttempts:  3,
        InitialDelay: 100 * time.Millisecond,
        MaxDelay:     5 * time.Second,
        Multiplier:   2.0,
        Condition:    nil,  // nil = retry all errors
    }
    
    // Retry policy for gRPC external services - only retry specific codes
    grpcRetry := workflow.RetryConfig{
        MaxAttempts:  5,
        InitialDelay: 50 * time.Millisecond,
        MaxDelay:     2 * time.Second,
        Multiplier:   1.5,
        Condition: workflow.RetryOnGRPCCodes(
            codes.Unavailable,       // Service temporarily unavailable
            codes.ResourceExhausted, // Rate limited
            codes.DeadlineExceeded,  // Timeout - might succeed with retry
        ),
    }
    
    // HTTP retry policy - retry on transient HTTP errors
    httpRetry := workflow.RetryConfig{
        MaxAttempts:  3,
        InitialDelay: 100 * time.Millisecond,
        MaxDelay:     3 * time.Second,
        Multiplier:   2.0,
        Condition: workflow.RetryOnHTTPStatus(
            http.StatusTooManyRequests,    // 429
            http.StatusServiceUnavailable,  // 503
            http.StatusGatewayTimeout,      // 504
        ),
    }
    
    wf := workflow.New().
        WithRetry(defaultRetry).
        Step("validate", workflow.TypedStep(validateOrder)).
        Step("check_inventory", workflow.TypedStep(checkInventoryGRPC), 
            workflow.WithRetry(grpcRetry)).  // gRPC service with smart retry
        Step("check_pricing", workflow.TypedStep(checkPricingHTTP),
            workflow.WithRetry(httpRetry)).  // HTTP API with smart retry
        Step("reserve_inventory", workflow.TypedStep(reserveInventory),
            workflow.NoRetry()).  // No retry for idempotent mutations
        Step("process_payment", workflow.TypedStep(processPayment)).
        Step("publish_event", workflow.TypedStep(publishOrderEvent))
    
    return workflow.ExecuteTyped[OrderResult](ctx, wf, order)
}
```

### Example 3: Reusable Retry Policies and Advanced Conditions
```go
// Define reusable retry policies at package level or in a config
var (
    // Standard gRPC retry for all microservice calls
    StandardGRPCRetry = workflow.RetryConfig{
        MaxAttempts:  5,
        InitialDelay: 100 * time.Millisecond,
        MaxDelay:     2 * time.Second,
        Multiplier:   2.0,
        Condition: workflow.RetryOnGRPCCodes(
            codes.Unavailable,
            codes.ResourceExhausted,
            codes.DeadlineExceeded,
            codes.Internal,
        ),
    }
    
    // Conservative retry - only network errors
    NetworkOnlyRetry = workflow.RetryConfig{
        MaxAttempts:  3,
        InitialDelay: 50 * time.Millisecond,
        MaxDelay:     1 * time.Second,
        Multiplier:   2.0,
        Condition:    workflow.RetryOnNetworkErrors(),
    }
    
    // Complex condition: network errors OR specific gRPC codes, but NOT context errors
    SmartRetryCondition = workflow.CombineConditions(
        workflow.RetryOnNetworkErrors(),
        workflow.RetryOnGRPCCodes(codes.Unavailable, codes.ResourceExhausted),
    )
)

func HandleComplexWorkflow(ctx context.Context, input ComplexInput) (ComplexOutput, error) {
    // Custom condition for business logic errors
    retryOnBusinessLogic := func(err error) bool {
        var bizErr *BusinessLogicError
        if errors.As(err, &bizErr) {
            return bizErr.IsTransient  // Only retry transient business errors
        }
        return false
    }
    
    customRetry := workflow.RetryConfig{
        MaxAttempts:  3,
        InitialDelay: 200 * time.Millisecond,
        MaxDelay:     2 * time.Second,
        Multiplier:   2.0,
        Condition:    retryOnBusinessLogic,
    }
    
    wf := workflow.New().
        // Use standard gRPC retry for all external service calls
        Step("fetch_user", workflow.TypedStep(fetchUserFromService),
            workflow.WithRetry(StandardGRPCRetry)).
        Step("fetch_account", workflow.TypedStep(fetchAccountFromService),
            workflow.WithRetry(StandardGRPCRetry)).
        // Network-only retry for database calls
        Step("query_db", workflow.TypedStep(queryDatabase),
            workflow.WithRetry(NetworkOnlyRetry)).
        // Custom retry logic for business rules
        Step("validate_rules", workflow.TypedStep(validateBusinessRules),
            workflow.WithRetry(customRetry)).
        // Smart retry combining multiple conditions
        Step("external_api", workflow.TypedStep(callExternalAPI),
            workflow.WithRetry(workflow.RetryConfig{
                MaxAttempts:  5,
                InitialDelay: 100 * time.Millisecond,
                MaxDelay:     3 * time.Second,
                Multiplier:   2.0,
                Condition:    SmartRetryCondition,
            }))
    
    return workflow.ExecuteTyped[ComplexOutput](ctx, wf, input)
}

// Example: NOT combinator to exclude context errors
func HandleWithNoContextRetry(ctx context.Context, input Input) (Output, error) {
    // Retry on any error EXCEPT context cancellation/timeout
    retryExceptContext := workflow.RetryConfig{
        MaxAttempts:  3,
        InitialDelay: 100 * time.Millisecond,
        MaxDelay:     2 * time.Second,
        Multiplier:   2.0,
        Condition:    workflow.Not(workflow.RetryOnContextErrors()),
    }
    
    wf := workflow.New().
        Step("process", workflow.TypedStep(processWithTimeout),
            workflow.WithRetry(retryExceptContext))
    
    return workflow.ExecuteTyped[Output](ctx, wf, input)
}
```

### Example 4: Parallel Execution
```go
type UserContext struct {
    UserID int
}

type EnrichedContext struct {
    UserID      int
    Profile     UserProfile
    Permissions []Permission
    Preferences UserPreferences
}

func HandleGetUserContext(ctx context.Context, input UserContext) (EnrichedContext, error) {
    wf := workflow.New().
        Step("parallel_fetch", workflow.Parallel[UserContext, EnrichedContext](
            workflow.ParallelConfig{Strategy: workflow.AllMustSucceed},
            fetchProfile,
            fetchPermissions,
            fetchPreferences,
        ))
    
    return workflow.ExecuteTyped[EnrichedContext](ctx, wf, input)
}
```

### Example 5: Conditional Branching (Simple)
```go
type CheckRequest struct {
    UserID int
    IsPremium bool
}

type ProcessingResult struct {
    Path    string
    Result  any
}

func HandleConditionalProcessing(ctx context.Context, req CheckRequest) (ProcessingResult, error) {
    wf := workflow.New().
        Step("route", workflow.If[CheckRequest, ProcessingResult](
            func(r CheckRequest) bool { return r.IsPremium },
            workflow.TypedStep(processPremiumUser),
            workflow.TypedStep(processStandardUser),
        ))
    
    return workflow.ExecuteTyped[ProcessingResult](ctx, wf, req)
}

func processPremiumUser(ctx context.Context, req CheckRequest) (ProcessingResult, error) {
    return ProcessingResult{Path: "premium", Result: "fast-tracked"}, nil
}

func processStandardUser(ctx context.Context, req CheckRequest) (ProcessingResult, error) {
    return ProcessingResult{Path: "standard", Result: "normal-queue"}, nil
}
```

### Example 6: Tree-Like Workflow with Nested Branches

This example demonstrates a tree-like workflow with 3 levels of nesting:

```
Validate Order
    ├─ If Premium User?
    │   ├─ YES (Premium Flow):
    │   │   ├─ Shipping Decision (nested workflow)
    │   │   │   ├─ If Express Shipping?
    │   │   │   │   ├─ YES: Fast-track → Priority Notify
    │   │   │   │   └─ NO: Standard → Priority Notify
    │   │   └─ Apply Premium Discount
    │   │
    │   └─ NO (Standard Flow):
    │       ├─ Check Inventory
    │       ├─ Standard Fulfillment
    │       └─ Standard Notify
    │
Record Order
```

```go
type OrderContext struct {
    OrderID     int
    UserID      int
    IsPremium   bool
    IsExpress   bool
    TotalAmount float64
}

type FulfillmentResult struct {
    FulfillmentID int
    Method        string
    Priority      int
}

type ProcessedOrder struct {
    OrderID       int
    FulfillmentID int
    DiscountApplied float64
    Status        string
}

func HandleOrderProcessing(ctx context.Context, order OrderContext) (ProcessedOrder, error) {
    // Build nested workflow for shipping decision (premium users only)
    shippingDecision := workflow.New().
        If("shipping_type", 
            func(o OrderContext) bool { return o.IsExpress },
            // Express shipping branch
            workflow.Compose[OrderContext, FulfillmentResult](
                workflow.TypedStep(fastTrackFulfillment),
                workflow.TypedStep(priorityNotify),
            ),
            // Standard shipping for premium branch
            workflow.Compose[OrderContext, FulfillmentResult](
                workflow.TypedStep(standardFulfillment),
                workflow.TypedStep(priorityNotify),
            ),
        )
    
    // Build premium user workflow
    premiumFlow := workflow.New().
        Step("shipping", shippingDecision.AsStep()).
        Step("discount", workflow.TypedStep(applyPremiumDiscount))
    
    // Build standard user workflow
    standardFlow := workflow.New().
        Step("inventory", workflow.TypedStep(checkInventory)).
        Step("fulfill", workflow.TypedStep(standardFulfillment)).
        Step("notify", workflow.TypedStep(standardNotify))
    
    // Main workflow with tree-like structure
    wf := workflow.New().
        WithRetry(workflow.RetryConfig{
            MaxAttempts:  3,
            InitialDelay: 100 * time.Millisecond,
            MaxDelay:     5 * time.Second,
            Multiplier:   2.0,
        }).
        Step("validate", workflow.TypedStep(validateOrder)).
        If("process_by_user_type",
            func(o OrderContext) bool { return o.IsPremium },
            premiumFlow.AsStep(),    // Premium path with nested shipping decision
            standardFlow.AsStep(),   // Standard path
        ).
        Step("record", workflow.TypedStep(recordOrder))
    
    return workflow.ExecuteTyped[ProcessedOrder](ctx, wf, order)
}

// Helper functions
func validateOrder(ctx context.Context, order OrderContext) (OrderContext, error) {
    if order.TotalAmount <= 0 {
        return OrderContext{}, errors.New("invalid order amount")
    }
    return order, nil
}

func fastTrackFulfillment(ctx context.Context, order OrderContext) (FulfillmentResult, error) {
    return FulfillmentResult{FulfillmentID: 1, Method: "express", Priority: 1}, nil
}

func standardFulfillment(ctx context.Context, order OrderContext) (FulfillmentResult, error) {
    return FulfillmentResult{FulfillmentID: 2, Method: "standard", Priority: 3}, nil
}

func priorityNotify(ctx context.Context, result FulfillmentResult) (FulfillmentResult, error) {
    // Send priority notification
    return result, nil
}

func standardNotify(ctx context.Context, result FulfillmentResult) (FulfillmentResult, error) {
    // Send standard notification
    return result, nil
}

func applyPremiumDiscount(ctx context.Context, result FulfillmentResult) (ProcessedOrder, error) {
    return ProcessedOrder{
        FulfillmentID:   result.FulfillmentID,
        DiscountApplied: 0.10,
        Status:          "completed",
    }, nil
}

func checkInventory(ctx context.Context, order OrderContext) (OrderContext, error) {
    // Check inventory availability
    return order, nil
}

func recordOrder(ctx context.Context, processed ProcessedOrder) (ProcessedOrder, error) {
    // Record order in database
    return processed, nil
}
```

### Example 7: Complete Microservice Handler with Observability
```go
type metricsObserver struct {
    metrics MetricsClient
}

func (m *metricsObserver) OnStepStart(ctx context.Context, stepName string) {
    m.metrics.Increment("workflow.step.started", map[string]string{"step": stepName})
}

func (m *metricsObserver) OnStepComplete(ctx context.Context, stepName string, duration time.Duration, err error) {
    tags := map[string]string{"step": stepName}
    if err != nil {
        tags["status"] = "error"
    } else {
        tags["status"] = "success"
    }
    m.metrics.Timing("workflow.step.duration", duration, tags)
}

// ... implement other Observer methods

func NewOrderHandler(metrics MetricsClient) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()
        
        var input OrderInput
        if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
            http.Error(w, "invalid input", http.StatusBadRequest)
            return
        }
        
        wf := workflow.New().
            WithRetry(workflow.RetryConfig{
                MaxAttempts:  3,
                InitialDelay: 100 * time.Millisecond,
                MaxDelay:     5 * time.Second,
                Multiplier:   2.0,
            }).
            WithObserver(&metricsObserver{metrics: metrics}).
            Step("validate", workflow.TypedStep(validateOrder)).
            Step("check_inventory", workflow.TypedStep(checkInventory)).
            Step("process", workflow.TypedStep(processOrder)).
            Step("notify", workflow.TypedStep(notifyCustomer))
        
        result, err := workflow.ExecuteTyped[OrderResult](ctx, wf, input)
        if err != nil {
            log.Printf("processing order: %v", err)
            http.Error(w, "failed to process order", http.StatusInternalServerError)
            return
        }
        
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(result)
    }
}
```

## Testing Strategy

### Unit Tests
- Test each component in isolation
- Table-driven tests for all core functions
- Mock external dependencies
- Test error paths and edge cases
- Aim for 100% coverage on exported APIs

### Integration Tests
- Test complete workflows end-to-end
- Test with real context cancellation
- Test with concurrent execution
- Separate from unit tests (build tags or separate package)

### Benchmark Tests
- Benchmark workflow execution overhead
- Compare with baseline (raw function calls)
- Ensure overhead is < 1% for realistic workflows

## Non-Goals (Future Considerations)

The following are explicitly out of scope for the initial implementation:

- **Persistent state**: All state is in-memory only
- **Saga pattern**: No distributed transactions or compensation
- **Async/long-running workflows**: Only synchronous, short-lived workflows
- **Workflow versioning**: No version management
- **Workflow visualization**: No diagram generation
- **Built-in circuit breakers**: Users can implement via step functions
- **Built-in rate limiting**: Users can implement via step functions
- **Workflow scheduling**: No cron or delayed execution
- **Workflow pause/resume**: No checkpoint/restore
- **Distributed execution**: Single-process only

These features may be considered for future versions based on user feedback.

## Success Criteria

The implementation will be considered successful when:

1. ✅ All phases complete with passing tests
2. ✅ Test coverage >= 90% on exported APIs
3. ✅ Benchmarks show < 1% overhead for typical workflows
4. ✅ Documentation complete with working examples
5. ✅ Code passes `golangci-lint` with no warnings
6. ✅ README provides clear quick start guide
7. ✅ At least 7 realistic usage examples included (covering all major features)
8. ✅ Retry conditions work with gRPC and HTTP error types
9. ✅ Tree-like workflows support unlimited nesting depth

## Timeline

Estimated implementation time: **3-4 weeks**

- Phase 1: 2-3 days
- Phase 2: 3-4 days (includes retry conditions and built-in helpers)
- Phase 3: 3-4 days
- Phase 4: 3 days (includes Compose and AsStep)
- Phase 5: 2-3 days
- Phase 6: 3-4 days (includes additional retry condition examples)
- Buffer for testing and refinement: 3-4 days

