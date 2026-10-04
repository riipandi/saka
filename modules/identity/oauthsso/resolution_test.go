package oauthsso

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/modules/identity/signin"
)

// stubIssuer is the session mint the resolution tests run against. The
// account read answers from the database the way the real issuer does —
// a JIT-created row must be findable — while the mint answers a canned
// result, because the token signing is the sign-in feature's own test.
type stubIssuer struct {
	pool    *datastore.Postgres
	issued  int
	issueFn func(ctx context.Context, account *signin.Account) (signin.Result, error)
}

func (s *stubIssuer) FindAccountByIDAny(_ context.Context, id uuid.UUID) (*signin.Account, error) {
	var row signin.Account
	err := s.pool.QueryRow(context.Background(),
		`SELECT u.id, coalesce(u.username, ''), u.email, coalesce(u.display_name, ''),
		        u.disabled, coalesce(ar.kind, ''), '', u.email_verified_at
		 FROM public.users u
		 LEFT JOIN public.account_restrictions ar
		        ON ar.user_id = u.id AND ar.lifted_at IS NULL
		       AND (ar.expires_at IS NULL OR ar.expires_at > now())
		 WHERE u.id = $1`, id).
		Scan(&row.ID, &row.Username, &row.Email, &row.DisplayName,
			&row.Disabled, &row.RestrictionKind, &row.PasswordHash, &row.EmailVerifiedAt)
	if errors.Is(err, datastore.ErrNoRows) {
		return nil, datastore.ErrNoRows
	}
	return &row, err
}

func (s *stubIssuer) IssueSession(_ context.Context, _ datastore.Querier, account *signin.Account, provider, event string, _ signin.SessionParams) (signin.Result, error) {
	s.issued++
	if s.issueFn != nil {
		return s.issueFn(context.Background(), account)
	}
	return signin.Result{
		AccessToken: "access-token",
		SessionID:   "sess_test",
		User: signin.User{
			ID:          account.ID.String(),
			Username:    account.Username,
			Email:       account.Email,
			DisplayName: account.DisplayName,
		},
	}, nil
}

// stubGate is the second factor's seam: the accounts it keeps a
// confirmed factor for answer the bridge, everything else the session.
type stubGate struct {
	confirmed map[uuid.UUID]bool
	bridges   int
}

func (g *stubGate) KeepsConfirmedFactor(_ context.Context, id uuid.UUID) (bool, error) {
	return g.confirmed[id], nil
}

func (g *stubGate) GateSignIn(_ context.Context, id uuid.UUID, _ bool) (signin.PendingSignIn, error) {
	g.bridges++
	return signin.PendingSignIn{Token: "bridge-" + id.String(), ExpiresAt: time.Now().Add(5 * time.Minute)}, nil
}

// staticSettings is the policy's runtime source.
type staticSettings struct {
	values map[string]any
}

func (s staticSettings) GetString(_ context.Context, key string) (string, error) {
	if value, ok := s.values[key].(string); ok {
		return value, nil
	}
	return "", errors.New("not set")
}

func (s staticSettings) GetBool(_ context.Context, key string) (bool, error) {
	if value, ok := s.values[key].(bool); ok {
		return value, nil
	}
	return false, errors.New("not set")
}

// stubNotifier records the code the flow sent. The raw value rides the
// stub — the row keeps the hash — so the spend test can read it back.
type stubNotifier struct {
	sent []string
}

func (n *stubNotifier) DeliverSignInCode(_ context.Context, _, _, code string, _ time.Duration) error {
	n.sent = append(n.sent, code)
	return nil
}

// resolutionService is the continue tests' service: the connection and
// flow fixtures of the flow tests, plus the seams the resolution names.
func resolutionService(t *testing.T, pool *datastore.Postgres, mutate ...func(*Service)) *Service {
	t.Helper()
	service := testService(t, pool, nil).WithBaseURL("https://app.hogwarts.example")
	issuer := &stubIssuer{pool: pool}
	service.WithIssuer(issuer)
	gate := &stubGate{confirmed: map[uuid.UUID]bool{}}
	service.WithMFAGate(gate)
	service.WithCodeNotifier(&stubNotifier{})
	for _, change := range mutate {
		change(service)
	}
	return service
}

// settingsFor is the static source the JIT tests arm. The empty map is
// the unreadable source: every read fails, and the doors close.
func settingsFor(values map[string]any) settingsReader {
	return staticSettings{values: values}
}

// resolvedFlow runs one full begin-callback ceremony against the fake
// provider and answers the flow token the SPA would carry.
func resolvedFlow(t *testing.T, service *Service, identity ExternalIdentity) (Flow, string) {
	t.Helper()
	provider := &fakeProvider{identity: identity}
	service.providers.Custom = provider
	params := customParams()
	params.Enabled = true
	created, err := service.Create(t.Context(), params)
	require.NoError(t, err)

	authorizeURL, err := service.Begin(t.Context(), created.Provider)
	require.NoError(t, err)
	flowToken, err := service.Callback(t.Context(), created.Provider, "the-code", stateOf(t, authorizeURL))
	require.NoError(t, err)

	flow, err := service.FlowByToken(t.Context(), flowToken, StageResolved, StageRequireNames)
	require.NoError(t, err)
	return flow, flowToken
}

// seedAccount writes one account row directly and answers its id — the
// fixture the binding and the email-match branches read. The username
// derives from the address's local part, so the fixture's rows never
// collide on the column's unique index.
func seedAccount(t *testing.T, pool *datastore.Postgres, email string, password bool) uuid.UUID {
	t.Helper()
	local := email
	if at := strings.IndexByte(email, '@'); at > 0 {
		local = email[:at]
	}
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(),
		`INSERT INTO public.users (id, username, email, display_name, email_verified_at)
		 VALUES (uuidv7(), $1, $2, $3, now()) RETURNING id`,
		local, email, "Hermione Granger").Scan(&id))
	if password {
		if _, err := pool.Exec(t.Context(),
			`INSERT INTO public.user_passwords (user_id, password_hash) VALUES ($1, 'hash')`, id); err != nil {
			t.Fatalf("seed password: %v", err)
		}
	}
	return id
}

// mustExec runs one fixture write and fails the test on its error.
func mustExec(t *testing.T, pool *datastore.Postgres, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), query, args...); err != nil {
		t.Fatalf("fixture write: %v", err)
	}
}

func TestContinueSignInSignsTheBoundAccountIn(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	flow, flowToken := resolvedFlow(t, service, ExternalIdentity{
		ProviderAccountID: "prov-1",
		Email:             "hermione@hogwarts.example",
		EmailVerified:     true,
		GivenName:         "Hermione",
		FamilyName:        "Granger",
	})

	// The binding predates the flow: the provider identity already
	// rides an account.
	userID := seedAccount(t, pool, "hermione@hogwarts.example", true)
	mustExec(t, pool,
		`INSERT INTO public.oauth_linked_accounts (user_id, connection_id, provider_account_id, email, email_verified)
		 VALUES ($1, $2, 'prov-1', 'hermione@hogwarts.example', true)`, userID, flow.ConnectionID)

	answer, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	require.NoError(t, err)
	require.NotNil(t, answer.Session)
	assert.Equal(t, "hermione@hogwarts.example", answer.Session.User.Email)

	var stage string
	var boundID *uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT stage, user_id FROM public.oauth_flows WHERE id = $1`, flow.ID).Scan(&stage, &boundID))
	assert.Equal(t, string(StageCompleted), stage)
	require.NotNil(t, boundID)
	assert.Equal(t, userID, *boundID)
}

func TestContinueSignInLinksAVerifiedAddressToItsAccount(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	_, flowToken := resolvedFlow(t, service, ExternalIdentity{
		ProviderAccountID: "prov-2",
		Email:             "sophie@hogwarts.example",
		EmailVerified:     true,
	})

	userID := seedAccount(t, pool, "sophie@hogwarts.example", true)

	answer, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	require.NoError(t, err)
	require.NotNil(t, answer.Session)

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.oauth_linked_accounts WHERE user_id = $1 AND provider_account_id = 'prov-2'`, userID).
		Scan(&count))
	assert.Equal(t, 1, count, "the binding the verified address earned is on the table")

	// The linked audit event names the account the binding joined.
	var records int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs WHERE event = 'oauthsso_account_linked'`).Scan(&records))
	assert.Positive(t, records)
}

func TestContinueSignInPausesAnUnverifiedAddressForTheCode(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	flow, flowToken := resolvedFlow(t, service, ExternalIdentity{
		ProviderAccountID: "prov-3",
		Email:             "langdon@hogwarts.example",
		EmailVerified:     false,
	})

	answer, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	require.NoError(t, err)
	assert.Equal(t, StageVerifyEmail, answer.Stage)
	assert.Nil(t, answer.Session)

	var codeHash string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT email_code_hash FROM public.oauth_flows WHERE id = $1`, flow.ID).Scan(&codeHash))
	assert.NotEmpty(t, codeHash)

	// The account the address names stays untouched: nothing binds
	// before the code is spent.
	_, err = service.repo.AccountIDByEmail(t.Context(), pool, "langdon@hogwarts.example")
	assert.ErrorIs(t, err, datastore.ErrNoRows)
}

func TestVerifySignInEmailSpendsTheCodeAndTheFlowCompletes(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	_, flowToken := resolvedFlow(t, service, ExternalIdentity{
		ProviderAccountID: "prov-4",
		Email:             "vetra@hogwarts.example",
		EmailVerified:     false,
		GivenName:         "Vittoria",
		FamilyName:        "Vetra",
	})
	answer, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	require.NoError(t, err)
	require.Equal(t, StageVerifyEmail, answer.Stage)

	notifier := service.codes.(*stubNotifier)
	require.Len(t, notifier.sent, 1)
	rawCode := notifier.sent[0]

	// The address the proven code names has an account: the spend runs
	// the resolution through to the binding and the session.
	seedAccount(t, pool, "vetra@hogwarts.example", true)

	stage, err := service.VerifySignInEmail(t.Context(), flowToken, rawCode)
	require.NoError(t, err)
	assert.Equal(t, StageResolved, stage)

	final, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	require.NoError(t, err)
	require.NotNil(t, final.Session)
	assert.Equal(t, "vetra@hogwarts.example", final.Session.User.Email)

	// The code is single-use: the spent stage answers every later
	// attempt with the unknown-flow answer.
	_, err = service.VerifySignInEmail(t.Context(), flowToken, rawCode)
	assert.ErrorIs(t, err, ErrFlowUnknown)
}

func TestThreeWrongCodesEndTheFlow(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	_, flowToken := resolvedFlow(t, service, ExternalIdentity{
		ProviderAccountID: "prov-5",
		Email:             "griffindor@hogwarts.example",
		EmailVerified:     false,
	})
	_, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	require.NoError(t, err)

	for range 2 {
		_, err = service.VerifySignInEmail(t.Context(), flowToken, "wrongcode01")
		require.ErrorIs(t, err, ErrInvalidCode)
	}
	_, err = service.VerifySignInEmail(t.Context(), flowToken, "wrongcode02")
	require.ErrorIs(t, err, ErrFlowEnded)

	_, err = service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	assert.ErrorIs(t, err, ErrFlowUnknown, "an ended flow answers nothing")
}

func TestContinueSignInCreatesTheJITAccountOnAnOpenMode(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool, func(s *Service) {
		s.WithSettings(settingsFor(map[string]any{
			SettingAccessMode: "open",
		}))
	})
	_, flowToken := resolvedFlow(t, service, ExternalIdentity{
		ProviderAccountID: "prov-6",
		Email:             "neville@hogwarts.example",
		EmailVerified:     true,
		GivenName:         "Neville",
		FamilyName:        "Longbottom",
	})

	answer, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	require.NoError(t, err)
	require.NotNil(t, answer.Session)
	assert.Equal(t, "Neville Longbottom", answer.Session.User.DisplayName)
	assert.NotEmpty(t, answer.Session.User.Username, "the address's local part named the account")

	var verified *time.Time
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT email_verified_at FROM public.users WHERE email = $1`, "neville@hogwarts.example").
		Scan(&verified))
	require.NotNil(t, verified, "a provider-verified address skips the verification gate")

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.oauth_linked_accounts l
		 JOIN public.users u ON u.id = l.user_id WHERE u.email = $1 AND l.provider_account_id = 'prov-6'`,
		"neville@hogwarts.example").Scan(&count))
	assert.Equal(t, 1, count)
}

func TestContinueSignInPausesTheJITAccountForNames(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool, func(s *Service) {
		s.WithSettings(settingsFor(map[string]any{SettingAccessMode: "open"}))
	})
	flow, flowToken := resolvedFlow(t, service, ExternalIdentity{
		ProviderAccountID: "prov-7",
		Email:             "longbottom@hogwarts.example",
		EmailVerified:     true,
	})

	answer, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	require.NoError(t, err)
	assert.Equal(t, StageRequireNames, answer.Stage)

	_, err = service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	require.ErrorIs(t, err, ErrNamesRequired, "the names stage judges their presence")

	_, err = service.ContinueSignIn(t.Context(), ContinueParams{
		FlowToken: flowToken, GivenName: "Neville", FamilyName: "Longbottom",
	})
	require.NoError(t, err)

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.oauth_flows WHERE id = $1 AND stage = 'completed' AND user_id IS NOT NULL`,
		flow.ID).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestContinueSignInRefusesTheJITAccountOnAnInviteMode(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool, func(s *Service) {
		s.WithSettings(settingsFor(map[string]any{SettingAccessMode: "invite"}))
	})
	_, flowToken := resolvedFlow(t, service, ExternalIdentity{
		ProviderAccountID: "prov-8",
		Email:             "voldemort@hogwarts.example",
		EmailVerified:     true,
	})

	_, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	assert.ErrorIs(t, err, ErrSignUpRefused)

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.users WHERE email = $1`, "voldemort@hogwarts.example").Scan(&count))
	assert.Zero(t, count)
}

func TestTheUnreadableSettingsSourceClosesTheJITDoor(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool, func(s *Service) {
		s.WithSettings(settingsFor(nil))
	})
	_, flowToken := resolvedFlow(t, service, ExternalIdentity{
		ProviderAccountID: "prov-9",
		Email:             "riddle@hogwarts.example",
		EmailVerified:     true,
		GivenName:         "Tom",
		FamilyName:        "Riddle",
	})

	_, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	assert.ErrorIs(t, err, ErrSignUpRefused)
}

func TestTheDisabledLinkingKeepsTheVerifiedAddressOut(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool, func(s *Service) {
		s.WithSettings(settingsFor(map[string]any{
			SettingAccessMode:            "open",
			SettingAccountLinkingEnabled: false,
		}))
	})
	_, flowToken := resolvedFlow(t, service, ExternalIdentity{
		ProviderAccountID: "prov-10",
		Email:             "granger@hogwarts.example",
		EmailVerified:     true,
	})
	seedAccount(t, pool, "granger@hogwarts.example", true)

	_, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	assert.ErrorIs(t, err, ErrLinkingDisabled)

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.oauth_linked_accounts`).Scan(&count))
	assert.Zero(t, count)
}

func TestTheMFAForkAnswersTheBridgeNotASession(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	flow, flowToken := resolvedFlow(t, service, ExternalIdentity{
		ProviderAccountID: "prov-11",
		Email:             "potter@hogwarts.example",
		EmailVerified:     true,
	})
	userID := seedAccount(t, pool, "potter@hogwarts.example", true)
	mustExec(t, pool,
		`INSERT INTO public.oauth_linked_accounts (user_id, connection_id, provider_account_id, email, email_verified)
		 VALUES ($1, $2, 'prov-11', 'potter@hogwarts.example', true)`, userID, flow.ConnectionID)

	service.mfa.(*stubGate).confirmed[userID] = true

	answer, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	require.NoError(t, err)
	require.NotNil(t, answer.Bridge)
	assert.Nil(t, answer.Session, "the challenge is the answer, never a session")
	assert.Equal(t, StageCompleted, answer.Stage)
}

func TestTheRacedContinueLosesTheFlowRow(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	flow, flowToken := resolvedFlow(t, service, ExternalIdentity{
		ProviderAccountID: "prov-12",
		Email:             "dumbledore@hogwarts.example",
		EmailVerified:     true,
	})
	userID := seedAccount(t, pool, "dumbledore@hogwarts.example", true)
	mustExec(t, pool,
		`INSERT INTO public.oauth_linked_accounts (user_id, connection_id, provider_account_id, email, email_verified)
		 VALUES ($1, $2, 'prov-12', 'dumbledore@hogwarts.example', true)`, userID, flow.ConnectionID)

	first, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	require.NoError(t, err)
	require.NotNil(t, first.Session)

	_, err = service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	assert.ErrorIs(t, err, ErrFlowUnknown, "a spent flow answers nothing")
}

func TestASeededBindingRidesPastTheEmailGates(t *testing.T) {
	// A binding whose provider email was verified at the source but the
	// row says unverified: the binding branch runs before every email
	// question, so the flow completes with no code and no pause.
	pool := migratedPool(t)
	service := resolutionService(t, pool)
	flow, flowToken := resolvedFlow(t, service, ExternalIdentity{
		ProviderAccountID: "prov-13",
		Email:             "snape@hogwarts.example",
		EmailVerified:     false,
	})
	userID := seedAccount(t, pool, "snape@hogwarts.example", true)
	mustExec(t, pool,
		`INSERT INTO public.oauth_linked_accounts (user_id, connection_id, provider_account_id, email, email_verified)
		 VALUES ($1, $2, 'prov-13', 'snape@hogwarts.example', true)`, userID, flow.ConnectionID)

	answer, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	require.NoError(t, err)
	require.NotNil(t, answer.Session)
}
