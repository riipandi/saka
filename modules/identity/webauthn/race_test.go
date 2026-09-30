package webauthn

// The parallel racers: the ceremonies two callers drive at once. The
// sequential suite pins the refusals; these pin the serialization the
// count-then-write judgements lean on.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/signin"
)

// passwordIssuer models the password join the real issuer runs: a
// passwordless account answers the Any read and refuses the inner-join read.
type passwordIssuer struct {
	accounts     map[uuid.UUID]*signin.Account
	passwordless map[uuid.UUID]bool
	mu           sync.Mutex
	issued       int
}

func (f *passwordIssuer) FindAccountByID(_ context.Context, id uuid.UUID) (*signin.Account, error) {
	if f.passwordless[id] {
		return nil, ErrNoRows
	}
	return f.findAny(id)
}

func (f *passwordIssuer) FindAccountByIDAny(_ context.Context, id uuid.UUID) (*signin.Account, error) {
	return f.findAny(id)
}

func (f *passwordIssuer) findAny(id uuid.UUID) (*signin.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	account, ok := f.accounts[id]
	if !ok {
		return nil, ErrNoRows
	}
	return account, nil
}

func (f *passwordIssuer) VerifyPassword(_ context.Context, id uuid.UUID, _ string) (bool, error) {
	if f.passwordless[id] {
		return false, ErrNoRows
	}
	return true, nil
}

func (f *passwordIssuer) IssueSession(_ context.Context, _ datastore.Querier, account *signin.Account, _, _ string, _ signin.SessionParams) (signin.Result, error) {
	f.mu.Lock()
	f.issued++
	f.mu.Unlock()
	return signin.Result{AccessToken: "at", SessionID: "sess_test", User: signin.User{ID: account.ID.String()}}, nil
}

// TestParallelVerifiesSpendOneCeremony races eight verifies of one handle:
// the consume is the DELETE's WHERE, so exactly one buys the session and
// the rest answer the spent ceremony a replay earns.
func TestParallelVerifiesSpendOneCeremony(t *testing.T) {
	service, _, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer := &passwordIssuer{accounts: map[uuid.UUID]*signin.Account{userID: account}}
	service.issuer = issuer

	soft := NewSoftAuthenticator(false, false, true)
	options, sessionID, err := service.BeginRegistration(t.Context(), userID)
	require.NoError(t, err)
	soft.Create(t, options, testOrigin)
	_, err = service.VerifyRegistration(t.Context(), userID, sessionID, soft.Create(t, options, testOrigin), "key")
	require.NoError(t, err)

	options, sessionID, err = service.BeginLogin(t.Context())
	require.NoError(t, err)
	assertion := soft.Get(t, options, testOrigin, userID[:])

	const racers = 8
	wins := make(chan error, racers)
	var start sync.WaitGroup
	start.Add(1)
	for range racers {
		go func() {
			start.Wait()
			_, err := service.VerifyLogin(t.Context(), sessionID, assertion, signin.SessionParams{})
			wins <- err
		}()
	}
	start.Done()

	winners, losers := 0, 0
	for range racers {
		err := <-wins
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrCeremonyInvalid):
			losers++
		default:
			t.Fatalf("unexpected refusal: %v", err)
		}
	}
	assert.Equal(t, 1, winners, "exactly one verify bought the session")
	assert.Equal(t, racers-1, losers, "the rest answered the spent ceremony")
	assert.Equal(t, 1, issuer.issued, "one session opened")
}

// TestParallelEnrollmentsRespectTheCredentialLimit races two enrollments
// past a limit of one: the limit judgement and the insert share one
// transaction under the account's lock, so the second reads the first's
// committed row and refuses.
func TestParallelEnrollmentsRespectTheCredentialLimit(t *testing.T) {
	values := defaultSettings()
	values[SettingMaxCredentials] = "1"
	service, _, _ := webauthnTestService(t, values)
	userID, account := seedAccount(t, service.pool, "hermione")
	service.issuer = &passwordIssuer{accounts: map[uuid.UUID]*signin.Account{userID: account}}

	type attempt struct {
		session string
		body    string
	}
	attempts := make([]attempt, 0, 2)
	for range 2 {
		soft := NewSoftAuthenticator(false, false, true)
		options, sessionID, err := service.BeginRegistration(t.Context(), userID)
		require.NoError(t, err)
		// The attestation body is built now; only the verify races.
		attempts = append(attempts, attempt{session: sessionID, body: soft.Create(t, options, testOrigin)})
	}

	var start sync.WaitGroup
	start.Add(1)
	results := make(chan error, 2)
	for _, a := range attempts {
		go func() {
			start.Wait()
			_, err := service.VerifyRegistration(t.Context(), userID, a.session, a.body, "")
			results <- err
		}()
	}
	start.Done()

	landed := 0
	for range 2 {
		if err := <-results; err == nil {
			landed++
		}
	}
	rows, err := service.repo.ListCredentials(t.Context(), service.pool, userID)
	require.NoError(t, err)
	assert.Equal(t, 1, len(rows), "a limit of one lands one credential, whatever the racing does")
	assert.Equal(t, 1, landed, "the second verify answered the limit refusal")
}

// TestParallelRemovalsKeepAWayIn races the removal of a passwordless
// account's last two credentials: the stranding judgement and the delete
// share the account's lock, so the second removal reads the first's commit
// and refuses the way in it would have taken.
func TestParallelRemovalsKeepAWayIn(t *testing.T) {
	service, _, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, service.pool, "silas")
	issuer := &passwordIssuer{
		accounts:     map[uuid.UUID]*signin.Account{userID: account},
		passwordless: map[uuid.UUID]bool{userID: true},
	}
	service.issuer = issuer
	_, err := service.pool.Exec(context.Background(),
		`DELETE FROM public.user_passwords WHERE user_id = $1`, userID)
	require.NoError(t, err)

	var wires []string
	for _, name := range []string{"wand-one", "wand-two"} {
		soft := NewSoftAuthenticator(false, false, true)
		options, sessionID, err := service.BeginRegistration(t.Context(), userID)
		require.NoError(t, err)
		body := soft.Create(t, options, testOrigin)
		enrolled, err := service.VerifyRegistration(t.Context(), userID, sessionID, body, name)
		require.NoError(t, err)
		wires = append(wires, enrolled.ID)
	}

	var start sync.WaitGroup
	start.Add(1)
	results := make(chan error, 2)
	for _, wire := range wires {
		go func() {
			start.Wait()
			results <- service.DeleteCredential(t.Context(), userID, wire)
		}()
	}
	start.Done()

	removed := 0
	for range 2 {
		if err := <-results; err == nil {
			removed++
		}
	}
	rows, err := service.repo.ListCredentials(t.Context(), service.pool, userID)
	require.NoError(t, err)
	assert.Equal(t, 1, len(rows), "the passwordless account keeps one credential — its only way in")
	assert.Equal(t, 1, removed, "the second removal answered the stranding refusal")
}
