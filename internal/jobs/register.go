package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/mailer"
	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/internal/scheduler"
	"github.com/riipandi/tango/internal/storage"
)

// Register wires the task processors onto a client. It is pure wiring — no
// database, no context — so a client can be built without touching a
// connection; the recurring seeds are the Seeder's job.
//
// uploader is the storage engine the upload and the garbage collection
// run through. A nil uploader registers none of its jobs: a queue that
// cannot answer its tasks is not a schedule, it is a failure. mail is the
// service the verification email submits through, and the same rule applies.
// expiryEmailEnabled says whether the API-key expiry reminder runs: the scan
// is seeded only when the deployment asked for it, and a scan a deployment
// did not ask for would remind nobody and still cost a query a day.
func Register(client *queue.Client, cleanupInterval time.Duration, uploader *storage.Manager, mail *mailer.Service, pool *datastore.Postgres, baseURL string, expiryEmailEnabled bool) {
	client.Register(queue.NewQueue[CleanupTask](cleanupProcessor))
	// The audit retention runs on the pool rather than through a service: it
	// deletes rows nothing reads back, so it needs no feature to own it.
	client.Register(queue.NewQueue[AuditCleanupTask](func(ctx context.Context, task AuditCleanupTask) error {
		return auditCleanupProcessor(ctx, task, pool)
	}))
	if uploader != nil {
		client.Register(queue.NewQueue[ChunkUploadTask](func(ctx context.Context, task ChunkUploadTask) error {
			return uploadProcessor(ctx, task, uploader)
		}))
		client.Register(queue.NewQueue[StorageGCTask](func(ctx context.Context, task StorageGCTask) error {
			return gcProcessor(ctx, task, uploader)
		}))
	}
	client.Register(queue.NewQueue[APIKeyExpiryScanTask](func(ctx context.Context, task APIKeyExpiryScanTask) error {
		return apiKeyExpiryScanProcessor(ctx, task, pool, client, expiryEmailEnabled && mail != nil, mail)
	}))
	if mail != nil {
		client.Register(queue.NewQueue[EmailVerificationTask](func(ctx context.Context, task EmailVerificationTask) error {
			return emailVerificationProcessor(ctx, task, mail, baseURL)
		}))
		client.Register(queue.NewQueue[APIKeyExpiryEmailTask](func(ctx context.Context, task APIKeyExpiryEmailTask) error {
			return apiKeyExpiryEmailProcessor(ctx, task, mail)
		}))
		client.Register(queue.NewQueue[OneTimeAccessEmailTask](func(ctx context.Context, task OneTimeAccessEmailTask) error {
			return oneTimeAccessProcessor(ctx, task, mail, baseURL)
		}))
		client.Register(queue.NewQueue[UserBannedEmailTask](func(ctx context.Context, task UserBannedEmailTask) error {
			return userBannedProcessor(ctx, task, mail)
		}))
		client.Register(queue.NewQueue[UserUnbannedEmailTask](func(ctx context.Context, task UserUnbannedEmailTask) error {
			return userUnbannedProcessor(ctx, task, mail)
		}))
		client.Register(queue.NewQueue[PasswordResetEmailTask](func(ctx context.Context, task PasswordResetEmailTask) error {
			return passwordResetProcessor(ctx, task, mail, baseURL)
		}))
		client.Register(queue.NewQueue[PasswordChangedNoticeTask](func(ctx context.Context, task PasswordChangedNoticeTask) error {
			return passwordChangedNoticeProcessor(ctx, task, mail)
		}))
		client.Register(queue.NewQueue[MfaDisabledNoticeTask](func(ctx context.Context, task MfaDisabledNoticeTask) error {
			return mfaDisabledNoticeProcessor(ctx, task, mail)
		}))
	}
}

// Seeder seeds the recurring jobs. It is a service of its own — not a side
// effect of building the queue — so seeding against the database is an
// explicit step the prewarm walk makes, ordered after the queue exists, and
// its failure fails the run before the listener opens.
//
// Seeding is guarded by the pending count, so every process start that finds
// no task of a recurring queue pending adds exactly one: the schedule
// survives restarts without multiplying. Two processes starting together may
// seed twice, which is harmless — the jobs are idempotent and each run
// re-enqueues one successor, so the population stays at what the race left.
type Seeder struct {
	client          *queue.Client
	cleanupInterval time.Duration
	uploader        *storage.Manager
	retentionDays   int
	expiryEmail     bool
	log             *slog.Logger
}

// NewSeeder builds the seeder over the client whose processors Register
// wired. retentionDays is the audit window the retention job is seeded with;
// a run that changes the configuration carries the new window into the
// seeded task, which is what makes the change take effect at the next run.
func NewSeeder(client *queue.Client, cleanupInterval time.Duration, uploader *storage.Manager, retentionDays int, expiryEmail bool, log *slog.Logger) *Seeder {
	return &Seeder{
		client:          client,
		cleanupInterval: cleanupInterval,
		uploader:        uploader,
		retentionDays:   retentionDays,
		expiryEmail:     expiryEmail,
		log:             log,
	}
}

// Seed seeds every recurring job while none of its tasks is pending.
func (s *Seeder) Seed(ctx context.Context) error {
	if err := s.seedOnce(ctx, CleanupName, cleanupSeed(s.cleanupInterval), s.cleanupInterval); err != nil {
		return err
	}
	if err := s.seedOnce(ctx, AuditCleanupName,
		auditCleanupSeed(DefaultAuditCleanupInterval, s.retentionDays),
		DefaultAuditCleanupInterval); err != nil {
		return err
	}
	if s.expiryEmail {
		if err := s.seedOnce(ctx, APIKeyExpiryScanName, APIKeyExpiryScanTask{}, DefaultAPIKeyExpiryInterval); err != nil {
			return err
		}
	}
	if s.uploader == nil {
		return nil
	}
	return s.seedOnce(ctx, StorageGCName, gcSeed(DefaultStorageGCInterval), DefaultStorageGCInterval)
}

// seedOnce seeds one recurring job while none of its tasks is pending. The
// interval is both the first run's delay and the schedule the task carries
// in its payload, so the run a restart seeds keeps the schedule it was
// seeded with.
func (s *Seeder) seedOnce(ctx context.Context, name string, task queue.Task, interval time.Duration) error {
	pending, err := s.client.Pending(ctx, name)
	if err != nil {
		return err
	}
	if pending > 0 {
		return nil
	}
	if _, err := s.client.Add(task).Ctx(ctx).Wait(interval).Save(); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "queue: recurring job seeded",
		"queue", name, "interval", interval.String())
	return nil
}

// Scheduled lists the jobs the cron scheduler enqueues at their times. The
// list is empty until a feature asks for a cron schedule — a recurring job
// that runs on a fixed interval belongs with Register, whose self-enqueued
// successor is durable without a second mechanism.
func Scheduled() []scheduler.Job {
	return nil
}
