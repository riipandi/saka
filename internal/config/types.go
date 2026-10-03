package config

import (
	"time"
)

// Config is the resolved application configuration. It is the result of merging
// the built-in defaults, the JSON config file, and the command-line flags, in
// that order. The environment is not a layer: it is the table the config file's
// directives resolve from.
//
// The struct is the schema: every key has a koanf tag (how a source names it),
// a json tag (how it is written back out, and how Default is flattened), a
// default in Default(), and a rule in Validate().
type Config struct {
	App       App       `koanf:"app" json:"app"`
	Auth      Auth      `koanf:"auth" json:"auth"`
	Cache     Cache     `koanf:"cache" json:"cache"`
	Database  Database  `koanf:"database" json:"database"`
	Fetcher   Fetcher   `koanf:"fetcher" json:"fetcher"`
	KVStore   KVStore   `koanf:"kvstore" json:"kvstore"`
	Log       Log       `koanf:"log" json:"log"`
	Mailer    Mailer    `koanf:"mailer" json:"mailer"`
	OIDC      OIDC      `koanf:"oidc" json:"oidc"`
	OTEL      OTEL      `koanf:"otel" json:"otel"`
	Queue     Queue     `koanf:"queue" json:"queue"`
	RateLimit RateLimit `koanf:"rate_limit" json:"rate_limit"`
	Server    Server    `koanf:"server" json:"server"`
	Storage   Storage   `koanf:"storage" json:"storage"`
	Webhook   Webhook   `koanf:"webhook" json:"webhook"`

	// origin records which source last set each key, for conflict resolution.
	// It is unexported so it never reaches JSON or a log line.
	origin map[string]string
	// unresolved records the keys whose directive named a missing variable,
	// mapped to the variable name. It is state about the sources, not config.
	unresolved map[string]string
}

// App holds process-level settings.
type App struct {
	// Mode names the runtime mode: development, staging, production, or test.
	Mode string `koanf:"mode" json:"mode"`
	// SecretKey is the hex-encoded AES-256 key used to seal stored values.
	SecretKey string `koanf:"secret_key" json:"secret_key"`
	// BaseURL is the public origin, used to build absolute links.
	BaseURL string `koanf:"base_url" json:"base_url"`
	// AssetsURL is where the browser fetches stored files from: the built-in
	// /storage/{bucket}/{key} mount by default, an S3 bucket or a CDN origin
	// whenever a deployment points the variable there.
	AssetsURL string `koanf:"assets_url" json:"assets_url"`
	// ExposeResetToken answers the raw password-reset token in the
	// ForgotPassword response. It is a development mode only aid — the
	// response hands the account to anyone who knows the address — and
	// validation refuses it anywhere else.
	ExposeResetToken bool `koanf:"expose_reset_token" json:"expose_reset_token"`
	// ExposeTotpSecret answers the decrypted authenticator secret in the
	// ListTotpEnrollments response. It is a development aid for debugging
	// enrollment — the response hands every account's second factor to the
	// caller — so validation refuses it outside the development mode.
	ExposeTotpSecret bool `koanf:"expose_totp_secret" json:"expose_totp_secret"`
	// AuditRetentionDays is how long an audit record is kept. The cleanup
	// job deletes what is older, so the table is bounded by a policy rather
	// than by whoever remembers to prune it. Zero would mean "keep
	// forever", which is why Validate refuses it: an unbounded audit table
	// is a decision a deployment should make on purpose, not a default it
	// never noticed.
	AuditRetentionDays int `koanf:"audit_retention_days" json:"audit_retention_days"`
	// Timezone is the one zone the deployment runs in: every pooled
	// database session is pinned to it and a cron spec that names no zone
	// of its own is resolved against it, so "0 3 * * *" is three in the
	// morning in this zone everywhere the process looks.
	Timezone string `koanf:"timezone" json:"timezone"`
}

// Auth holds the JWT signing settings. The asymmetric signing key lives in
// the database (public.jwks, provisioned by saka initialize); the config
// carries only the optional HMAC secret and the algorithm override.
type Auth struct {
	// SecretKey is the hex-encoded HMAC key, used when no key pair is given.
	SecretKey string `koanf:"secret_key" json:"secret_key"`
	// JWTAlgorithm names the algorithm a token is signed with when the
	// deployment configures both stacks. An empty value derives it: the
	// algorithm the active signing row carries when the database holds one,
	// the HMAC secret's length otherwise.
	//
	// It exists because two configured stacks are otherwise ambiguous, and
	// which one signs is a deployment decision. It is not part of the sample
	// file: a deployment that configures one stack never needs it, and the
	// derived answer is the right one for every such deployment.
	JWTAlgorithm string `koanf:"jwt_algorithm" json:"jwt_algorithm"`
	// Issuer is the iss claim placed in every token.
	Issuer string `koanf:"issuer" json:"issuer"`
	// AccessTTL is the lifetime of an access token. The session bounds —
	// the remembered lifetime, inactivity, reauthentication — are not here:
	// they are settings the database owns.
	AccessTTL time.Duration `koanf:"access_ttl" json:"access_ttl"`
	// HIBPAPIKey is the Have I Been Pwned account key. The password checks
	// never need it — the range API's k-anonymity is unauthenticated — so
	// the key only becomes load-bearing when a deployment calls the
	// account-level endpoints itself. An empty value does not turn the
	// corpus check off: the check rides `password.reject_compromised` and
	// answers without the header.
	HIBPAPIKey string `koanf:"hibp_api_key" json:"hibp_api_key"`
	// ExpiryEmailEnabled turns the API key's expiry reminder on. Off by
	// default, the way the upstream feature ships: a mailer that reaches
	// account holders on a schedule is a decision, not a default. It lives
	// beside the token lifetimes because the reminder is the tokens'
	// housekeeping, not the machine credentials' own surface.
	ExpiryEmailEnabled bool `koanf:"expiry_email_enabled" json:"expiry_email_enabled"`
	// SessionDriver is the backend the sign-in session store reads: SessionDB
	// or SessionKV. It lives beside the token lifetimes because the store
	// serves exactly those tokens, and there is no second session concept to
	// configure.
	SessionDriver string `koanf:"session_driver" json:"session_driver"`
	// OneTimeAccessEmailAsAdminEnabled opens the administrative email path:
	// an operator may have a one-time access code sent to an account's
	// address. Off by default, the way the upstream feature ships.
	OneTimeAccessEmailAsAdminEnabled bool `koanf:"one_time_access_email_as_admin_enabled" json:"one_time_access_email_as_admin_enabled"`
	// OneTimeAccessEmailAsUnauthenticatedEnabled opens the public email path:
	// a caller with no session may ask for a code by naming an address. Off
	// by default, because it turns the mailer into something anyone on the
	// internet can drive.
	OneTimeAccessEmailAsUnauthenticatedEnabled bool `koanf:"one_time_access_email_as_unauthenticated_enabled" json:"one_time_access_email_as_unauthenticated_enabled"`
}

// Cache holds the key-value cache settings.
type Cache struct {
	// Enable switches the cache on. Disabled by default, so a feature that
	// has not decided to be cacheable cannot grow one by accident; a driver
	// that is configured while the cache is off is never read.
	Enable bool `koanf:"enable" json:"enable"`
	// Driver is CacheMemory or CacheKV. It is read only while Enable is
	// true, the way a signal section is read only while the signal is on.
	Driver string `koanf:"driver" json:"driver"`
	// TTL is the default lifetime of a cached entry.
	TTL time.Duration `koanf:"ttl" json:"ttl"`
	// MaxMemory is the byte budget of the in-memory driver. When the budget
	// runs out the driver resets itself, keeping the memory it already owns
	// rather than growing without bound.
	MaxMemory int64 `koanf:"max_memory" json:"max_memory"`
}

// Database holds the Postgres connection and pool settings. The fields mirror
// datastore.PostgresOptions, so a resolved Config maps onto a pool directly.
type Database struct {
	// URL is the connection string. It is the one secret that must never be
	// logged; use Redacted before printing a Config.
	URL string `koanf:"url" json:"url"`
	// MaxConns and MinConns bound the pool size.
	MaxConns int32 `koanf:"max_conns" json:"max_conns"`
	MinConns int32 `koanf:"min_conns" json:"min_conns"`
	// MaxConnLifetime and MaxConnIdleTime recycle pooled connections.
	MaxConnLifetime time.Duration `koanf:"max_conn_lifetime" json:"max_conn_lifetime"`
	MaxConnIdleTime time.Duration `koanf:"max_conn_idle_time" json:"max_conn_idle_time"`
	// ConnectTimeout bounds the initial connection attempt.
	ConnectTimeout time.Duration `koanf:"connect_timeout" json:"connect_timeout"`
	// ConnectAttempts is how many times the startup probe tries to reach the
	// database before the run fails, and ConnectRetryInterval is the wait
	// between two attempts. One attempt means the probe reports instead of
	// waiting, which is what a command that only reads one key wants.
	ConnectAttempts      int           `koanf:"connect_attempts" json:"connect_attempts"`
	ConnectRetryInterval time.Duration `koanf:"connect_retry_interval" json:"connect_retry_interval"`
	// SearchPath is applied to every pooled connection.
	SearchPath string `koanf:"search_path" json:"search_path"`
}

// Fetcher holds the outbound HTTP client used to call external services.
//
// Callers pass an absolute URL. The address of an upstream lives in the
// code that calls it. The values here are the resilience shared by every call.
type Fetcher struct {
	// UserAgent is the product token sent on every request. It names this
	// application. It is not a browser token and not another client's token.
	UserAgent string `koanf:"user_agent" json:"user_agent"`
	// Timeout bounds one attempt. A caller's context deadline still ends the
	// whole call, retries included.
	Timeout time.Duration `koanf:"timeout" json:"timeout"`
	// RetryCount is how many extra attempts follow a transient failure.
	// Zero disables retry. POST and PATCH are not retried: a second
	// submission can apply the operation twice.
	RetryCount int `koanf:"retry_count" json:"retry_count"`
	// RetryWait is the floor of the exponential backoff, and RetryMaxWait
	// is the ceiling. The wait is jittered so retries from many processes
	// do not land together.
	RetryWait    time.Duration `koanf:"retry_wait" json:"retry_wait"`
	RetryMaxWait time.Duration `koanf:"retry_max_wait" json:"retry_max_wait"`
	// CircuitFailureThreshold is how many failed attempts open the breaker.
	// It stays above RetryCount so the retries of one call cannot open it.
	CircuitFailureThreshold int `koanf:"circuit_failure_threshold" json:"circuit_failure_threshold"`
	// CircuitSuccessThreshold is how many successful probes close an open
	// breaker. CircuitResetTimeout is how long it stays open before the
	// first probe. Each upstream host has its own breaker, so one host
	// opening does not stop calls to another.
	CircuitSuccessThreshold int           `koanf:"circuit_success_threshold" json:"circuit_success_threshold"`
	CircuitResetTimeout     time.Duration `koanf:"circuit_reset_timeout" json:"circuit_reset_timeout"`
	// MaxBodyBytes is how much of a response body is kept. The rest is
	// refused, so an upstream cannot grow the process without a bound.
	MaxBodyBytes int64 `koanf:"max_body_bytes" json:"max_body_bytes"`
}

// KVStore holds the optional key-value backend settings, for a Valkey or Redis
// compatible server.
//
// It is opt-in and never required: with Enable false the application runs on
// Postgres and in-process memory alone, which is what keeps a local checkout
// from needing a second server. Enable gates availability; the driver fields
// on Cache and RateLimit and the auth session driver still choose which
// backend each one uses, so switching the kvstore on does not silently move
// anything.
type KVStore struct {
	// Enable reports whether the key-value backend is available. A driver set to
	// kvstore while this is false is a contradiction Validate reports.
	Enable bool `koanf:"enable" json:"enable"`
	// URL is the connection string, such as
	// redis://default:password@localhost:6379. It carries a password, so Redacted
	// hides it and a report reduces it to host:port/database.
	URL string `koanf:"url" json:"url"`
	// DB is the logical database index to select.
	DB int `koanf:"db" json:"db"`
}

// Log holds the logging settings.
type Log struct {
	// Level is one of debug, info, warn, or error.
	Level string `koanf:"level" json:"level"`
	// Transport names the sinks to write to, in the order given. More than one
	// may be named, so a run can keep the terminal and ship to a collector at
	// once. The default is the console alone, which is what a fresh checkout
	// needs and what a container that logs to stdout wants.
	//
	// It is a list rather than a set of switches because the sinks are not
	// alternatives: naming one is what turns it on, and there is no second flag
	// that could disagree with the list.
	Transport []string `koanf:"transport" json:"transport"`
	// Format names the console sink's rendering: LogPretty or LogStructured.
	// It lives at the top level because the console is the only sink a
	// person reads: a file or a collector keeps its machine form regardless,
	// so no other sink has a key to choose it.
	Format string `koanf:"format" json:"format"`
	// File holds the rotating file sink settings, read when Transport names
	// LogTransportFile.
	File LogFile `koanf:"file" json:"file"`
	// OTLP holds the log export settings that are specific to logs, read when
	// Transport names LogTransportOTLP.
	OTLP LogOTLP `koanf:"otlp" json:"otlp"`
}

// LogOTLP holds the log export settings that are specific to logs.
//
// The address is deliberately not here: one collector receives every signal, so
// where it is belongs to otel.endpoint, and a second copy of it could disagree.
// What logs own is the route they take on that collector, which is where a
// collector configured to split a signal puts them. It sits under log rather
// than under otel because it is a property of this sink, the way log.file holds
// the rotation settings of the file sink.
type LogOTLP struct {
	// Path is the collector route for logs. Empty means the protocol's own
	// /v1/logs, which is what a collector serves.
	Path string `koanf:"path" json:"path"`
	// Timeout bounds one export attempt, the way otel.tracing.export_timeout
	// bounds a trace export. Written as a plain number of seconds.
	Timeout time.Duration `koanf:"timeout" json:"timeout"`
}

// LogFile holds the rotating file sink settings.
//
// There is no filename key: the sink writes under the one data directory of the
// process, storage.local_path + /logs, so a run cannot disagree with the
// configuration about where its files live. The rotation settings are read only
// when the file transport is named.
type LogFile struct {
	// MaxSize is the size in megabytes the active file reaches before it is
	// rotated.
	MaxSize int `koanf:"max_size" json:"max_size"`
	// MaxBackups is how many rotated files are kept, and MaxAge how many days
	// one is kept for. They are independent: a file is deleted when either
	// limit is exceeded, so zero on both keeps every rotated file forever.
	MaxBackups int `koanf:"max_backups" json:"max_backups"`
	MaxAge     int `koanf:"max_age" json:"max_age"`
	// Compress gzips a rotated file.
	Compress bool `koanf:"compress" json:"compress"`
}

// OIDC holds the identity-provider surface's settings: the switch the
// protocol endpoints answer behind, and the policy the client-id metadata
// document feature fetches under.
type OIDC struct {
	// Enabled serves the OIDC protocol endpoints (/oidc/*, the discovery
	// documents). Off, the endpoints answer not found and the management
	// surface still runs, so a deployment that does not federate can keep
	// the surface dark without losing its client records.
	Enabled bool `koanf:"enabled" json:"enabled"`
	// CIMDURLAllowlist is the URL patterns a client-id metadata document
	// (a CIMD client) may materialize from. An empty list refuses CIMD
	// entirely: no document is ever fetched.
	CIMDURLAllowlist []string `koanf:"cimd_url_allowlist" json:"cimd_url_allowlist"`
}

// OTEL holds the OpenTelemetry settings the three signals share, plus the
// section each one owns.
//
// One collector address serves all three, because that is what a collector is:
// a single endpoint that receives logs, traces, and metrics. A signal whose
// collector routes it elsewhere overrides only its own path, never its own
// address, so there is one place that says where the collector is.
//
// Every signal is opt-in and none is required, like every other external
// backend: a run that enables nothing dials nothing and needs no collector.
type OTEL struct {
	// Endpoint is the collector's OTLP/HTTP address: http://localhost:4318 or
	// https://collector.example.com:4318. The scheme decides whether the
	// connection is TLS, so it is not a separate setting.
	//
	// One address serves every signal, and one collector receives the three of
	// them — an HTTP exporter dials the URL as written.
	//
	// It is read when any signal is enabled: logs name the otlp transport,
	// traces set tracing.enable, or metrics set metrics.enable.
	Endpoint string `koanf:"endpoint" json:"endpoint"`
	// ServiceName is the service every signal is attributed to. It defaults to
	// the application identifier rather than to an empty string, because a
	// record with no service name cannot be attributed at all.
	ServiceName string `koanf:"service_name" json:"service_name"`
	// Environment names the deployment a signal came from, such as production
	// or staging. It is a resource attribute, so one collector can tell two
	// deployments apart.
	Environment string `koanf:"environment" json:"environment"`
	// Compression is OTELCompressionGzip or OTELCompressionNone. Gzip is the
	// protocol's own default; none saves the CPU on a collector reached over a
	// loopback or a local network.
	Compression string `koanf:"compression" json:"compression"`
	// Headers are sent with every export, for a collector that authenticates
	// the sender. They are shared by the three signals because the one collector
	// they reach is the one that checks them.
	//
	// A header value is a secret by default: an authorization token is the
	// reason this key exists at all, and a value that must not be logged cannot
	// be told from one that may, so every value is rendered through the same
	// path a secret takes.
	Headers map[string]string `koanf:"headers" json:"headers"`
	// Queue bounds the in-memory buffer each signal exports from.
	Queue OTELQueue `koanf:"queue" json:"queue"`
	// Tracing holds the trace export settings.
	Tracing OTELTracing `koanf:"tracing" json:"tracing"`
	// Metrics holds the metric export settings.
	Metrics OTELMetrics `koanf:"metrics" json:"metrics"`
}

// OTELQueue bounds the buffer a signal exports from.
//
// It is the setting that keeps export off the request path: a span or a
// measurement is handed to an in-memory queue and the caller returns, while a
// background goroutine drains the queue to the collector. Nothing here ever
// blocks the goroutine that produced a signal, so a slow or unreachable
// collector costs dropped telemetry, never a slow request.
type OTELQueue struct {
	// MaxSize is how many items one signal buffers before it starts dropping.
	// The default is generous rather than minimal, because the queue is what
	// absorbs a collector that is briefly down.
	MaxSize int `koanf:"max_size" json:"max_size"`
}

// OTELTracing holds the trace export settings.
//
// It is read only when Enable is true, so a deployment that does not collect
// traces is not held to a sampler it never runs.
type OTELTracing struct {
	// Enable exports spans. A service that does not trace dials nothing.
	Enable bool `koanf:"enable" json:"enable"`
	// Path is the collector route for traces. Empty means the protocol's own
	// /v1/traces, which is what a collector serves.
	Path string `koanf:"path" json:"path"`
	// Sampler is one of OTELSamplers. It decides which traces are recorded.
	Sampler string `koanf:"sampler" json:"sampler"`
	// Ratio is the fraction of traces recorded, and is read only by the two
	// ratio samplers. It is a fraction rather than a percentage so the value
	// reads the way the sampler's own name does.
	Ratio float64 `koanf:"ratio" json:"ratio"`
	// BatchTimeout is how long a span waits in the queue before the exporter
	// ships it, and ExportTimeout bounds one export attempt.
	BatchTimeout  time.Duration `koanf:"batch_timeout" json:"batch_timeout"`
	ExportTimeout time.Duration `koanf:"export_timeout" json:"export_timeout"`
	// MaxBatchSize is how many spans one export carries.
	MaxBatchSize int `koanf:"max_batch_size" json:"max_batch_size"`
}

// OTELMetrics holds the metric export settings.
//
// It is read only when Enable is true. Pull is the primary route: the
// Prometheus exposition at prometheus_path is always served when metrics are
// enabled, and a scrape reads a snapshot the bridge already holds, so the
// metrics keep flowing while the collector is down. Push is opt-in: a
// deployment that has no scraper — or an application the collector cannot
// reach — sets Push to add the periodic reader, which exports to the collector
// the way the other signals do.
type OTELMetrics struct {
	// Enable records and exports metrics.
	Enable bool `koanf:"enable" json:"enable"`
	// Push adds the OTLP push leg on top of the pull exposition. It is read
	// only when Enable is true; the pull route needs no second switch.
	Push bool `koanf:"push" json:"push"`
	// Path is the collector route for metrics. Empty means the protocol's own
	// /v1/metrics. It is read only when Push is true.
	Path string `koanf:"path" json:"path"`
	// PrometheusPath is where the Prometheus exposition is served, on the
	// application's own port. It is a path rather than a switch: the exposition
	// is always served when metrics are enabled, and a scrape job needs the
	// path to be a decision rather than a second enable flag.
	PrometheusPath string `koanf:"prometheus_path" json:"prometheus_path"`
	// Interval is how often measurements are handed to the push exporter, and
	// ExportTimeout bounds one export attempt. Both are read only when Push is
	// true: the pull route has no exporter to schedule.
	Interval      time.Duration `koanf:"interval" json:"interval"`
	ExportTimeout time.Duration `koanf:"export_timeout" json:"export_timeout"`
}

// Mailer holds the outbound email settings. The mailer is optional: with no SMTP
// host the application still runs, it just cannot send mail. That is what keeps
// a local checkout from needing a mail server.
type Mailer struct {
	// FromEmail and FromName are the sender every message is sent as.
	FromEmail string `koanf:"from_email" json:"from_email"`
	FromName  string `koanf:"from_name" json:"from_name"`
	// SMTPHost is the mail server. An empty host means the mailer is not
	// configured, and it is not a configuration error.
	SMTPHost string `koanf:"smtp_host" json:"smtp_host"`
	// SMTPPort is the submission port: 587 for STARTTLS, 465 for implicit TLS.
	SMTPPort int `koanf:"smtp_port" json:"smtp_port"`
	// SMTPUsername and SMTPPassword authenticate the session. The password is a
	// secret, so Redacted hides it and Sample writes it as a directive.
	SMTPUsername string `koanf:"smtp_username" json:"smtp_username"`
	SMTPPassword string `koanf:"smtp_password" json:"smtp_password"`
	// SMTPSecure selects implicit TLS on connect instead of STARTTLS.
	SMTPSecure bool `koanf:"smtp_secure" json:"smtp_secure"`
	// SMTPAllowPlaintextAuth permits a credential to be sent over an
	// unencrypted connection to a host that is not this machine. It defaults to
	// false: a password in the clear is a leak, and a server that offers
	// STARTTLS is never affected. A loopback host needs no setting, because a
	// connection to it never leaves the machine.
	SMTPAllowPlaintextAuth bool `koanf:"smtp_allow_plaintext_auth" json:"smtp_allow_plaintext_auth"`
	// Timeout bounds one send: the dial, the handshake, the commands, and the
	// message body. A submission that hangs must fail rather than hold the
	// caller for as long as the kernel's own connect timeout allows.
	Timeout time.Duration `koanf:"timeout" json:"timeout"`
	// Notifications gates the notice emails per flow. Only the notices are
	// toggleable: the transactional emails (a password reset link, a
	// verification token, a one-time access code) carry the flow itself, so
	// switching them off would break the feature rather than save the cost.
	Notifications MailerNotifications `koanf:"notifications" json:"notifications"`
}

// MailerNotifications says which notice emails a deployment pays for. Every
// flow defaults to on: a notice left off is a deliberate cost decision, and
// the account side of the fact — the audit record — is written either way.
type MailerNotifications struct {
	// NewDeviceNoticeEnabled gates the sign-in notice an unseen browser
	// fingerprint sends.
	NewDeviceNoticeEnabled bool `koanf:"new_device_notice_enabled" json:"new_device_notice_enabled"`
	// PasswordChangedNoticeEnabled gates the "your password was changed"
	// receipt a completed reset sends.
	PasswordChangedNoticeEnabled bool `koanf:"password_changed_notice_enabled" json:"password_changed_notice_enabled"`
	// PasswordRemovedNoticeEnabled gates the "your password was removed"
	// receipt a completed removal sends.
	PasswordRemovedNoticeEnabled bool `koanf:"password_removed_notice_enabled" json:"password_removed_notice_enabled"`
	// PasskeyAddedNoticeEnabled gates the receipt an enrollment sends.
	PasskeyAddedNoticeEnabled bool `koanf:"passkey_added_notice_enabled" json:"passkey_added_notice_enabled"`
	// PasskeyRemovedNoticeEnabled gates the receipt a passkey removal
	// sends — the holder's own removal and the administrator's alike.
	PasskeyRemovedNoticeEnabled bool `koanf:"passkey_removed_notice_enabled" json:"passkey_removed_notice_enabled"`
	// MfaDisabledNoticeEnabled gates the notice an administrative MFA removal
	// sends.
	MfaDisabledNoticeEnabled bool `koanf:"mfa_disabled_notice_enabled" json:"mfa_disabled_notice_enabled"`
	// UserBannedNoticeEnabled gates the notice a ban sends.
	UserBannedNoticeEnabled bool `koanf:"user_banned_notice_enabled" json:"user_banned_notice_enabled"`
	// UserUnbannedNoticeEnabled gates the notice a lifted ban sends.
	UserUnbannedNoticeEnabled bool `koanf:"user_unbanned_notice_enabled" json:"user_unbanned_notice_enabled"`
	// APIKeyExpiringNoticeEnabled gates the expiry warning an aging API key
	// sends.
	APIKeyExpiringNoticeEnabled bool `koanf:"api_key_expiring_notice_enabled" json:"api_key_expiring_notice_enabled"`
	// EmailChangeNoticeEnabled gates the notice the old address receives while
	// a change is pending and the confirmation the new address receives after
	// it completes. The request token itself is transactional and always sent.
	EmailChangeNoticeEnabled bool `koanf:"email_change_notice_enabled" json:"email_change_notice_enabled"`
	// SignupAttemptNoticeEnabled gates the notice the address on file
	// receives when the strict enumeration mode answers a taken-email
	// sign-up for it.
	SignupAttemptNoticeEnabled bool `koanf:"signup_attempt_notice_enabled" json:"signup_attempt_notice_enabled"`
	// UserLockedNoticeEnabled gates the notice the failed-attempt policy's
	// lockout sends.
	UserLockedNoticeEnabled bool `koanf:"user_locked_notice_enabled" json:"user_locked_notice_enabled"`
	// AnnouncementEmailEnabled gates the email pass a published notification
	// asks for. The notice itself is always written — this gate is only the
	// mail the pass would duplicate it with.
	AnnouncementEmailEnabled bool `koanf:"announcement_email_enabled" json:"announcement_email_enabled"`
}

// Queue holds the background task queue settings. The queue runs on the same
// Postgres the application already uses, so there is no backend to enable: a
// serve run carries a worker pool, and the values here size it.
type Queue struct {
	// NumWorkers is the number of goroutines that execute queued tasks
	// concurrently.
	NumWorkers int `koanf:"num_workers" json:"num_workers"`
	// ReleaseAfter is how long a claimed task may run before the queue
	// considers its worker lost and hands the task to another one. It must
	// exceed the longest execution a queue's Timeout allows, or a slow task
	// would be run twice.
	//
	// The default is an hour, twice the longest job the application runs. A
	// deployment that raises a job's timeout must raise this with it:
	// queue.Client.Register refuses to register a queue whose Timeout reaches
	// the value, so the mismatch fails the run before the listener opens
	// rather than executing the task twice.
	ReleaseAfter time.Duration `koanf:"release_after" json:"release_after"`
	// CleanupInterval is how often the maintenance job deletes the completed
	// records their retention has expired.
	CleanupInterval time.Duration `koanf:"cleanup_interval" json:"cleanup_interval"`
	// Encrypt seals task payloads at rest with the application secret
	// (app.secret_key). A task still in flight when the flag flips is read
	// as the plaintext it is; every task written afterwards is sealed.
	Encrypt bool `koanf:"encrypt" json:"encrypt"`
}

// RateLimit holds the request throttling settings.
//
// Limit is the default bucket: the budget every counted procedure shares
// that is not named into a tighter one. AuthLimit is the credential bucket —
// the sign-in, the sign-up, the code and token verifications, and the email
// senders — whose tighter budget is what an attacker's script meets. Both
// buckets share the window: one window to reason about, two budgets.
type RateLimit struct {
	// Driver is RateLimitDB or RateLimitKV.
	Driver string `koanf:"driver" json:"driver"`
	// Limit is the default bucket's number of requests per Window.
	Limit int `koanf:"limit" json:"limit"`
	// AuthLimit is the credential bucket's number of requests per Window.
	// The procedures it throttles are named in internal/guard.
	AuthLimit int `koanf:"auth_limit" json:"auth_limit"`
	// Window is the period the budgets apply to.
	Window time.Duration `koanf:"window" json:"window"`
}

// Server holds the HTTP server settings.
type Server struct {
	// Host and Port are the listen address.
	Host string `koanf:"host" json:"host"`
	Port int    `koanf:"port" json:"port"`
	// ReadTimeout, WriteTimeout, and IdleTimeout are the net/http timeouts.
	ReadTimeout  time.Duration `koanf:"read_timeout" json:"read_timeout"`
	WriteTimeout time.Duration `koanf:"write_timeout" json:"write_timeout"`
	IdleTimeout  time.Duration `koanf:"idle_timeout" json:"idle_timeout"`
	// ShutdownTimeout bounds the graceful shutdown drain.
	ShutdownTimeout time.Duration `koanf:"shutdown_timeout" json:"shutdown_timeout"`
	// MaxRequestBytes bounds the body one RPC request may carry. The Connect
	// handler refuses a larger one before a procedure runs, which is what
	// keeps a caller from pinning the process with an oversized payload; the
	// REST surface bounds its own reads where the shape of a route demands it.
	MaxRequestBytes int `koanf:"max_request_bytes" json:"max_request_bytes"`
	// TrustedProxyHeaders name the headers the deployment's reverse proxies
	// set the client's address in, in precedence order, and which this
	// process therefore believes. Empty means the process is reached directly
	// and the connection's own address is used.
	//
	// It is a list rather than one header because a deployment can have more
	// than one hop that sets one: a CDN in front of an own reverse proxy
	// names two headers, and the order decides which wins when both are
	// present — the first one that carries a parseable address is the hop
	// closest to the client.
	//
	// It is a configuration key rather than a built-in choice because a
	// header is only trustworthy when the hop in front of the process
	// overwrites it on every request: X-Forwarded-For and X-Real-IP are
	// caller-written everywhere else, and believing one would let a caller
	// choose the address an audit record and a rate-limit bucket are keyed
	// by. Only a deployment knows which headers its own proxies set.
	TrustedProxyHeaders []string `koanf:"trusted_proxy_headers" json:"trusted_proxy_headers"`
	// CORS holds the browser cross-origin policy for the API.
	CORS CORS `koanf:"cors" json:"cors"`
}

// CORS holds the browser cross-origin policy, applied to every route by the
// transport middleware. The lists are explicit rather than a single on/off
// switch, because an API that names its origins can also drop the wildcard.
//
// An empty AllowedOrigins disables cross-origin access, which is the honest
// default for a same-origin SPA; a deployment that serves the SPA from another
// origin lists it.
type CORS struct {
	// AllowedOrigins lists the origins a browser may call from. Each is a
	// scheme://host origin without a path, or "*" for any origin, which
	// AllowCredentials forbids combining with.
	AllowedOrigins []string `koanf:"allowed_origins" json:"allowed_origins"`
	// AllowedMethods lists the HTTP methods a cross-origin request may use.
	AllowedMethods []string `koanf:"allowed_methods" json:"allowed_methods"`
	// AllowedHeaders lists the request headers a cross-origin call may set.
	AllowedHeaders []string `koanf:"allowed_headers" json:"allowed_headers"`
	// ExposedHeaders lists the response headers a browser script may read on
	// a cross-origin response. Headers outside the CORS-safelist are invisible
	// to a script unless they are named here.
	ExposedHeaders []string `koanf:"exposed_headers" json:"exposed_headers"`
	// AllowCredentials lets a cross-origin call carry cookies and credentials.
	// The CORS specification forbids credentials with a wildcard origin, so
	// Validate refuses that combination.
	AllowCredentials bool `koanf:"allow_credentials" json:"allow_credentials"`
	// MaxAge is how long a browser may cache a preflight answer.
	MaxAge time.Duration `koanf:"max_age" json:"max_age"`
}

// Storage holds the file storage settings.
type Storage struct {
	// Driver is StorageLocal or StorageS3.
	Driver string `koanf:"driver" json:"driver"`
	// LocalPath is the directory the local driver writes to, relative to the
	// working directory or absolute. It is the one data directory of the
	// process: the backup default and the storage health check both read it, so
	// there is no second path to disagree with it.
	LocalPath string `koanf:"local_path" json:"local_path"`
	// S3 holds the object-storage settings, used when Driver is StorageS3.
	S3 S3 `koanf:"s3" json:"s3"`
}

// S3 holds the object-storage settings for an S3-compatible service, which may
// be AWS or anything speaking the same protocol.
//
// The section is nested rather than flattened because its keys cover several
// concerns at once — credentials, the bucket, the endpoint, the addressing
// style, and the lifetime of a signed link. A common prefix on every one of them
// would be noise the nesting already carries.
type S3 struct {
	// AccessKey and SecretKey authenticate every request. Both are secrets,
	// so Redacted hides them and Sample writes them as directives.
	AccessKey string `koanf:"access_key" json:"access_key"`
	SecretKey string `koanf:"secret_key" json:"secret_key"`
	// EndpointURL is the base URL of a service other than AWS, such as
	// http://localhost:9100. Empty means AWS, addressed through Region.
	EndpointURL string `koanf:"endpoint_url" json:"endpoint_url"`
	// ForcePathStyle addresses a bucket as a path segment (host/bucket/key)
	// instead of a subdomain (bucket.host/key). MinIO and Silo need it: the
	// client does not fall back on its own, and bucket.localhost does not
	// resolve.
	ForcePathStyle bool `koanf:"force_path_style" json:"force_path_style"`
	// Region is the signing region. It is required even when EndpointURL is
	// set, because the client refuses to resolve an endpoint without one, so
	// the default is a usable value rather than an empty string.
	Region string `koanf:"region" json:"region"`
}

// Webhook holds the outbound webhook delivery settings.
type Webhook struct {
	// AllowPrivateNetwork admits delivery endpoints whose host resolves to a
	// loopback, private, or link-local address. The default refusal is the
	// SSRF guard: an administrator-registered destination is trusted with
	// the public internet, and reaching into the deployment's own network —
	// a metadata service, the database, another container — is a capability
	// a leaked admin credential should not carry by default. Enable it for
	// deployments whose receivers genuinely live beside the server.
	AllowPrivateNetwork bool `koanf:"allow_private_network" json:"allow_private_network"`
}
