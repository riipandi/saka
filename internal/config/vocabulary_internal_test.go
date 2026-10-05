package config

import (
	"testing"

	"github.com/stretchr/testify/assert"

	fcache "github.com/riipandi/saka/framework/cache"
	flogger "github.com/riipandi/saka/framework/logger"
	fobserver "github.com/riipandi/saka/framework/observer"
)

// The schema's value vocabularies and the framework packages' own typed ones
// must spell the same words, the way the JWT algorithm list and the JWS
// library's do: a value Validate accepts must be a value the engine maps, and
// a name the framework grows must fail here until the schema lists it too.
func TestTheVocabularyAgreesWithTheFramework(t *testing.T) {
	assert.Equal(t, []string{CacheMemory, CacheKV}, fcache.Drivers(),
		"cache.driver values must match the cache package's drivers")
	assert.Equal(t, LogTransports(), flogger.TransportNames(),
		"log.transport values must match the logger package's transports")
	assert.Equal(t, []string{LogDebug, LogInfo, LogWarn, LogError}, flogger.Levels(),
		"log.level values must match the logger package's levels")
	assert.Equal(t, []string{LogPretty, LogStructured}, flogger.Formats(),
		"log.format values must match the logger package's formats")
	assert.Equal(t, OTELSamplers(), fobserver.Samplers(),
		"otel.tracing.sampler values must match the observer package's samplers")
	assert.Equal(t, OTELCompressions(), fobserver.Compressions(),
		"otel.compression values must match the observer package's compressions")
}
