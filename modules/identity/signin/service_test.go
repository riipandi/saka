package signin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"testing"
	"time"
	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jetify.com/typeid"

	"connectrpc.com/connect"

	authnv1 "github.com/riipandi/tango/codegen/proto/go/tango/authn/v1"
	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/jwks"
	"github.com/riipandi/tango/modules/identity/session"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/jwtutils"
	"github.com/riipandi/tango/pkg/testutils"
)

// The HMAC secret a test deployment signs with: 32 bytes of hex, the form
// key:generate writes for HS256. No key pair is configured, which is the
// one-stack deployment.
const testSecretHex = "0123456789abcdeffedcba98765432100123456789abcdeffedcba9876543210"

func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	return testutils.MigratedPostgres(t, "signin_test")
}

// testConfig is the one-stack configuration: the HMAC secret alone signs.
func testConfig() config.Config {
	cfg := config.Default()
	cfg.Auth.SecretKey = testSecretHex
	return cfg
}

// testService builds the service over a real pool and a real key-set service.
func testService(t *testing.T, pool *datastore.Postgres) *Service {
	t.Helper()
	return NewService(testConfig(), pool, NewRepository(pool), jwks.NewService(testConfig(), nil, nil, nil), nil, nil)
}

type accountFixture struct {
	username     string
	email        string
	passwordHash string
	disabled     bool
	bannedAt     *time.Time
	banExpires   *time.Time
}

// createAccount writes a user and its password straight into the tables, the
// fixture the sign-in reads back through the repository.
func createAccount(t *testing.T, pool *datastore.Postgres, username, email, password string, mutate func(*accountFixture)) uuid.UUID {
	t.Helper()

	hash, err := crypto.NewPasswordHasher().Hash(password)
	require.NoError(t, err)

	fixture := accountFixture{username: username, email: email, passwordHash: hash}
	if mutate != nil {
		mutate(&fixture)
	}

	id := uuid.NewV7()
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto("public.users")
	ib.Cols("id", "username", "email", "display_name", "disabled", "banned_at", "ban_expires")
	ib.Values(id, fixture.username, fixture.email, "Hogwarts Student", fixture.disabled, fixture.bannedAt, fixture.banExpires)
	query, args := ib.Build()
	_, err = pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)

	pb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	pb.InsertInto("public.user_passwords")
	pb.Cols("user_id", "password_hash")
	pb.Values(id, fixture.passwordHash)
	query, args = pb.Build()
	_, err = pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)
	return id
}

func TestSignInIssuesTheTokenPair(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	ctx := t.Context()

	const password = "expecto-patronum"
	userID := createAccount(t, pool, "hermione", "hermione@example.com", password, nil)

	result, err := service.SignIn(ctx, Params{
		Identity:  "hermione@example.com",
		Password:  password,
		UserAgent: "signin_test/1",
		IPAddress: "192.0.2.10",
	})
	require.NoError(t, err)

	assert.Equal(t, TokenType, result.TokenType)
	assert.Equal(t, "hermione@example.com", result.User.Email)
	assert.NotEmpty(t, result.RefreshToken)
	assert.Equal(t, int32(testConfig().Auth.AccessTTL.Seconds()), result.AccessExpiresIn)
	assert.Equal(t, int32(testConfig().Auth.RefreshShortTTL.Seconds()), result.RefreshExpiresIn)

	// The session identifier is the typed id, and the refresh token is
	// stored under its hash alone.
	require.Regexp(t, regexp.MustCompile(`^sess_[a-z0-9]{26}$`), result.SessionID)
	sid, err := typeid.Parse[session.SessionID](result.SessionID)
	require.NoError(t, err)
	sum := sha256.Sum256([]byte(result.RefreshToken))

	// The access token verifies against the same material the deployment
	// signs with: the issuer from configuration, the subject the account.
	cfg := testConfig()
	keys := jwks.NewService(cfg, nil, nil, nil)
	key, err := keys.HMACKey(ctx)
	require.NoError(t, err)
	algorithm, err := keys.HMACAlgorithm()
	require.NoError(t, err)
	assert.Equal(t, jwa.HS256(), algorithm)

	verifier, err := jwtutils.NewVerifier[jwtutils.AccessClaims](key, algorithm)
	require.NoError(t, err)
	verified, err := verifier.
		WithIssuer(cfg.Auth.Issuer).
		Verify(result.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, user.FormatID(userID), verified.Subject,
		"the subject is the wire form: the row's UUID never leaves the server")
	assert.Equal(t, result.SessionID, verified.Private.SessionID)
	assert.Equal(t, "hermione@example.com", verified.Private.Email)
	assert.Empty(t, verified.Private.Roles)

	// The refresh token is not stored in the clear, the caller address
	// reached the row, the row lives under the typed id, and the provider
	// names the credential kind.
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("token_hash", "user_agent", "remember", "provider", "host(ip_address)", "id")
	sb.From(session.SessionTable)
	sb.Where(sb.Equal("user_id", userID))
	query, args := sb.Build()
	var tokenHash, userAgent, provider string
	var remember bool
	var ipAddress *string
	var rowID string
	require.NoError(t, pool.QueryRow(ctx, query, args...).Scan(&tokenHash, &userAgent, &remember, &provider, &ipAddress, &rowID))
	assert.Equal(t, hex.EncodeToString(sum[:]), tokenHash, "the row must hold the hash, not the token")
	assert.Equal(t, "signin_test/1", userAgent)
	assert.False(t, remember)
	assert.Equal(t, ProviderPassword, provider)
	require.NotNil(t, ipAddress)
	assert.Equal(t, "192.0.2.10", *ipAddress)
	assert.Equal(t, sid.UUID(), rowID)

	// The account records the sign-in.
	lb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	lb.Select("last_login_at")
	lb.From("public.users")
	lb.Where(lb.Equal("id", userID))
	query, args = lb.Build()
	var lastLogin *time.Time
	require.NoError(t, pool.QueryRow(ctx, query, args...).Scan(&lastLogin))
	assert.NotNil(t, lastLogin)
}

func TestSignInAcceptsUsernameCaseInsensitively(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	createAccount(t, pool, "hermione", "hermione@example.com", "expecto-patronum", nil)

	_, err := service.SignIn(t.Context(), Params{Identity: "HERMIONE", Password: "expecto-patronum"})
	require.NoError(t, err)
}

func TestSignInHidesWhichHalfFailed(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	createAccount(t, pool, "hermione", "hermione@example.com", "expecto-patronum", nil)

	for name, params := range map[string]Params{
		"unknown email":        {Identity: "nobody@example.com", Password: "expecto-patronum"},
		"unknown username":     {Identity: "nobody", Password: "expecto-patronum"},
		"wrong password":       {Identity: "hermione@example.com", Password: "wrong horse"},
		"cased email mismatch": {Identity: "ADA@EXAMPLE.COM", Password: "expecto-patronum"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.SignIn(t.Context(), params)
			// The email is matched exactly, the way its unique index is,
			// so a cased variant of a live address is just unknown.
			require.ErrorIs(t, err, ErrInvalidCredentials)
		})
	}
}

func TestSignInRefusesTheStatesThatCannotSignIn(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	now := time.Now()

	hourAgo := now.Add(-time.Hour)
	minuteAgo := now.Add(-time.Minute)
	hourAhead := now.Add(time.Hour)

	cases := []struct {
		name    string
		mutate  func(*accountFixture)
		wantErr error
	}{
		{
			name:    "disabled",
			mutate:  func(f *accountFixture) { f.disabled = true },
			wantErr: ErrAccountDisabled,
		},
		{
			name:    "banned_forever",
			mutate:  func(f *accountFixture) { f.bannedAt = &hourAgo },
			wantErr: ErrAccountBanned,
		},
		{
			name: "banned_window",
			mutate: func(f *accountFixture) {
				f.bannedAt = &minuteAgo
				f.banExpires = &hourAhead
			},
			wantErr: ErrAccountBanned,
		},
		{
			name: "ban_expired",
			mutate: func(f *accountFixture) {
				f.bannedAt = &hourAgo
				f.banExpires = &minuteAgo
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			createAccount(t, pool, tc.name, tc.name+"@example.com", "expecto-patronum", tc.mutate)

			result, err := service.SignIn(t.Context(), Params{Identity: tc.name, Password: "expecto-patronum"})
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, result.AccessToken)
		})
	}
}

func TestSignInStoresANullAddressWhenNoneIsKnown(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	createAccount(t, pool, "hermione", "hermione@example.com", "expecto-patronum", nil)

	result, err := service.SignIn(t.Context(), Params{Identity: "hermione", Password: "expecto-patronum"})
	require.NoError(t, err)

	sid, err := typeid.Parse[session.SessionID](result.SessionID)
	require.NoError(t, err)

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("ip_address")
	sb.From(session.SessionTable)
	sb.Where(sb.Equal("id", sid.UUID()))
	query, args := sb.Build()
	var ip *string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&ip))
	assert.Nil(t, ip)
}

// TestRememberSelectsTheConfiguredLifetime pins the two windows to the
// configuration keys, not to constants: a deployment that changes
// auth.refresh_short_ttl and auth.refresh_long_ttl changes what remember
// means, and the flag alone decides between them.
func TestRememberSelectsTheConfiguredLifetime(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)

	cfg := testConfig()
	cfg.Auth.RefreshShortTTL = 1 * time.Hour
	cfg.Auth.RefreshLongTTL = 48 * time.Hour
	service := NewService(cfg, pool, NewRepository(pool), jwks.NewService(cfg, nil, nil, nil), nil, nil)

	createAccount(t, pool, "hermione", "hermione@example.com", "expecto-patronum", nil)

	for name, params := range map[string]Params{
		"short window": {Identity: "hermione", Password: "expecto-patronum"},
		"long window":  {Identity: "hermione", Password: "expecto-patronum", Remember: true},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := service.SignIn(t.Context(), params)
			require.NoError(t, err)

			sid, err := typeid.Parse[session.SessionID](result.SessionID)
			require.NoError(t, err)

			sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
			sb.Select("remember", "created_at", "expires_at")
			sb.From(session.SessionTable)
			sb.Where(sb.Equal("id", sid.UUID()))
			query, args := sb.Build()
			var remember bool
			var createdAt, expiresAt time.Time
			require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&remember, &createdAt, &expiresAt))

			lifetime := expiresAt.Sub(createdAt)
			if params.Remember {
				assert.True(t, remember)
				assert.Equal(t, 48*time.Hour, lifetime)
				return
			}
			assert.False(t, remember)
			assert.Equal(t, time.Hour, lifetime)
		})
	}
}

func TestMapErrorCarriesTheConnectCodes(t *testing.T) {
	cases := []struct {
		err  error
		code connect.Code
	}{
		{ErrInvalidCredentials, connect.CodeUnauthenticated},
		{ErrAccountDisabled, connect.CodePermissionDenied},
		{ErrAccountBanned, connect.CodePermissionDenied},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.code, connect.CodeOf(mapError(tc.err)), "%v", tc.err)
	}
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(mapError(errors.New("boom"))))
}

func TestRPCSignInRefusesAnIncompleteCredential(t *testing.T) {
	handler := &rpcHandler{service: nil} // the guard runs before the service
	request := connect.NewRequest(&authnv1.SignInRequest{})
	_, err := handler.SignIn(t.Context(), request)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
