package mailer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsLoopbackHost(t *testing.T) {
	// The guard exists so a local development server works without a
	// certificate; every other host has to be encrypted or opted in.
	cases := map[string]bool{
		"localhost":        true,
		"LOCALHOST":        true,
		"127.0.0.1":        true,
		"127.0.0.53":       true,
		"::1":              true,
		"[::1]":            true,
		"smtp.example.com": false,
		"192.168.1.10":     false,
		"10.0.0.1":         false,
		"":                 false,
	}
	for host, want := range cases {
		assert.Equal(t, want, isLoopbackHost(host), "host %q", host)
	}
}

func TestCredentialIsSafe(t *testing.T) {
	// The guard the stdlib's net/smtp performs inside PlainAuth and go-sasl
	// does not: a password is never offered over an unencrypted connection that
	// leaves this machine.
	//
	// The predicate reads the session's encryption, not the configured intent:
	// mailer.smtp_secure asks for implicit TLS, and whether it happened is the
	// session's answer.
	cases := map[string]struct {
		host       string
		allowPlain bool
		encrypted  bool
		want       bool
	}{
		"remote host over plaintext":  {host: "smtp.example.com", want: false},
		"remote host opted in":        {host: "smtp.example.com", allowPlain: true, want: true},
		"remote host upgraded to tls": {host: "smtp.example.com", encrypted: true, want: true},
		"loopback over plaintext":     {host: "localhost", want: true},
		"loopback ip over plaintext":  {host: "127.0.0.1", want: true},
		"private host over plaintext": {host: "192.168.1.10", want: false},
	}
	for name, tc := range cases {
		var settings Options
		settings.SMTPHost = tc.host
		settings.SMTPAllowPlaintextAuth = tc.allowPlain
		assert.Equal(t, tc.want, credentialIsSafe(settings, tc.encrypted), name)
	}
}

func TestAuthenticateRefusesPlaintextToARemoteHost(t *testing.T) {
	// The guard runs before the mechanism is chosen, so a nil session — which
	// reports as unencrypted — is refused rather than dereferenced. The raw
	// sentinel is what authenticate returns; send is what classifies it.
	opts := Options{
		SMTPHost:     "smtp.example.com",
		SMTPPort:     587,
		SMTPUsername: "bot",
		SMTPPassword: "hunter2",
	}

	client, err := New(opts, nil)
	require.NoError(t, err)

	err = client.authenticate(nil)
	require.ErrorIs(t, err, ErrInsecureAuth)
	assert.NotErrorIs(t, err, errNoAuthMechanism)
}

func TestClassifyKeepsTheInsecureAuthReason(t *testing.T) {
	// A caller matches ErrAuth for "authentication failed" and ErrInsecureAuth
	// for "and the reason is the transport", which is the actionable half: the
	// fix is TLS or the opt-in, not a different password.
	err := classify(OpAuth, "smtp.example.com:587", ErrInsecureAuth)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAuth)
	assert.ErrorIs(t, err, ErrInsecureAuth)
	assert.NotErrorIs(t, err, ErrTemporary)
	assert.NotErrorIs(t, err, ErrRejected)
}

func TestAuthenticateSkipsAnUnconfiguredCredential(t *testing.T) {
	// No username means nothing to authenticate with, so the guard does not
	// even look at the transport.
	client, err := New(Options{}, nil)
	require.NoError(t, err)

	assert.NoError(t, client.authenticate(nil))
}
