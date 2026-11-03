package registry

import (
	"errors"
	"testing"
	"uuid"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/modules/notification"
)

// The lazy publisher's two absent-area behaviors (issue-20261014_0730): a
// container without the notification area skips the advisory notice — the
// way its consumer's contract documents — and a container whose area is
// present but under construction answers the construction error the queue's
// retry budget works on. The delivered path is pinned by the
// upload-finished job's own stub tests.

func TestThePublisherSkipsTheNoticeTheAbsentAreaCannotDeliver(t *testing.T) {
	publisher := lazyPublisher{injector: do.New()}

	require.NoError(t, publisher.CreateSystemNotice(
		t.Context(), uuid.NewV7(), "the file is ready", "pictures/robert-langdon.png"))
}

func TestThePublisherAnswersAConstructionFailureTheQueueCanRetry(t *testing.T) {
	injector := do.New(func(i do.Injector) {
		do.Provide(i, func(do.Injector) (*notification.Service, error) {
			return nil, errors.New("the area is under construction")
		})
	})
	publisher := lazyPublisher{injector: injector}

	err := publisher.CreateSystemNotice(
		t.Context(), uuid.NewV7(), "the file is ready", "pictures/robert-langdon.png")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "under construction")
}
