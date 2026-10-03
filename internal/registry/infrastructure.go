package registry

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/samber/do/v2"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/cache"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/fetcher"
	"github.com/riipandi/saka/internal/health"
	"github.com/riipandi/saka/internal/jobs"
	"github.com/riipandi/saka/internal/mailer"
	"github.com/riipandi/saka/internal/queue"
	"github.com/riipandi/saka/internal/scheduler"
	"github.com/riipandi/saka/internal/storage"
	"github.com/riipandi/saka/internal/transport/middleware"
	"github.com/riipandi/saka/modules/apikey"
	"github.com/riipandi/saka/modules/federation/scimsync"
	"github.com/riipandi/saka/modules/identity"
	"github.com/riipandi/saka/modules/identity/jwks"
	"github.com/riipandi/saka/modules/identity/signup"
	"github.com/riipandi/saka/modules/notification"
	"github.com/riipandi/saka/modules/webhook"
	"github.com/riipandi/saka/pkg/crypto"
	"uuid"
)

// infrastructure registers what the process runs on: the pool, the cache, the
// queue, the storage engine, the outbound client, and the services built on
// them. Nothing here names a module, so this file cannot depend on one — a
// module reaches these services by invoking them from the container.
//
// The context is captured rather than registered. It is the run's own context,
// which the command cancels on a signal, so it is a property of this run
// instead of a service anything could resolve and replace.
// lazyPublisher resolves the notification area's service when a task runs,
// not when the queue is built: that service's own provider resolves this
// queue for its email pass, so building one against the other would order
// the two around each other. A container without the notification area —
// a consumer served without it — answers the failure the skip falls back
// to, the notice being advisory.
type lazyPublisher struct {
	injector do.Injector
}

// CreateSystemNotice delivers one automated notice through the notification
// area's own create.
func (p lazyPublisher) CreateSystemNotice(ctx context.Context, userID uuid.UUID, title, body string) error {
	service, err := do.Invoke[*notification.Service](p.injector)
	if err != nil || service == nil {
		return err
	}
	return service.CreateSystemNotice(ctx, userID, title, body)
}

// lazyScimSyncer resolves the federation area's sync service at task-run
// time. A container without the federation area answers nil here: the
// pass is skipped rather than failed, the way a deployment without a
// provider row behaves.
type lazyScimSyncer struct {
	injector do.Injector
}

// SyncAll runs one provisioning pass per provider.
func (s lazyScimSyncer) SyncAll(ctx context.Context) error {
	service, err := do.Invoke[*scimsync.Service](s.injector)
	if err != nil || service == nil {
		if err != nil {
			return err
		}
		return nil
	}
	return service.SyncAll(ctx)
}

// lazyWebhookRunner resolves the webhook area's service at task-run time,
// the way the SCIM passes resolve theirs: the delivery task's queue is
// registered in this wiring, and the runner's provider builds over it, so
// the two must not order each other around. A container without the webhook
// area answers the failure here — the delivery task's queue is registered
// regardless, and its attempts name the missing wiring.
type lazyWebhookRunner struct {
	injector do.Injector
}

// RunDelivery performs one delivery attempt for the delivery the task names.
func (r lazyWebhookRunner) RunDelivery(ctx context.Context, deliveryID string) error {
	service, err := do.Invoke[*webhook.Service](r.injector)
	if err != nil {
		return err
	}
	return service.RunDelivery(ctx, deliveryID)
}

// PruneAttempts deletes the attempt rows the retention window has aged out.
func (r lazyWebhookRunner) PruneAttempts(ctx context.Context) (int64, error) {
	service, err := do.Invoke[*webhook.Service](r.injector)
	if err != nil {
		return 0, err
	}
	return service.PruneAttempts(ctx)
}

// PruneDeliveries deletes the terminal delivery rows the retention window
// has aged out; their attempts cascade with them.
func (r lazyWebhookRunner) PruneDeliveries(ctx context.Context) (int64, error) {
	service, err := do.Invoke[*webhook.Service](r.injector)
	if err != nil {
		return 0, err
	}
	return service.PruneDeliveries(ctx)
}

func infrastructure(ctx context.Context) func(do.Injector) {
	return do.Package(
		do.Lazy(func(i do.Injector) (*fetcher.Client, error) {
			c := do.MustInvoke[*config.Config](i)
			log := do.MustInvoke[*slog.Logger](i)
			return fetcher.New(*c, log)
		}),

		do.Lazy(func(i do.Injector) (*datastore.Postgres, error) {
			c := do.MustInvoke[*config.Config](i)
			log := do.MustInvoke[*slog.Logger](i)
			return datastore.NewPostgres(ctx, datastore.PostgresOptions{
				DSN:                  c.Database.URL,
				ApplicationName:      config.AppIdentifier,
				SearchPath:           c.Database.SearchPath,
				Timezone:             c.Database.Timezone,
				MaxConns:             c.Database.MaxConns,
				MinConns:             c.Database.MinConns,
				MaxConnLifetime:      c.Database.MaxConnLifetime,
				MaxConnIdleTime:      c.Database.MaxConnIdleTime,
				ConnectTimeout:       c.Database.ConnectTimeout,
				ConnectAttempts:      c.Database.ConnectAttempts,
				ConnectRetryInterval: c.Database.ConnectRetryInterval,
				Logger:               log,
			})
		}),

		do.Lazy(func(i do.Injector) (*datastore.Valkey, error) {
			c := do.MustInvoke[*config.Config](i)
			return datastore.NewValkey(ctx, datastore.ValkeyOptions{
				URL:             c.KVStore.URL,
				DB:              c.KVStore.DB,
				ApplicationName: config.AppIdentifier,
			})
		}),

		do.Lazy(func(i do.Injector) (cache.Cache, error) {
			c := do.MustInvoke[*config.Config](i)
			// The backend client is resolved only while it is enabled: a run
			// without it never opens a connection, and the cache factory
			// answers the missing client with the no-op driver.
			var kv *datastore.Valkey
			if c.KVStore.Enable {
				kv = do.MustInvoke[*datastore.Valkey](i)
			}
			return cache.New(*c, kv), nil
		}),

		do.Lazy(func(i do.Injector) (*health.Checker, error) {
			c := do.MustInvoke[*config.Config](i)
			pool := do.MustInvoke[*datastore.Postgres](i)
			checks := []health.Check{
				// The endpoint publishes this report to an unauthenticated
				// caller, so neither check names the host it dials: the
				// target-bearing forms are the CLI's, which an operator who
				// owns the machine reads.
				health.DatabaseCheck(pool),
				health.StorageCheck(c.Storage.LocalPath),
			}
			if c.KVStore.Enable {
				kv := do.MustInvoke[*datastore.Valkey](i)
				checks = append(checks, health.KVStoreCheck(kv))
			}
			return health.NewChecker(
				health.WithChecks(checks...),
				health.WithInfo(map[string]string{
					"version": config.AppVersion,
					"mode":    c.App.Mode,
				}),
				health.WithInfoFunc(uptime),
			), nil
		}),

		// The audit recorder is infrastructure because every feature reaches
		// it, exactly the way they reach the pool: a record is written in the
		// transaction that caused it, so the writer cannot belong to any one
		// area. The area that *reads* records is modules/auditlog, which
		// imports nothing from here but the vocabulary.
		do.Lazy(func(i do.Injector) (*audit.Recorder, error) {
			log := do.MustInvoke[*slog.Logger](i)
			return audit.NewRecorder(log), nil
		}),

		do.Lazy(func(i do.Injector) (*mailer.Service, error) {
			c := do.MustInvoke[*config.Config](i)
			log := do.MustInvoke[*slog.Logger](i)
			client, err := mailer.New(*c, log)
			if err != nil {
				return nil, err
			}
			// The templates are embedded, so a parse failure here is a broken
			// build rather than a bad configuration; it still fails the run,
			// before the listener opens, rather than the first send.
			templates, err := mailer.NewTemplates(mailer.SenderFrom(*c))
			if err != nil {
				return nil, err
			}
			return mailer.NewService(client, templates), nil
		}),

		do.Lazy(func(i do.Injector) (storage.Store, error) {
			c := do.MustInvoke[*config.Config](i)
			return storage.New(*c)
		}),

		do.Lazy(func(i do.Injector) (*storage.Manager, error) {
			c := do.MustInvoke[*config.Config](i)
			pool := do.MustInvoke[*datastore.Postgres](i)
			store := do.MustInvoke[storage.Store](i)
			log := do.MustInvoke[*slog.Logger](i)
			return storage.NewManager(store, pool,
				filepath.Join(c.Storage.LocalPath, "staging"), log), nil
		}),

		do.Lazy(func(i do.Injector) (*queue.Client, error) {
			c := do.MustInvoke[*config.Config](i)
			pool := do.MustInvoke[*datastore.Postgres](i)
			log := do.MustInvoke[*slog.Logger](i)
			uploader := do.MustInvoke[*storage.Manager](i)
			mailer := do.MustInvoke[*mailer.Service](i)
			var encryptor *crypto.Cipher
			if c.Queue.Encrypt {
				// Validation refuses an encrypted queue without a usable secret,
				// so a failing parse here is a broken deployment, not a silent
				// switch to plaintext.
				cipher, err := crypto.NewCipherFromHex(c.App.SecretKey)
				if err != nil {
					return nil, err
				}
				encryptor = cipher
			}
			client, err := queue.NewClient(queue.ClientConfig{
				Store:        pool,
				Logger:       log,
				NumWorkers:   c.Queue.NumWorkers,
				ReleaseAfter: c.Queue.ReleaseAfter,
				Encryptor:    encryptor,
			})
			if err != nil {
				return nil, err
			}

			// The processors are wired onto the engine here — pure wiring, no
			// connection is touched. The recurring seeds are the Seeder's
			// service, resolved by the prewarm walk. The upload-finished
			// notices resolve the notification area's service at task-run
			// time, not build time, because that service's own provider
			// resolves this queue for its email pass — a build-time
			// resolution would order the two around each other. The SCIM
			// passes resolve the sync service the same lazy way: the
			// federation area's provider builds it, and this wiring must
			// not order the two around each other either.
			jobs.Register(client, c.Queue.CleanupInterval, uploader, mailer, pool, c.App.BaseURL, c.Auth.ExpiryEmailEnabled, c.Mailer.Notifications.APIKeyExpiringNoticeEnabled, lazyPublisher{i}, lazyScimSyncer{i}, signup.NewRepository(), lazyWebhookRunner{i}, do.MustInvoke[*fetcher.Client](i), log)

			// The upload's after-sync hook rides here rather than on the
			// manager's provider: the hook enqueues through the client this
			// provider builds, and the manager must not construct against
			// the queue to stay buildable without one.
			uploader.WithAfterSync(jobs.NewUploadFinishedEnqueuer(client, log).Uploaded)
			return client, nil
		}),

		do.Lazy(func(i do.Injector) (*jobs.Seeder, error) {
			c := do.MustInvoke[*config.Config](i)
			client := do.MustInvoke[*queue.Client](i)
			uploader := do.MustInvoke[*storage.Manager](i)
			log := do.MustInvoke[*slog.Logger](i)
			return jobs.NewSeeder(client, c.Queue.CleanupInterval, uploader, c.App.AuditRetentionDays, c.Auth.ExpiryEmailEnabled, true, log), nil
		}),

		do.Lazy(func(i do.Injector) (*storage.Watcher, error) {
			c := do.MustInvoke[*config.Config](i)
			log := do.MustInvoke[*slog.Logger](i)
			manager := do.MustInvoke[*storage.Manager](i)
			client := do.MustInvoke[*queue.Client](i)
			return storage.NewWatcher(manager.Staging(), c.Storage.Watch.Debounce, log,
				func(key string) {
					if _, err := client.Add(jobs.ChunkUploadTask{Key: key}).Save(); err != nil {
						// The staging file is still on disk, so the loss is a
						// delayed upload, not a lost one: the next scan or the
						// next write re-enqueues it.
						log.Error("storage: enqueue upload", "key", key, "err", err)
					}
				}), nil
		}),

		do.Lazy(func(i do.Injector) (*scheduler.Scheduler, error) {
			c := do.MustInvoke[*config.Config](i)
			pool := do.MustInvoke[*datastore.Postgres](i)
			client := do.MustInvoke[*queue.Client](i)
			log := do.MustInvoke[*slog.Logger](i)
			location, err := time.LoadLocation(c.Queue.Timezone)
			if err != nil {
				return nil, err
			}
			return scheduler.New(scheduler.Config{
				Store:    pool,
				Client:   client,
				Logger:   log,
				Location: location,
				Jobs:     jobs.Scheduled(),
			})
		}),

		do.Lazy(func(i do.Injector) (middleware.Authenticator, error) {
			c := do.MustInvoke[*config.Config](i)
			keys := do.MustInvoke[*jwks.Service](i)
			machine := do.MustInvoke[*apikey.Service](i)
			return middleware.APIKeyAuth(identity.Authenticate(keys, *c), machine), nil
		}),

		do.Lazy(func(i do.Injector) (middleware.Limiter, error) {
			c := do.MustInvoke[*config.Config](i)
			switch c.RateLimit.Driver {
			case config.RateLimitDB:
				pool := do.MustInvoke[*datastore.Postgres](i)
				return middleware.NewDatabaseLimiter(pool), nil
			case config.RateLimitKV:
				// Validation refuses a kvstore driver while the backend is
				// disabled, so resolving the client here is always a run that
				// asked for it.
				kv := do.MustInvoke[*datastore.Valkey](i)
				return middleware.NewKVStoreLimiter(kv.Client()), nil
			default:
				return nil, fmt.Errorf("registry: rate_limit.driver: unknown driver %q", c.RateLimit.Driver)
			}
		}),
	)
}
