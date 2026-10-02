package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/fetcher"
	"github.com/riipandi/tango/internal/mailer"
	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/internal/scheduler"
	"github.com/riipandi/tango/internal/storage"
	"github.com/riipandi/tango/modules/notification"
	"github.com/riipandi/tango/modules/webhook"
)

// Register wires the task processors onto a client. It is pure wiring — no
// database, no context — so a client can be built without touching a
// connection; the recurring seeds are the Seeder's job.
//
// uploader is the storage engine the upload and the garbage collection
// run through. A nil uploader registers none of its jobs: a queue that
// cannot answer its tasks is not a schedule, it is a failure. mail is the
// service the verification email submits through, and the same rule applies.
// http is the outbound client the back-channel logout deliveries POST
// through; a nil client registers the delivery queue anyway, and its
// attempts fail until the wiring lands — the logout itself never waits
// on it.
// expiryEmailEnabled says whether the API-key expiry reminder runs — its
// feature switch, AND-ed with the notice flag the deployment's cost decision
// carries: the scan is seeded only when both agree, and a scan a deployment
// did not ask for would remind nobody and still cost a query a day.
func Register(client *queue.Client, cleanupInterval time.Duration, uploader *storage.Manager, mail *mailer.Service, pool *datastore.Postgres, baseURL string, expiryEmailEnabled bool, apiKeyExpiringNoticeEnabled bool, notices NoticePublisher, scimSyncer ScimSyncer, signupSweeper SignupTokenSweeper, webhookRunner WebhookRunner, http *fetcher.Client, log *slog.Logger) {
	client.Register(queue.NewQueue[CleanupTask](func(ctx context.Context, task CleanupTask) error {
		return cleanupProcessor(ctx, task, pool, signupSweeper)
	}))
	// The audit retention runs on the pool rather than through a service: it
	// deletes rows nothing reads back, so it needs no feature to own it.
	client.Register(queue.NewQueue[AuditCleanupTask](func(ctx context.Context, task AuditCleanupTask) error {
		return auditCleanupProcessor(ctx, task, pool)
	}))
	// The protocol state's retention runs the same way: the expired
	// oauth2_sessions rows belong to no feature, and the sweeps the queue
	// carries are the one deletion path they have.
	client.Register(queue.NewQueue[ProtocolCleanupTask](func(ctx context.Context, task ProtocolCleanupTask) error {
		return protocolCleanupProcessor(ctx, task, pool)
	}))
	// The WebAuthn ceremony sessions expire by the minute and outlive no
	// verify path; the sweep is the deletion their abandoned rows need.
	client.Register(queue.NewQueue[WebauthnCleanupTask](func(ctx context.Context, task WebauthnCleanupTask) error {
		return webauthnCleanupProcessor(ctx, task, pool)
	}))
	// The back-channel logout deliveries ride the shared fetch client: a
	// token is POSTed to the relying party's registered destination, and
	// a failed attempt is the queue's retry, not the logout's failure.
	client.Register(queue.NewQueue[BackchannelLogoutTask](func(ctx context.Context, task BackchannelLogoutTask) error {
		return backchannelLogoutProcessor(ctx, task, http)
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
		return apiKeyExpiryScanProcessor(ctx, task, pool, client, expiryEmailEnabled && apiKeyExpiringNoticeEnabled && mail != nil, mail)
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
		client.Register(queue.NewQueue[ReauthenticationCodeEmailTask](func(ctx context.Context, task ReauthenticationCodeEmailTask) error {
			return reauthenticationCodeProcessor(ctx, task, mail)
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
		client.Register(queue.NewQueue[NewDeviceEmailTask](func(ctx context.Context, task NewDeviceEmailTask) error {
			return newDeviceEmailProcessor(ctx, task, mail)
		}))
		client.Register(queue.NewQueue[EmailChangeRequestEmailTask](func(ctx context.Context, task EmailChangeRequestEmailTask) error {
			return emailChangeRequestProcessor(ctx, task, mail, baseURL)
		}))
		client.Register(queue.NewQueue[EmailChangeNoticeTask](func(ctx context.Context, task EmailChangeNoticeTask) error {
			return emailChangeNoticeProcessor(ctx, task, mail)
		}))
		client.Register(queue.NewQueue[notification.NotificationEmailTask](func(ctx context.Context, task notification.NotificationEmailTask) error {
			return notificationEmailProcessor(ctx, task, pool, mail)
		}))
	}
	if notices != nil {
		client.Register(queue.NewQueue[UploadFinishedTask](func(ctx context.Context, task UploadFinishedTask) error {
			return uploadFinishedProcessor(ctx, task, notices)
		}))
	}
	client.Register(queue.NewQueue[ScimSyncDebouncedTask](func(ctx context.Context, task ScimSyncDebouncedTask) error {
		return scimSyncDebouncedProcessor(ctx, task, scimSyncer, log)
	}))
	// The webhook deliveries sign the stored body and send it through the
	// shared fetch client: a failed attempt is the queue's retry, and the
	// attempt rows carry the story. A container without the webhook area
	// registers the queue anyway, and its attempts fail until the wiring
	// lands — the emission that enqueued them never waits on it.
	client.Register(queue.NewQueue[webhook.DeliverTask](func(ctx context.Context, task webhook.DeliverTask) error {
		return webhookDeliverProcessor(ctx, task, webhookRunner)
	}))
	// The attempt retention runs on the runner beside the deliveries: the
	// attempts are the runner's rows, and the sweep is their one deletion
	// path.
	client.Register(queue.NewQueue[WebhookPruneTask](func(ctx context.Context, task WebhookPruneTask) error {
		return webhookPruneProcessor(ctx, task, webhookRunner, log)
	}))
	if scimSyncer != nil {
		client.Register(queue.NewQueue[ScimSyncTask](func(ctx context.Context, task ScimSyncTask) error {
			return (&scimSyncProcessor{syncer: scimSyncer, log: log}).Process(ctx, task)
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
	scimSync        bool
	log             *slog.Logger
}

// NewSeeder builds the seeder over the client whose processors Register
// wired. retentionDays is the audit window the retention job is seeded with;
// a run that changes the configuration carries the new window into the
// seeded task, which is what makes the change take effect at the next run.
// scimSync arms the hourly provisioning pass; a run without a provider row
// still seeds it, and the pass answers nothing to push.
func NewSeeder(client *queue.Client, cleanupInterval time.Duration, uploader *storage.Manager, retentionDays int, expiryEmail bool, scimSync bool, log *slog.Logger) *Seeder {
	return &Seeder{
		client:          client,
		cleanupInterval: cleanupInterval,
		uploader:        uploader,
		retentionDays:   retentionDays,
		expiryEmail:     expiryEmail,
		scimSync:        scimSync,
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
	if err := s.seedOnce(ctx, ProtocolCleanupName,
		protocolCleanupSeed(DefaultProtocolCleanupInterval),
		DefaultProtocolCleanupInterval); err != nil {
		return err
	}
	if err := s.seedOnce(ctx, WebauthnCleanupName,
		webauthnCleanupSeed(DefaultWebauthnCleanupInterval),
		DefaultWebauthnCleanupInterval); err != nil {
		return err
	}
	if err := s.seedOnce(ctx, WebhookPruneName,
		webhookPruneSeed(DefaultWebhookPruneInterval),
		DefaultWebhookPruneInterval); err != nil {
		return err
	}
	if s.expiryEmail {
		if err := s.seedOnce(ctx, APIKeyExpiryScanName, APIKeyExpiryScanTask{}, DefaultAPIKeyExpiryInterval); err != nil {
			return err
		}
	}
	if s.scimSync {
		if err := s.seedOnce(ctx, ScimSyncName, scimSyncSeed(DefaultScimSyncInterval), DefaultScimSyncInterval); err != nil {
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
