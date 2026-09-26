package config_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/config"
)

// resolveAndValidate is the pair a caller that needs the whole configuration
// uses: Load merges the sources, Validate checks the result.
func resolveAndValidate(t *testing.T, opts config.Options) (config.Config, error) {
	t.Helper()

	cfg, err := config.Load(opts)
	if err != nil {
		return config.Config{}, err
	}
	return cfg, cfg.Validate()
}

// resolveFile validates the configuration a file body resolves to.
func resolveFile(t *testing.T, extra string) error {
	t.Helper()

	_, err := resolveAndValidate(t, config.Options{
		ConfigFile: configFile(t, extra),
		Environ:    baseEnv(),
	})
	return err
}

func TestValidationReportsEveryProblem(t *testing.T) {
	// A missing DSN and an impossible port: both must be reported at once, so a
	// file with several mistakes is fixed in one pass.
	_, err := resolveAndValidate(t, config.Options{
		ConfigFile: writeConfig(t, `{"auth": {"secret_key": "env:AUTH_SECRET_KEY"}, "server": {"port": 0}}`),
		Environ:    []string{"AUTH_SECRET_KEY=" + secret},
	})
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "database.url")
	assert.Contains(t, err.Error(), "server.port")
}

func TestValidationRejectsBadDriver(t *testing.T) {
	err := resolveFile(t, `"cache": {"driver": "bogus", "enable": true}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "cache.driver")
}

func TestValidationRequiresASigningKey(t *testing.T) {
	// The key pair and the HMAC secret are alternatives; neither present means
	// no token could be signed.
	_, err := resolveAndValidate(t, config.Options{
		ConfigFile: writeConfig(t, `{"database": {"url": "env:DATABASE_URL"}}`),
		Environ:    []string{"DATABASE_URL=" + dsn},
	})
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "auth.private_key or auth.secret_key")
}

func TestValidationRequiresPublicKeyWithPrivateKey(t *testing.T) {
	err := resolveFile(t, `"auth": {"private_key": "abc"}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "auth.public_key")
}

func TestValidationRejectsMinAboveMax(t *testing.T) {
	err := resolveFile(t, `"database": {"min_conns": 20, "max_conns": 5}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "database.min_conns")
}

func TestValidationRestrictsTheExposureAidsToDevelopment(t *testing.T) {
	// The raw reset token and the decrypted authenticator secret hand an
	// account's credential to anyone who can reach the response, so the
	// flags are development aids: any other mode is a refused run.
	err := resolveFile(t, `"app": {"mode": "production", "expose_reset_token": true}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "app.expose_reset_token")

	err = resolveFile(t, `"app": {"mode": "staging", "expose_totp_secret": true}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "app.expose_totp_secret")
}

func TestValidationAcceptsTheExposureAidsInDevelopment(t *testing.T) {
	// The default mode is development, so the flags answer their aid.
	err := resolveFile(t, `"app": {"expose_reset_token": true, "expose_totp_secret": true}`)
	require.NoError(t, err)
}

func TestValidationRejectsAConnectWaitWithoutAttemptsOrInterval(t *testing.T) {
	// The wait is what makes the startup survive a database that is still
	// starting; zero attempts would disable it and zero interval would turn
	// the attempts into a busy loop, so neither is a value a file may set.
	err := resolveFile(t, `"database": {"connect_attempts": 0, "connect_retry_interval": 0}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "database.connect_attempts")
	assert.Contains(t, err.Error(), "database.connect_retry_interval")
}

func TestValidationAcceptsAValidConfiguration(t *testing.T) {
	require.NoError(t, resolveFile(t, ""))
}

func TestValidationRejectsAnInvalidModeName(t *testing.T) {
	err := resolveFile(t, `"app": {"mode": "prod"}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "app.mode")
}

// The mailer is optional, so a configuration that names no SMTP host is valid:
// that is what lets a local checkout run without a mail server.
func TestValidationAcceptsAnUnconfiguredMailer(t *testing.T) {
	cfg, err := resolveAndValidate(t, config.Options{
		ConfigFile: configFile(t, ""),
		Environ:    baseEnv(),
	})
	require.NoError(t, err)

	assert.Empty(t, cfg.Mailer.SMTPHost)
	assert.Equal(t, "mailer@example.com", cfg.Mailer.FromEmail)
	assert.Equal(t, "Tango Mailer", cfg.Mailer.FromName)
	assert.Equal(t, 587, cfg.Mailer.SMTPPort)
}

func TestValidationRejectsAnInvalidSenderAddress(t *testing.T) {
	err := resolveFile(t, `"mailer": {"from_email": "not-an-address"}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "mailer.from_email")
}

func TestValidationRejectsAPasswordWithoutAUsername(t *testing.T) {
	// A password that authenticates nothing is a mistake, not a setting.
	err := resolveFile(t, `"mailer": {"smtp_password": "hunter2"}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "mailer.smtp_username")
}

func TestValidationRejectsANonPositiveMailerTimeout(t *testing.T) {
	// A submission with no budget would wait out the library's own five-minute
	// default, which is not a bound this process chose.
	err := resolveFile(t, `"mailer": {"timeout": 0}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "mailer.timeout")
}

func TestValidationAcceptsRemoteCredentialsWithoutTLS(t *testing.T) {
	// The configuration cannot know whether the server offers STARTTLS, so a
	// remote host with credentials is valid. Refusing it here would reject the
	// ordinary submission server; the decision is made on the session, where
	// the answer exists (mailer.ErrInsecureAuth).
	cfg, err := resolveAndValidate(t, config.Options{
		ConfigFile: configFile(t, `"mailer": {"smtp_host": "smtp.example.com", "smtp_username": "bot", "smtp_password": "hunter2"}`),
		Environ:    baseEnv(),
	})
	require.NoError(t, err)
	assert.False(t, cfg.Mailer.SMTPSecure)
	assert.False(t, cfg.Mailer.SMTPAllowPlaintextAuth)
}

func TestRedactedHidesTheSMTPPassword(t *testing.T) {
	cfg, err := resolveAndValidate(t, config.Options{
		ConfigFile: configFile(t, `"mailer": {"smtp_username": "bot", "smtp_password": "hunter2"}`),
		Environ:    baseEnv(),
	})
	require.NoError(t, err)

	redacted := cfg.Redacted()

	assert.Equal(t, "[redacted]", redacted.Mailer.SMTPPassword)
	assert.Equal(t, "bot", redacted.Mailer.SMTPUsername, "a username is not a secret")
	assert.NotContains(t, redacted.String(), "hunter2")
}

func TestRedactedHidesSecrets(t *testing.T) {
	cfg, err := resolveAndValidate(t, config.Options{
		ConfigFile: configFile(t, ""),
		Environ:    baseEnv(),
	})
	require.NoError(t, err)

	redacted := cfg.Redacted()

	assert.Equal(t, "[redacted]", redacted.Auth.SecretKey)
	assert.NotContains(t, redacted.Database.URL, "pass")
	assert.Contains(t, redacted.Database.URL, "localhost:5432/tango")
	assert.NotContains(t, cfg.String(), "pass", "String must not leak the password")
}

func TestRedactedLeavesNothingBehind(t *testing.T) {
	cfg, err := resolveAndValidate(t, config.Options{
		ConfigFile: configFile(t, `"app": {"secret_key": "env:APP_SECRET_KEY"}`),
		Environ:    append(baseEnv(), "APP_SECRET_KEY="+secret),
	})
	require.NoError(t, err)

	redacted := cfg.Redacted()

	assert.Empty(t, redacted.Origin("database.url"), "Origins must be dropped with the secrets")
	for _, secret := range []string{cfg.App.SecretKey, cfg.Auth.SecretKey, cfg.Database.URL} {
		assert.NotContains(t, redacted.String(), secret)
	}
}

func TestKeysMatchTheStruct(t *testing.T) {
	keys := config.Keys()
	require.NotEmpty(t, keys)

	assert.Contains(t, keys, "storage.local_path")
	assert.Contains(t, keys, "app.mode")
	assert.Contains(t, keys, "database.url")
	assert.Contains(t, keys, "server.port")
	assert.Contains(t, keys, "auth.access_ttl")
	assert.IsIncreasing(t, keys, "Keys must be sorted")
}

func TestMaskedKeepsTheEndsOfALongSecret(t *testing.T) {
	// The whole point of masking: two keys that look alike can be told apart,
	// while nothing usable is revealed.
	cfg := config.Default()
	cfg.App.SecretKey = secret

	assert.Equal(t, secret[:4]+"****"+secret[len(secret)-4:], cfg.Masked().App.SecretKey)
}

func TestMaskedHidesAShortSecretCompletely(t *testing.T) {
	// Keeping eight of a twelve-character password would leave most of it
	// readable, so a value below the floor is hidden in full.
	for _, password := range []string{"hunter2", "0123456789abcde"} {
		cfg := config.Default()
		cfg.Mailer.SMTPPassword = password

		assert.Equal(t, "[redacted]", cfg.Masked().Mailer.SMTPPassword, password)
	}

	cfg := config.Default()
	cfg.Mailer.SMTPPassword = "0123456789abcdef"
	assert.Equal(t, "0123****cdef", cfg.Masked().Mailer.SMTPPassword)
}

func TestMaskedLeavesAnEmptySecretEmpty(t *testing.T) {
	// An unset secret must read as unset, not as a masked value: the difference
	// is the whole answer when a key is missing.
	assert.Empty(t, config.Default().Masked().App.SecretKey)
	assert.Empty(t, config.Default().Masked().Auth.SecretKey)
}

func TestMaskedReducesTheDSNRatherThanMaskingIt(t *testing.T) {
	// A connection string is a composite value: revealing part of the string
	// says nothing, while the host is the part a reader needs.
	cfg := config.Default()
	cfg.Database.URL = "postgresql://user:sup3rs3cret@localhost:5432/tango?sslmode=disable"

	masked := cfg.Masked()

	assert.Equal(t, "localhost:5432/tango", masked.Database.URL)
	assert.NotContains(t, masked.Database.URL, "sup3rs3cret")
}

func TestMaskedAndRedactedAreDifferent(t *testing.T) {
	// Two renderings, two jobs. Masked is for a report the operator runs, and
	// shows enough to trace a value; Redacted is the fail-safe behind a log
	// line, and shows nothing at all.
	cfg := config.Default()
	cfg.App.SecretKey = secret

	assert.NotEqual(t, cfg.Masked().App.SecretKey, cfg.Redacted().App.SecretKey)
	assert.Equal(t, "[redacted]", cfg.Redacted().App.SecretKey)
	assert.NotContains(t, cfg.String(), secret[:4], "a log line must not show part of a key")
}

func TestValidationAcceptsACacheDriverWithTheBackendOff(t *testing.T) {
	// The cache is the one feature that may point at a switched-off backend:
	// a cache that cannot reach its server is not a broken system, it is a
	// run without caching — the driver bypasses to no-op at start-up, so
	// this is accepted rather than reported.
	err := resolveFile(t, `"cache": {"driver": "kvstore", "enable": true}`)
	require.NoError(t, err)
}

func TestValidationNamesEveryKVDriverThatDisagrees(t *testing.T) {
	err := resolveFile(t,
		`"rate_limit": {"driver": "kvstore"}, `+
			`"auth": {"session_driver": "kvstore"}`)
	require.ErrorIs(t, err, config.ErrInvalid)

	// One message naming both beats two messages naming one each.
	for _, key := range []string{"rate_limit.driver", "auth.session_driver"} {
		assert.Contains(t, err.Error(), key)
	}
}

func TestValidationAcceptsAKVDriverWithTheBackendOn(t *testing.T) {
	err := resolveFile(t, `"rate_limit": {"driver": "kvstore"}, "auth": {"session_driver": "kvstore"}, "kvstore": {"enable": true}`)
	assert.NoError(t, err)
}

func TestValidationSkipsTheCacheSectionWhileDisabled(t *testing.T) {
	// The default state: the cache is off, so a driver that would be dead
	// weight is not reported and no budget is held to anything.
	require.NoError(t, resolveFile(t, `"cache": {"driver": "kvstore", "ttl": 0}`))
}

func TestValidationAcceptsADisabledKVStore(t *testing.T) {
	// The default state: nothing points at the backend and it is switched off.
	require.NoError(t, resolveFile(t, ""))
}

func TestValidationChecksTheKVURLOnlyWhenEnabled(t *testing.T) {
	// A disabled backend is never dialled, so its URL is not held to anything.
	// Holding it would report a problem in a part of the file that is off.
	require.NoError(t, resolveFile(t, `"kvstore": {"enable": false, "url": "not-a-url"}`))

	err := resolveFile(t, `"kvstore": {"enable": true, "url": "not-a-url"}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "kvstore.url")
}

func TestValidationAcceptsTheURLGrammarTheClientAccepts(t *testing.T) {
	// Validation must not be stricter than the client, or it rejects a URL that
	// would have connected. These are the forms redis.ParseURL accepts: the
	// default port, an explicit port, TLS, a database index, a missing host
	// (defaulted to localhost:6379), and a unix socket.
	for _, rawURL := range []string{
		"redis://localhost",
		"redis://localhost:6379",
		"redis://default:securedb@localhost:6379",
		"rediss://cache.example.com:6380",
		"redis://localhost:6379/3",
		"redis://localhost:6379/0?dial_timeout=3&max_retries=2",
		"redis://",
		"unix:///var/run/valkey.sock",
	} {
		body := `"kvstore": {"enable": true, "url": ` + strconv.Quote(rawURL) + `}`
		assert.NoError(t, resolveFile(t, body), rawURL)
	}
}

func TestValidationRejectsAURLTheClientWouldReject(t *testing.T) {
	for _, rawURL := range []string{
		"http://localhost:6379",      // not a key-value scheme
		"redis://localhost:6379/a",   // the path must be a database number
		"redis://localhost:6379/1/2", // at most one segment
		"unix://",                    // a unix socket needs its path
		"redis://localhost:6379/%20", // an empty database number
	} {
		body := `"kvstore": {"enable": true, "url": ` + strconv.Quote(rawURL) + `}`
		assert.Error(t, resolveFile(t, body), rawURL)
	}
}

func TestRedactKVURLNamesTheServerWithoutThePassword(t *testing.T) {
	assert.Equal(t, "localhost:6379", config.RedactKVURL("redis://default:securedb@localhost:6379"))
	assert.Equal(t, "cache.example.com:6380/1", config.RedactKVURL("rediss://u:p@cache.example.com:6380/1"))
	assert.Equal(t, "localhost:6379", config.RedactKVURL("redis://"),
		"a missing host is what the client defaults, not a hidden URL")
	assert.Equal(t, "", config.RedactKVURL(""))
}

func TestRedactedHidesTheKVPassword(t *testing.T) {
	cfg := config.Default()
	cfg.KVStore.URL = "redis://default:sup3rs3cret@localhost:6379"

	for name, rendered := range map[string]config.Config{
		"Redacted": cfg.Redacted(),
		"Masked":   cfg.Masked(),
	} {
		assert.NotContains(t, rendered.KVStore.URL, "sup3rs3cret", name)
		assert.Contains(t, rendered.KVStore.URL, "localhost:6379", name)
	}
	assert.NotContains(t, cfg.String(), "sup3rs3cret")
}

func TestValidationLeavesTheS3SectionAloneOnTheLocalDriver(t *testing.T) {
	// The default deployment writes to disk and never dials an object store, so
	// its S3 settings are not held to anything: a user keeping credentials
	// there for a later switch must still be able to run.
	require.NoError(t, resolveFile(t, `"storage": {"driver": "local"}`))

	// Even a value that would be refused on the S3 driver is ignored.
	require.NoError(t, resolveFile(t, `"storage": {"driver": "local",
		"s3": {"bucket_name": "", "endpoint_url": "not-a-url", "signed_url_expires": 0}}`))
}

func TestValidationRequiresTheS3SettingsWhenTheDriverIsS3(t *testing.T) {
	// Switching the driver on is what makes the section live, and the keys a
	// request cannot be made without are then required.
	//
	// The region is not among them: it has a concrete default, so an unset
	// variable leaves a usable value rather than an empty one.
	err := resolveFile(t, `"storage": {"driver": "s3"}`)
	require.ErrorIs(t, err, config.ErrInvalid)

	for _, key := range []string{
		"storage.s3.bucket_name",
		"storage.s3.access_key_id",
		"storage.s3.access_key_secret",
	} {
		assert.Contains(t, err.Error(), key)
	}
	assert.NotContains(t, err.Error(), "storage.s3.region")
}

func TestValidationFallsBackToARegionTheClientWillAccept(t *testing.T) {
	// A region cannot be empty: the client refuses to resolve an endpoint
	// without one, so every request fails, even against a service that ignores
	// the region such as MinIO. An unset variable therefore leaves a usable
	// value rather than an empty one, and the run works.
	_, err := resolveAndValidate(t, config.Options{
		ConfigFile: writeConfig(t, `{
			"database": {"url": "env:DATABASE_URL"},
			"auth": {"secret_key": "env:AUTH_SECRET_KEY"},
			"storage": {"driver": "s3",
				"s3": {"bucket_name": "devbucket",
					"region": "env:STORAGE_S3_REGION",
					"access_key_id": "s3admin", "access_key_secret": "s3passw0rd"}}
		}`),
		Environ: baseEnv(),
	})
	require.NoError(t, err)

	// An explicitly empty region is a different matter: the user named the key
	// and gave it no value, which the client would reject at request time.
	err = resolveFile(t, `"storage": {"driver": "s3",
		"s3": {"bucket_name": "b", "region": "",
			"access_key_id": "k", "access_key_secret": "s"}}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "storage.s3.region")
}

func TestValidationNamesTheUnsetS3Variable(t *testing.T) {
	// A key left at its default because its variable is unset is reported by
	// variable name, which is what a user has to fix.
	_, err := resolveAndValidate(t, config.Options{
		ConfigFile: writeConfig(t, `{
			"database": {"url": "env:DATABASE_URL"},
			"auth": {"secret_key": "env:AUTH_SECRET_KEY"},
			"storage": {"driver": "s3",
				"s3": {"bucket_name": "env:STORAGE_S3_BUCKET_NAME",
					"region": "us-east-1",
					"access_key_id": "env:STORAGE_S3_ACCESS_KEY_ID",
					"access_key_secret": "env:STORAGE_S3_ACCESS_KEY_SECRET"}}
		}`),
		Environ: baseEnv(),
	})
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "STORAGE_S3_BUCKET_NAME variable is not set")
	assert.Contains(t, err.Error(), "STORAGE_S3_ACCESS_KEY_ID variable is not set")
}

func TestValidationAcceptsAnS3Deployment(t *testing.T) {
	err := resolveFile(t, `"storage": {"driver": "s3",
		"s3": {"bucket_name": "devbucket", "region": "us-east-1",
			"access_key_id": "s3admin", "access_key_secret": "s3passw0rd"}}`)
	assert.NoError(t, err)
}

func TestValidationAcceptsAnAWSStyleDeploymentWithoutAnEndpoint(t *testing.T) {
	// An empty endpoint means AWS, reached through the region alone; that is a
	// complete configuration, not a missing one.
	err := resolveFile(t, `"storage": {"driver": "s3",
		"s3": {"bucket_name": "devbucket", "region": "eu-west-1",
			"access_key_id": "AKIAEXAMPLE", "access_key_secret": "s3passw0rd",
			"force_path_style": false}}`)
	assert.NoError(t, err)
}

func TestValidationRejectsAnS3EndpointThatIsNotAURL(t *testing.T) {
	err := resolveFile(t, `"storage": {"driver": "s3",
		"s3": {"bucket_name": "b", "region": "us-east-1",
			"access_key_id": "k", "access_key_secret": "s",
			"endpoint_url": "localhost:9100"}}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "storage.s3.endpoint_url")
}

func TestValidationHoldsTheSignedURLLifetimeInsideSevenDays(t *testing.T) {
	// The client takes a zero as "use my own default" rather than as a
	// lifetime, and the protocol caps a link at seven days, so both ends are
	// rejected here instead of failing at signing time.
	for _, seconds := range []int{-1, 0, 604801} {
		body := `"storage": {"driver": "s3",
			"s3": {"bucket_name": "b", "region": "us-east-1",
				"access_key_id": "k", "access_key_secret": "s",
				"signed_url_expires": ` + strconv.Itoa(seconds) + `}}`
		err := resolveFile(t, body)
		require.ErrorIs(t, err, config.ErrInvalid, "signed_url_expires=%d", seconds)
		assert.Contains(t, err.Error(), "storage.s3.signed_url_expires")
	}

	// The boundaries themselves are valid.
	for _, seconds := range []int{1, 3600, 604800} {
		body := `"storage": {"driver": "s3",
			"s3": {"bucket_name": "b", "region": "us-east-1",
				"access_key_id": "k", "access_key_secret": "s",
				"signed_url_expires": ` + strconv.Itoa(seconds) + `}}`
		assert.NoError(t, resolveFile(t, body), "signed_url_expires=%d", seconds)
	}
}

func TestValidationLeavesTheFileSinkAloneUntilTheTransportNamesIt(t *testing.T) {
	// The console is the only sink by default, so the rotation settings are not
	// read: a container that logs to stdout must not be told to configure a
	// rotation it never uses.
	require.NoError(t, resolveFile(t, `"log": {"file": {"max_size": 0, "max_backups": 0, "max_age": 0}}`))
}

func TestValidationRefusesARotationThatNeverDeletes(t *testing.T) {
	// A file sink that keeps every rotated file fills a disk quietly, so the one
	// combination that does that is refused where the sink is named.
	err := resolveFile(t, `"log": {"transport": ["console", "file"],
		"file": {"max_backups": 0, "max_age": 0}}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "log.file")

	// Either limit on its own is a retention policy.
	assert.NoError(t, resolveFile(t, `"log": {"transport": ["file"], "file": {"max_backups": 3, "max_age": 0}}`))
	assert.NoError(t, resolveFile(t, `"log": {"transport": ["file"], "file": {"max_backups": 0, "max_age": 7}}`))
}

func TestValidationRejectsANonPositiveFileSize(t *testing.T) {
	err := resolveFile(t, `"log": {"transport": ["file"], "file": {"max_size": 0}}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "log.file.max_size")
}

func TestValidationRejectsATransportThatIsNotASink(t *testing.T) {
	// A name no sink matches would leave the run with fewer destinations than
	// the file asks for, which is the failure the list exists to prevent.
	err := resolveFile(t, `"log": {"transport": ["console", "syslog"]}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "log.transport")
	assert.Contains(t, err.Error(), "syslog")
}

func TestValidationRejectsARepeatedTransport(t *testing.T) {
	// Naming one twice says nothing a reader can act on, and the second entry
	// would build a second sink writing the same lines.
	err := resolveFile(t, `"log": {"transport": ["console", "console"]}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "log.transport")
}

func TestValidationRequiresATransport(t *testing.T) {
	// An empty list is a logger that writes nowhere, which is never what a
	// deployment meant.
	err := resolveFile(t, `"log": {"transport": []}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "log.transport")
}

func TestValidationAcceptsEveryTransportTogether(t *testing.T) {
	// Naming several is the point of the list: the terminal stays readable while
	// the same entries go to a file and to a collector.
	err := resolveFile(t, `"log": {"transport": ["console", "file", "otlp"],
		"otlp": {"endpoint": "http://localhost:4318"}}`)
	assert.NoError(t, err)
}

func TestAListDirectiveResolvesToTheTransportsItNames(t *testing.T) {
	// The whole path: a comma-separated variable reaches the slice field as the
	// entries it names, which is what lets a deployment switch a sink on without
	// editing the file. `LOG_TRANSPORT=console,file,otlp` is the form an
	// environment variable can carry, and the one the docs advertise.
	cfg, err := resolveAndValidate(t, config.Options{
		ConfigFile: writeConfig(t, `{
			"database": {"url": "env:DATABASE_URL"},
			"auth": {"secret_key": "env:AUTH_SECRET_KEY"},
			"log": {"transport": "env:LOG_TRANSPORT"}
		}`),
		Environ: append(baseEnv(), "LOG_TRANSPORT=console,file,otlp"),
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"console", "file", "otlp"}, cfg.Log.Transport)
}

func TestASpaceSeparatedDirectiveIsNotAList(t *testing.T) {
	// Space is not the separator: a value with spaces in it is one name, which
	// validation then refuses by name rather than silently reading one sink out
	// of a string that named three.
	_, err := resolveAndValidate(t, config.Options{
		ConfigFile: writeConfig(t, `{
			"database": {"url": "env:DATABASE_URL"},
			"auth": {"secret_key": "env:AUTH_SECRET_KEY"},
			"log": {"transport": "env:LOG_TRANSPORT"}
		}`),
		Environ: append(baseEnv(), "LOG_TRANSPORT=console file otlp"),
	})
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "console file otlp")
}

func TestValidationChecksTheOTLPEndpointOnlyWhenASignalIsOn(t *testing.T) {
	// A collector the run never dials is not held to anything: a console-only
	// run may carry an address for a later switch without it stopping the run.
	require.NoError(t, resolveFile(t, `"log": {"transport": ["console"]}, "otel": {"endpoint": "not-a-url"}`))

	err := resolveFile(t, `"log": {"transport": ["otlp"]}, "otel": {"endpoint": "not-a-url"}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "otel.endpoint")

	// The same address is checked when a signal other than logs is switched on,
	// because all three share it.
	err = resolveFile(t, `"otel": {"endpoint": "not-a-url", "tracing": {"enable": true}}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "otel.endpoint")
}

func TestValidationFallsBackToAnEndpointTheExporterWillDial(t *testing.T) {
	// The endpoint has a concrete default, so an unset variable leaves a usable
	// value rather than an empty one: an unset OTEL_ENDPOINT must not stop a
	// deployment whose collector is on the default port.
	cfg, err := resolveAndValidate(t, config.Options{
		ConfigFile: writeConfig(t, `{
			"database": {"url": "env:DATABASE_URL"},
			"auth": {"secret_key": "env:AUTH_SECRET_KEY"},
			"log": {"transport": ["otlp"]},
			"otel": {"endpoint": "env:OTEL_ENDPOINT"}
		}`),
		Environ: baseEnv(),
	})
	require.NoError(t, err)
	assert.Equal(t, config.DefaultOTLPEndpoint, cfg.OTEL.Endpoint)
}

func TestValidationAcceptsAnOTLPDeployment(t *testing.T) {
	// Both schemes are valid: the endpoint's scheme is what decides whether the
	// connection is TLS, so there is no second setting to keep in step with it.
	for _, endpoint := range []string{"http://localhost:4318", "https://collector.example.com:4318"} {
		body := `"log": {"transport": ["otlp"]}, "otel": {"endpoint": ` + strconv.Quote(endpoint) + `}`
		assert.NoError(t, resolveFile(t, body), endpoint)
	}
}

func TestValidationRefusesAnUnknownProtocol(t *testing.T) {
	// A name outside the specification's three is refused rather than mapped to
	// a default: a deployment that asked for one protocol and silently got
	// another has telemetry its collector may reject without saying so.
	err := resolveFile(t, `"log": {"transport": ["otlp"]}, "otel": {"protocol": "http"}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "otel.protocol")
	assert.Contains(t, err.Error(), "http/protobuf")
}

func TestValidationHoldsTheEndpointToTheConfiguredProtocol(t *testing.T) {
	// The two protocols address a service differently: HTTP wants a URL, gRPC
	// wants host:port. Each is held to its own form, so a value that would fail
	// at the first export fails here instead, beside the key that caused it.
	for _, protocol := range []string{"http/protobuf", "http/json"} {
		body := `"log": {"transport": ["otlp"]}, "otel": {"endpoint": "localhost:4318", "protocol": ` +
			strconv.Quote(protocol) + `}`
		err := resolveFile(t, body)
		require.ErrorIs(t, err, config.ErrInvalid, protocol)
		assert.Contains(t, err.Error(), "must be an absolute http or https URL", protocol)
	}

	// A scheme on a gRPC address is refused rather than stripped: the exporter
	// would accept it and ignore the scheme, so a user who wrote one has likely
	// mistaken the port as well.
	body := `"log": {"transport": ["otlp"]}, "otel": {"endpoint": "http://localhost:4317", "protocol": "grpc"}`
	err := resolveFile(t, body)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "must be host:port")

	// The HTTP default port is refused for gRPC: switching the protocol without
	// moving the address is the one change that looks applied and sends nothing,
	// because the collector's two protocols are two listeners.
	body = `"log": {"transport": ["otlp"]}, "otel": {"endpoint": "localhost:4318", "protocol": "grpc"}`
	err = resolveFile(t, body)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "is the HTTP port")
	assert.Contains(t, err.Error(), "4317")

	assert.NoError(t, resolveFile(t,
		`"log": {"transport": ["otlp"]}, "otel": {"endpoint": "localhost:4317", "protocol": "grpc"}`))
}

func TestValidationRefusesJSONForTheSignalsTheSDKCannotEncode(t *testing.T) {
	// Only the trace exporter encodes JSON. Metrics and logs would send protobuf
	// to a collector expecting JSON, so the mismatch is refused by name rather
	// than shipped — and the message names the signal that would be wrong.
	err := resolveFile(t,
		`"log": {"transport": ["otlp"]}, "otel": {"protocol": "http/json", "tracing": {"enable": true}}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "http/json")
	assert.Contains(t, err.Error(), "logs")

	// Traces alone are fine: that exporter is the one that implements it.
	assert.NoError(t, resolveFile(t,
		`"log": {"transport": ["console"]}, "otel": {"protocol": "http/json", "tracing": {"enable": true}}`))

	// Metrics are refused, and both offending signals are named in one message.
	err = resolveFile(t,
		`"log": {"transport": ["otlp"]}, "otel": {"protocol": "http/json", "metrics": {"enable": true}}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "logs")
	assert.Contains(t, err.Error(), "metrics")
}

func TestValidationRefusesAPathTheGRPCProtocolCannotFollow(t *testing.T) {
	// A gRPC exporter is addressed by host and port alone, so a per-signal path
	// would be a value nothing reads. The combination is refused by name rather
	// than silently ignored, and only for the signals that are switched on.
	err := resolveFile(t,
		`"log": {"transport": ["otlp"]}, "otel": {"endpoint": "localhost:4317", "protocol": "grpc", `+
			`"tracing": {"enable": true, "path": "/v1/traces"}}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "otel.tracing.path")
	assert.Contains(t, err.Error(), "host and port")

	// The same path is fine over HTTP, which is the protocol that follows it.
	assert.NoError(t, resolveFile(t,
		`"log": {"transport": ["otlp"]}, "otel": {"tracing": {"enable": true, "path": "/v1/traces"}}`))

	// A path on a signal that is off is not flagged: it is a value nothing
	// reads either way, and the grpc check only names paths a signal would use.
	assert.NoError(t, resolveFile(t,
		`"log": {"transport": ["otlp"]}, "otel": {"endpoint": "localhost:4317", "protocol": "grpc", `+
			`"metrics": {"path": "/v1/metrics"}}`))
}

func TestValidationAcceptsHeadersInBothForms(t *testing.T) {
	// A JSON object is what a config file writes, and the specification's own
	// comma-separated string is what a directive resolves to. Both must reach
	// the field the exporters read.
	cfg, err := resolveAndValidate(t, config.Options{
		ConfigFile: writeConfig(t, `{
			"database": {"url": "env:DATABASE_URL"},
			"auth": {"secret_key": "env:AUTH_SECRET_KEY"},
			"log": {"transport": ["otlp"]},
			"otel": {"headers": {"authorization": "Bearer token", "x-tenant": "acme"}}
		}`),
		Environ: baseEnv(),
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"authorization": "Bearer token",
		"x-tenant":      "acme",
	}, cfg.OTEL.Headers, "a JSON object must survive as a map")

	cfg, err = resolveAndValidate(t, config.Options{
		ConfigFile: writeConfig(t, `{
			"database": {"url": "env:DATABASE_URL"},
			"auth": {"secret_key": "env:AUTH_SECRET_KEY"},
			"log": {"transport": ["otlp"]},
			"otel": {"headers": "env:OTEL_HEADERS"}
		}`),
		Environ: append(baseEnv(), "OTEL_HEADERS=authorization=Bearer token,x-tenant=acme"),
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"authorization": "Bearer token",
		"x-tenant":      "acme",
	}, cfg.OTEL.Headers, "a comma-separated directive must become the same map")
}

func TestRedactedHidesHeaderValuesButKeepsTheirNames(t *testing.T) {
	// A header value is a secret: an authorization token is why the key exists,
	// and a value that must not be logged cannot be told from one that may. The
	// names are kept, because a name says which credential is missing without
	// revealing it.
	cfg := config.Default()
	cfg.OTEL.Headers = map[string]string{"authorization": "Bearer super-secret"}

	redacted := cfg.Redacted()
	require.Contains(t, redacted.OTEL.Headers, "authorization")
	assert.NotContains(t, redacted.OTEL.Headers["authorization"], "super-secret")

	masked := cfg.Masked()
	assert.NotContains(t, masked.OTEL.Headers["authorization"], "super-secret")
}

func TestCollectorEndpointServesBothProtocolForms(t *testing.T) {
	// One endpoint is shared by the three signals, and the two protocols address
	// a service differently. The gRPC form is the URL reduced to its host and
	// port, so the same value reaches both exporters.
	cfg := config.Default()
	cfg.OTEL.Endpoint = "http://collector.example.com:4317"

	cfg.OTEL.Protocol = config.OTELProtocolHTTPProtobuf
	assert.Equal(t, "http://collector.example.com:4317", cfg.CollectorEndpoint())
	assert.False(t, cfg.CollectorSecure())

	cfg.OTEL.Protocol = config.OTELProtocolGRPC
	assert.Equal(t, "collector.example.com:4317", cfg.CollectorEndpoint(),
		"a gRPC exporter wants host:port, not the URL")

	// The scheme is what decides TLS, for both protocols: a gRPC address carries
	// none of its own, so an https URL is how a secure connection is asked for.
	cfg.OTEL.Endpoint = "https://collector.example.com:4317"
	assert.Equal(t, "collector.example.com:4317", cfg.CollectorEndpoint())
	assert.True(t, cfg.CollectorSecure())

	// A bare host:port is plaintext, which is what a collector on the same host
	// or the same private network wants.
	cfg.OTEL.Endpoint = "collector.example.com:4317"
	assert.False(t, cfg.CollectorSecure())
}

func TestRedactedLeavesTheLogTargetsAlone(t *testing.T) {
	// Neither the transport list nor the collector address is a credential, and
	// a report that hid them could not say where the logs go.
	cfg := config.Default()
	cfg.Log.Transport = []string{"console", "otlp"}
	cfg.OTEL.Endpoint = "https://collector.example.com:4318"

	redacted := cfg.Redacted()

	assert.Equal(t, cfg.Log.Transport, redacted.Log.Transport)
	assert.Equal(t, cfg.OTEL.Endpoint, redacted.OTEL.Endpoint)
}

func TestRedactedHidesTheS3Credentials(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.S3.AccessKeyID = "AKIAIOSFODNN7EXAMPLE"
	cfg.Storage.S3.AccessKeySecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"

	for name, rendered := range map[string]config.Config{
		"Redacted": cfg.Redacted(),
		"Masked":   cfg.Masked(),
	} {
		assert.NotContains(t, rendered.Storage.S3.AccessKeyID, "AKIAIOSFODNN7EXAMPLE", name)
		assert.NotContains(t, rendered.Storage.S3.AccessKeySecret, "wJalrXUtnFEMI", name)
	}
	assert.NotContains(t, cfg.String(), "wJalrXUtnFEMI")

	// The bucket and the endpoint are not secrets: a report that hid them could
	// not say which bucket it was describing.
	assert.Equal(t, cfg.Storage.S3.BucketName, cfg.Redacted().Storage.S3.BucketName)
	assert.Equal(t, cfg.Storage.S3.EndpointURL, cfg.Redacted().Storage.S3.EndpointURL)
}

func TestValidationAcceptsTheDefaultCORSPolicy(t *testing.T) {
	require.NoError(t, resolveFile(t, ""))
}

func TestValidationAcceptsAWildcardWithoutCredentials(t *testing.T) {
	err := resolveFile(t, `"server": {"cors": {"allowed_origins": ["*"], "allow_credentials": false}}`)
	require.NoError(t, err)
}

func TestValidationRejectsWildcardWithCredentials(t *testing.T) {
	// The CORS specification forbids the combination: a browser refuses the
	// answer, so a run that held it would be quietly closed.
	err := resolveFile(t, `"server": {"cors": {"allowed_origins": ["*"], "allow_credentials": true}}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "server.cors.allow_credentials")
}

func TestValidationRejectsAnOriginThatIsNotOne(t *testing.T) {
	// A path is not part of an origin, so it can never match what a browser
	// sends; accepting it would silently close the policy.
	err := resolveFile(t, `"server": {"cors": {"allowed_origins": ["http://localhost:3000/app"]}}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "server.cors.allowed_origins")
}

func TestValidationRejectsABadHeaderName(t *testing.T) {
	err := resolveFile(t, `"server": {"cors": {"allowed_headers": ["content type"]}}`)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "server.cors.allowed_headers")
}
