package webauthn

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/mailer"
)

// fakeCodeEnqueuer records the code deliveries the service hands over.
type fakeCodeEnqueuer struct {
	mail []CodeEmail
}

func (f *fakeCodeEnqueuer) EnqueueReauthenticationCodeEmail(_ context.Context, mail CodeEmail) error {
	f.mail = append(f.mail, mail)
	return nil
}

// delivery pairs the service with a configured mailer and the recording
// enqueuer — the wiring a deployment with SMTP answers.
func delivery(service *Service) (*Service, *fakeCodeEnqueuer) {
	enqueuer := &fakeCodeEnqueuer{}
	cfg := config.Default()
	cfg.Mailer.SMTPHost = "smtp.example.test"
	cfg.Mailer.SMTPPort = 25
	mail, err := mailer.New(cfg, nil)
	if err != nil {
		panic(err)
	}
	service.WithDelivery(mailer.NewService(mail, nil), enqueuer)
	return service, enqueuer
}

func TestTheEmailCodeProofMintsTheToken(t *testing.T) {
	base, issuer, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, base.pool, "hermione")
	issuer.accounts[userID] = account
	service, enqueuer := delivery(base)

	// The send answers nothing about the account; the enqueuer carries the
	// only clear-text copy.
	require.NoError(t, service.SendReauthenticationCode(t.Context(), userID))
	require.Len(t, enqueuer.mail, 1)
	sent := enqueuer.mail[0]
	assert.Equal(t, account.Email, sent.Email)
	assert.Len(t, sent.Token, reauthCodeLength)

	// The code proves the caller and mints the standard token — the one
	// the guarded call's header spends exactly once.
	token, expiresAt, err := service.Reauthenticate(t.Context(), userID, "", "", "", sent.Token)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.True(t, expiresAt.After(time.Now()))
	require.NoError(t, service.ConsumeReauthentication(t.Context(), stepUpCaller(t, userID), token))
}

func TestAWrongEmailCodeIsNotAProof(t *testing.T) {
	base, issuer, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, base.pool, "hermione")
	issuer.accounts[userID] = account
	service, enqueuer := delivery(base)

	require.NoError(t, service.SendReauthenticationCode(t.Context(), userID))
	sent := enqueuer.mail[0]

	// A code that was never issued earns the one refusal.
	_, _, err := service.Reauthenticate(t.Context(), userID, "", "", "", "NOT-THE-CODE")
	assert.ErrorIs(t, err, ErrProofRefused)

	// The real code still works: a wrong guess spent nothing.
	_, _, err = service.Reauthenticate(t.Context(), userID, "", "", "", sent.Token)
	require.NoError(t, err)

	// But the code itself is single use: the replay is the refusal.
	_, _, err = service.Reauthenticate(t.Context(), userID, "", "", "", sent.Token)
	assert.ErrorIs(t, err, ErrProofRefused)
}

func TestAResendReplacesTheLiveCode(t *testing.T) {
	base, issuer, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, base.pool, "hermione")
	issuer.accounts[userID] = account
	service, enqueuer := delivery(base)

	require.NoError(t, service.SendReauthenticationCode(t.Context(), userID))
	first := enqueuer.mail[0]

	// The send inside the cooldown is refused — the first code stands.
	assert.ErrorIs(t, service.SendReauthenticationCode(t.Context(), userID), ErrResendTooSoon)

	// Past the cooldown the resend replaces: the first code is dead, the
	// second is the only one that proves.
	at := time.Now()
	service.now = func() time.Time { return at.Add(2 * resendCooldown) }
	require.NoError(t, service.SendReauthenticationCode(t.Context(), userID))
	second := enqueuer.mail[1]
	assert.NotEqual(t, first.Token, second.Token)

	_, _, err := service.Reauthenticate(t.Context(), userID, "", "", "", first.Token)
	assert.ErrorIs(t, err, ErrProofRefused)
	_, _, err = service.Reauthenticate(t.Context(), userID, "", "", "", second.Token)
	assert.NoError(t, err)
}

func TestAnUnwiredDeliveryAnswersUnavailable(t *testing.T) {
	// No delivery wired: the code path answers unavailable, and the proofs
	// a caller still holds go on working — the state a bare wiring is in.
	service, issuer, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer.accounts[userID] = account

	assert.ErrorIs(t, service.SendReauthenticationCode(t.Context(), userID), ErrCodeSendUnavailable)

	_, _, err := service.Reauthenticate(t.Context(), userID, "expecto-patronum", "", "", "")
	assert.NoError(t, err)
}
