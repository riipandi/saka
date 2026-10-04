// Package health aggregates dependency checks into one availability status.
// The result is the same whether it is asked for by the CLI or written by the
// REST handler, so both report the same thing.
//
// The design follows github.com/alexliesenfeld/health without depending on it:
// named check functions, an aggregated status, a per-check result with a
// timestamp, a result cache, and a global timeout that a check can shorten.
package health

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"
)

// Status is the availability of one component check.
type Status string

const (
	// StatusUnknown means a check has not run yet, so nothing is known about
	// it. It counts as not ready.
	StatusUnknown Status = "unknown"
	// StatusUp means the component answered.
	StatusUp Status = "up"
	// StatusDown means the component failed to answer.
	StatusDown Status = "down"
)

// GlobalStatus is the aggregated availability of the whole system. It is a
// different vocabulary from a component Status on purpose: a component is up or
// down, and the system it belongs to is healthy or unhealthy.
type GlobalStatus string

const (
	// GlobalHealthy means every required component passed.
	GlobalHealthy GlobalStatus = "healthy"
	// GlobalUnhealthy means at least one required component failed.
	GlobalUnhealthy GlobalStatus = "unhealthy"
)

// Healthy reports whether the system is serving traffic. Only healthy does; an
// unknown component is not a passing one.
func (s GlobalStatus) Healthy() bool { return s == GlobalHealthy }

// ErrCheckTimeout is the result of a check that did not finish inside its
// deadline. A check function that respects its context returns this too, so the
// error is recognizable either way.
var ErrCheckTimeout = errors.New("health: check timed out")

// Defaults applied when an option does not override them.
const (
	// DefaultTimeout bounds one Check call, including every check function.
	DefaultTimeout = 5 * time.Second
	// DefaultCacheTTL is how long a check result is reused. Without it every
	// caller would reach the dependencies, and a probe loop would hammer them.
	DefaultCacheTTL = 1 * time.Second
)

// Check is one dependency to verify.
type Check struct {
	// Name identifies the check in the result. Required, and unique.
	Name string
	// Check is the function to run. Required. Returning an error marks the
	// component down.
	Check func(ctx context.Context) error
	// Target names what is inspected, such as a DSN host or a directory. It
	// appears in the text report so a reader can see which database or path was
	// checked. Optional: a check that inspects nothing specific leaves it empty.
	Target string
	// Timeout bounds this check. It can only shorten the checker timeout, so
	// one check cannot outlive the whole call.
	Timeout time.Duration
	// Optional means the component is reported but does not affect the
	// aggregated status, so a missing optional backend is not a failure.
	Optional bool
}

// Result is the aggregated outcome of one Check call, and is the payload both
// surfaces publish: the CLI prints it as text or JSON, and the REST handler
// sends the same object. Its JSON form is defined in marshal.go.
type Result struct {
	// Status is the aggregated availability, and is what callers branch on.
	Status GlobalStatus
	// Details holds one entry per check, keyed by check name.
	Details map[string]CheckResult
	// Duration is how long the call took.
	Duration time.Duration
	// Info holds static metadata about the reporting process, such as its
	// version. It identifies which build answered a probe.
	Info map[string]string
}

// Healthy reports whether the aggregated status is healthy.
func (r Result) Healthy() bool { return r.Status.Healthy() }

// Failed returns the names of the checks that are down, in name order. A
// caller that only prints a status uses it to say what is wrong.
func (r Result) Failed() []string {
	names := make([]string, 0, len(r.Details))
	for _, name := range sortedNames(r.Details) {
		if r.Details[name].Status == StatusDown {
			names = append(names, name)
		}
	}
	return names
}

// CheckResult is the outcome of a single check. Its JSON form is defined in
// marshal.go, which reports the duration in milliseconds.
type CheckResult struct {
	// Name is the check this result belongs to, repeated here so a result
	// stays readable once it is detached from the map that held it.
	Name string
	// Target is what the check inspected, empty when it inspects nothing
	// specific.
	Target string
	// Status is up when the check passed and down when it did not.
	Status Status
	// Error is the failure message, empty when the check passed. It is a
	// string, not an error, so the result serializes the same way everywhere.
	Error string
	// Timestamp is when the check last ran, in UTC.
	Timestamp time.Time
	// Duration is how long the check took.
	Duration time.Duration
	// Optional mirrors the Check it came from, so a reader of the result can
	// tell whether a down entry explains the status.
	Optional bool
}

// Option configures a Checker.
type Option func(*config)

// config is the resolved Checker configuration.
type config struct {
	timeout   time.Duration
	cacheTTL  time.Duration
	checks    []Check
	info      map[string]string
	infoFuncs []func(ctx context.Context) map[string]string
	onStatus  func(ctx context.Context, result Result)
	now       func() time.Time
}

// WithTimeout sets the deadline for one Check call. Default is DefaultTimeout.
func WithTimeout(timeout time.Duration) Option {
	return func(c *config) { c.timeout = timeout }
}

// WithCacheTTL sets how long a check result is reused before the check runs
// again. Zero disables the cache. Default is DefaultCacheTTL.
func WithCacheTTL(ttl time.Duration) Option {
	return func(c *config) { c.cacheTTL = ttl }
}

// WithCheck adds a check. A duplicate name replaces the earlier check, so the
// result never holds two entries for one component.
func WithCheck(check Check) Option {
	return WithChecks(check)
}

// WithChecks adds checks.
func WithChecks(checks ...Check) Option {
	return func(c *config) {
		c.checks = append(c.checks, checks...)
	}
}

// WithStatusListener registers a function called when the aggregated status
// changes. It runs after the result is assembled and must not block: it is
// called from the caller's goroutine, which may be serving a probe.
func WithStatusListener(listener func(ctx context.Context, result Result)) Option {
	return func(c *config) { c.onStatus = listener }
}

// WithInfo adds static metadata to every result, such as the application
// version. Both surfaces publish it, so a probe response identifies the build
// that answered.
//
// A key that changes per call belongs in WithInfoFunc instead: this map is
// copied once and reported as is.
func WithInfo(values map[string]string) Option {
	return func(c *config) {
		c.info = make(map[string]string, len(values))
		maps.Copy(c.info, values)
	}
}

// WithInfoFunc adds metadata computed at the time of each Check call, such as
// the process uptime. Functions run in order and their values merge into the
// result's Info; a key also set by WithInfo keeps the WithInfo value, because
// static facts about the build outrank derived ones.
func WithInfoFunc(functions ...func(ctx context.Context) map[string]string) Option {
	return func(c *config) { c.infoFuncs = functions }
}

// Checker runs a fixed set of checks and aggregates their results.
//
// A Checker is safe for concurrent use and holds no background goroutines: it
// runs the checks when asked. The cache is what keeps a probe loop cheap.
type Checker struct {
	cfg config

	// state guards the cached results. Each entry is a whole CheckResult, so a
	// cached result is returned exactly as it was produced.
	state state
}

// NewChecker builds a Checker. It panics on an invalid configuration, because
// a check set is assembled at startup from code, not from user input.
func NewChecker(options ...Option) *Checker {
	cfg := config{
		timeout:  DefaultTimeout,
		cacheTTL: DefaultCacheTTL,
		now:      time.Now,
	}
	for _, option := range options {
		option(&cfg)
	}
	if cfg.timeout <= 0 {
		panic("health: timeout must be greater than zero")
	}
	if cfg.cacheTTL < 0 {
		panic("health: cache TTL must not be negative")
	}

	seen := make(map[string]bool, len(cfg.checks))
	for _, check := range cfg.checks {
		if check.Name == "" {
			panic("health: every check needs a name")
		}
		if check.Check == nil {
			panic(fmt.Sprintf("health: check %q needs a function", check.Name))
		}
		if seen[check.Name] {
			panic(fmt.Sprintf("health: duplicate check name %q", check.Name))
		}
		seen[check.Name] = true
	}

	for _, key := range sortedKeys(cfg.info) {
		if slices.Contains(reservedInfoKeys, key) {
			panic(fmt.Sprintf("health: info key %q shadows a wire field", key))
		}
	}

	return &Checker{
		cfg:   cfg,
		state: newState(len(cfg.checks)),
	}
}

// Check runs every check that has no fresh result and returns the aggregate.
// Checks run concurrently, so one slow dependency does not delay the others,
// and the call never outlives the configured timeout.
func (c *Checker) Check(ctx context.Context) Result {
	started := c.cfg.now()

	ctx, cancel := context.WithTimeout(ctx, c.cfg.timeout)
	defer cancel()

	c.runDue(ctx)

	result := c.result(ctx, c.cfg.now().Sub(started))
	c.notifyStatusChange(ctx, result)
	return result
}

// notifyStatusChange calls the listener when the aggregated status differs from
// the one reported last.
func (c *Checker) notifyStatusChange(ctx context.Context, result Result) {
	if c.cfg.onStatus == nil {
		return
	}
	if c.state.recordStatus(result.Status) {
		c.cfg.onStatus(ctx, result)
	}
}

// runDue executes the checks whose cached result is missing or stale. Results
// are collected on a buffered channel, so a check that outlives the deadline
// does not leak a blocked goroutine when it finally returns.
func (c *Checker) runDue(ctx context.Context) {
	due := c.state.due(c.cfg.checks, c.cfg.now(), c.cfg.cacheTTL)
	if len(due) == 0 {
		return
	}

	results := make(chan CheckResult, len(due))
	for _, check := range due {
		go func(check Check) {
			results <- c.runOne(ctx, check)
		}(check)
	}

	fresh := make(map[string]CheckResult, len(due))
	for range due {
		result := <-results
		fresh[result.Name] = result
	}
	c.state.store(fresh)
}

// runOne executes a single check and turns its outcome into a result. A check
// that panics becomes a failure instead of taking the process down: a probe
// must not be able to crash the server.
func (c *Checker) runOne(ctx context.Context, check Check) CheckResult {
	started := c.cfg.now()

	checkCtx := ctx
	if check.Timeout > 0 {
		var cancel context.CancelFunc
		checkCtx, cancel = context.WithTimeout(ctx, check.Timeout)
		defer cancel()
	}

	err := runCheckFunc(checkCtx, check.Check)

	result := CheckResult{
		Name:      check.Name,
		Target:    check.Target,
		Status:    StatusUp,
		Timestamp: started.UTC(),
		Duration:  c.cfg.now().Sub(started),
		Optional:  check.Optional,
	}
	if err != nil {
		result.Status = StatusDown
		result.Error = err.Error()
	}
	return result
}

// runCheckFunc calls fn and reports a timeout or a panic as an error. The call
// runs in its own goroutine so a check that ignores its context cannot hold up
// the aggregate past the deadline.
func runCheckFunc(ctx context.Context, fn func(ctx context.Context) error) error {
	done := make(chan error, 1)

	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- panicError(recovered)
			}
		}()
		done <- fn(ctx)
	}()

	select {
	case err := <-done:
		// A check that respects its context reports the deadline as
		// context.DeadlineExceeded. Report the same error the deadline branch
		// would, so the result does not depend on which branch wins the race.
		if errors.Is(err, context.DeadlineExceeded) {
			return ErrCheckTimeout
		}
		return err
	case <-ctx.Done():
		// A check that ignores its context never reaches the branch above.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ErrCheckTimeout
		}
		return ctx.Err()
	}
}

// panicError converts a recovered panic value into an error.
func panicError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return fmt.Errorf("health: check panicked: %w", err)
	}
	return fmt.Errorf("health: check panicked: %v", recovered)
}

// result assembles the aggregate from the cached check results. A component is
// up or down; the aggregate it produces is healthy or unhealthy.
func (c *Checker) result(ctx context.Context, duration time.Duration) Result {
	details := c.state.snapshot()

	status := GlobalHealthy
	for _, name := range sortedNames(details) {
		detail := details[name]
		if detail.Optional {
			continue
		}
		if detail.Status != StatusUp {
			status = GlobalUnhealthy
		}
	}

	return Result{
		Status:   status,
		Details:  details,
		Duration: duration,
		Info:     c.buildInfo(ctx),
	}
}

// buildInfo merges the static metadata with the values the info functions
// compute now. A function cannot override a static key, so a build fact stays
// fixed.
func (c *Checker) buildInfo(ctx context.Context) map[string]string {
	if len(c.cfg.info) == 0 && len(c.cfg.infoFuncs) == 0 {
		return nil
	}

	info := make(map[string]string, len(c.cfg.info))
	for _, function := range c.cfg.infoFuncs {
		maps.Copy(info, function(ctx))
	}
	// Applied last, so a static value wins over a computed one.
	maps.Copy(info, c.cfg.info)
	return info
}

// Checks returns the names of the configured checks, in the order they were
// added. It lets a caller describe what will be verified before running it.
func (c *Checker) Checks() []string {
	names := make([]string, 0, len(c.cfg.checks))
	for _, check := range c.cfg.checks {
		names = append(names, check.Name)
	}
	return names
}

// sortedNames returns the keys of a detail map in name order, so output and
// aggregation do not depend on map iteration order.
func sortedNames(details map[string]CheckResult) []string {
	return slices.Sorted(maps.Keys(details))
}

// sortedKeys returns the keys of an info map in key order.
func sortedKeys[V any](values map[string]V) []string {
	return slices.Sorted(maps.Keys(values))
}
