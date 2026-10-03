package observer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/config"
)

// The resource is asserted directly rather than only through an export, so a
// change to it is caught without a collector. This file is internal because the
// resource builder is an implementation detail no other package needs.
func TestResourceAttributesCarryTheConfiguration(t *testing.T) {
	cfg := config.Default()
	cfg.OTEL.ServiceName = "saka-test"
	cfg.OTEL.Environment = "staging"

	set := newResource(cfg).Set()

	name, ok := set.Value("service.name")
	require.True(t, ok)
	assert.Equal(t, "saka-test", name.AsString())

	environment, ok := set.Value("deployment.environment.name")
	require.True(t, ok)
	assert.Equal(t, "staging", environment.AsString())

	version, ok := set.Value("service.version")
	require.True(t, ok)
	assert.Equal(t, config.AppVersion, version.AsString())
}

func TestAnEmptyEnvironmentAddsNoAttribute(t *testing.T) {
	// An unset deployment environment is not reported as an empty string: the
	// attribute is absent, which is what "not stated" means in a resource.
	cfg := config.Default()
	cfg.OTEL.ServiceName = "saka-test"
	cfg.OTEL.Environment = ""

	set := newResource(cfg).Set()
	_, ok := set.Value("deployment.environment.name")
	assert.False(t, ok)
}
