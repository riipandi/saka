package registry_test

import (
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
)

// shutdownSpy records whether the container shut it down.
type shutdownSpy struct{ called bool }

func (s *shutdownSpy) Shutdown() { s.called = true }

// TestTheContainerShutsDownAServiceItBuilt pins the interface the composition
// root relies on: a service whose lifecycle is owned by the container must
// implement Shutdown, because samber/do never calls Close.
func TestTheContainerShutsDownAServiceItBuilt(t *testing.T) {
	spy := &shutdownSpy{}
	injector := do.New()
	do.ProvideValue(injector, spy)

	report := injector.Shutdown()

	assert.True(t, report.Succeed)
	assert.True(t, spy.called, "the container must call Shutdown on a service it built")
}
