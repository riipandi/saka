package appconfig

import (
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	systemv1 "github.com/riipandi/tango/codegen/proto/go/tango/system/v1"
	"github.com/riipandi/tango/internal/config"
)

// publicConfig maps the deployment's configuration onto the subset an
// unauthenticated client may read. Every field is one the SPA bootstrap
// needs; nothing else crosses, so a key added to the configuration later
// stays private until this mapping names it.
func publicConfig(c config.Config) *systemv1.AppConfigPublic {
	return &systemv1.AppConfigPublic{
		App: &systemv1.AppConfigPublicApp{
			Mode:      c.App.Mode,
			BaseUrl:   c.App.BaseURL,
			AssetsUrl: c.App.AssetsURL,
		},
		Auth: &systemv1.AppConfigPublicAuth{
			OneTimeAccessEmailAsAdminEnabled:           c.Auth.OneTimeAccessEmailAsAdminEnabled,
			OneTimeAccessEmailAsUnauthenticatedEnabled: c.Auth.OneTimeAccessEmailAsUnauthenticatedEnabled,
		},
		Mailer: &systemv1.AppConfigPublicMailer{
			AnnouncementEmailEnabled: c.Mailer.Notifications.AnnouncementEmailEnabled,
		},
	}
}

// fullConfig maps the deployment's configuration onto the administrator's
// view. It is an explicit field-per-field mapping rather than a reflection
// walk so a secret can only leak by being named here, and no secret is: the
// key material, the SMTP password, the S3 key secret, the collector headers,
// and the datastore URLs are read from the file or the environment that owns
// them, never published.
func fullConfig(c config.Config) *systemv1.AppConfig {
	return &systemv1.AppConfig{
		App: &systemv1.AppConfigApp{
			Mode:               c.App.Mode,
			BaseUrl:            c.App.BaseURL,
			AssetsUrl:          c.App.AssetsURL,
			ExposeResetToken:   c.App.ExposeResetToken,
			ExposeTotpSecret:   c.App.ExposeTotpSecret,
			AuditRetentionDays: int64(c.App.AuditRetentionDays),
		},
		Auth: &systemv1.AppConfigAuth{
			JwtAlgorithm:                     c.Auth.JWTAlgorithm,
			Issuer:                           c.Auth.Issuer,
			AccessTtl:                        dur(c.Auth.AccessTTL),
			RefreshShortTtl:                  dur(c.Auth.RefreshShortTTL),
			RefreshLongTtl:                   dur(c.Auth.RefreshLongTTL),
			ExpiryEmailEnabled:               c.Auth.ExpiryEmailEnabled,
			SessionDriver:                    c.Auth.SessionDriver,
			OneTimeAccessEmailAsAdminEnabled: c.Auth.OneTimeAccessEmailAsAdminEnabled,
			OneTimeAccessEmailAsUnauthenticatedEnabled: c.Auth.OneTimeAccessEmailAsUnauthenticatedEnabled,
		},
		Cache: &systemv1.AppConfigCache{
			Enable:    c.Cache.Enable,
			Driver:    c.Cache.Driver,
			Ttl:       dur(c.Cache.TTL),
			MaxMemory: c.Cache.MaxMemory,
		},
		Database: &systemv1.AppConfigDatabase{
			MaxConns:             int64(c.Database.MaxConns),
			MinConns:             int64(c.Database.MinConns),
			MaxConnLifetime:      dur(c.Database.MaxConnLifetime),
			MaxConnIdleTime:      dur(c.Database.MaxConnIdleTime),
			ConnectTimeout:       dur(c.Database.ConnectTimeout),
			ConnectAttempts:      int64(c.Database.ConnectAttempts),
			ConnectRetryInterval: dur(c.Database.ConnectRetryInterval),
			SearchPath:           c.Database.SearchPath,
			Timezone:             c.Database.Timezone,
		},
		Fetcher: &systemv1.AppConfigFetcher{
			UserAgent:               c.Fetcher.UserAgent,
			Timeout:                 dur(c.Fetcher.Timeout),
			RetryCount:              int64(c.Fetcher.RetryCount),
			RetryWait:               dur(c.Fetcher.RetryWait),
			RetryMaxWait:            dur(c.Fetcher.RetryMaxWait),
			CircuitFailureThreshold: int64(c.Fetcher.CircuitFailureThreshold),
			CircuitSuccessThreshold: int64(c.Fetcher.CircuitSuccessThreshold),
			CircuitResetTimeout:     dur(c.Fetcher.CircuitResetTimeout),
			MaxBodyBytes:            c.Fetcher.MaxBodyBytes,
		},
		Kvstore: &systemv1.AppConfigKVStore{
			Enable: c.KVStore.Enable,
			Db:     int64(c.KVStore.DB),
		}, Log: &systemv1.AppConfigLog{
			Level:     c.Log.Level,
			Transport: c.Log.Transport,
			Format:    c.Log.Format,
			File: &systemv1.AppConfigLogFile{
				MaxSize:    int64(c.Log.File.MaxSize),
				MaxBackups: int64(c.Log.File.MaxBackups),
				MaxAge:     int64(c.Log.File.MaxAge),
				Compress:   c.Log.File.Compress,
			},
			Otlp: &systemv1.AppConfigLogOTLP{
				Path:    c.Log.OTLP.Path,
				Timeout: dur(c.Log.OTLP.Timeout),
			},
		},
		Mailer: &systemv1.AppConfigMailer{
			FromEmail:              c.Mailer.FromEmail,
			FromName:               c.Mailer.FromName,
			SmtpHost:               c.Mailer.SMTPHost,
			SmtpPort:               int64(c.Mailer.SMTPPort),
			SmtpUsername:           c.Mailer.SMTPUsername,
			SmtpSecure:             c.Mailer.SMTPSecure,
			SmtpAllowPlaintextAuth: c.Mailer.SMTPAllowPlaintextAuth,
			Timeout:                dur(c.Mailer.Timeout),
			Notifications:          mailerNotifications(c),
		},
		Otel: &systemv1.AppConfigOTel{
			Endpoint:    c.OTEL.Endpoint,
			ServiceName: c.OTEL.ServiceName,
			Environment: c.OTEL.Environment,
			Protocol:    c.OTEL.Protocol,
			Compression: c.OTEL.Compression,
			Queue: &systemv1.AppConfigOTelQueue{
				MaxSize: int64(c.OTEL.Queue.MaxSize),
			},
			Tracing: &systemv1.AppConfigOTelTracing{
				Enable:        c.OTEL.Tracing.Enable,
				Path:          c.OTEL.Tracing.Path,
				Sampler:       c.OTEL.Tracing.Sampler,
				Ratio:         c.OTEL.Tracing.Ratio,
				BatchTimeout:  dur(c.OTEL.Tracing.BatchTimeout),
				ExportTimeout: dur(c.OTEL.Tracing.ExportTimeout),
				MaxBatchSize:  int64(c.OTEL.Tracing.MaxBatchSize),
			},
			Metrics: &systemv1.AppConfigOTelMetrics{
				Enable:         c.OTEL.Metrics.Enable,
				Push:           c.OTEL.Metrics.Push,
				Path:           c.OTEL.Metrics.Path,
				PrometheusPath: c.OTEL.Metrics.PrometheusPath,
				Interval:       dur(c.OTEL.Metrics.Interval),
				ExportTimeout:  dur(c.OTEL.Metrics.ExportTimeout),
			},
		},
		Queue: &systemv1.AppConfigQueue{
			NumWorkers:      int64(c.Queue.NumWorkers),
			ReleaseAfter:    dur(c.Queue.ReleaseAfter),
			CleanupInterval: dur(c.Queue.CleanupInterval),
			Encrypt:         c.Queue.Encrypt,
			Timezone:        c.Queue.Timezone,
		},
		RateLimit: &systemv1.AppConfigRateLimit{
			Driver: c.RateLimit.Driver,
			Limit:  int64(c.RateLimit.Limit),
			Window: dur(c.RateLimit.Window),
		},
		Server: &systemv1.AppConfigServer{
			Host:                c.Server.Host,
			Port:                int64(c.Server.Port),
			ReadTimeout:         dur(c.Server.ReadTimeout),
			WriteTimeout:        dur(c.Server.WriteTimeout),
			IdleTimeout:         dur(c.Server.IdleTimeout),
			ShutdownTimeout:     dur(c.Server.ShutdownTimeout),
			MaxRequestBytes:     int64(c.Server.MaxRequestBytes),
			TrustedProxyHeaders: c.Server.TrustedProxyHeaders,
			Cors: &systemv1.AppConfigCORS{
				AllowedOrigins:   c.Server.CORS.AllowedOrigins,
				AllowedMethods:   c.Server.CORS.AllowedMethods,
				AllowedHeaders:   c.Server.CORS.AllowedHeaders,
				ExposedHeaders:   c.Server.CORS.ExposedHeaders,
				AllowCredentials: c.Server.CORS.AllowCredentials,
				MaxAge:           dur(c.Server.CORS.MaxAge),
			},
		},
		Storage: &systemv1.AppConfigStorage{
			Driver:    c.Storage.Driver,
			LocalPath: c.Storage.LocalPath,
			Watch: &systemv1.AppConfigStorageWatch{
				Enable:   c.Storage.Watch.Enable,
				Debounce: dur(c.Storage.Watch.Debounce),
			},
			S3: &systemv1.AppConfigStorageS3{
				AccessKeyId:      c.Storage.S3.AccessKeyID,
				BucketName:       c.Storage.S3.BucketName,
				EndpointUrl:      c.Storage.S3.EndpointURL,
				ForcePathStyle:   c.Storage.S3.ForcePathStyle,
				PathPrefix:       c.Storage.S3.PathPrefix,
				Region:           c.Storage.S3.Region,
				SignedUrlExpires: dur(c.Storage.S3.SignedURLExpires),
			},
		},
	}
}

func mailerNotifications(c config.Config) *systemv1.AppConfigMailerNotifications {
	n := c.Mailer.Notifications
	return &systemv1.AppConfigMailerNotifications{
		NewDeviceNoticeEnabled:       n.NewDeviceNoticeEnabled,
		PasswordChangedNoticeEnabled: n.PasswordChangedNoticeEnabled,
		MfaDisabledNoticeEnabled:     n.MfaDisabledNoticeEnabled,
		UserBannedNoticeEnabled:      n.UserBannedNoticeEnabled,
		UserUnbannedNoticeEnabled:    n.UserUnbannedNoticeEnabled,
		ApiKeyExpiringNoticeEnabled:  n.APIKeyExpiringNoticeEnabled,
		EmailChangeNoticeEnabled:     n.EmailChangeNoticeEnabled,
		AnnouncementEmailEnabled:     n.AnnouncementEmailEnabled,
	}
}

// dur maps a duration. A zero stays a zero: the field is absent on the wire
// because that is what an unset proto3 field is, not because it was hidden.
func dur(d time.Duration) *durationpb.Duration {
	return durationpb.New(d)
}
