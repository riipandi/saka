package config

import (
	"path/filepath"
	"strings"

	fcache "github.com/riipandi/saka/framework/cache"
	fetcher "github.com/riipandi/saka/framework/fetcher"
	flogger "github.com/riipandi/saka/framework/logger"
	fmailer "github.com/riipandi/saka/framework/mailer"
	fobserver "github.com/riipandi/saka/framework/observer"
	fstorage "github.com/riipandi/saka/framework/storage"
)

// The adapters are the only place the schema meets the framework options: a
// framework package takes typed values and reads nothing itself, so the
// resolved configuration is what decides, and the mapping happens once here
// (decisions 8 and 9).

// FetcherOptions maps the fetcher section onto the outbound client's options.
// The telemetry identity is this application's: the namespace prefixes the
// series names, the scope names the instrumenting code.
func (c Config) FetcherOptions() fetcher.Options {
	return fetcher.Options{
		Scope:                   "github.com/riipandi/saka/framework/fetcher",
		TelemetryNamespace:      AppIdentifier,
		UserAgent:               c.Fetcher.UserAgent,
		Timeout:                 c.Fetcher.Timeout,
		RetryCount:              c.Fetcher.RetryCount,
		RetryWait:               c.Fetcher.RetryWait,
		RetryMaxWait:            c.Fetcher.RetryMaxWait,
		CircuitFailureThreshold: c.Fetcher.CircuitFailureThreshold,
		CircuitSuccessThreshold: c.Fetcher.CircuitSuccessThreshold,
		CircuitResetTimeout:     c.Fetcher.CircuitResetTimeout,
		MaxBodyBytes:            c.Fetcher.MaxBodyBytes,
	}
}

// CacheOptions maps the cache and kvstore sections onto the cache options.
// The key prefix namespaces this deployment's keys inside a shared backend.
func (c Config) CacheOptions() fcache.Options {
	return fcache.Options{
		Enable:    c.Cache.Enable,
		Driver:    fcache.CacheDriver(c.Cache.Driver),
		TTL:       c.Cache.TTL,
		MaxMemory: c.Cache.MaxMemory,
		KeyPrefix: CacheKeyPrefix,
		KVEnabled: c.KVStore.Enable,
	}
}

// ObserverOptions maps the otel section and the collector security rule onto
// the observer's options.
func (c Config) ObserverOptions() fobserver.Options {
	return fobserver.Options{
		Endpoint:     c.OTEL.Endpoint,
		Compression:  fobserver.Compression(c.OTEL.Compression),
		Headers:      headers(c.OTEL.Headers),
		Secure:       c.CollectorSecure(),
		QueueMaxSize: c.OTEL.QueueMaxSize,
		ServiceName:  c.OTEL.ServiceName,
		Version:      AppVersion,
		Environment:  c.OTEL.Environment,
		Tracing: fobserver.Tracing{
			Enable:        c.OTEL.Tracing.Enable,
			Path:          c.OTEL.Tracing.Path,
			Sampler:       fobserver.Sampler(c.OTEL.Tracing.Sampler),
			Ratio:         c.OTEL.Tracing.Ratio,
			BatchTimeout:  c.OTEL.Tracing.BatchTimeout,
			ExportTimeout: c.OTEL.Tracing.ExportTimeout,
			MaxBatchSize:  c.OTEL.Tracing.MaxBatchSize,
		},
		Metrics: fobserver.Metrics{
			Enable:        c.OTEL.Metrics.Enable,
			Push:          c.OTEL.Metrics.Push,
			Path:          c.OTEL.Metrics.Path,
			Interval:      c.OTEL.Metrics.Interval,
			ExportTimeout: c.OTEL.Metrics.ExportTimeout,
		},
	}
}

// LoggerOptions maps the log and otel sections onto the logger's options.
func (c Config) LoggerOptions() flogger.Options {
	transports := make([]flogger.Transport, 0, len(c.Log.Transport))
	for _, name := range c.Log.Transport {
		transports = append(transports, flogger.Transport(name))
	}
	return flogger.Options{
		Level:       flogger.Level(c.Log.Level),
		Format:      flogger.Format(c.Log.Format),
		Transports:  transports,
		File:        flogger.FileOptions(c.Log.File),
		OTLP:        c.OTLPOptions(),
		ServiceName: AppIdentifier,
		Version:     AppVersion,
		Environment: c.OTEL.Environment,
		FilePath:    c.LogFilePath(),
	}
}

// OTLPOptions is the collector settings the log sink exports through. It is
// the otel section's shared address with the log sink's own timeout and
// route.
func (c Config) OTLPOptions() flogger.OTLPOptions {
	return flogger.OTLPOptions{
		Endpoint:     c.OTEL.Endpoint,
		Headers:      headers(c.OTEL.Headers),
		Compression:  fobserver.Compression(c.OTEL.Compression),
		QueueMaxSize: c.OTEL.QueueMaxSize,
		Timeout:      c.Log.OTLP.Timeout,
		Path:         c.Log.OTLP.Path,
		Secure:       c.CollectorSecure(),
	}
}

// LogFilePath is the active log file, under the one data directory of the
// process. It is exported because a command that reports where logs go — or a
// test that reads them — needs the same answer the logger is built with, and
// re-deriving it at the call site is how two paths start to disagree.
func (c Config) LogFilePath() string {
	dir := c.Storage.LocalPath
	if dir == "" {
		dir = DefaultDataDir
	}
	return filepath.Join(dir, LogDir, LogFileName)
}

// headers copies a header map, so a caller holding the resolved configuration
// cannot be surprised by a framework package mutating what it was handed.
func headers(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for name, value := range in {
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		out[name] = value
	}
	return out
}

// MailerOptions maps the mailer section onto the SMTP engine's options.
// The notifications section stays schema-side: gating a notice is a feature
// decision, not an engine input.
func (c Config) MailerOptions() fmailer.Options {
	return fmailer.Options{
		FromEmail:              c.Mailer.FromEmail,
		FromName:               c.Mailer.FromName,
		SMTPHost:               c.Mailer.SMTPHost,
		SMTPPort:               c.Mailer.SMTPPort,
		SMTPUsername:           c.Mailer.SMTPUsername,
		SMTPPassword:           c.Mailer.SMTPPassword,
		SMTPSecure:             c.Mailer.SMTPSecure,
		SMTPAllowPlaintextAuth: c.Mailer.SMTPAllowPlaintextAuth,
		Timeout:                c.Mailer.Timeout,
	}
}

// StorageOptions maps the storage section onto the object engine's options.
// The buckets stay registered from the modules; the engine knows only drivers.
func (c Config) StorageOptions() fstorage.Options {
	return fstorage.Options{
		Driver:    c.Storage.Driver,
		LocalPath: c.Storage.LocalPath,
		S3: fstorage.S3Options{
			Region:         c.Storage.S3.Region,
			AccessKey:      c.Storage.S3.AccessKey,
			SecretKey:      c.Storage.S3.SecretKey,
			EndpointURL:    c.Storage.S3.EndpointURL,
			ForcePathStyle: c.Storage.S3.ForcePathStyle,
		},
		TelemetryNamespace: AppIdentifier,
	}
}
