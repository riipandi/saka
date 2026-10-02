package jobs

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/fetcher"
	"github.com/riipandi/tango/internal/queue"
)

// The back-channel logout delivery: the token rides the form member the
// specification names, an empty 200 is the success, and anything else is
// the attempt's failure the queue retries.

func TestBackchannelLogoutDeliversTheFormEncodedToken(t *testing.T) {
	var receivedForm string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedForm = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	client, err := fetcher.New(config.Default(), nil)
	require.NoError(t, err)

	task := BackchannelLogoutTask{ClientID: "client-one", URL: server.URL, Token: "signed.logout.token"}
	require.NoError(t, backchannelLogoutProcessor(t.Context(), task, client))

	assert.Equal(t, "logout_token=signed.logout.token", receivedForm,
		"the delivery is the form the specification names, one member")
}

func TestBackchannelLogoutRefusesANonSuccessAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	client, err := fetcher.New(config.Default(), nil)
	require.NoError(t, err)

	task := BackchannelLogoutTask{ClientID: "client-one", URL: server.URL, Token: "signed"}
	err = backchannelLogoutProcessor(t.Context(), task, client)
	require.Error(t, err, "a 5xx is the attempt's failure; the queue's retry owns the next one")

	// The task's own config keeps the attempts bounded, the timeout below the
	// release window, and its dead tasks replayable.
	config := task.Config()
	assert.Equal(t, queue.QueueConfig{
		Name:        BackchannelLogoutName,
		MaxAttempts: 5,
		Timeout:     30 * time.Second,
		Backoff:     time.Minute,
		Retention:   queue.DeadLetter(),
	}, config)
}

func TestBackchannelLogoutRefusesAnUnwiredClient(t *testing.T) {
	task := BackchannelLogoutTask{ClientID: "c", URL: "https://rp.example", Token: "t"}
	err := backchannelLogoutProcessor(context.Background(), task, nil)
	require.Error(t, err, "a nil fetch client is a wiring miss, not a silent drop")
}
