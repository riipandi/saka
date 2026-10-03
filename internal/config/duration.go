package config

import (
	"time"
)

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

// normalizeDurations reads every duration key of a layer as seconds when it
// holds a number, so a config file can say 900 rather than "15m".
//
// Only a named key is converted, and only a number: a duration string such as
// "15m" is left for the decoder to parse, and a number under any other key stays
// the type error it is.
func normalizeDurations(keys map[string]any) {
	for _, key := range durationKeys {
		value, ok := keys[key]
		if !ok {
			continue
		}
		seconds, ok := asSeconds(value)
		if !ok {
			continue
		}
		keys[key] = time.Duration(seconds * float64(time.Second))
	}
}

// asSeconds reads a number of seconds from the forms a source may hand over: a
// JSON number arrives as a float64, and a caller building Options.Flags may pass
// any Go numeric type. A string is not a number here, even though the decoder
// would read "900" as one: a quoted value is a duration string, and the unit it
// carries is the one it means.
func asSeconds(value any) (float64, bool) {
	var seconds float64
	switch number := value.(type) {
	case float64:
		seconds = number
	case float32:
		seconds = float64(number)
	case int:
		seconds = float64(number)
	case int32:
		seconds = float64(number)
	case int64:
		seconds = float64(number)
	case uint:
		seconds = float64(number)
	case uint32:
		seconds = float64(number)
	case uint64:
		seconds = float64(number)
	default:
		return 0, false
	}

	// Reject NaN, an infinity, and a value that would overflow the
	// multiplication, so a nonsense number cannot wrap into a duration that
	// looks plausible.
	if seconds != seconds || seconds > maxDurationSeconds || seconds < -maxDurationSeconds {
		return 0, false
	}
	return seconds, true
}

// maxDurationSeconds is the largest number of seconds a time.Duration can hold,
// about 292 years.
const maxDurationSeconds = float64(1<<63-1) / float64(time.Second)
