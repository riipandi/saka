package jwks

import (
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/jwtutils"
	"github.com/riipandi/tango/pkg/testutils"
)

// TestEndpointPublishesAStoredKeyFromTheDatabase is the whole path: a row in
// public.jwks reaches the document a client fetches, beside the configured
// key, through the same query the server runs.
func TestEndpointPublishesAStoredKeyFromTheDatabase(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	ctx := t.Context()

	stored := storedPublicKey(t, "ES384", "provider-key")
	_, err := pool.Exec(ctx, `
		INSERT INTO public.jwks (key_id, algorithm, key_type, public_key, use_for, is_active, expires_at)
		VALUES ($1, $2, 'EC', $3, 'sig', TRUE, $4)`,
		stored.KeyID, stored.Algorithm, stored.PublicKey, time.Now().Add(time.Hour))
	require.NoError(t, err)

	service := NewService(testConfig(t), NewRepository(pool, testCipher(t)), nil, nil)
	require.NoError(t, service.Err())

	keys := decodeKeys(t, serve(t, service))

	require.Len(t, keys, 1, "the stored one")
	assert.Equal(t, "provider-key", keys[0]["kid"])
	assert.Equal(t, "ES384", keys[0]["alg"])
	assert.Equal(t, KeyUsageSignature, keys[0]["use"])
	assert.NotContains(t, keys[0], "d", "a stored private key must not be published")
}

// TestATokenSignedByAStoredKeyVerifiesAgainstThePublishedSet is the contract
// that makes the endpoint worth serving: the set it publishes is the one
// verification accepts, with no second list to keep in step.
func TestATokenSignedByAStoredKeyVerifiesAgainstThePublishedSet(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	ctx := t.Context()

	// The provider generates a key pair and stores the public half.
	privateEncoded, publicEncoded, err := crypto.GenerateKeyPair("ES384")
	require.NoError(t, err)

	privateKey := parseGenerated(t, privateEncoded, "signer-key")
	_ = parseGenerated(t, publicEncoded, "signer-key")
	encodedPublic, err := json.Marshal(parseGenerated(t, publicEncoded, "signer-key"))
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO public.jwks (key_id, algorithm, key_type, public_key, use_for, is_active)
		VALUES ('signer-key', 'ES384', 'EC', $1, 'sig', TRUE)`, encodedPublic)
	require.NoError(t, err)

	// The token is signed with the private half the provider holds.
	signer, err := jwtutils.NewSigner[struct{}](privateKey, jwa.ES384())
	require.NoError(t, err)
	token, err := signer.WithIssuer("tango").Sign(struct{}{}, jwtutils.Standard{Subject: "usr_1"})
	require.NoError(t, err)

	// The set the endpoint publishes is what a verifier reads.
	service := NewService(testConfig(t), NewRepository(pool, testCipher(t)), nil, nil)
	set, err := service.VerifyKeySet(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, set.Len(), "the stored one")

	// The published key is the public half: the set must not carry the
	// private material the signer holds.
	published, ok := findByKid(t, set, "signer-key")
	require.True(t, ok)
	_, isPrivate := published.(jwk.ECDSAPrivateKey)
	assert.False(t, isPrivate, "the published key must be the public half")

	// A verifier handed the published set accepts the token.
	verifier, err := jwtutils.NewVerifier[struct{}](nil, jwa.ES384())
	require.NoError(t, err)
	verified, err := verifier.WithKeySet(set).WithIssuer("tango").Verify(token)
	require.NoError(t, err, "a token signed by a stored key must verify against the published set")
	assert.Equal(t, "usr_1", verified.Subject)
}

// findByKid returns the key a token would select by its `kid` header.
func findByKid(t *testing.T, set jwk.Set, kid string) (jwk.Key, bool) {
	t.Helper()

	for i := range set.Len() {
		key, ok := set.Key(i)
		require.True(t, ok)
		if id, ok := key.KeyID(); ok && id == kid {
			return key, true
		}
	}
	return nil, false
}

// parseGenerated reads a base64 JWK and stamps the kid it is stored under.
func parseGenerated(t *testing.T, encoded, kid string) jwk.Key {
	t.Helper()

	raw, err := base64Decode(encoded)
	require.NoError(t, err)
	key, err := jwk.ParseKey(raw)
	require.NoError(t, err)
	require.NoError(t, key.Set(jwk.KeyIDKey, kid))
	return key
}
