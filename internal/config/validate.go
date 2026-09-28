package config

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ErrInvalid reports a resolved configuration a source made invalid.
var ErrInvalid = errors.New("config: invalid configuration")

// Validate checks the resolved configuration and reports every problem it
// finds, not just the first: a user fixing a config file should see all of the
// mistakes in one run. Each problem is wrapped with ErrInvalid, so a caller can
// match the class of failure with errors.Is.
func (c Config) Validate() error {
	var problems []error

	check := func(ok bool, format string, args ...any) {
		if !ok {
			problems = append(problems, fmt.Errorf(format, args...))
		}
	}

	check(isOneOf(c.App.Mode, ModeDevelopment, ModeStaging, ModeProduction, ModeTest),
		"app.mode: %q is not one of %s", c.App.Mode,
		joinValues(ModeDevelopment, ModeStaging, ModeProduction, ModeTest))
	check(c.App.SecretKey == "" || isHexKey(c.App.SecretKey),
		"app.secret_key: must be 64 hex characters")
	check(c.App.BaseURL == "" || isHTTPURL(c.App.BaseURL),
		"app.base_url: %q must be an absolute http or https URL", c.App.BaseURL)
	// The decrypted authenticator secret is a development aid only: outside
	// the development mode the listing would hand every account's second
	// factor to the caller, so the combination is a refused run rather than
	// a flag a production deployment quietly carries.
	check(!c.App.ExposeTotpSecret || c.App.Mode == ModeDevelopment,
		"app.expose_totp_secret: true is allowed only when app.mode is %q", ModeDevelopment)
	// The raw reset token rides the same restriction: a response that names
	// the account's credential to anyone who knows the address is a
	// development aid, and the modes above development answer it with a
	// refused run.
	check(!c.App.ExposeResetToken || c.App.Mode == ModeDevelopment,
		"app.expose_reset_token: true is allowed only when app.mode is %q", ModeDevelopment)
	check(c.App.AssetsURL == "" || isHTTPURL(c.App.AssetsURL),
		"app.assets_url: %q must be an absolute http or https URL", c.App.AssetsURL)

	// The cache is off by default, so a file that has not switched it on is
	// not held to a driver or a budget it never runs — the way a signal
	// section is read only while the signal is on.
	if c.Cache.Enable {
		check(isOneOf(c.Cache.Driver, CacheMemory, CacheKV),
			"cache.driver: %q is not one of %s", c.Cache.Driver, joinValues(CacheMemory, CacheKV))
		check(c.Cache.TTL > 0, "cache.ttl: must be positive")
		check(c.Cache.MaxMemory > 0, "cache.max_memory: must be positive")
	}

	check(c.Database.URL != "", "database.url: %s",
		c.unsetNote("database.url", "must not be empty (set DATABASE_URL)"))
	check(isPostgresDSN(c.Database.URL), "database.url: must be a postgres connection string")
	check(c.Database.MaxConns > 0, "database.max_conns: must be positive")
	check(c.Database.MinConns >= 0, "database.min_conns: must not be negative")
	check(c.Database.MinConns <= c.Database.MaxConns,
		"database.min_conns: %d must not exceed database.max_conns: %d",
		c.Database.MinConns, c.Database.MaxConns)
	check(c.Database.MaxConnLifetime > 0, "database.max_conn_lifetime: must be positive")
	check(c.Database.MaxConnIdleTime > 0, "database.max_conn_idle_time: must be positive")
	check(c.Database.ConnectTimeout > 0, "database.connect_timeout: must be positive")
	check(c.Database.ConnectAttempts > 0, "database.connect_attempts: must be positive")
	check(c.Database.ConnectRetryInterval > 0, "database.connect_retry_interval: must be positive")
	check(c.Database.SearchPath != "", "database.search_path: must not be empty")
	check(c.Database.Timezone != "", "database.timezone: must not be empty")

	check(isHeaderValue(c.Fetcher.UserAgent),
		"fetcher.user_agent: must be a single header value of at most 256 characters")
	check(c.Fetcher.Timeout > 0, "fetcher.timeout: must be positive")
	check(c.Fetcher.RetryCount >= 0 && c.Fetcher.RetryCount <= maxFetcherRetries,
		"fetcher.retry_count: %d must be between 0 and %d", c.Fetcher.RetryCount, maxFetcherRetries)
	check(c.Fetcher.RetryWait > 0, "fetcher.retry_wait: must be positive")
	check(c.Fetcher.RetryMaxWait >= c.Fetcher.RetryWait,
		"fetcher.retry_max_wait: must not be shorter than fetcher.retry_wait")
	check(c.Fetcher.CircuitFailureThreshold > c.Fetcher.RetryCount,
		"fetcher.circuit_failure_threshold: %d must exceed fetcher.retry_count: %d",
		c.Fetcher.CircuitFailureThreshold, c.Fetcher.RetryCount)
	check(c.Fetcher.CircuitSuccessThreshold > 0, "fetcher.circuit_success_threshold: must be positive")
	check(c.Fetcher.CircuitResetTimeout >= c.Fetcher.Timeout,
		"fetcher.circuit_reset_timeout: must not be shorter than fetcher.timeout")
	check(c.Fetcher.MaxBodyBytes > 0 && c.Fetcher.MaxBodyBytes <= maxFetcherBody,
		"fetcher.max_body_bytes: %d must be between 1 and %d", c.Fetcher.MaxBodyBytes, maxFetcherBody)

	check(isOneOf(c.Log.Level, LogDebug, LogInfo, LogWarn, LogError),
		"log.level: %q is not one of %s", c.Log.Level, joinValues(LogDebug, LogInfo, LogWarn, LogError))
	check(len(c.Log.Transport) > 0, "log.transport: at least one transport is required")
	// Every name is checked, not just the first: a list with one good name and
	// one typo would otherwise start with a sink silently missing, which is the
	// failure the list exists to prevent.
	for _, transport := range c.Log.Transport {
		check(isOneOf(transport, LogTransports()...),
			"log.transport: %q is not one of %s", transport, joinValues(LogTransports()...))
	}
	check(noDuplicates(c.Log.Transport),
		"log.transport: %s must not repeat a transport", strings.Join(c.Log.Transport, ", "))

	// The console is the only sink with a rendering choice, so its format is
	// read only when it is named: holding it to anything would report a
	// problem in a part of the file that is switched off. A file or a
	// collector has no key to choose, because neither has a second form.
	if c.logTransport(LogTransportConsole) {
		check(isOneOf(c.Log.Format, LogPretty, LogStructured),
			"log.format: %q is not one of %s", c.Log.Format, joinValues(LogPretty, LogStructured))
	}

	// The file sink is built only when it is named, so the rotation settings are
	// read only then: holding them to anything would report a problem in a part
	// of the file that is switched off.
	if c.logTransport(LogTransportFile) {
		check(c.Log.File.MaxSize > 0, "log.file.max_size: must be positive")
		check(c.Log.File.MaxBackups >= 0, "log.file.max_backups: must not be negative")
		check(c.Log.File.MaxAge >= 0, "log.file.max_age: must not be negative")
		// Zero on both is the one combination that never deletes a rotated
		// file, so it is refused here rather than filling a disk later.
		check(c.Log.File.MaxBackups > 0 || c.Log.File.MaxAge > 0,
			"log.file: set max_backups or max_age; zero on both keeps every rotated file")
	}

	// The collector is dialled only when it is named, so its endpoint is held to
	// a URL only then. The address is otel.endpoint for all three signals, so a
	// log-only deployment still reads it here.
	if c.logTransport(LogTransportOTLP) {
		check(c.OTEL.Endpoint != "", "otel.endpoint: %s",
			c.unsetNote("otel.endpoint", "must not be empty when log.transport names otlp"))
	}

	check(isOneOf(c.OTEL.Protocol, OTELProtocols()...),
		"otel.protocol: %q is not one of %s", c.OTEL.Protocol, joinValues(OTELProtocols()...))
	check(isOneOf(c.OTEL.Compression, OTELCompressions()...),
		"otel.compression: %q is not one of %s", c.OTEL.Compression, joinValues(OTELCompressions()...))
	check(c.OTEL.Queue.MaxSize > 0, "otel.queue.max_size: must be positive")

	// A header name becomes part of the request, so an empty one or a value
	// that cannot be a header value is refused here rather than at the first
	// export, where the exporter reports it far from the key that caused it.
	for name := range c.OTEL.Headers {
		check(name != "", "otel.headers: a header name must not be empty")
	}

	// http/json is the one protocol combination the Go exporters do not all
	// implement: only the trace exporter encodes JSON. Metrics and logs would
	// silently send protobuf to a collector expecting JSON, so the mismatch is
	// refused by name instead of shipped. The signals are named in one message
	// so a deployment that enabled both fixes both in one pass.
	if c.OTEL.Protocol == OTELProtocolHTTPJSON {
		check(len(c.jsonUnsupportedSignals()) == 0,
			"otel.protocol: %q is not supported for %s; use %q",
			OTELProtocolHTTPJSON, joinValues(c.jsonUnsupportedSignals()...), OTELProtocolHTTPProtobuf)
	}

	// The address is checked whenever any signal is enabled, and the scheme
	// decides TLS, so a bad one fails here rather than at the first export. gRPC
	// addresses a service by host and port, so it is held to that instead of to
	// a URL.
	if c.otelEnabled() {
		check(c.OTEL.Endpoint != "", "otel.endpoint: %s",
			c.unsetNote("otel.endpoint", "must not be empty when a signal is enabled"))
		if UsesHTTP(c.OTEL.Protocol) {
			check(c.OTEL.Endpoint == "" || isHTTPURL(c.OTEL.Endpoint),
				"otel.endpoint: %q must be an absolute http or https URL for protocol %q",
				c.OTEL.Endpoint, c.OTEL.Protocol)
		} else {
			check(c.OTEL.Endpoint == "" || isGRPCTarget(c.OTEL.Endpoint),
				"otel.endpoint: %q must be host:port for protocol %q", c.OTEL.Endpoint, c.OTEL.Protocol)
			// The HTTP default port is the trap this catches: switching the
			// protocol without moving the address is the one change that looks
			// applied and sends nothing, because the collector's two protocols
			// are two listeners. A host other than the default's is left alone,
			// since a deployment may legitimately front both on one port.
			check(!strings.HasSuffix(c.OTEL.Endpoint, ":"+DefaultOTELHTTPPort),
				"otel.endpoint: %q is the HTTP port; protocol %q listens on %s",
				c.OTEL.Endpoint, c.OTEL.Protocol, DefaultOTELGRPCPort)
		}
		check(c.OTEL.ServiceName != "", "otel.service_name: must not be empty")
	}

	// The trace section is read only when tracing is switched on: holding a
	// sampler that never runs to anything would report a problem in a part of
	// the file nothing reads.
	if c.OTEL.Tracing.Enable {
		check(isOneOf(c.OTEL.Tracing.Sampler, OTELSamplers()...),
			"otel.tracing.sampler: %q is not one of %s",
			c.OTEL.Tracing.Sampler, joinValues(OTELSamplers()...))
		// A ratio is read by two samplers and ignored by the other two, so a
		// value outside 0..1 is refused here rather than silently doing nothing.
		if usesOTELRatio(c.OTEL.Tracing.Sampler) {
			check(c.OTEL.Tracing.Ratio >= 0 && c.OTEL.Tracing.Ratio <= 1,
				"otel.tracing.ratio: %v must be between 0 and 1 for sampler %q",
				c.OTEL.Tracing.Ratio, c.OTEL.Tracing.Sampler)
		}
		check(c.OTEL.Tracing.BatchTimeout > 0, "otel.tracing.batch_timeout: must be positive")
		check(c.OTEL.Tracing.ExportTimeout > 0, "otel.tracing.export_timeout: must be positive")
		check(c.OTEL.Tracing.MaxBatchSize > 0, "otel.tracing.max_batch_size: must be positive")
	}

	if c.OTEL.Metrics.Enable {
		check(isPath(c.OTEL.Metrics.PrometheusPath),
			"otel.metrics.prometheus_path: %q must be a path such as /metrics", c.OTEL.Metrics.PrometheusPath)
	}

	// Push is the only reader with a cadence and a collector route to check:
	// the pull exposition needs neither.
	if c.OTEL.Metrics.Push {
		if !c.OTEL.Metrics.Enable {
			check(false, "otel.metrics.push: requires otel.metrics.enable")
		}
		check(c.OTEL.Metrics.Interval > 0, "otel.metrics.interval: must be positive")
		check(c.OTEL.Metrics.ExportTimeout > 0, "otel.metrics.export_timeout: must be positive")
	}

	// A route is read only when the signal that owns it ships to a collector:
	// the path of a signal nothing exports would be a value nothing reads.
	if c.logTransport(LogTransportOTLP) {
		check(isOTELPath(c.Log.OTLP.Path),
			"log.otlp.path: %q must be a path such as /v1/logs", c.Log.OTLP.Path)
		check(c.Log.OTLP.Timeout > 0, "log.otlp.timeout: must be positive")
	}
	if c.OTEL.Tracing.Enable {
		check(isOTELPath(c.OTEL.Tracing.Path),
			"otel.tracing.path: %q must be a path such as /v1/traces", c.OTEL.Tracing.Path)
	}
	if c.OTEL.Metrics.Push {
		check(isOTELPath(c.OTEL.Metrics.Path),
			"otel.metrics.path: %q must be a path such as /v1/metrics", c.OTEL.Metrics.Path)
	}

	// A gRPC exporter is addressed by host and port alone, so a per-signal path
	// is a setting the exporter never reads. The combination is refused by name
	// rather than silently ignored: a path that says where a signal goes while
	// nothing follows it is the trap a configuration check exists for.
	if c.OTEL.Protocol == OTELProtocolGRPC {
		paths := []struct{ key, value string }{}
		if c.logTransport(LogTransportOTLP) {
			paths = append(paths, struct{ key, value string }{"log.otlp.path", c.Log.OTLP.Path})
		}
		if c.OTEL.Tracing.Enable {
			paths = append(paths, struct{ key, value string }{"otel.tracing.path", c.OTEL.Tracing.Path})
		}
		if c.OTEL.Metrics.Push {
			paths = append(paths, struct{ key, value string }{"otel.metrics.path", c.OTEL.Metrics.Path})
		}
		for _, p := range paths {
			check(p.value == "",
				"%s: %q has no effect with protocol %q; a gRPC exporter is addressed by host and port",
				p.key, p.value, OTELProtocolGRPC)
		}
	}

	// The key-value backend is opt-in. Its Enable flag and the per-feature driver
	// fields are two different decisions, so they can disagree, and a driver
	// pointing at a backend that is switched off is the one combination that
	// cannot work: report it by name rather than leaving it to fail at start-up.
	check(c.KVStore.DB >= 0, "kvstore.db: %d must not be negative", c.KVStore.DB)
	if c.KVStore.Enable {
		// An unset variable is reported by name, which is the message a user
		// needs; a URL that is present is held to the grammar the client accepts.
		check(c.KVStore.URL != "", "kvstore.url: %s",
			c.unsetNote("kvstore.url", "must not be empty when kvstore.enable is true"))
		check(c.KVStore.URL == "" || isKVURL(c.KVStore.URL),
			"kvstore.url: %q must be a redis://, rediss://, or unix:// connection string", c.KVStore.URL)
	}
	if drivers := c.kvStoreDrivers(); len(drivers) > 0 && !c.KVStore.Enable {
		problems = append(problems, fmt.Errorf(
			"kvstore.enable: false, but %s set to %q; enable the kvstore or choose another driver",
			strings.Join(drivers, ", "), CacheKV))
	}

	// The mailer is optional: with no SMTP host the application runs, it just
	// cannot send mail, so an empty host is not a problem. What is checked is
	// that a host which is set has a usable port, and that a password is not
	// given without the username it authenticates.
	check(isEmail(c.Mailer.FromEmail), "mailer.from_email: %q must be an email address", c.Mailer.FromEmail)
	check(c.Mailer.FromName != "", "mailer.from_name: must not be empty")
	check(c.Mailer.SMTPPort > 0 && c.Mailer.SMTPPort <= 65535,
		"mailer.smtp_port: %d must be between 1 and 65535", c.Mailer.SMTPPort)
	check(c.Mailer.SMTPPassword == "" || c.Mailer.SMTPUsername != "",
		"mailer.smtp_username: required when mailer.smtp_password is set")
	check(c.Mailer.Timeout > 0, "mailer.timeout: must be positive")

	check(isOneOf(c.RateLimit.Driver, RateLimitDB, RateLimitKV),
		"rate_limit.driver: %q is not one of %s", c.RateLimit.Driver, joinValues(RateLimitDB, RateLimitKV))
	check(c.RateLimit.Limit > 0, "rate_limit.limit: must be positive")
	check(c.RateLimit.AuthLimit > 0, "rate_limit.auth_limit: must be positive")
	check(c.RateLimit.Window > 0, "rate_limit.window: must be positive")

	check(c.Queue.NumWorkers >= 1, "queue.num_workers: %d must be at least 1", c.Queue.NumWorkers)
	check(c.Queue.ReleaseAfter > 0, "queue.release_after: must be positive")
	check(c.Queue.CleanupInterval > 0, "queue.cleanup_interval: must be positive")
	check(!c.Queue.Encrypt || c.App.SecretKey != "",
		"queue.encrypt: requires app.secret_key, the secret it seals payloads with")
	if _, err := time.LoadLocation(c.Queue.Timezone); err != nil {
		check(false, "queue.timezone: %q is not a time zone: %v", c.Queue.Timezone, err)
	}

	check(c.App.AuditRetentionDays > 0, "app.audit_retention_days: must be greater than 0")
	check(c.Server.Host != "", "server.host: must not be empty")
	check(c.Server.Port > 0 && c.Server.Port <= 65535, "server.port: %d must be between 1 and 65535", c.Server.Port)
	check(c.Server.ReadTimeout > 0, "server.read_timeout: must be positive")
	check(c.Server.WriteTimeout > 0, "server.write_timeout: must be positive")
	check(c.Server.IdleTimeout > 0, "server.idle_timeout: must be positive")
	check(c.Server.ShutdownTimeout > 0, "server.shutdown_timeout: must be positive")
	check(c.Server.MaxRequestBytes > 0, "server.max_request_bytes: must be positive")
	// An empty list is the direct case, so only the entries have to be header
	// names. A header name is a token, the same rule the CORS header list
	// applies, and a duplicate would make the order meaningless.
	for index, header := range c.Server.TrustedProxyHeaders {
		check(isToken(header),
			"server.trusted_proxy_headers[%d]: %q is not a header name", index, header)
		check(index == slices.Index(c.Server.TrustedProxyHeaders, header),
			"server.trusted_proxy_headers[%d]: %q is listed twice", index, header)
	}
	checkCORS(&c.Server.CORS, check)

	check(c.Auth.SessionDriver == "" || isOneOf(c.Auth.SessionDriver, SessionDB, SessionKV),
		"auth.session_driver: %q is not one of %s", c.Auth.SessionDriver, joinValues(SessionDB, SessionKV))

	check(isOneOf(c.Storage.Driver, StorageLocal, StorageS3),
		"storage.driver: %q is not one of %s", c.Storage.Driver, joinValues(StorageLocal, StorageS3))
	check(c.Storage.LocalPath != "", "storage.local_path: must not be empty")
	check(c.Storage.Watch.Debounce > 0, "storage.watch.debounce: must be positive")

	// The S3 section is checked only when the driver selects it. A local
	// deployment never dials an object store, so holding its settings to
	// anything would report a problem in a part of the file that is switched
	// off, and a user keeping credentials there for a later switch could not
	// run at all.
	if c.Storage.Driver == StorageS3 {
		s3 := c.Storage.S3
		check(s3.BucketName != "", "storage.s3.bucket_name: %s",
			c.unsetNote("storage.s3.bucket_name", "must not be empty when storage.driver is s3"))
		check(s3.Region != "", "storage.s3.region: %s",
			c.unsetNote("storage.s3.region", "must not be empty when storage.driver is s3"))
		check(s3.AccessKeyID != "", "storage.s3.access_key_id: %s",
			c.unsetNote("storage.s3.access_key_id", "must not be empty when storage.driver is s3"))
		check(s3.AccessKeySecret != "", "storage.s3.access_key_secret: %s",
			c.unsetNote("storage.s3.access_key_secret", "must not be empty when storage.driver is s3"))
		check(s3.EndpointURL == "" || isHTTPURL(s3.EndpointURL),
			"storage.s3.endpoint_url: %q must be an absolute http or https URL", s3.EndpointURL)
		// The protocol caps a signed link at seven days, and the client takes a
		// zero as "use my own fifteen-minute default" rather than as a
		// lifetime, so both ends are held here instead of failing at signing
		// time.
		check(s3.SignedURLExpires > 0 && s3.SignedURLExpires <= maxS3SignedURLExpires,
			"storage.s3.signed_url_expires: %s must be between 1s and %s",
			s3.SignedURLExpires, maxS3SignedURLExpires)
	}

	checkAuth(&c, check)

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w:\n%s", ErrInvalid, indentProblems(problems))
}

// unsetNote describes a key left at its default because the variable its
// directive named is not set.
//
// An unset variable is reported only where it leaves the key unusable, not for
// every key that references one: app.mode falls back to development and
// auth.private_key to the HMAC secret, so naming those would report a choice the
// user made on purpose. The caller supplies the wording for the resolved case,
// so the message reads the same whether the file named a variable or not.
func (c Config) unsetNote(key, resolved string) string {
	name, ok := c.unresolved[key]
	if !ok {
		return resolved
	}
	return fmt.Sprintf("must not be empty: the %s variable is not set", name)
}

// indentProblems renders one problem per line, each indented under the header.
// Every line is indented, not just the first: errors.Join separates them with a
// newline, so a single indent would leave the rest at column zero.
func indentProblems(problems []error) string {
	lines := make([]string, 0, len(problems))
	for _, problem := range problems {
		lines = append(lines, "  "+problem.Error())
	}
	return strings.Join(lines, "\n")
}

// isOneOf reports whether value matches one of the accepted values.
func isOneOf[T comparable](value T, accepted ...T) bool {
	return slices.Contains(accepted, value)
}

// checkCORS validates the cross-origin policy. It runs for every configuration,
// because the middleware applies it to every route: a policy that cannot be
// honoured has to fail the run, not disable itself quietly.
func checkCORS(cors *CORS, check func(ok bool, format string, args ...any)) {
	credentials := cors.AllowCredentials

	for _, origin := range cors.AllowedOrigins {
		if origin == corsWildcard {
			// The specification forbids credentials with a wildcard: the
			// browser refuses the combination, so a run would hold it
			// silently.
			check(!credentials, "server.cors.allow_credentials: must be false when allowed_origins is %s", corsWildcard)
			continue
		}
		check(isOrigin(origin), "server.cors.allowed_origins: %q must be a scheme://host origin or %s", origin, corsWildcard)
	}

	for _, method := range cors.AllowedMethods {
		check(isToken(method), "server.cors.allowed_methods: %q is not an HTTP method", method)
	}
	for _, header := range cors.AllowedHeaders {
		check(isToken(header), "server.cors.allowed_headers: %q is not a header name", header)
	}
	for _, header := range cors.ExposedHeaders {
		check(isToken(header), "server.cors.exposed_headers: %q is not a header name", header)
	}
	check(cors.MaxAge >= 0, "server.cors.max_age: %s must not be negative", cors.MaxAge)
}

// noDuplicates reports whether every value appears once. A repeated value is a
// mistake worth naming: the list is read as "which sinks", and naming one twice
// says nothing a reader can act on.
func noDuplicates(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

// joinValues renders a value list for a validation message.
func joinValues(values ...string) string {
	return strings.Join(values, ", ")
}

// isHexKey reports whether value is a 64-character hex string, the encoding
// pkg/crypto expects for an AES-256 key.
func isHexKey(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := strconv.ParseUint(value[:16], 16, 64)
	if err != nil {
		return false
	}
	_, err = strconv.ParseUint(value[48:], 16, 64)
	return err == nil
}

// logTransport reports whether the configured transport list names one sink.
// It is how a section is read only when the sink that owns it is switched on.
func (c Config) logTransport(name string) bool {
	return slices.Contains(c.Log.Transport, name)
}

// otelEnabled reports whether any signal ships to the collector. It is what
// makes the shared endpoint and service name required: with every signal off,
// nothing dials and the values are not read.
func (c Config) otelEnabled() bool {
	return c.logTransport(LogTransportOTLP) || c.OTEL.Tracing.Enable || c.OTEL.Metrics.Enable
}

// jsonUnsupportedSignals names the enabled signals whose exporter cannot encode
// JSON. It is what turns the one protocol the Go SDK implements unevenly into a
// message naming the signal that would be wrong, rather than a bare refusal of a
// value the specification allows.
func (c Config) jsonUnsupportedSignals() []string {
	var signals []string
	if c.OTEL.Metrics.Enable {
		signals = append(signals, "metrics")
	}
	if c.logTransport(LogTransportOTLP) {
		signals = append(signals, "logs")
	}
	return signals
}

// kvStoreDrivers returns the feature keys whose driver is the key-value backend.
// It is what turns "a driver points at a switched-off backend" into a message
// that names the key, rather than a failure at start-up. A feature that is
// itself switched off reads no driver, so it is not reported.
//
// The cache is deliberately absent: it degrades to the no-op driver when its
// backend is unavailable, which is a caching question, not a start-up one.
func (c Config) kvStoreDrivers() []string {
	var keys []string
	if c.RateLimit.Driver == RateLimitKV {
		keys = append(keys, "rate_limit.driver")
	}
	if c.Auth.SessionDriver == SessionKV {
		keys = append(keys, "auth.session_driver")
	}
	return keys
}

// maxS3SignedURLExpires is the longest lifetime a presigned URL may carry. The
// S3 protocol caps a signed link at seven days; a client that asked for longer
// would be refused by the server at signing time, so the limit is enforced where
// the value is read.
const maxS3SignedURLExpires = 7 * 24 * time.Hour

// checkAuth validates the JWT signing material.
func checkAuth(c *Config, check func(ok bool, format string, args ...any)) {
	// The key pair and the HMAC secret are alternatives: a token is signed with
	// one or the other, so at least one must be present.
	check(c.Auth.PrivateKey != "" || c.Auth.SecretKey != "",
		"auth: set auth.private_key or auth.secret_key")
	check(c.Auth.PrivateKey == "" || c.Auth.PublicKey != "",
		"auth.public_key: required when auth.private_key is set")
	check(c.Auth.JWTAlgorithm == "" || isOneOf(c.Auth.JWTAlgorithm, JWTAlgorithms...),
		"auth.jwt_algorithm: %q is not one of %s", c.Auth.JWTAlgorithm, joinValues(JWTAlgorithms...))
	// A named algorithm must match the material the deployment configured. A
	// symmetric algorithm needs the secret; an asymmetric one needs the pair.
	// Both are checked only when the algorithm is named, so an unset value
	// derives the right answer from whatever is configured.
	if c.Auth.JWTAlgorithm != "" {
		if IsHMACAlgorithm(c.Auth.JWTAlgorithm) {
			check(c.Auth.SecretKey != "",
				"auth.jwt_algorithm: %q requires auth.secret_key", c.Auth.JWTAlgorithm)
		} else {
			check(c.Auth.PrivateKey != "",
				"auth.jwt_algorithm: %q requires auth.private_key", c.Auth.JWTAlgorithm)
		}
	}
	check(c.Auth.Issuer != "", "auth.issuer: must not be empty")
	check(c.Auth.AccessTTL > 0, "auth.access_ttl: must be positive")
	check(c.Auth.RefreshShortTTL > 0, "auth.refresh_short_ttl: must be positive")
	check(c.Auth.RefreshLongTTL > 0, "auth.refresh_long_ttl: must be positive")
	check(c.Auth.RefreshShortTTL >= c.Auth.AccessTTL,
		"auth.refresh_short_ttl: must not be shorter than auth.access_ttl")
	check(c.Auth.RefreshLongTTL >= c.Auth.RefreshShortTTL,
		"auth.refresh_long_ttl: must not be shorter than auth.refresh_short_ttl")
}

// String renders the configuration with every secret redacted, so an accidental
// %v of a Config cannot leak a credential.
func (c Config) String() string {
	redacted := c.Redacted()
	return fmt.Sprintf("app=%s database=%s server=%s:%d log=%s/%s",
		redacted.App.Mode, RedactDSN(redacted.Database.URL), redacted.Server.Host,
		redacted.Server.Port, redacted.Log.Level, redacted.Log.Format)
}
