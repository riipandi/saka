package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/riipandi/saka/internal/queue"
)

// OAuthOffboardingName is the queue the offboarding passes run on.
const OAuthOffboardingName = "oauth_offboarding"

// DefaultOAuthOffboardingInterval is how often the bindings are probed.
// The hourly cadence is the shape the other recurring passes keep — dead
// identities are caught within the hour, and the provider's token
// endpoints are asked once per binding per hour at most.
const DefaultOAuthOffboardingInterval = time.Hour

// DefaultOffboardingBatch bounds one pass: at most this many bindings are
// probed per run, the rest waiting for the next hourly turn. A pass that
// judges the whole table in one run would let a provider outage decide a
// backlog in a burst; the bound keeps the judgement paced.
const DefaultOffboardingBatch = 100

// OAuthOffboardingTask is one recurring offboarding pass. The interval it
// re-enqueues itself with rides in the payload, so a run that changes the
// schedule takes effect at the next run rather than needing the queue
// reseeded.
type OAuthOffboardingTask struct {
	IntervalMillis int64 `json:"interval_millis"`
}

// Interval is the wait between two passes.
func (t OAuthOffboardingTask) Interval() time.Duration {
	return time.Duration(t.IntervalMillis) * time.Millisecond
}

// Config returns the queue the passes run on. A failed pass is retried
// shortly: the bindings it did not judge are still there, and the probe
// never offboards on a transport failure.
func (t OAuthOffboardingTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        OAuthOffboardingName,
		MaxAttempts: 3,
		Timeout:     10 * time.Minute,
		Backoff:     5 * time.Minute,
	}
}

// OAuthOffboarder is the seam the processor drives: the oauthsso feature's
// own pass, narrowed to the one shape the job needs. The concrete service
// is resolved where the processors are wired, and a nil offboarder is a
// container without the identity area — the pass is skipped rather than
// failed.
type OAuthOffboarder interface {
	OffboardPass(ctx context.Context, batch int) error
}

// oauthOffboardingProcessor probes the bindings and queues the next pass.
type oauthOffboardingProcessor struct {
	offboarder OAuthOffboarder
	batch      int
	log        *slog.Logger
}

func (p *oauthOffboardingProcessor) Process(ctx context.Context, task OAuthOffboardingTask) error {
	client := queue.FromContext(ctx)
	if client == nil {
		return errors.New("oauth_offboarding: queue client missing from context")
	}
	interval := task.Interval()
	if interval <= 0 {
		return errors.New("oauth_offboarding: interval must be positive")
	}

	// The next pass is queued before this one succeeds, so the schedule
	// never depends on the process that ran the last one.
	_, err := client.Add(OAuthOffboardingTask{
		IntervalMillis: task.IntervalMillis,
	}).Ctx(ctx).Wait(interval).Save()
	if err != nil {
		return err
	}

	if p.offboarder == nil {
		return nil
	}
	if err := p.offboarder.OffboardPass(ctx, p.batch); err != nil {
		p.log.ErrorContext(ctx, "queue: oauth offboarding pass failed", "error", err)
		return fmt.Errorf("oauth offboarding: %w", err)
	}
	return nil
}

// oauthOffboardingSeed is the payload the first run of the schedule is
// seeded with.
func oauthOffboardingSeed(interval time.Duration) OAuthOffboardingTask {
	return OAuthOffboardingTask{IntervalMillis: interval.Milliseconds()}
}
