package verification

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssuedTokensArePlainHex(t *testing.T) {
	// The token travels in a URL's query string and through whatever client
	// copies it, so the alphabet is the whole contract: lowercase hex only
	// — no base64 dashes or underscores, nothing a mail client or a QR
	// encoder can mangle. The same shape the password-reset token takes.
	hexOnly := regexp.MustCompile(`^[0-9a-f]{64}$`)

	for range 64 {
		raw, err := newToken()
		require.NoError(t, err)
		assert.Regexp(t, hexOnly, raw, "a token must be 64 lowercase hex characters")
	}

	// Two mints never agree: the token is the flow's whole credential, and
	// its defense is the randomness.
	first, err := newToken()
	require.NoError(t, err)
	second, err := newToken()
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
}
