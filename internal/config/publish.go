// The published views of the configuration: the documents the REST
// configuration endpoint answers. The mapping lives beside the types it
// projects — a key added to the configuration is published by naming it
// here or not at all, and no second contract restates the shape.
package config

import "time"

// Duration is a length of time as the published document writes it: a whole
// number of seconds, the unit every duration in a config file carries. The
// encoding/json/v2 this surface answers with refuses a time.Duration, so a
// duration crosses as the seconds its readers see elsewhere.
type Duration int64

// seconds converts a duration to the published unit.
func seconds(d time.Duration) Duration {
	return Duration(int64(d / time.Second))
}

// Published is the configuration document the endpoint serves. It is one
// struct for both scopes: the public fields are what an unauthenticated
// caller reads, the rest only fills in when the caller is an administrator.
// The secret keys are part of the document, but a secret value never
// crosses: the full document reads them through the redaction path
// redact.go prints (`[redacted]`, the datastore URLs reduced to
// host:port/database), and an unset secret stays empty, so an omitted key
// is told apart from a redacted one.
//
// The JSON names follow the configuration keys, and unset values are
// omitted, so the public body carries only the public facts.
type Published struct {
	App       PublishedApp       `json:"app,omitzero"`
	Auth      PublishedAuth      `json:"auth,omitzero"`
	Cache     PublishedCache     `json:"cache,omitzero"`
	Database  PublishedDatabase  `json:"database,omitzero"`
	Fetcher   PublishedFetcher   `json:"fetcher,omitzero"`
	KVStore   PublishedKVStore   `json:"kvstore,omitzero"`
	Log       PublishedLog       `json:"log,omitzero"`
	Mailer    PublishedMailer    `json:"mailer,omitzero"`
	OIDC      PublishedOIDC      `json:"oidc,omitzero"`
	OTEL      PublishedOTEL      `json:"otel,omitzero"`
	Queue     PublishedQueue     `json:"queue,omitzero"`
	RateLimit PublishedRateLimit `json:"rate_limit,omitzero"`
	Server    PublishedServer    `json:"server,omitzero"`
	Storage   PublishedStorage   `json:"storage,omitzero"`
	Webhook   PublishedWebhook   `json:"webhook,omitzero"`
}

// PublishedApp is the app section. The mode is public: the SPA reads it to
// know which deployment it fronts. The secret key is published only as its
// redaction.
type PublishedApp struct {
	Mode    string `json:"mode,omitzero"`
	BaseURL string `json:"base_url,omitzero"`

	// AssetsURL is public — the SPA loads its assets from it — while the
	// rest of the section is administrative.
	AssetsURL string `json:"assets_url,omitzero"`

	ExposeResetToken   bool   `json:"expose_reset_token,omitzero"`
	ExposeTotpSecret   bool   `json:"expose_totp_secret,omitzero"`
	AuditRetentionDays int    `json:"audit_retention_days,omitzero"`
	SecretKey          string `json:"secret_key,omitzero"`
}

// PublishedAuth is the auth section. The HMAC secret is published only as its
// redaction — the asymmetric signing keys live in the database (public.jwks)
// and their public halves are served at /.well-known/jwks.json anyway. The
// one-time access toggles are public: they decide which sign-in
// options the login screen offers.
type PublishedAuth struct {
	JWTAlgorithm       string   `json:"jwt_algorithm,omitzero"`
	Issuer             string   `json:"issuer,omitzero"`
	AccessTTL          Duration `json:"access_ttl,omitzero"`
	ExpiryEmailEnabled bool     `json:"expiry_email_enabled,omitzero"`
	SessionDriver      string   `json:"session_driver,omitzero"`

	OneTimeAccessEmailAsAdminEnabled           bool `json:"one_time_access_email_as_admin_enabled,omitzero"`
	OneTimeAccessEmailAsUnauthenticatedEnabled bool `json:"one_time_access_email_as_unauthenticated_enabled,omitzero"`

	SecretKey string `json:"secret_key,omitzero"`
}

// PublishedCache is the cache section.
type PublishedCache struct {
	Enable    bool     `json:"enable,omitzero"`
	Driver    string   `json:"driver,omitzero"`
	TTL       Duration `json:"ttl,omitzero"`
	MaxMemory int64    `json:"max_memory,omitzero"`
}

// PublishedDatabase is the database section. The DSN is published only in
// the reduced form RedactDSN prints — host:port/database.
type PublishedDatabase struct {
	URL                  string   `json:"url,omitzero"`
	MaxConns             int32    `json:"max_conns,omitzero"`
	MinConns             int32    `json:"min_conns,omitzero"`
	MaxConnLifetime      Duration `json:"max_conn_lifetime,omitzero"`
	MaxConnIdleTime      Duration `json:"max_conn_idle_time,omitzero"`
	ConnectTimeout       Duration `json:"connect_timeout,omitzero"`
	ConnectAttempts      int      `json:"connect_attempts,omitzero"`
	ConnectRetryInterval Duration `json:"connect_retry_interval,omitzero"`
	SearchPath           string   `json:"search_path,omitzero"`
	Timezone             string   `json:"timezone,omitzero"`
}

// PublishedFetcher is the fetcher section.
type PublishedFetcher struct {
	UserAgent               string   `json:"user_agent,omitzero"`
	Timeout                 Duration `json:"timeout,omitzero"`
	RetryCount              int      `json:"retry_count,omitzero"`
	RetryWait               Duration `json:"retry_wait,omitzero"`
	RetryMaxWait            Duration `json:"retry_max_wait,omitzero"`
	CircuitFailureThreshold int      `json:"circuit_failure_threshold,omitzero"`
	CircuitSuccessThreshold int      `json:"circuit_success_threshold,omitzero"`
	CircuitResetTimeout     Duration `json:"circuit_reset_timeout,omitzero"`
	MaxBodyBytes            int64    `json:"max_body_bytes,omitzero"`
}

// PublishedKVStore is the kvstore section. The URL is published only in the
// reduced form RedactKVURL prints — host:port/database.
type PublishedKVStore struct {
	Enable bool   `json:"enable,omitzero"`
	URL    string `json:"url,omitzero"`
	DB     int    `json:"db,omitzero"`
}

// PublishedLog is the log section.
type PublishedLog struct {
	Level     string           `json:"level,omitzero"`
	Transport []string         `json:"transport,omitzero"`
	Format    string           `json:"format,omitzero"`
	File      PublishedLogFile `json:"file,omitzero"`
	OTLP      PublishedLogOTLP `json:"otlp,omitzero"`
}

// PublishedLogFile is the rotating file sink's settings.
type PublishedLogFile struct {
	MaxSize    int  `json:"max_size,omitzero"`
	MaxBackups int  `json:"max_backups,omitzero"`
	MaxAge     int  `json:"max_age,omitzero"`
	Compress   bool `json:"compress,omitzero"`
}

// PublishedLogOTLP is the OTLP log sink's route on the collector.
type PublishedLogOTLP struct {
	Path    string   `json:"path,omitzero"`
	Timeout Duration `json:"timeout,omitzero"`
}

// PublishedMailer is the mailer section. The SMTP password is published
// only as its redaction.
type PublishedMailer struct {
	FromEmail              string                       `json:"from_email,omitzero"`
	FromName               string                       `json:"from_name,omitzero"`
	SMTPHost               string                       `json:"smtp_host,omitzero"`
	SMTPPort               int                          `json:"smtp_port,omitzero"`
	SMTPUsername           string                       `json:"smtp_username,omitzero"`
	SMTPPassword           string                       `json:"smtp_password,omitzero"`
	SMTPSecure             bool                         `json:"smtp_secure,omitzero"`
	SMTPAllowPlaintextAuth bool                         `json:"smtp_allow_plaintext_auth,omitzero"`
	Timeout                Duration                     `json:"timeout,omitzero"`
	Notifications          PublishedMailerNotifications `json:"notifications,omitzero"`
}

// PublishedMailerNotifications is the notification toggles the mailer
// serves. The announcement switch is public: it says whether announcements
// reach their audience by email as well as in the application.
type PublishedMailerNotifications struct {
	NewDeviceNoticeEnabled       bool `json:"new_device_notice_enabled,omitzero"`
	PasswordChangedNoticeEnabled bool `json:"password_changed_notice_enabled,omitzero"`
	MfaDisabledNoticeEnabled     bool `json:"mfa_disabled_notice_enabled,omitzero"`
	UserBannedNoticeEnabled      bool `json:"user_banned_notice_enabled,omitzero"`
	UserUnbannedNoticeEnabled    bool `json:"user_unbanned_notice_enabled,omitzero"`
	APIKeyExpiringNoticeEnabled  bool `json:"api_key_expiring_notice_enabled,omitzero"`
	EmailChangeNoticeEnabled     bool `json:"email_change_notice_enabled,omitzero"`
	AnnouncementEmailEnabled     bool `json:"announcement_email_enabled,omitzero"`
}

// PublishedOTEL is the otel section. The collector headers are published
// with their names kept and every value redacted — a header is a secret as
// a whole, because an authorization token is why the key exists.
type PublishedOTEL struct {
	Endpoint    string               `json:"endpoint,omitzero"`
	ServiceName string               `json:"service_name,omitzero"`
	Environment string               `json:"environment,omitzero"`
	Compression string               `json:"compression,omitzero"`
	Headers     map[string]string    `json:"headers,omitzero"`
	Queue       PublishedOTELQueue   `json:"queue,omitzero"`
	Tracing     PublishedOTELTracing `json:"tracing,omitzero"`
	Metrics     PublishedOTELMetrics `json:"metrics,omitzero"`
}

// PublishedOTELQueue bounds each signal's export buffer.
type PublishedOTELQueue struct {
	MaxSize int `json:"max_size,omitzero"`
}

// PublishedOTELTracing is the trace export settings.
type PublishedOTELTracing struct {
	Enable        bool     `json:"enable,omitzero"`
	Path          string   `json:"path,omitzero"`
	Sampler       string   `json:"sampler,omitzero"`
	Ratio         float64  `json:"ratio,omitzero"`
	BatchTimeout  Duration `json:"batch_timeout,omitzero"`
	ExportTimeout Duration `json:"export_timeout,omitzero"`
	MaxBatchSize  int      `json:"max_batch_size,omitzero"`
}

// PublishedOTELMetrics is the metric export settings.
type PublishedOTELMetrics struct {
	Enable         bool     `json:"enable,omitzero"`
	Push           bool     `json:"push,omitzero"`
	Path           string   `json:"path,omitzero"`
	PrometheusPath string   `json:"prometheus_path,omitzero"`
	Interval       Duration `json:"interval,omitzero"`
	ExportTimeout  Duration `json:"export_timeout,omitzero"`
}

// PublishedQueue is the queue section.
type PublishedQueue struct {
	NumWorkers      int      `json:"num_workers,omitzero"`
	ReleaseAfter    Duration `json:"release_after,omitzero"`
	CleanupInterval Duration `json:"cleanup_interval,omitzero"`
	Encrypt         bool     `json:"encrypt,omitzero"`
	Timezone        string   `json:"timezone,omitzero"`
}

// PublishedOIDC is the oidc section. The enable switch is public — it is
// the sign-in option's answer to whether the deployment federates — and
// the CIMD allowlist is administrative, naming the origins the deployment
// is willing to trust a metadata document from.
type PublishedOIDC struct {
	Enabled          bool     `json:"enabled,omitzero"`
	CIMDURLAllowlist []string `json:"cimd_url_allowlist,omitzero"`
}

// PublishedRateLimit is the rate_limit section.
type PublishedRateLimit struct {
	Driver string   `json:"driver,omitzero"`
	Limit  int      `json:"limit,omitzero"`
	Window Duration `json:"window,omitzero"`
}

// PublishedServer is the server section.
type PublishedServer struct {
	Host                string        `json:"host,omitzero"`
	Port                int           `json:"port,omitzero"`
	ReadTimeout         Duration      `json:"read_timeout,omitzero"`
	WriteTimeout        Duration      `json:"write_timeout,omitzero"`
	IdleTimeout         Duration      `json:"idle_timeout,omitzero"`
	ShutdownTimeout     Duration      `json:"shutdown_timeout,omitzero"`
	MaxRequestBytes     int           `json:"max_request_bytes,omitzero"`
	TrustedProxyHeaders []string      `json:"trusted_proxy_headers,omitzero"`
	CORS                PublishedCORS `json:"cors,omitzero"`
}

// PublishedCORS is the browser cross-origin policy.
type PublishedCORS struct {
	AllowedOrigins   []string `json:"allowed_origins,omitzero"`
	AllowedMethods   []string `json:"allowed_methods,omitzero"`
	AllowedHeaders   []string `json:"allowed_headers,omitzero"`
	ExposedHeaders   []string `json:"exposed_headers,omitzero"`
	AllowCredentials bool     `json:"allow_credentials,omitzero"`
	MaxAge           Duration `json:"max_age,omitzero"`
}

// PublishedStorage is the storage section. The S3 credentials are published
// only as their redactions.
type PublishedStorage struct {
	Driver    string         `json:"driver,omitzero"`
	LocalPath string         `json:"local_path,omitzero"`
	Watch     PublishedWatch `json:"watch,omitzero"`
	S3        PublishedS3    `json:"s3,omitzero"`
}

// PublishedWatch is the staging watcher's settings.
type PublishedWatch struct {
	Enable   bool     `json:"enable,omitzero"`
	Debounce Duration `json:"debounce,omitzero"`
}

// PublishedWebhook is the webhook section: the delivery policy an operator
// reads to know what a registered destination may reach.
type PublishedWebhook struct {
	AllowPrivateNetwork bool `json:"allow_private_network,omitzero"`
}

// PublishedS3 is the object-storage settings. Both credentials are
// published only as their redactions.
type PublishedS3 struct {
	AccessKey        string   `json:"access_key,omitzero"`
	SecretKey        string   `json:"secret_key,omitzero"`
	BucketName       string   `json:"bucket_name,omitzero"`
	EndpointURL      string   `json:"endpoint_url,omitzero"`
	ForcePathStyle   bool     `json:"force_path_style,omitzero"`
	PathPrefix       string   `json:"path_prefix,omitzero"`
	Region           string   `json:"region,omitzero"`
	SignedURLExpires Duration `json:"signed_url_expires,omitzero"`
}

// publishedSecretRender renders a secret for the published document. An
// empty secret stays empty — an unset key is omitted, not redacted — and
// everything else is the placeholder Redacted prints, so the document is
// what a fail-safe print would show and nothing more.
func publishedSecretRender(secret string) string {
	if secret == "" {
		return ""
	}
	return redacted
}

// Published projects the configuration onto the document the endpoint
// serves. The public scope fills only the facts an unauthenticated client
// may read — the mode, the addresses, and the toggles that decide which
// sign-in options exist; the full scope adds everything else the process
// runs on, which is what an administrator gets.
func (c Config) Published(full bool) Published {
	public := Published{
		App: PublishedApp{
			Mode:    c.App.Mode,
			BaseURL: c.App.BaseURL,
		},
		Auth: PublishedAuth{
			OneTimeAccessEmailAsAdminEnabled:           c.Auth.OneTimeAccessEmailAsAdminEnabled,
			OneTimeAccessEmailAsUnauthenticatedEnabled: c.Auth.OneTimeAccessEmailAsUnauthenticatedEnabled,
		},
		OIDC: PublishedOIDC{
			Enabled: c.OIDC.Enabled,
		},
		Mailer: PublishedMailer{
			Notifications: PublishedMailerNotifications{
				AnnouncementEmailEnabled: c.Mailer.Notifications.AnnouncementEmailEnabled,
			},
		},
	}

	if !full {
		return public
	}

	// Every secret value the full document carries is read from the
	// redacted copy, never from the configuration itself: the placeholder
	// is decided by the same path `config:print`'s fail-safe uses, so a
	// secret cannot reach the wire by being populated.
	sealed := c.withSecrets(publishedSecretRender)

	public.App.AssetsURL = c.App.AssetsURL
	public.App.ExposeResetToken = c.App.ExposeResetToken
	public.App.ExposeTotpSecret = c.App.ExposeTotpSecret
	public.App.AuditRetentionDays = c.App.AuditRetentionDays
	public.App.SecretKey = sealed.App.SecretKey
	public.Auth.JWTAlgorithm = c.Auth.JWTAlgorithm
	public.Auth.Issuer = c.Auth.Issuer
	public.Auth.AccessTTL = seconds(c.Auth.AccessTTL)
	public.Auth.ExpiryEmailEnabled = c.Auth.ExpiryEmailEnabled
	public.Auth.SessionDriver = c.Auth.SessionDriver
	public.Auth.SecretKey = sealed.Auth.SecretKey
	public.Cache = PublishedCache{
		Enable:    c.Cache.Enable,
		Driver:    c.Cache.Driver,
		TTL:       seconds(c.Cache.TTL),
		MaxMemory: c.Cache.MaxMemory,
	}
	public.Database = PublishedDatabase{
		URL:                  sealed.Database.URL,
		MaxConns:             c.Database.MaxConns,
		MinConns:             c.Database.MinConns,
		MaxConnLifetime:      seconds(c.Database.MaxConnLifetime),
		MaxConnIdleTime:      seconds(c.Database.MaxConnIdleTime),
		ConnectTimeout:       seconds(c.Database.ConnectTimeout),
		ConnectAttempts:      c.Database.ConnectAttempts,
		ConnectRetryInterval: seconds(c.Database.ConnectRetryInterval),
		SearchPath:           c.Database.SearchPath,
		Timezone:             c.Database.Timezone,
	}
	public.Fetcher = PublishedFetcher{
		UserAgent:               c.Fetcher.UserAgent,
		Timeout:                 seconds(c.Fetcher.Timeout),
		RetryCount:              c.Fetcher.RetryCount,
		RetryWait:               seconds(c.Fetcher.RetryWait),
		RetryMaxWait:            seconds(c.Fetcher.RetryMaxWait),
		CircuitFailureThreshold: c.Fetcher.CircuitFailureThreshold,
		CircuitSuccessThreshold: c.Fetcher.CircuitSuccessThreshold,
		CircuitResetTimeout:     seconds(c.Fetcher.CircuitResetTimeout),
		MaxBodyBytes:            c.Fetcher.MaxBodyBytes,
	}
	public.KVStore = PublishedKVStore{
		Enable: c.KVStore.Enable,
		URL:    sealed.KVStore.URL,
		DB:     c.KVStore.DB,
	}
	public.Log = PublishedLog{
		Level:     c.Log.Level,
		Transport: c.Log.Transport,
		Format:    c.Log.Format,
		File: PublishedLogFile{
			MaxSize:    c.Log.File.MaxSize,
			MaxBackups: c.Log.File.MaxBackups,
			MaxAge:     c.Log.File.MaxAge,
			Compress:   c.Log.File.Compress,
		},
		OTLP: PublishedLogOTLP{
			Path:    c.Log.OTLP.Path,
			Timeout: seconds(c.Log.OTLP.Timeout),
		},
	}
	public.Mailer.FromEmail = c.Mailer.FromEmail
	public.Mailer.FromName = c.Mailer.FromName
	public.Mailer.SMTPHost = c.Mailer.SMTPHost
	public.Mailer.SMTPPort = c.Mailer.SMTPPort
	public.Mailer.SMTPUsername = c.Mailer.SMTPUsername
	public.Mailer.SMTPPassword = sealed.Mailer.SMTPPassword
	public.Mailer.SMTPSecure = c.Mailer.SMTPSecure
	public.Mailer.SMTPAllowPlaintextAuth = c.Mailer.SMTPAllowPlaintextAuth
	public.Mailer.Timeout = seconds(c.Mailer.Timeout)
	public.Mailer.Notifications = PublishedMailerNotifications{
		NewDeviceNoticeEnabled:       c.Mailer.Notifications.NewDeviceNoticeEnabled,
		PasswordChangedNoticeEnabled: c.Mailer.Notifications.PasswordChangedNoticeEnabled,
		MfaDisabledNoticeEnabled:     c.Mailer.Notifications.MfaDisabledNoticeEnabled,
		UserBannedNoticeEnabled:      c.Mailer.Notifications.UserBannedNoticeEnabled,
		UserUnbannedNoticeEnabled:    c.Mailer.Notifications.UserUnbannedNoticeEnabled,
		APIKeyExpiringNoticeEnabled:  c.Mailer.Notifications.APIKeyExpiringNoticeEnabled,
		EmailChangeNoticeEnabled:     c.Mailer.Notifications.EmailChangeNoticeEnabled,
		AnnouncementEmailEnabled:     c.Mailer.Notifications.AnnouncementEmailEnabled,
	}
	public.OIDC = PublishedOIDC{
		Enabled:          c.OIDC.Enabled,
		CIMDURLAllowlist: c.OIDC.CIMDURLAllowlist,
	}
	public.OTEL = PublishedOTEL{
		Endpoint:    c.OTEL.Endpoint,
		ServiceName: c.OTEL.ServiceName,
		Environment: c.OTEL.Environment,
		Compression: c.OTEL.Compression,
		Headers:     sealed.OTEL.Headers,
		Queue:       PublishedOTELQueue{MaxSize: c.OTEL.Queue.MaxSize},
		Tracing: PublishedOTELTracing{
			Enable:        c.OTEL.Tracing.Enable,
			Path:          c.OTEL.Tracing.Path,
			Sampler:       c.OTEL.Tracing.Sampler,
			Ratio:         c.OTEL.Tracing.Ratio,
			BatchTimeout:  seconds(c.OTEL.Tracing.BatchTimeout),
			ExportTimeout: seconds(c.OTEL.Tracing.ExportTimeout),
			MaxBatchSize:  c.OTEL.Tracing.MaxBatchSize,
		},
		Metrics: PublishedOTELMetrics{
			Enable:         c.OTEL.Metrics.Enable,
			Push:           c.OTEL.Metrics.Push,
			Path:           c.OTEL.Metrics.Path,
			PrometheusPath: c.OTEL.Metrics.PrometheusPath,
			Interval:       seconds(c.OTEL.Metrics.Interval),
			ExportTimeout:  seconds(c.OTEL.Metrics.ExportTimeout),
		},
	}
	public.Queue = PublishedQueue{
		NumWorkers:      c.Queue.NumWorkers,
		ReleaseAfter:    seconds(c.Queue.ReleaseAfter),
		CleanupInterval: seconds(c.Queue.CleanupInterval),
		Encrypt:         c.Queue.Encrypt,
		Timezone:        c.Queue.Timezone,
	}
	public.RateLimit = PublishedRateLimit{
		Driver: c.RateLimit.Driver,
		Limit:  c.RateLimit.Limit,
		Window: seconds(c.RateLimit.Window),
	}
	public.Server = PublishedServer{
		Host:                c.Server.Host,
		Port:                c.Server.Port,
		ReadTimeout:         seconds(c.Server.ReadTimeout),
		WriteTimeout:        seconds(c.Server.WriteTimeout),
		IdleTimeout:         seconds(c.Server.IdleTimeout),
		ShutdownTimeout:     seconds(c.Server.ShutdownTimeout),
		MaxRequestBytes:     c.Server.MaxRequestBytes,
		TrustedProxyHeaders: c.Server.TrustedProxyHeaders,
		CORS: PublishedCORS{
			AllowedOrigins:   c.Server.CORS.AllowedOrigins,
			AllowedMethods:   c.Server.CORS.AllowedMethods,
			AllowedHeaders:   c.Server.CORS.AllowedHeaders,
			ExposedHeaders:   c.Server.CORS.ExposedHeaders,
			AllowCredentials: c.Server.CORS.AllowCredentials,
			MaxAge:           seconds(c.Server.CORS.MaxAge),
		},
	}
	public.Storage = PublishedStorage{
		Driver:    c.Storage.Driver,
		LocalPath: c.Storage.LocalPath,
		Watch: PublishedWatch{
			Enable:   c.Storage.Watch.Enable,
			Debounce: seconds(c.Storage.Watch.Debounce),
		},
		S3: PublishedS3{
			AccessKey:        sealed.Storage.S3.AccessKey,
			SecretKey:        sealed.Storage.S3.SecretKey,
			BucketName:       c.Storage.S3.BucketName,
			EndpointURL:      c.Storage.S3.EndpointURL,
			ForcePathStyle:   c.Storage.S3.ForcePathStyle,
			PathPrefix:       c.Storage.S3.PathPrefix,
			Region:           c.Storage.S3.Region,
			SignedURLExpires: seconds(c.Storage.S3.SignedURLExpires),
		},
	}
	public.Webhook = PublishedWebhook{
		AllowPrivateNetwork: c.Webhook.AllowPrivateNetwork,
	}
	return public
}
