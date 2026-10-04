package config

// listKeys are the config keys whose value is a list of names, and which a
// config file therefore writes as a JSON array.
//
// A directive is the reason this exists. `"transport": "env:LOG_TRANSPORT"` is
// one string by the time the file layer has resolved it, and a list written as
// one comma-separated string is the only form an environment variable can carry:
// `LOG_TRANSPORT=console,file`. Without this step koanf decodes that string into
// a one-element list holding "console,file", which names no sink.
//
// A test asserts this list is exactly the set of slice-valued fields on Config,
// so adding one fails until it is listed here.
var listKeys = []string{
	"log.transport",
	"oidc.cimd_url_allowlist",
	"server.cors.allowed_headers",
	"server.cors.allowed_methods",
	"server.cors.allowed_origins",
	"server.cors.exposed_headers",
	"server.trusted_proxy_headers",
}

// mapKeys are the config keys whose value is a map of its own, and which a
// config file therefore writes as a JSON object rather than as a section.
//
// The distinction matters at load time. A section is walked into dotted keys
// (otel.tracing.enable), which is how koanf merges one leaf without replacing
// its siblings. A map is one value holding several entries whose names the user
// chooses: walking otel.headers would produce otel.headers.authorization, a key
// that is not part of Config, and FilterKnown would drop it — leaving the header
// silently unset rather than reported.
//
// A test asserts every key here resolves to a map-valued field on Config, so a
// rename cannot leave a key behind that nothing reads.
var mapKeys = []string{
	"otel.headers",
}
