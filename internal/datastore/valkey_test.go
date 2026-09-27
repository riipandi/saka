package datastore_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/pkg/testutils"
)

func TestValkeyAnswersAPing(t *testing.T) {
	backend := testutils.StartValkeyWithTimeout(t)

	v, err := datastore.NewValkey(t.Context(), datastore.ValkeyOptions{
		URL:             backend.URL,
		ApplicationName: "tango-test",
	})
	require.NoError(t, err)
	defer v.Shutdown(context.Background())

	require.NoError(t, v.Ping(t.Context()))
}

func TestValkeySelectsTheConfiguredDatabase(t *testing.T) {
	backend := testutils.StartValkeyWithTimeout(t)

	// A DB index in the options outranks the one the URL names, which is
	// how a URL written for a shared server is retargeted per feature.
	five, err := datastore.NewValkey(t.Context(), datastore.ValkeyOptions{
		URL: backend.URL + "/0",
		DB:  5,
	})
	require.NoError(t, err)
	defer five.Shutdown(context.Background())

	client := five.Client()
	require.NoError(t, client.Do(t.Context(),
		client.B().Set().Key("isolated").Value("five").Build()).Error())

	// A second client on database 0 must not see the entry: the index the
	// options named is the one the connection selected.
	zero, err := datastore.NewValkey(t.Context(), datastore.ValkeyOptions{URL: backend.URL})
	require.NoError(t, err)
	defer zero.Shutdown(context.Background())

	err = zero.Client().Do(t.Context(),
		zero.Client().B().Get().Key("isolated").Build()).Error()
	assert.True(t, valkey.IsValkeyNil(err),
		"database 0 must not see an entry stored in database 5")
}

func TestValkeyFailsFastOnAnUnreachableServer(t *testing.T) {
	// A port nothing listens on: the ping in the constructor must fail the
	// build, not hand out a client that errors on first use.
	v, err := datastore.NewValkey(t.Context(), datastore.ValkeyOptions{
		URL: "redis://127.0.0.1:1/0",
	})
	require.Error(t, err)
	assert.Nil(t, v, "a failed construction hands out no client")
}

func TestValkeyRejectsAMalformedURL(t *testing.T) {
	v, err := datastore.NewValkey(t.Context(), datastore.ValkeyOptions{URL: "postgres://localhost:5432/tango"})
	require.Error(t, err)
	assert.Nil(t, v)
}

func TestValkeyRequiresAURL(t *testing.T) {
	v, err := datastore.NewValkey(t.Context(), datastore.ValkeyOptions{})
	require.ErrorIs(t, err, datastore.ErrNoValkeyURL)
	assert.Nil(t, v)
}
