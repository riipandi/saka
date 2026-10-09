package signup

import (
	"context"
	"errors"
	"testing"
	"time"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"connectrpc.com/connect/v2"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/internal/testutils"
	"github.com/riipandi/saka/modules/identity/blocklist"
	"github.com/riipandi/saka/modules/identity/jwks"
	"github.com/riipandi/saka/modules/identity/signin"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/modules/identity/usergroup"
	"github.com/riipandi/saka/modules/identity/verification"
	"github.com/riipandi/saka/pkg/crypto"
	conttest "github.com/riipandi/saka/pkg/testutils"
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
	ib.InsertInto(entity.TableSignupTokens)
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
	sb.From(entity.TableSignupTokens)
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
	sb.From(entity.TableUsers)

	query, args := sb.Build()
	var count int
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&count))
	return count
}

func TestSignupCreatesTheAccount(t *testing.T) {
	conttest.SkipWithoutDocker(t)

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
	// instant the database stamped. The bare wiring carries no verification
	// gate, so the account signs in at once — the stamped column is the
	// proof.
	assert.Equal(t, "hermione", user.Username)
	assert.Equal(t, "hermione@example.com", user.Email)
	require.NotNil(t, user.FirstName)
	assert.Equal(t, "Hermione", *user.FirstName)
	require.NotNil(t, user.LastName)
	assert.Equal(t, "Granger", *user.LastName)
	assert.True(t, user.EmailVerified)
	assert.False(t, user.Disabled)
	assert.False(t, user.CreatedAt.IsZero())

	// The account row carries the composed display name and the stamp the
	// no-gate policy writes beside it.
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("email", "display_name", "email_verified_at")
	sb.From(entity.TableUsers)
	sb.Where(sb.Equal("username", "hermione"))
	query, args := sb.Build()
	var email, displayName string
	var verifiedAt *time.Time
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&email, &displayName, &verifiedAt))
	assert.Equal(t, "hermione@example.com", email)
	assert.NotNil(t, verifiedAt)

	// The credential is stored as the hash the verifier accepts, never in
	// the clear.
	pb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	pb.Select("password_hash")
	pb.From(entity.TableUserPasswords)
	pb.Where(pb.Equal("user_id", rowID(t, user.ID)))
	query, args = pb.Build()
	var passwordHash string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&passwordHash))
	match, err := crypto.NewPasswordHasher().Verify("expecto-patronum", passwordHash)
	require.NoError(t, err)
	assert.True(t, match)

	assert.Equal(t, int32(1), tokenUsageCount(t, pool, "elder-wand"))

	// The password the caller chose signs in immediately: the bare wiring
	// arms no gate.
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
	cfg.Auth.SecretKey = "0123456789abcdeffedcba98765432100123456789abcdeffedcba9876543210"
	return cfg
}

func TestSignupComposesTheDisplayNameFromTheNames(t *testing.T) {
	conttest.SkipWithoutDocker(t)

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
	conttest.SkipWithoutDocker(t)

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
	conttest.SkipWithoutDocker(t)

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
	conttest.SkipWithoutDocker(t)

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
		{ErrSignupNotAllowed, connect.CodeNotFound},
		{ErrUsernameRequired, connect.CodeInvalidArgument},
		{ErrUsernameInvalid, connect.CodeInvalidArgument},
		{ErrTokenNotFound, connect.CodeNotFound},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.code, connect.CodeOf(mapError(tc.err)), "%v", tc.err)
	}
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(mapError(errors.New("boom"))))
}

// signupSettings is the test's view of the catalog: the keys the sign-up
// reads, answered from a map.
type signupSettings map[string]string

func (s signupSettings) GetString(_ context.Context, key string) (string, error) {
	if value, ok := s[key]; ok {
		return value, nil
	}
	return "", errors.New("unreadable setting")
}

func (s signupSettings) GetBool(_ context.Context, key string) (bool, error) {
	value, ok := s[key]
	if !ok {
		return false, errors.New("unreadable setting")
	}
	return value == "true", nil
}

// recordingVerifier is the seam's test double: it stores the code's row the
// way the real feature does and remembers the raw values for the round trip.
type recordingVerifier struct {
	repo      verificationRepository
	issued    []string
	delivered []string
}

// verificationRepository is the slice of the verification feature's
// repository the double borrows — the same row shape, one dependency less.
type verificationRepository interface {
	UpsertToken(ctx context.Context, db datastore.Querier, userID uuid.UUID, tokenHash string, expiresAt, sentAt time.Time) error
}

func (v *recordingVerifier) IssueForSignup(ctx context.Context, tx datastore.Querier, userID uuid.UUID, _ string, _ string) (string, error) {
	raw := "verhecy-" + uuid.NewV7().String()[:12]
	if err := v.repo.UpsertToken(ctx, tx, userID, crypto.HashHexToken(raw), time.Now().Add(time.Hour), time.Now()); err != nil {
		return "", err
	}
	v.issued = append(v.issued, raw)
	return raw, nil
}

func (v *recordingVerifier) DeliverForSignup(_ context.Context, _ uuid.UUID, _, _, rawToken string) error {
	v.delivered = append(v.delivered, rawToken)
	return nil
}

// openMode is the settings the open, no-toggles sign-up reads.
var openMode = signupSettings{
	"access.mode": "open",
}

func TestOpenModeSignsUpWithoutAToken(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool).WithSignupSettings(openMode)

	account, err := service.Signup(t.Context(), Params{
		Username: "ron",
		Email:    "ron@example.com",
		Password: "expecto-patronum",
	})
	require.NoError(t, err)
	assert.Equal(t, "ron", account.Username)
	assert.True(t, account.EmailVerified, "the no-gate policy stamps the account verified")
}

func TestOpenModeIssuesTheVerificationCode(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	verifier := &recordingVerifier{repo: verification.NewRepository()}
	service := testService(t, pool).
		WithSignupSettings(signupSettings{
			"access.mode":                 "open",
			"auth.verify_email_at_signup": "true",
		}).
		WithVerification(verifier)

	account, err := service.Signup(t.Context(), Params{
		Username: "vittoria",
		Email:    "vittoria@example.com",
		Password: "expecto-patronum",
	})
	require.NoError(t, err)

	// The code was issued in the sign-up's transaction and delivered after
	// the commit.
	require.Len(t, verifier.issued, 1)
	require.Len(t, verifier.delivered, 1)

	// The gate's other side: the column rests empty while the code is
	// outstanding.
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("email_verified_at")
	sb.From(entity.TableUsers)
	sb.Where(sb.Equal("id", rowID(t, account.ID)))
	query, args := sb.Build()
	var verifiedAt *time.Time
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&verifiedAt))
	assert.Nil(t, verifiedAt)
}

func TestInviteModeRefusesAnUnknownTokenCheaply(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool).WithSignupSettings(signupSettings{"access.mode": "invite"})

	// No token at all is the same refusal as an unknown one, and neither
	// creates anything.
	for _, token := range []string{"", "philosophers-stone"} {
		_, err := service.Signup(t.Context(), Params{
			Username: "sophie",
			Email:    "sophie@example.com",
			Password: "expecto-patronum",
			Token:    token,
		})
		assert.ErrorIs(t, err, ErrInvalidToken)
	}
	assert.Equal(t, 0, userCount(t, pool))
}

func TestInviteModeStillSignsUpWithAValidToken(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool).WithSignupSettings(signupSettings{"access.mode": "invite"})
	insertToken(t, pool, "chamber-of-secrets", 1, 0)

	account, err := service.Signup(t.Context(), Params{
		Username: "neville",
		Email:    "neville@example.com",
		Password: "expecto-patronum",
		Token:    "chamber-of-secrets",
	})
	require.NoError(t, err)
	assert.Equal(t, "neville", account.Username)
	assert.Equal(t, int32(1), tokenUsageCount(t, pool, "chamber-of-secrets"))
	_ = account
}

func TestTheAllowlistFiltersOpenSignups(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool).WithSignupSettings(signupSettings{
		"access.mode":              "open",
		"access.allowlist_enabled": "true",
		"access.allowlist":         "sophie@example.com, @hogwarts.example",
	})

	// An unlisted address earns the account-blind refusal.
	_, err := service.Signup(t.Context(), Params{
		Username: "robert",
		Email:    "robert.langdon@elsewhere.example",
		Password: "expecto-patronum",
	})
	assert.ErrorIs(t, err, ErrSignupNotAllowed)

	// A listed address and a domain match both pass.
	for _, attempt := range []Params{
		{Username: "sophie", Email: "SOPHIE@example.com", Password: "expecto-patronum"},
		{Username: "vittoria", Email: "vittoria@hogwarts.example", Password: "expecto-patronum"},
	} {
		_, err := service.Signup(t.Context(), attempt)
		require.NoError(t, err, "%s", attempt.Email)
	}
}

func TestTheTogglesDecideTheUsername(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	// The username identity off: the field may be absent.
	optional := testService(t, pool).WithSignupSettings(signupSettings{"access.mode": "open"})
	account, err := optional.Signup(t.Context(), Params{
		Email:    "sophie@example.com",
		Password: "expecto-patronum",
	})
	require.NoError(t, err)
	assert.Empty(t, account.Username)

	// The username identity on with the require toggle: absence is refused.
	required := testService(t, pool).WithSignupSettings(signupSettings{
		"access.mode":                  "open",
		"auth.signup_username_enabled": "true",
		"auth.require_username":        "true",
	})
	_, err = required.Signup(t.Context(), Params{
		Email:    "robert@example.com",
		Password: "expecto-patronum",
	})
	assert.ErrorIs(t, err, ErrUsernameRequired)

	// Present but ill-shaped: the column's pattern, enforced in the service.
	_, err = required.Signup(t.Context(), Params{
		Username: "no spaces",
		Email:    "vittoria@example.com",
		Password: "expecto-patronum",
	})
	assert.ErrorIs(t, err, ErrUsernameInvalid)

	// Present and well-shaped under the same toggles: accepted.
	_, err = required.Signup(t.Context(), Params{
		Username: "vittoria",
		Email:    "vittoria@example.com",
		Password: "expecto-patronum",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, userCount(t, pool))
}

func TestSignInRefusesAnUnverifiedAccountUntilTheCodeConfirms(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	verifier := &recordingVerifier{repo: verification.NewRepository()}
	service := testService(t, pool).
		WithSignupSettings(signupSettings{
			"access.mode":                 "open",
			"auth.verify_email_at_signup": "true",
		}).
		WithVerification(verifier)

	_, err := service.Signup(t.Context(), Params{
		Username: "sophie",
		Email:    "sophie@example.com",
		Password: "expecto-patronum",
	})
	require.NoError(t, err)
	require.Len(t, verifier.issued, 1)

	// The gate: the password is right, the address is not confirmed yet,
	// and the answer is the distinct refusal — not the credentials' one.
	signinService := signin.NewService(testConfig(), pool, signin.NewRepository(pool), jwks.NewService(testConfig(), nil, nil, nil), nil, nil)
	_, err = signinService.SignIn(t.Context(), signin.Params{
		Identity: "sophie@example.com",
		Password: "expecto-patronum",
	})
	assert.ErrorIs(t, err, signin.ErrEmailUnverified)

	// The code confirming the address is the gate's release: the
	// verification feature stamps the column; here the effect is applied
	// directly, the consumption logic being that feature's own suite.
	_, err = pool.Exec(t.Context(), `UPDATE public.users SET email_verified_at = now() WHERE username = 'sophie'`)
	require.NoError(t, err)

	result, err := signinService.SignIn(t.Context(), signin.Params{
		Identity: "sophie@example.com",
		Password: "expecto-patronum",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result.AccessToken)
}

func TestSignupTokenIssueStoresTheHashAlone(t *testing.T) {
	conttest.SkipWithoutDocker(t)

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
	sb.From(entity.TableSignupTokens)
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
	conttest.SkipWithoutDocker(t)

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
	ib.InsertInto(entity.TableUserGroups)
	ib.Cols("id", "name", "display_name")
	ib.Values(id, name, displayName)

	query, args := ib.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)
	return usergroup.FormatID(id)
}

func TestSignupJoinsTheGroupsTheTokenCarried(t *testing.T) {
	conttest.SkipWithoutDocker(t)

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
	sb.From(entity.TableUserGroupsUsers)
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

// fakeBlocklist is the gate's test double: it answers from a pattern list
// the way the real service does, and its read can fail on demand.
type fakeBlocklist struct {
	patterns []string
	failing  bool
	asked    []string
}

func (f *fakeBlocklist) Blocked(_ context.Context, address string) (bool, error) {
	if f.failing {
		return false, errors.New("the blocklist is unreadable")
	}
	f.asked = append(f.asked, address)
	// The real match, borrowed directly: the helper is pure, so the double
	// answers exactly what the wired gate answers.
	return blocklist.Matches(address, f.patterns), nil
}

func (f *fakeBlocklist) CollisionTaken(_ context.Context, _ string) (bool, error) {
	if f.failing {
		return false, errors.New("the blocklist is unreadable")
	}
	return false, nil
}

// openMode is the catalog the open-mode tests read: no invite token, no
// allowlist, and the blocklist toggle as the test set it.
func openSignup(blocklistOn bool) signupSettings {
	return signupSettings{
		SettingAccessMode:             "open",
		SettingAccessAllowlistEnabled: "false",
		SettingAccessBlocklistEnabled: map[bool]string{true: "true", false: "false"}[blocklistOn],
	}
}

// TestSignupRefusesABlockedAddress pins the blocklist gate: the toggle on,
// a blocked address — and the subaddressed variant of it, which the real
// gate's carry-over answers — is refused with the same failure an
// allowlist refusal and a closed mode carry, so the refusal names nothing.
func TestSignupRefusesABlockedAddress(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	service.WithSignupSettings(openSignup(true))
	gate := &fakeBlocklist{patterns: []string{"john.doe@example.com", "@spam.example"}}
	service.WithBlocklist(gate)

	for _, address := range []string{"john.doe@example.com", "JOHN.DOE@EXAMPLE.COM", "hermione@spam.example"} {
		_, err := service.Signup(t.Context(), Params{
			Username: "rongranger",
			Email:    address,
			Password: "expecto-patronum",
		})
		assert.ErrorIs(t, err, ErrSignupNotAllowed, "%q", address)
	}
	assert.Equal(t, 0, userCount(t, pool), "a refused sign-up created nothing")
}

// TestSignupLetsAnUnblockedAddressPass pins the gate's other side: the
// toggle on and the address unblocked, the sign-up runs to its account.
func TestSignupLetsAnUnblockedAddressPass(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	service.WithSignupSettings(openSignup(true))
	service.WithBlocklist(&fakeBlocklist{patterns: []string{"john.doe@example.com"}})

	_, err := service.Signup(t.Context(), Params{
		Username: "hermione",
		Email:    "hermione@example.com",
		Password: "expecto-patronum",
	})
	require.NoError(t, err)
}

// TestSignupAppliesTheAllowlistOverTheBlocklist pins the lists' precedence:
// an address the allowlist accepts passes even when the blocklist names it,
// so the two lists' one conflict is decided the same way every time.
func TestSignupAppliesTheAllowlistOverTheBlocklist(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	service.WithSignupSettings(signupSettings{
		SettingAccessMode:             "open",
		SettingAccessAllowlistEnabled: "true",
		SettingAccessAllowlist:        "@example.com",
		SettingAccessBlocklistEnabled: "true",
	})
	// The gate would refuse everything it is asked about — the allowlist
	// must answer first for the sign-up to pass at all.
	service.WithBlocklist(&fakeBlocklist{patterns: []string{"hermione@example.com"}})

	_, err := service.Signup(t.Context(), Params{
		Username: "hermione",
		Email:    "hermione@example.com",
		Password: "expecto-patronum",
	})
	require.NoError(t, err)
}

// TestSignupToleratesAnUnreadableBlocklist pins the fail-open stance: a
// broken read refuses nothing — a deployment's own door never locks for a
// breakage.
func TestSignupToleratesAnUnreadableBlocklist(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	service.WithSignupSettings(openSignup(true))
	service.WithBlocklist(&fakeBlocklist{failing: true})

	_, err := service.Signup(t.Context(), Params{
		Username: "hermione",
		Email:    "hermione@example.com",
		Password: "expecto-patronum",
	})
	require.NoError(t, err)
}

// TestSignupWithoutTheBlocklistToggleOrTheSeam pins the off states: the
// toggle off keeps the gate shut even with a gate wired, and a bare wiring
// — no seam at all — keeps the sign-up running.
func TestSignupWithoutTheBlocklistToggleOrTheSeam(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)

	toggleOff := testService(t, pool)
	toggleOff.WithSignupSettings(openSignup(false))
	toggleOff.WithBlocklist(&fakeBlocklist{patterns: []string{"hermione@example.com"}})
	_, err := toggleOff.Signup(t.Context(), Params{
		Username: "hermione",
		Email:    "hermione@example.com",
		Password: "expecto-patronum",
	})
	require.NoError(t, err, "the toggle off, the wired gate is not asked")

	noSeam := testService(t, pool)
	noSeam.WithSignupSettings(openSignup(true))
	_, err = noSeam.Signup(t.Context(), Params{
		Username: "rongranger",
		Email:    "rongranger@example.com",
		Password: "expecto-patronum",
	})
	require.NoError(t, err, "no seam, no gate")
}

// recordingNotices is the strict mode's seam double: it remembers the
// addresses the taken-email notices went to.
type recordingNotices struct{ attempts []string }

func (r *recordingNotices) EnqueueSignupAttemptExistingEmail(_ context.Context, email, _ string) {
	r.attempts = append(r.attempts, email)
}

// TestStrictModeAnswersTheSuccessShapeForATakenEmail pins the strict
// enumeration answer: a sign-up that names a taken email earns the shape a
// fresh sign-up answers with — a minted identifier, the caller's own
// fields, the verification state the gate stamps — and the address on file
// earns the notice instead of a verification code. Nothing is created.
func TestStrictModeAnswersTheSuccessShapeForATakenEmail(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	notices := &recordingNotices{}
	verifier := &recordingVerifier{repo: verification.NewRepository()}
	service := testService(t, pool).
		WithSignupSettings(signupSettings{
			"access.mode":                    "open",
			"auth.verify_email_at_signup":    "true",
			SettingUserEnumerationProtection: "strict",
		}).
		WithVerification(verifier).
		WithExistingEmailNotifier(notices)

	// The honest counterpart first: a fresh sign-up under the same policy.
	honest, err := service.Signup(t.Context(), Params{
		Username: "vittoria",
		Email:    "vittoria@example.com",
		Password: "expecto-patronum",
	})
	require.NoError(t, err)

	// The strict shim: the address vittoria's account already holds, a
	// username nobody holds.
	shim, err := service.Signup(t.Context(), Params{
		Username: "sophie",
		Email:    "vittoria@example.com",
		Password: "expecto-patronum",
	})
	require.NoError(t, err, "strict answers the success shape, not a refusal")

	// The wire shapes agree: the same fields carry the same kinds of
	// values, so a diff between the two answers names no account.
	assert.NotEmpty(t, shim.ID)
	assert.NotEqual(t, honest.ID, shim.ID, "the decoy identifier is minted, not read")
	assert.Equal(t, "vittoria@example.com", shim.Email)
	assert.Equal(t, "sophie", shim.Username)
	assert.Equal(t, honest.EmailVerified, shim.EmailVerified, "the gate stamps both the same way")
	assert.False(t, shim.CreatedAt.IsZero())

	// Nothing was created, no code was spent on the unknown caller (the
	// one issued code is the honest counterpart's), and the address on
	// file learned of the attempt.
	assert.Equal(t, 1, userCount(t, pool))
	require.Len(t, verifier.issued, 1, "the honest sign-up's code is the only one")
	require.Len(t, notices.attempts, 1)
	assert.Equal(t, "vittoria@example.com", notices.attempts[0])
}

// TestStrictModeStillRefusesATakenUsername pins the mode's asymmetry: a
// username is not a verified contact channel, so its refusal stays honest —
// there is nothing to leak past the name the caller typed.
func TestStrictModeStillRefusesATakenUsername(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	notices := &recordingNotices{}
	service := testService(t, pool).
		WithSignupSettings(signupSettings{
			"access.mode":                    "open",
			SettingUserEnumerationProtection: "strict",
		}).
		WithExistingEmailNotifier(notices)
	_, err := service.Signup(t.Context(), Params{
		Username: "vittoria",
		Email:    "vittoria@example.com",
		Password: "expecto-patronum",
	})
	require.NoError(t, err)

	_, err = service.Signup(t.Context(), Params{
		Username: "vittoria",
		Email:    "sophie@example.com",
		Password: "expecto-patronum",
	})
	assert.ErrorIs(t, err, ErrAccountExists)
	assert.Empty(t, notices.attempts, "the notice belongs to the email's owner only")
	assert.Equal(t, 1, userCount(t, pool))
}

// TestBulkModeKeepsTheHonestRefusal pins the default: without strict, a
// taken email answers the refusal it always answered.
func TestBulkModeKeepsTheHonestRefusal(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	notices := &recordingNotices{}
	service := testService(t, pool).
		WithSignupSettings(openMode).
		WithExistingEmailNotifier(notices)
	_, err := service.Signup(t.Context(), Params{
		Username: "vittoria",
		Email:    "vittoria@example.com",
		Password: "expecto-patronum",
	})
	require.NoError(t, err)

	_, err = service.Signup(t.Context(), Params{
		Username: "sophie",
		Email:    "vittoria@example.com",
		Password: "expecto-patronum",
	})
	assert.ErrorIs(t, err, ErrAccountExists)
	assert.Empty(t, notices.attempts)
	assert.Equal(t, 1, userCount(t, pool))
}
