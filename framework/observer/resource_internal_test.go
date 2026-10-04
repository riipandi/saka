package observer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The resource is asserted directly rather than only through an export, so a
// change to it is caught without a collector. This file is internal because the
// resource builder is an implementation detail no other package needs.
func TestResourceAttributesCarryTheOptions(t *testing.T) {
	opts := Options{
		ServiceName: "hogwarts-test",
		Version:     "1.2.3",
		Environment: "staging",
	}

	set := newResource(opts).Set()

	name, ok := set.Value("service.name")
	require.True(t, ok)
	assert.Equal(t, "hogwarts-test", name.AsString())

	environment, ok := set.Value("deployment.environment.name")
	require.True(t, ok)
	assert.Equal(t, "staging", environment.AsString())

	version, ok := set.Value("service.version")
	require.True(t, ok)
	assert.Equal(t, opts.Version, version.AsString())
}

func TestAnEmptyEnvironmentAddsNoAttribute(t *testing.T) {
	// An unset deployment environment is not reported as an empty string: the
	// attribute is absent, which is what "not stated" means in a resource.
	opts := Options{ServiceName: "hogwarts-test"}

	set := newResource(opts).Set()
	_, ok := set.Value("deployment.environment.name")
	assert.False(t, ok)
}
