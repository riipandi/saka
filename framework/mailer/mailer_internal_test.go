package mailer

import (
	"crypto/tls"
	"net"
	"testing"

	"github.com/emersion/go-smtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/pkg/testutils"
)

func TestStartTLSUnsupportedMatchesTheLibraryMessage(t *testing.T) {
	// The library exports no sentinel for "this server has no STARTTLS", so
	// startTLSUnsupported is the message itself. A Mailpit container without TLS
	// answers exactly that, which pins the text: a library change fails this
	// test rather than quietly sending a session in the clear.
	server := testutils.StartMailpit(t.Context(), t)

	conn, err := net.Dial("tcp", server.SMTPAddr)
	require.NoError(t, err)
	defer conn.Close()

	_, err = smtp.NewClientStartTLS(conn, &tls.Config{ServerName: "localhost"})
	require.Error(t, err)
	assert.Equal(t, startTLSUnsupported, err.Error())
	assert.True(t, isStartTLSUnsupported(err))
}

func TestIsStartTLSUnsupportedRejectsOtherFailures(t *testing.T) {
	assert.False(t, isStartTLSUnsupported(nil))
	assert.False(t, isStartTLSUnsupported(errNoAuthMechanism))
}

func TestAuthenticateRefusesAPlaintextSessionToARemoteHost(t *testing.T) {
	// The end-to-end statement for the credential guard: a real plaintext
	// session — Mailpit without TLS, so TLSConnectionState reports plain — with
	// the configuration naming a host that is not this machine. The password is
	// refused, not offered.
	server := testutils.StartMailpit(t.Context(), t)

	conn, err := net.Dial("tcp", server.SMTPAddr)
	require.NoError(t, err)
	defer conn.Close()

	session := smtp.NewClient(conn)
	require.NoError(t, session.Hello("hogwarts.test"))
	_, encrypted := session.TLSConnectionState()
	require.False(t, encrypted, "the container must serve a plaintext session for this test to mean anything")

	// The container is reached over loopback; the options name a remote
	// server, which is the case the guard exists for.
	opts := Options{
		SMTPHost:     "smtp.example.com",
		SMTPPort:     587,
		SMTPUsername: server.Username,
		SMTPPassword: server.Password,
	}

	client, err := New(opts, nil)
	require.NoError(t, err)

	err = client.authenticate(session)
	assert.ErrorIs(t, err, ErrInsecureAuth)

	// The same session, with the opt-in, is allowed through to the server.
	opts.SMTPAllowPlaintextAuth = true
	allowed, err := New(opts, nil)
	require.NoError(t, err)
	assert.NoError(t, allowed.authenticate(session))
}
