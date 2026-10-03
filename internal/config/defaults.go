package config

import "time"

// DefaultDataDir is where the application keeps local files, relative to the
// working directory. It is the default of Storage.LocalPath and of the storage
// health check, and it matches the compose volume (./storage:/srv/storage).
const DefaultDataDir = "storage"

// LogDir is the subdirectory of Storage.LocalPath the file sink writes to. It is
// a subdirectory rather than the data directory itself so the rotating files sit
// beside the backups and the certificates instead of among them.
const LogDir = "logs"

// LogFileName is the name of the active log file, inside LogDir. The rotated
// files take a timestamp suffix from it.
const LogFileName = "tango.log"

// UploadsDir is the subdirectory of Storage.LocalPath the /static route serves:
// files a feature wrote there whole, such as an avatar or an exported report.
// It is a subdirectory rather than the data directory itself so an upload never
// collides with the chunk store, the staging area, or the logs, and so the
// served tree holds only what is meant to be served.
//
// It is deliberately not where the chunk engine writes: a stored file is cut
// into content-addressed chunks under chunks/, which is not a browsable tree
// and is never served. A feature that wants a file reachable by URL writes a
// whole copy here.
const UploadsDir = "uploads"

// DefaultS3Region is the signing region a deployment that never sets one gets.
//
// A region cannot be empty: the S3 client refuses to resolve an endpoint without
// one and every request fails, even against a service that ignores the region
// such as MinIO. The value is therefore a usable one rather than a placeholder,
// and a deployment with a real region replaces it.
const DefaultS3Region = "us-east-1"

// DefaultOTLPEndpoint is the address a local OpenTelemetry collector listens on.
// It is the protocol's own default port, so one endpoint reaches a collector
// started on the same host. It is shared by logs, traces, and metrics: a
// collector is one endpoint receiving three signals.
const DefaultOTLPEndpoint = "http://localhost:4318"

// DefaultOTELHTTPPort is the port the default endpoint belongs to, and the one a
// gRPC configuration is refused at.
const DefaultOTELHTTPPort = "4318"

// DefaultOTELQueueSize is how many items one signal buffers before it starts
// dropping. The queue is what keeps export off the request path, so it is sized
// to absorb a collector that is briefly down rather than to a minimum.
const DefaultOTELQueueSize = 4096

// DefaultOTELBatchTimeout is how long a span waits in the queue before it is
// shipped, and DefaultOTELMaxBatchSize is how many go in one export. Together
// they trade export frequency against payload size.
const (
	DefaultOTELBatchTimeout = 5 * time.Second
	DefaultOTELMaxBatchSize = 512
)

// DefaultOTELExportTimeout bounds one export attempt for every signal. It is
// shorter than the batch interval so a stalled collector cannot make the export
// goroutine fall behind its own schedule.
const DefaultOTELExportTimeout = 10 * time.Second

// DefaultOTELMetricInterval is how often measurements are handed to the
// exporter. It is longer than the trace batch because a metric export carries
// the state of every instrument at once, not one event.
const DefaultOTELMetricInterval = 60 * time.Second

// DefaultPrometheusPath is where the Prometheus exposition is served on the
// application's own port. It is the path a scraper looks for by convention, so
// a scrape job needs no configuration beyond the address.
const DefaultPrometheusPath = "/metrics"

// DefaultCORSMaxAge is how long a browser may cache a preflight answer. An hour
// keeps the preflight out of most sessions without promising an origin list a
// redeploy of the configuration could have changed.
const DefaultCORSMaxAge = time.Hour

// DefaultCacheMaxMemory is the byte budget of the in-memory cache driver. It
// bounds what the driver may hold: a cache that grows without bound would
// quietly become the largest consumer of the process. A deployment serving
// more replaces it through the configuration.
const DefaultCacheMaxMemory = 32 << 20

// DefaultStorageWatchDebounce is how long a staging path must stay quiet
// before the watcher enqueues its upload, so a file written in many small
// writes triggers one upload, not one per write.
const DefaultStorageWatchDebounce = 2 * time.Second

// DefaultCORSOrigins is the origin list a fresh checkout gets: the Vite dev
// server the SPA is served from in development. Production names its own
// origin through the configuration, so a browser has to prove where the call
// comes from rather than being trusted by default.
var DefaultCORSOrigins = []string{"http://localhost:3080"}

// DefaultCORSMethods is the method list the dual surface needs: the GET and
// POST the Connect protocol answers with, plus the PUT, PATCH, and DELETE the
// REST surface mounts. The methods connectrpc/cors-go presets are GET and
// POST alone — its surface has no REST half.
var DefaultCORSMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// DefaultCORSHeaders is the header list a browser call may set: the fetch
// headers the API envelope reads, the authorization header the tokens arrive
// in, the machine credential's header, and the protocol headers
// connectrpc/cors-go presets for Connect and gRPC-Web consumers.
var DefaultCORSHeaders = []string{
	// The fetch headers any HTTP call may carry.
	"Accept",
	"Content-Type",
	// The credentials the two transports arrive in: the Bearer token and the
	// API key on both surfaces.
	"Authorization",
	"X-Requested-With",
	"X-Api-Key",
	// The Connect protocol's own headers.
	"Connect-Protocol-Version",
	"Connect-Timeout-Ms",
	"X-User-Agent",
	// The gRPC-Web headers, so the policy survives a gRPC-Web consumer.
	"Grpc-Timeout",
	"X-Grpc-Web",
}

// DefaultCORSExposedHeaders is the response header list a browser script may
// read on a cross-origin answer: the gRPC-Web status fields
// connectrpc/cors-go exposes, so an error's code and message are visible to
// the caller instead of opaque. Tango serves no trailers, so the list needs
// nothing else.
var DefaultCORSExposedHeaders = []string{
	"Grpc-Message",
	"Grpc-Status",
	"Grpc-Status-Details-Bin",
}

// Mode names of the supported runtime modes.
const (
	ModeDevelopment = "development"
	ModeStaging     = "staging"
	ModeProduction  = "production"
	ModeTest        = "test"
)

// Log levels accepted by Log.Level.
const (
	LogDebug = "debug"
	LogInfo  = "info"
	LogWarn  = "warn"
	LogError = "error"
)

// Defaults for the rotating file sink. The sink is opt-in, so these apply only
// once log.transport names it.
const (
	// DefaultLogMaxSizeMB is the size at which the active file is rotated. It
	// matches the sink's own default, so leaving the key out and writing 100
	// produce the same file.
	DefaultLogMaxSizeMB = 100
	// DefaultLogMaxBackups and DefaultLogMaxAge bound what is kept. Both are
	// needed: they are independent limits, and zero on both would keep every
	// rotated file forever, which fills a disk quietly.
	DefaultLogMaxBackups = 7
	DefaultLogMaxAge     = 30
)

// Defaults for the background task queue. The worker pool is sized for a
// small deployment.
//
// ReleaseAfter must stay above the longest queue Timeout any feature
// configures, or a slow task is handed to a second worker while the first one
// is still running. The longest today is the chunk upload's 30 minutes, so the
// default is twice that. queue.Client.Register refuses a queue whose Timeout
// reaches it, so a feature that needs longer fails at wiring rather than
// running twice.
const (
	DefaultQueueNumWorkers     = 5
	DefaultQueueReleaseAfter   = time.Hour
	DefaultQueueCleanupArchive = time.Hour
)

// DefaultSchedulerTimezone is where a spec that names no zone fires. UTC is
// the only zone that needs no data file and reads the same on every host.
const DefaultSchedulerTimezone = "UTC"

// DefaultRateLimitAuthLimit is the credential bucket's budget per window: the
// sign-in, the code verifications, and the email senders share it. Ten a
// minute is several attempts more than one person needs and far fewer than
// a script wants; the deployment can loosen it through the configuration.
const DefaultRateLimitAuthLimit = 10

// DefaultMaxRequestBytes is the body size one RPC request may carry. A
// contract's largest legitimate message — a profile picture upload rides the
// REST surface with its own bound — stays far below it, and a caller with
// more to send has an upload route, not a procedure payload.
const DefaultMaxRequestBytes = 1 << 20

// Defaults for the outbound HTTP client. The waits are long enough that a
// blip is retried and short enough that a dead upstream is abandoned, and
// the breaker opens only after more failures than one call can produce.
const (
	DefaultFetcherTimeout         = 10 * time.Second
	DefaultFetcherRetryCount      = 2
	DefaultFetcherRetryWait       = time.Second
	DefaultFetcherRetryMaxWait    = 8 * time.Second
	DefaultFetcherCircuitFailures = 5
	DefaultFetcherCircuitSuccess  = 2
	DefaultFetcherCircuitReset    = 30 * time.Second
	DefaultFetcherMaxBody         = 16 << 20
)

// DefaultUserAgent is the product token the outbound client sends when a
// deployment does not name its own. The comment is a URL a person can open.
// It is not a browser token: an upstream that special-cases browsers would
// be answering a client this process is not.
func DefaultUserAgent() string {
	return AppIdentifier + "/" + AppVersion + " (+https://github.com/riipandi/tango)"
}

// DefaultMailerTimeout bounds one mail submission. It is longer than the
// outbound HTTP attempt: an SMTP session is several round trips, and the last
// of them carries the whole message.
const DefaultMailerTimeout = 15 * time.Second

// DefaultAssetsURL is where the browser fetches static assets from when a
// deployment names none: the /static mount the application itself serves.
// A deployment may point app.assets_url at an S3 bucket or a CDN origin —
// the value is one URL, and nothing else changes with it.
const DefaultAssetsURL = "http://localhost:3080/static"

// Default returns the built-in configuration. These values are the lowest
// precedence layer: every other source may replace them, but a key no source
// mentions keeps the value set here.
func Default() Config {
	return Config{
		App: App{
			Mode: ModeDevelopment,
			// The public origin is empty by default: links are built absolute
			// only where a deployment asks for it.
			BaseURL:   "",
			AssetsURL: DefaultAssetsURL,
			// The reset token never appears in a response by default: the
			// email is the only channel the raw value travels through.
			ExposeResetToken: false,
			// The authenticator secret never appears in a listing: the
			// enrollment's one answer is the only channel it travels
			// through, and development debugging turns this on by hand.
			ExposeTotpSecret: false,
			// Ninety days is Pocket ID's own default, and the window a
			// security review usually asks for: long enough to answer "what
			// happened last quarter", short enough that the table stays a
			// table rather than an archive.
			AuditRetentionDays: 90,
		},
		Auth: Auth{
			Issuer:        "tango",
			AccessTTL:     15 * time.Minute,
			SessionDriver: SessionDB,
			// The API key's expiry reminder is off by default, the way the
			// upstream feature ships: a mailer that reaches account holders
			// on a schedule is a decision, not a default.
			ExpiryEmailEnabled: false,
			// Both email paths are opt-in: the administrative one because an
			// operator's mailer becomes a credential sender, the public one
			// because anyone reachable can drive it at any address.
			OneTimeAccessEmailAsAdminEnabled:           false,
			OneTimeAccessEmailAsUnauthenticatedEnabled: false,
		},
		Cache: Cache{
			// Off by default: a feature that wants caching switches it on
			// in the file, so a fresh checkout carries no cache at all.
			Enable: false,
			Driver: CacheMemory,
			TTL:    5 * time.Minute,
			// A budget the deployment can reason about: most of a small
			// container's memory must not belong to the cache by default.
			MaxMemory: DefaultCacheMaxMemory,
		},
		Fetcher: Fetcher{
			UserAgent:               DefaultUserAgent(),
			Timeout:                 DefaultFetcherTimeout,
			RetryCount:              DefaultFetcherRetryCount,
			RetryWait:               DefaultFetcherRetryWait,
			RetryMaxWait:            DefaultFetcherRetryMaxWait,
			CircuitFailureThreshold: DefaultFetcherCircuitFailures,
			CircuitSuccessThreshold: DefaultFetcherCircuitSuccess,
			CircuitResetTimeout:     DefaultFetcherCircuitReset,
			MaxBodyBytes:            DefaultFetcherMaxBody,
		},
		Database: Database{
			MaxConns:             10,
			MinConns:             2,
			MaxConnLifetime:      time.Hour,
			MaxConnIdleTime:      30 * time.Minute,
			ConnectTimeout:       5 * time.Second,
			ConnectAttempts:      5,
			ConnectRetryInterval: 2 * time.Second,
			SearchPath:           "public,internal,reference",
			Timezone:             "UTC",
		},
		KVStore: KVStore{
			// Disabled by default, so a fresh checkout runs on Postgres and
			// in-process memory alone. The URL points at the server compose
			// starts, so switching Enable on is the only step needed.
			Enable: false,
			URL:    "redis://default:securedb@localhost:6379",
			DB:     0,
		},
		Log: Log{
			Level: LogInfo,
			// The console alone: a fresh checkout writes to the terminal and
			// nothing else, so no run needs a volume or a collector to start.
			Transport: []string{LogTransportConsole},
			Format:    LogPretty,
			File: LogFile{
				MaxSize:    DefaultLogMaxSizeMB,
				MaxBackups: DefaultLogMaxBackups,
				MaxAge:     DefaultLogMaxAge,
				Compress:   true,
			},
			// A ten-second export attempt, shared with the other signals:
			// DefaultOTELExportTimeout is the one answer to "how long may one
			// export hold".
			OTLP: LogOTLP{Timeout: DefaultOTELExportTimeout},
		},
		OTEL: OTEL{
			// The collector a local receiver listens on, so enabling a signal
			// is the only step needed.
			Endpoint:    DefaultOTLPEndpoint,
			ServiceName: AppIdentifier,
			Compression: OTELCompressionGzip,
			// No headers: a collector that checks nothing needs nothing sent.
			Headers: map[string]string{},
			Queue: OTELQueue{
				MaxSize: DefaultOTELQueueSize,
			},
			// No path on any signal: a collector serves the protocol's own
			// routes, and the shared endpoint already names where it is.
			Tracing: OTELTracing{
				// Disabled: a signal nothing consumes is work nothing asked
				// for. The sampler records everything once it is switched on,
				// which is what a developer wiring the stack wants to see.
				Enable:        false,
				Sampler:       OTELSamplerAlways,
				Ratio:         1,
				BatchTimeout:  DefaultOTELBatchTimeout,
				ExportTimeout: DefaultOTELExportTimeout,
				MaxBatchSize:  DefaultOTELMaxBatchSize,
			},
			Metrics: OTELMetrics{
				Enable:         false,
				Push:           false,
				PrometheusPath: DefaultPrometheusPath,
				Interval:       DefaultOTELMetricInterval,
				ExportTimeout:  DefaultOTELExportTimeout,
			},
		},
		Mailer: Mailer{
			FromEmail: "mailer@example.com",
			FromName:  "Tango Mailer",
			SMTPPort:  587,
			Timeout:   DefaultMailerTimeout,
			Notifications: MailerNotifications{
				NewDeviceNoticeEnabled:       true,
				PasswordChangedNoticeEnabled: true,
				MfaDisabledNoticeEnabled:     true,
				UserBannedNoticeEnabled:      true,
				UserUnbannedNoticeEnabled:    true,
				APIKeyExpiringNoticeEnabled:  true,
				EmailChangeNoticeEnabled:     true,
				SignupAttemptNoticeEnabled:   true,
				UserLockedNoticeEnabled:      true,
				AnnouncementEmailEnabled:     true,
			},
		},
		RateLimit: RateLimit{
			Driver:    RateLimitDB,
			Limit:     60,
			AuthLimit: DefaultRateLimitAuthLimit,
			Window:    time.Minute,
		},
		OIDC: OIDC{
			// Off by default: the provider signs with the database's
			// stored keys, and a run without one is not an issuer —
			// opting in is the operator's decision.
			Enabled: false,
		},
		Queue: Queue{
			NumWorkers:      DefaultQueueNumWorkers,
			ReleaseAfter:    DefaultQueueReleaseAfter,
			CleanupInterval: DefaultQueueCleanupArchive,
			Encrypt:         false,
			Timezone:        DefaultSchedulerTimezone,
		},
		Server: Server{
			Host:            "0.0.0.0",
			Port:            3080,
			ReadTimeout:     15 * time.Second,
			WriteTimeout:    30 * time.Second,
			IdleTimeout:     60 * time.Second,
			ShutdownTimeout: 15 * time.Second,
			MaxRequestBytes: DefaultMaxRequestBytes,
			// The process is reached directly by default: a header is only
			// believed once a deployment says its proxy sets it. An empty
			// slice rather than nil, so a generated file writes it as [] and
			// the round trip reads back the same value.
			TrustedProxyHeaders: []string{},
			CORS: CORS{
				AllowedOrigins: DefaultCORSOrigins,
				AllowedMethods: DefaultCORSMethods,
				AllowedHeaders: DefaultCORSHeaders,
				ExposedHeaders: DefaultCORSExposedHeaders,
				MaxAge:         DefaultCORSMaxAge,
			},
		},
		Storage: Storage{
			Driver:    StorageLocal,
			LocalPath: DefaultDataDir,
			Watch: Watch{
				Enable:   true,
				Debounce: DefaultStorageWatchDebounce,
			},
			S3: S3{
				// Disabled by default along with the driver, but the values
				// point at the service compose starts, so switching the driver
				// is the only step needed.
				ForcePathStyle:   true,
				Region:           DefaultS3Region,
				SignedURLExpires: time.Hour,
			},
		},
		Webhook: Webhook{
			// The private network stays out of reach by default: a
			// deployment that hosts its own receivers turns this on, and
			// every other destination is public internet.
			AllowPrivateNetwork: false,
		},
	}
}

// DefaultsMap returns the built-in defaults as a flat map keyed by config path.
// A secret has an empty default, so a fresh checkout carries no placeholder
// credential: the layer that supplies it is the only source of that key.
func DefaultsMap() map[string]any {
	return flatten(Default())
}
