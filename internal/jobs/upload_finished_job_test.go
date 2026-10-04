package jobs

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/storage"
)

// stubPublisher records the notices the processor delivers.
type stubPublisher struct {
	calls []noticeCall
	err   error
}

type noticeCall struct {
	userID uuid.UUID
	title  string
	body   string
}

func (p *stubPublisher) CreateSystemNotice(ctx context.Context, userID uuid.UUID, title, body string) error {
	p.calls = append(p.calls, noticeCall{userID: userID, title: title, body: body})
	return p.err
}

func TestUploadFinishedProcessorTellsTheOwner(t *testing.T) {
	publisher := &stubPublisher{}
	owner := uuid.NewV7()

	err := uploadFinishedProcessor(t.Context(), UploadFinishedTask{
		Key:   "pictures/robert-langdon.png",
		Owner: owner.String(),
		Size:  512,
	}, publisher)
	require.NoError(t, err)
	require.Len(t, publisher.calls, 1)
	assert.Equal(t, owner, publisher.calls[0].userID)
	assert.Contains(t, publisher.calls[0].body, "pictures/robert-langdon.png")
}

func TestUploadFinishedProcessorSkipsWhatItCannotAddress(t *testing.T) {
	publisher := &stubPublisher{}

	// No owner named: the staging caller recorded nobody, so there is
	// nobody to tell — not a failure to retry.
	require.NoError(t, uploadFinishedProcessor(t.Context(), UploadFinishedTask{Key: "a.png"}, publisher))

	// A malformed owner is skipped the same way: the metadata is the
	// staging caller's own record, and a notice must not dead-letter over
	// a value no account answers.
	require.NoError(t, uploadFinishedProcessor(t.Context(), UploadFinishedTask{
		Key: "a.png", Owner: "not-a-uuid",
	}, publisher))
	assert.Empty(t, publisher.calls)

	// A publisher that refuses is the failure the queue retries: the
	// notice is advisory, but a retry-able outage is what the attempts
	// budget is for.
	failing := &stubPublisher{err: errors.New("notification area is down")}
	err := uploadFinishedProcessor(t.Context(), UploadFinishedTask{
		Key: "a.png", Owner: uuid.NewV7().String(),
	}, failing)
	require.Error(t, err)
}

func TestTheUploadFinishedEnqueuerNamesTheOwner(t *testing.T) {
	enqueuer := NewUploadFinishedEnqueuer(nil, nil)

	// A nil client is the state a queue-less container is in: the hook
	// answers without enqueueing, the file still stored.
	manifest := storage.Manifest{
		Key:      "pictures/robert-langdon.png",
		Status:   "ready",
		Size:     512,
		Metadata: map[string]any{"owner": uuid.NewV7().String()},
	}
	require.NoError(t, enqueuer.Uploaded(t.Context(), manifest))

	// A manifest that names no owner produces no task: there is nobody to
	// address, and an ownerless file's poll still answers from the
	// manifest.
	manifest.Metadata = nil
	require.NoError(t, enqueuer.Uploaded(t.Context(), manifest))
}
