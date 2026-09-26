package config

import (
	"fmt"
	"strings"
	"time"

	"encoding/json/jsontext"
	"encoding/json/v2"
)

// secretKeys are the keys a generated file must not carry a literal value for.
// A secret is a property of the key, not of its default: app.base_url has an
// empty default too, and it is not a secret.
//
// otel.headers is the one key here whose value is a map rather than a string,
// and it is a secret as a whole: an authorization token is the reason the key
// exists, and the names inside are chosen by the user, so there is no per-entry
// key to list. Every value is rendered through the same path (see
// redactHeaders), and a generated file asks for the one variable that carries
// them all.
//
// A test asserts this list is exactly the set of keys Redacted replaces
// (TestRedactedCoversExactlyTheSecretKeys), so the two cannot drift: adding a
// secret to one without the other fails.
var secretKeys = []string{
	"app.secret_key",
	"auth.private_key",
	"auth.public_key",
	"auth.secret_key",
	"database.url",
	"kvstore.url",
	"mailer.smtp_password",
	"otel.headers",
	"storage.s3.access_key_id",
	"storage.s3.access_key_secret",
}

// omittedKeys stay on Config and keep the built-in default, but a generated
// file does not list them. fetcher.user_agent is the product token the binary
// already sends. Writing it into the file would be the place a deployment
// replaces it.
var omittedKeys = []string{
	"fetcher.user_agent",
	// auth.jwt_algorithm is empty in every deployment that configures one
	// signing stack, because the answer is then derived: the key pair's own
	// `alg`, or the HMAC secret's length. It is set only by a deployment that
	// configures both stacks, so listing it would invite a choice that is
	// already made.
	"auth.jwt_algorithm",
}

// envKeys are the keys a generated file writes as an env: directive even though
// the value is not a secret, mapped to the variable each one names.
//
// A deployment sets the runtime mode, the public base URL, the origin the
// browser fetches assets from, and the collector it ships telemetry to, so a
// generated file asks for the variable rather than baking in a value that
// would be wrong there. The variable name is written out instead of derived
// from the key, because the two do not always agree: app.base_url is
// PUBLIC_BASE_URL, the name the origin is known by outside this file, not
// APP_BASE_URL, and app.assets_url is PUBLIC_ASSETS_URL for the same reason —
// the same origin an S3 bucket or a CDN is published at.
//
// A path is deliberately not here. storage.local_path comes from the file alone:
// where an instance writes its files is a property of the deployment image, and a
// variable would let a run disagree with the configuration about it. The file
// sink has no path key at all for the same reason; it writes under that
// directory.
//
// The collector is the other way round, and reads like the kvstore section: a
// deployment is the one that knows whether it has a collector and where it
// listens, so its address is a directive. The endpoint is shared by the three
// signals, so one variable moves all of them together, and each signal keeps its
// own enable switch in the file.
//
// The transport list is a directive for a third reason: it is the one logging
// key a deployment changes per environment, keeping the terminal locally and
// shipping to a collector in production, and a comma-separated value is exactly
// what an environment variable can carry (see listKeys).
//
// A key may also appear in secretKeys, which runs first: secretKeys says the
// value is a secret, and this map says what the variable is called. kvstore.url
// is the case where the two disagree, being VALKEY_URL rather than KVSTORE_URL.
//
// Validate reports a key here only when the variable leaves it unusable. An unset
// APP_MODE falls back to development, an empty base_url is a valid value, an unset
// APP_ASSETS_URL falls back to the built-in /static mount, an unset OTEL_ENDPOINT
// falls back to the collector on the default port, and an unset HOST or PORT falls
// back to the listen address in the defaults, so none of
// them is an error on its own: naming them would report a choice the user made on
// purpose.
var envKeys = map[string]string{
	"app.assets_url":                   "PUBLIC_ASSETS_URL",
	"app.base_url":                     "PUBLIC_BASE_URL",
	"app.mode":                         "APP_MODE",
	"cache.enable":                     "CACHE_ENABLE",
	"kvstore.db":                       "VALKEY_DB",
	"kvstore.enable":                   "VALKEY_ENABLE",
	"kvstore.url":                      "VALKEY_URL",
	"log.transport":                    "LOG_TRANSPORT",
	"mailer.smtp_allow_plaintext_auth": "MAILER_SMTP_ALLOW_PLAINTEXT_AUTH",
	"mailer.smtp_host":                 "MAILER_SMTP_HOST",
	"mailer.smtp_port":                 "MAILER_SMTP_PORT",
	"mailer.smtp_secure":               "MAILER_SMTP_SECURE",
	"mailer.smtp_username":             "MAILER_SMTP_USERNAME",
	"otel.endpoint":                    "OTEL_ENDPOINT",
	"otel.environment":                 "OTEL_ENVIRONMENT",
	"otel.metrics.enable":              "OTEL_METRICS_ENABLE",
	"otel.protocol":                    "OTEL_PROTOCOL",
	"otel.service_name":                "OTEL_SERVICE_NAME",
	"otel.tracing.enable":              "OTEL_TRACING_ENABLE",
	"server.cors.allowed_origins":      "CORS_ALLOWED_ORIGINS",
	"server.host":                      "SERVER_HOST",
	"server.port":                      "SERVER_PORT",
	"storage.s3.bucket_name":           "STORAGE_S3_BUCKET_NAME",
	"storage.s3.endpoint_url":          "STORAGE_S3_ENDPOINT_URL",
	"storage.s3.region":                "STORAGE_S3_REGION",
}

// Sample renders the config file a fresh checkout starts from: every key with its
// built-in default, every secret as an env: directive naming the variable
// key:generate writes for it, and every key in envKeys as a directive naming the
// variable a deployment sets.
//
// Every key is written out rather than only the ones a user is likely to change,
// so the file doubles as the list of what can be configured, except omittedKeys,
// which keep the built-in default by being absent. The output is deterministic:
// keys are sorted, so two runs produce the same bytes.
func Sample() ([]byte, error) {
	flat := DefaultsMap()
	for _, key := range omittedKeys {
		if _, ok := flat[key]; !ok {
			return nil, fmt.Errorf("config: omitted key %s is not part of Config", key)
		}
		delete(flat, key)
	}
	for _, key := range secretKeys {
		if _, ok := flat[key]; !ok {
			return nil, fmt.Errorf("config: secret key %s is not part of Config", key)
		}
		flat[key] = "env:" + EnvName(key)
	}
	for key, name := range envKeys {
		if _, ok := flat[key]; !ok {
			return nil, fmt.Errorf("config: env key %s is not part of Config", key)
		}
		flat[key] = "env:" + name
	}

	out, err := json.Marshal(nest(flat), jsontext.WithIndent("    "), json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("config: render sample: %w", err)
	}
	return append(out, '\n'), nil
}

// nest turns flat dotted keys back into the tree the file is written as. A
// duration is written as a number of seconds, which is the unit the file uses
// everywhere: "15m0s" reads as a Go expression, and 900 reads as a duration.
func nest(flat map[string]any) map[string]any {
	out := make(map[string]any)
	for key, value := range flat {
		parts := strings.Split(key, Delim)
		node := out
		for _, part := range parts[:len(parts)-1] {
			child, ok := node[part].(map[string]any)
			if !ok {
				child = make(map[string]any)
				node[part] = child
			}
			node = child
		}
		node[parts[len(parts)-1]] = renderable(value)
	}
	return out
}

// renderable converts a default into a value JSON can hold. A duration becomes a
// number of seconds, so the file says 900 rather than "15m0s". An exact division
// stays an integer: 900 seconds is written 900, not 900.0.
func renderable(value any) any {
	duration, ok := value.(time.Duration)
	if !ok {
		return value
	}
	seconds := duration.Seconds()
	if seconds == float64(int64(seconds)) {
		return int64(seconds)
	}
	return seconds
}
