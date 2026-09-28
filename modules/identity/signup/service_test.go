package signup

import (
	"errors"
	"testing"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"connectrpc.com/connect"

	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/jwks"
	"github.com/riipandi/tango/modules/identity/signin"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/modules/identity/usergroup"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/testutils"
)

func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	return testutils.MigratedPostgres(t, "signup_test")
}

func testService(t *testing.T, pool *datastore.Postgres) *Service {
	t.Helper()
	return NewService(pool, nil, nil)
}

// insertToken writes the signup token row a raw value hashes to. The expires
// window must sit ahead of the database clock, the way the table's check
// demands, so a test that needs an expired token moves the service's clock
// instead.
func insertToken(t *testing.T, pool *datastore.Postgres, raw string, usageLimit, usageCount int32) {
	t.Helper()

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(SignupTokenTable)
	ib.Cols("token_hash", "usage_limit", "usage_count", "expires_at")
	ib.Values(crypto.HashHexToken(raw), usageLimit, usageCount, time.Now().Add(time.Hour))

	query, args := ib.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)
}

func tokenUsageCount(t *testing.T, pool *datastore.Postgres, raw string) int32 {
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("usage_count")
	sb.From(SignupTokenTable)
	sb.Where(sb.Equal("token_hash", crypto.HashHexToken(raw)))

	query, args := sb.Build()
	var count int32
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&count))
	return count
}

func userCount(t *testing.T, pool *datastore.Postgres) int {
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From("public.users")

	query, args := sb.Build()
	var count int
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&count))
	return count
}

func TestSignupCreatesTheAccount(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	insertToken(t, pool, "elder-wand", 3, 0)

	user, err := service.Signup(t.Context(), Params{
		Username:  "hermione",
		Email:     "hermione@example.com",
		Password:  "expecto-patronum",
		Token:     "elder-wand",
		FirstName: "Hermione",
		LastName:  "Granger",
	})
	require.NoError(t, err)
	assert.Equal(t, "Hermione Granger", user.DisplayName)

	// The answer is the canonical account view: the names, the creation
	// instant the database stamped, and the unverified state.
	assert.Equal(t, "hermione", user.Username)
	assert.Equal(t, "hermione@example.com", user.Email)
	require.NotNil(t, user.FirstName)
	assert.Equal(t, "Hermione", *user.FirstName)
	require.NotNil(t, user.LastName)
	assert.Equal(t, "Granger", *user.LastName)
	assert.False(t, user.EmailVerified)
	assert.False(t, user.Disabled)
	assert.False(t, user.CreatedAt.IsZero())

	// The account row carries the composed display name and stays
	// unverified: email verification is a later procedure.
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("email", "display_name", "email_verified_at")
	sb.From("public.users")
	sb.Where(sb.Equal("username", "hermione"))
	query, args := sb.Build()
	var email, displayName string
	var verifiedAt *time.Time
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&email, &displayName, &verifiedAt))
	assert.Equal(t, "hermione@example.com", email)
	assert.Nil(t, verifiedAt)

	// The credential is stored as the hash the verifier accepts, never in
	// the clear.
	pb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	pb.Select("password_hash")
	pb.From("public.user_passwords")
	pb.Where(pb.Equal("user_id", rowID(t, user.ID)))
	query, args = pb.Build()
	var passwordHash string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&passwordHash))
	match, err := crypto.NewPasswordHasher().Verify("expecto-patronum", passwordHash)
	require.NoError(t, err)
	assert.True(t, match)

	assert.Equal(t, int32(1), tokenUsageCount(t, pool, "elder-wand"))

	// The password the caller chose signs in immediately.
	signinService := signin.NewService(testConfig(), pool, signin.NewRepository(pool), jwks.NewService(testConfig(), nil, nil, nil), nil, nil)
	result, err := signinService.SignIn(t.Context(), signin.Params{
		Identity: "hermione",
		Password: "expecto-patronum",
	})
	require.NoError(t, err)
	assert.Equal(t, user.ID, result.User.ID)
}

// rowID decodes the wire identifier the views carry into the UUID the rows
// key on.
func rowID(t *testing.T, wire string) string {
	t.Helper()
	id, err := user.ParseID(wire)
	require.NoError(t, err)
	return id.UUID()
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.Auth.PrivateKey = ""
	cfg.Auth.PublicKey = ""
	cfg.Auth.SecretKey = "0123456789abcdeffedcba98765432100123456789abcdeffedcba9876543210"
	return cfg
}

func TestSignupComposesTheDisplayNameFromTheNames(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	insertToken(t, pool, "elder-wand", 1, 0)

	user, err := service.Signup(t.Context(), Params{
		Username:  "hermione",
		Email:     "hermione@example.com",
		Password:  "expecto-patronum",
		Token:     "elder-wand",
		FirstName: "Hermione",
		LastName:  "Granger",
	})
	require.NoError(t, err)
	assert.Equal(t, "Hermione Granger", user.DisplayName)
}

func TestSignupRequiresValidToken(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	// The unknown token shares the failure with the expired and the spent
	// one, so the endpoint does not disclose which half was wrong.
	insertToken(t, pool, "spent-horcrux", 1, 1)
	insertToken(t, pool, "exhausted-horcrux", 2, 2)

	expired := testService(t, pool)
	expired.now = func() time.Time { return time.Now().Add(2 * time.Hour) }

	for name, tc := range map[string]struct {
		service *Service
		token   string
	}{
		"unknown":   {service, "nobody-token"},
		"spent":     {service, "spent-horcrux"},
		"exhausted": {service, "exhausted-horcrux"},
		"expired":   {expired, "exhausted-horcrux"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tc.service.Signup(t.Context(), Params{FirstName: "Hermione", LastName: "Granger",
				Username: "hermione",
				Email:    "hermione@example.com",
				Password: "expecto-patronum",
				Token:    tc.token,
			})
			require.ErrorIs(t, err, ErrInvalidToken)
		})
	}
	assert.Equal(t, 0, userCount(t, pool))
}

func TestSignupConsumesASingleUseTokenOnce(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	insertToken(t, pool, "single-use", 1, 0)

	params := Params{FirstName: "Hermione", LastName: "Granger", Username: "hermione", Email: "hermione@example.com", Password: "expecto-patronum", Token: "single-use"}
	_, err := service.Signup(t.Context(), params)
	require.NoError(t, err)

	_, err = service.Signup(t.Context(), Params{FirstName: "Hermione", LastName: "Granger",
		Username: "langdon",
		Email:    "langdon@example.com",
		Password: "expecto-patronum",
		Token:    "single-use",
	})
	require.ErrorIs(t, err, ErrInvalidToken)

	// The refused attempt wrote nothing: one account, one use.
	assert.Equal(t, 1, userCount(t, pool))
	assert.Equal(t, int32(1), tokenUsageCount(t, pool, "single-use"))
}

func TestSignupRejectsDuplicateAccount(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	insertToken(t, pool, "philosophers-stone", 1, 0)
	for _, token := range []string{"chamber-of-secrets", "prisoner-of-azkaban", "goblet-of-fire"} {
		insertToken(t, pool, token, 1, 0)
	}

	_, err := service.Signup(t.Context(), Params{FirstName: "Hermione", LastName: "Granger",
		Username: "hermione", Email: "hermione@example.com", Password: "expecto-patronum", Token: "philosophers-stone",
	})
	require.NoError(t, err)

	// The email is TEXT matched exactly, the way its unique index is, so a
	// cased variant of a live address is a different address and signs up.
	for name, params := range map[string]Params{
		"same username":  {Username: "hermione", Email: "langdon@example.com", Password: "expecto-patronum", Token: "chamber-of-secrets", FirstName: "Hermione", LastName: "Granger"},
		"cased username": {Username: "HERMIONE", Email: "langdon@example.com", Password: "expecto-patronum", Token: "prisoner-of-azkaban", FirstName: "Hermione", LastName: "Granger"},
		"same email":     {Username: "langdon", Email: "hermione@example.com", Password: "expecto-patronum", Token: "goblet-of-fire", FirstName: "Robert", LastName: "Langdon"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.Signup(t.Context(), params)
			require.ErrorIs(t, err, ErrAccountExists)
		})
	}

	// The refused attempts wrote nothing: one account, no use spent.
	assert.Equal(t, 1, userCount(t, pool))
	assert.Equal(t, int32(0), tokenUsageCount(t, pool, "chamber-of-secrets"))
}

func TestMapErrorCarriesTheConnectCodes(t *testing.T) {
	cases := []struct {
		err  error
		code connect.Code
	}{
		{ErrInvalidToken, connect.CodePermissionDenied},
		{ErrAccountExists, connect.CodeAlreadyExists},
		{ErrTokenNotFound, connect.CodeNotFound},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.code, connect.CodeOf(mapError(tc.err)), "%v", tc.err)
	}
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(mapError(errors.New("boom"))))
}

func TestSignupTokenIssueStoresTheHashAlone(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	created, err := service.CreateSignupToken(t.Context(), CreateTokenParams{TTL: 24 * time.Hour})
	require.NoError(t, err)

	assert.Regexp(t, `^[0-9a-f]{64}$`, created.RawToken, "the raw token is 256 bits of lowercase hex, URL-safe without symbols")
	assert.Equal(t, int32(1), created.Token.UsageLimit, "an unset budget is the single invitation")

	// The row stores the hash alone: the raw value must not be recoverable
	// from anything the database holds.
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("token_hash", "usage_limit", "usage_count", "expires_at")
	sb.From(SignupTokenTable)
	sb.Where(sb.Equal("id", created.Token.ID))
	query, args := sb.Build()
	var tokenHash string
	var usageLimit, usageCount int32
	var expiresAt time.Time
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&tokenHash, &usageLimit, &usageCount, &expiresAt))
	assert.Equal(t, crypto.HashHexToken(created.RawToken), tokenHash)
	assert.NotEqual(t, created.RawToken, tokenHash)
	assert.Equal(t, int32(1), usageLimit)
	assert.Equal(t, int32(0), usageCount)
	assert.WithinDuration(t, time.Now().Add(24*time.Hour), expiresAt, time.Minute)

	// The raw value admits the sign-up it was issued for.
	user, err := service.Signup(t.Context(), Params{FirstName: "Hermione", LastName: "Granger",
		Username: "hermione",
		Email:    "hermione@example.com",
		Password: "expecto-patronum",
		Token:    created.RawToken,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, user.ID)
	assert.Equal(t, int32(1), tokenUsageCount(t, pool, created.RawToken))
}

func TestSignupTokenListAndDelete(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	first, err := service.CreateSignupToken(t.Context(), CreateTokenParams{TTL: 24 * time.Hour})
	require.NoError(t, err)
	second, err := service.CreateSignupToken(t.Context(), CreateTokenParams{TTL: 48 * time.Hour, UsageLimit: 5})
	require.NoError(t, err)

	tokens, pagination, err := service.ListSignupTokens(t.Context(), "", false, 1, 0)
	require.NoError(t, err)
	require.Len(t, tokens, 2)
	assert.Equal(t, second.Token.ID, tokens[0].ID, "the list is newest first")
	assert.Equal(t, first.Token.ID, tokens[1].ID)
	assert.Equal(t, int32(5), tokens[0].UsageLimit)
	require.NotNil(t, pagination.TotalItems)
	assert.Equal(t, 2, *pagination.TotalItems)

	// A page beyond the set answers no items and an unknown range.
	tokens, pagination, err = service.ListSignupTokens(t.Context(), "", false, 2, 10)
	require.NoError(t, err)
	assert.Empty(t, tokens)
	assert.Nil(t, pagination.FirstItemIndex)

	require.NoError(t, service.DeleteSignupToken(t.Context(), first.Token.ID))

	tokens, pagination, err = service.ListSignupTokens(t.Context(), "", false, 1, 0)
	require.NoError(t, err)
	assert.Len(t, tokens, 1)
	require.NotNil(t, pagination.TotalItems)
	assert.Equal(t, 1, *pagination.TotalItems)

	assert.ErrorIs(t, service.DeleteSignupToken(t.Context(), first.Token.ID), ErrTokenNotFound)
	assert.ErrorIs(t, service.DeleteSignupToken(t.Context(), "not-a-uuid"), ErrTokenNotFound)
}

// insertGroup writes one user group row and answers its wire identifier, so
// a token can name it.
func insertGroup(t *testing.T, pool *datastore.Postgres, name, displayName string) string {
	t.Helper()

	id := uuid.NewV7()
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(usergroup.GroupTable)
	ib.Cols("id", "name", "display_name")
	ib.Values(id, name, displayName)

	query, args := ib.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)
	return usergroup.FormatID(id)
}

func TestSignupJoinsTheGroupsTheTokenCarried(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	gryffindor := insertGroup(t, pool, "gryffindor", "Gryffindor")
	hermione := insertGroup(t, pool, "hermione", "Hermione's Circle")

	created, err := service.CreateSignupToken(t.Context(), CreateTokenParams{
		TTL:      24 * time.Hour,
		GroupIDs: []string{gryffindor, hermione},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{gryffindor, hermione}, created.Token.GroupIDs)

	// The issued token answers with the groups it carries.
	tokens, _, err := service.ListSignupTokens(t.Context(), "", false, 1, 0)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	assert.ElementsMatch(t, []string{gryffindor, hermione}, tokens[0].GroupIDs)

	account, err := service.Signup(t.Context(), Params{
		Username:  "lunalovegood",
		Email:     "luna@example.com",
		Password:  "expecto-patronum",
		Token:     created.RawToken,
		FirstName: "Luna",
		LastName:  "Lovegood",
	})
	require.NoError(t, err)

	userID, parseErr := user.UUIDFromWire(account.ID)
	require.NoError(t, parseErr)

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("user_group_id")
	sb.From(usergroup.GroupMemberTable)
	sb.Where(sb.Equal("user_id", userID))
	query, args := sb.Build()
	rows, err := pool.Query(t.Context(), query, args...)
	require.NoError(t, err)
	defer rows.Close()

	joined := []string{}
	for rows.Next() {
		var raw string
		require.NoError(t, rows.Scan(&raw))
		joined = append(joined, usergroup.FormatID(uuid.MustParse(raw)))
	}
	require.NoError(t, rows.Err())
	assert.ElementsMatch(t, []string{gryffindor, hermione}, joined)

	// A token naming a group that does not exist is refused at issue time,
	// not halfway through a sign-up.
	_, err = service.CreateSignupToken(t.Context(), CreateTokenParams{
		TTL:      24 * time.Hour,
		GroupIDs: []string{usergroup.FormatID(uuid.Nil())},
	})
	require.ErrorIs(t, err, ErrGroupNotFound)
}
