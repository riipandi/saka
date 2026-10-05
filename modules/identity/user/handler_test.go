package user

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/authn"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/guard"
	"github.com/riipandi/saka/internal/transport/middleware"
	"github.com/riipandi/saka/modules/identity/jwks"
	"github.com/riipandi/saka/pkg/jwtutils"
	"github.com/riipandi/saka/pkg/testutils"
)

// testVerifier builds the verifier and the signer over one HMAC secret, so a
// test mints the token the PUT route verifies. The subject is the account the
// caller is, which is what the self rule compares the path against.
func testVerifier(t *testing.T, subject string) (*jwtutils.AccessVerifier, string) {
	t.Helper()

	cfg := config.Default()
	cfg.Auth.SecretKey = "0123456789abcdeffedcba98765432100123456789abcdeffedcba9876543210"
	keys := jwks.NewService(cfg, nil, nil, nil)
	key, err := keys.HMACKey(t.Context())
	require.NoError(t, err)
	algorithm, err := keys.SigningAlgorithm()
	require.NoError(t, err)
	signer, err := jwtutils.NewSigner[jwtutils.AccessClaims](key, algorithm)
	require.NoError(t, err)
	token, err := signer.Sign(jwtutils.AccessClaims{Username: "hermione", Roles: []string{"administrator"}},
		jwtutils.Standard{Issuer: cfg.Auth.Issuer, Subject: subject, IssuedAt: time.Now()})
	require.NoError(t, err)
	return jwtutils.NewAccessVerifier(keys, cfg.Auth.Issuer), token
}

// TestThePictureReadServesTheRESTRoute runs the plain HTTP routes the
// feature claims: the default answers by redirect, a stored picture answers
// its bytes, the write verifies the caller's token before it touches the
// engine, and an unknown identifier answers the envelope's not-found.
func TestThePictureReadServesTheRESTRoute(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testPictureService(t, pool)
	created, err := service.CreateUser(t.Context(), CreateParams{
		Username: "hermione", Email: "hermione@example.com", Password: "expecto-patronum",
		FirstName: "Hermione", LastName: "Granger",
	})
	require.NoError(t, err)
	verifier, token := testVerifier(t, created.ID)

	router := chi.NewRouter()
	// The transport's REST bearer middleware is what protects the write; the
	// test mounts it over the same public route the transport lists, with an
	// authenticator that answers the verifier's claims.
	auth := func(ctx context.Context, req *http.Request) (any, error) {
		bearer, ok := authn.BearerToken(req)
		if !ok {
			return nil, authn.Errorf("authentication required")
		}
		caller, err := verifier.VerifyCaller(ctx, bearer)
		if err != nil {
			return nil, authn.Errorf("invalid or expired token")
		}
		return caller, nil
	}
	router.Use(middleware.RESTBearer(auth, guard.RestRules))
	NewModule(service).Mount(router)

	// An account without a picture answers the bundled default's URL — a
	// relative location, so the client's own host serves it.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users/"+created.ID+"/profile-picture.png", nil))
	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, DefaultPicturePath, rec.Header().Get("Location"))

	// The write without a token is refused before the body is read.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/users/"+created.ID+"/profile-picture", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// The write with the owner's token lands the bytes, and the next read
	// carries them.
	picture := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 1, 2}
	req := httptest.NewRequest(http.MethodPut, "/api/users/"+created.ID+"/profile-picture", bytes.NewReader(picture))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "the profile picture was updated")

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users/"+created.ID+"/profile-picture.png", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "image/png", rec.Header().Get("Content-Type"))
	assert.Equal(t, picture, rec.Body.Bytes())

	// An unknown identifier answers the REST envelope's not-found.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/users/00000000-0000-0000-0000-000000000000/profile-picture.png", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "user not found")
}
