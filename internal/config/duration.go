package config

// durationKeys are the config keys whose value is a length of time, and which a
// config file therefore writes as a plain number of seconds.
//
// A bare number is the one form the decoder cannot be trusted with: koanf hands
// it to mapstructure as a float64, mapstructure sets the nanoseconds of a
// time.Duration straight from it, and 900 becomes 900ns. Naming the keys is what
// makes seconds the unit. The alternative, inferring the unit from the Go type,
// cannot work: a duration is an int64, so is an ordinary number, and a hook
// would have to guess at every integer in the configuration.
//
// A test asserts this list is exactly the set of time.Duration fields on Config,
// so adding a duration field fails until it is listed here.
var durationKeys = []string{
	"auth.access_ttl",
	"cache.ttl",
	"database.connect_retry_interval",
	"database.connect_timeout",
	"database.max_conn_idle_time",
	"database.max_conn_lifetime",
	"fetcher.circuit_reset_timeout",
	"fetcher.retry_max_wait",
	"fetcher.retry_wait",
	"fetcher.timeout",
	"log.otlp.timeout",
	"mailer.timeout",
	"otel.metrics.export_timeout",
	"otel.metrics.interval",
	"otel.tracing.batch_timeout",
	"otel.tracing.export_timeout",
	"queue.cleanup_interval",
	"queue.release_after",
	"rate_limit.window",
	"server.cors.max_age",
	"server.idle_timeout",
	"server.read_timeout",
	"server.shutdown_timeout",
	"server.write_timeout",
}
