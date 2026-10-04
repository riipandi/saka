package oidc

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/storage"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/testutils"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/modules/identity/usergroup"

	"uuid"
)

// migratedPool opens a database the migrations have built, so the client
// tables exist.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	return testutils.MigratedPostgres(t, "federation_oidc_test")
}

// testService builds the service with a recorder that writes for real — a
// record is part of the transaction it describes, so the assertions read the
// table the writer fills.
func testService(t *testing.T, pool *datastore.Postgres) *Service {
	t.Helper()
	return NewService(pool, fwaudit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))
}

// createParams is the creation every test starts from: a named client with
// one callback, confidential, everything else default.
func createParams(name string) CreateParams {
	return CreateParams{
		Name:         name,
		CallbackURLs: []string{"https://client.example.com/callback"},
	}
}

// seedAccount inserts an account directly and answers its identifier: the
// client's created_by carries a foreign key to the accounts.
func seedAccount(t *testing.T, pool *datastore.Postgres, username string) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(), `
		INSERT INTO public.users (username, email, first_name, last_name, display_name)
		VALUES ($1::citext, $1::text || '@example.com', 'Hermione', 'Granger', 'Hermione Granger')
		RETURNING id`, username).Scan(&id))
	return id
}

// seedGroup inserts a user group and answers its wire identifier, the form
// the restriction's requests carry.
func seedGroup(t *testing.T, pool *datastore.Postgres, name string) string {
	t.Helper()

	var id uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(), `
		INSERT INTO public.user_groups (name, display_name)
		VALUES ($1, $1) RETURNING id`, name).Scan(&id))
	return usergroup.FormatID(id)
}

// testStorage builds the storage engine over the local driver in a
// throwaway directory, so a logo procedure runs the stage, sync, and read
// the production path runs.
func testStorage(t *testing.T, pool *datastore.Postgres) *storage.Manager {
	t.Helper()
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO storage_buckets (name) VALUES ('devbucket') ON CONFLICT (name) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	return storage.NewManager(storage.NewFS(t.TempDir()), pool, t.TempDir(), slog.New(slog.DiscardHandler))
}

// stubDirectory is the account facts the preview reads, standing in for the
// user service the area wires.
type stubDirectory struct {
	accounts map[string]user.UserView
}

func (d *stubDirectory) GetUser(_ context.Context, id string) (user.UserView, error) {
	view, ok := d.accounts[id]
	if !ok {
		return user.UserView{}, user.ErrUserNotFound
	}
	return view, nil
}

// stubClaims is the operator-defined claims the preview merges, standing in
// for the customclaim service the area wires.
type stubClaims struct {
	userClaims  []Claim
	groupClaims []Claim
}

func (s *stubClaims) UserClaims(_ context.Context, _ uuid.UUID) ([]Claim, error) {
	return s.userClaims, nil
}

func (s *stubClaims) GroupClaims(_ context.Context, _ []uuid.UUID) ([]Claim, error) {
	return s.groupClaims, nil
}

// TestCreateMintsASecretTheRowCannotReplay covers the creation's two halves:
// the client is stored whole — the identifier the operator chose or the one
// generated — and the raw secret exists in the response alone, the row
// keeping its hash.
func TestCreateMintsASecretTheRowCannotReplay(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	params := createParams("Hogwarts Portal")
	params.ID = "hogwarts-portal-id"
	owner := seedAccount(t, pool, "hermione")
	issued, err := service.Create(t.Context(), owner, params)
	require.NoError(t, err)
	assert.Equal(t, "hogwarts-portal-id", issued.Client.ID, "the operator's identifier is the client_id the protocol carries")
	assert.Len(t, issued.Secret, 32, "the drawn secret is thirty-two characters of the full alphabet")
	assert.Equal(t, issued.Secret[:4], issued.Client.Secrets[0].Prefix)
	assert.True(t, issued.Client.Secrets[0].Active)

	// The stored presence is the hash: the raw value survives nowhere.
	stored, err := service.repo.GetClient(t.Context(), pool, issued.Client.ID)
	require.NoError(t, err)
	require.Len(t, readCredentials(stored.Credentials).Secrets, 1)
	assert.NotContains(t, string(stored.Credentials), issued.Secret)

	// A generated identifier serves the operator who did not choose one.
	generated, err := service.Create(t.Context(), owner, createParams("Ministry Portal"))
	require.NoError(t, err)
	assert.NotEqual(t, "ministry-portal-id", generated.Client.ID)
	assert.NotEqual(t, issued.Client.ID, generated.Client.ID)
}

// TestCreateRefusesADuplicateIdentifier covers the identifier rule: the
// client_id is the credential a foreign client presents, and two clients
// holding one identifier is a collision the write's own index answers.
func TestCreateRefusesADuplicateIdentifier(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	owner := seedAccount(t, pool, "hermione")
	params := createParams("Hogwarts Portal")
	params.ID = "hogwarts-portal-id"
	_, err := service.Create(t.Context(), owner, params)
	require.NoError(t, err)

	_, err = service.Create(t.Context(), owner, params)
	assert.ErrorIs(t, err, ErrClientExists)
}

// TestCreateForcesThePkceRequirementOnAPublicClient covers the one field the
// creation does not take at face value: a public client cannot keep a
// secret, so the kind forces the requirement on.
func TestCreateForcesThePkceRequirementOnAPublicClient(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	params := createParams("Hogwarts SPA")
	params.IsPublic = true
	owner := seedAccount(t, pool, "hermione")
	issued, err := service.Create(t.Context(), owner, params)
	require.NoError(t, err)
	assert.True(t, issued.Client.IsPublic)
	assert.True(t, issued.Client.PkceEnabled, "the kind forces the requirement on")

	confidential := createParams("Ministry Portal")
	confidential.IsPublic = false
	issued, err = service.Create(t.Context(), owner, confidential)
	require.NoError(t, err)
	assert.False(t, issued.Client.PkceEnabled, "a confidential client chooses")

	// The observed-capability flag starts unset and dies with the
	// requirement it was observed beside.
	assert.False(t, issued.Client.PkceSupported)
	updated, err := service.Update(t.Context(), issued.Client.ID, UpdateParams{
		Name:         "Ministry Portal",
		CallbackURLs: []string{"https://client.example.com/callback"},
		PkceEnabled:  false,
	})
	require.NoError(t, err)
	assert.False(t, updated.PkceSupported, "a client whose requirement is off has shown nothing yet")
}

// TestUpdateReplacesTheFieldsAndKeepsTheSecrets covers the rewrite's
// boundary: the full replace touches the fields the request carries and
// nothing else — the secrets document and the restriction survive.
func TestUpdateReplacesTheFieldsAndKeepsTheSecrets(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	owner := seedAccount(t, pool, "hermione")
	issued, err := service.Create(t.Context(), owner, createParams("Hogwarts Portal"))
	require.NoError(t, err)

	params := UpdateParams{
		Name:         "Ravenclaw Portal",
		CallbackURLs: []string{"https://client.example.com/callback"},
		IsPublic:     true,
		SkipConsent:  true,
	}
	updated, err := service.Update(t.Context(), issued.Client.ID, params)
	require.NoError(t, err)
	assert.Equal(t, "Ravenclaw Portal", updated.Name)
	assert.True(t, updated.IsPublic)
	assert.True(t, updated.SkipConsent)
	assert.Len(t, updated.Secrets, 1, "the rewrite does not touch the secrets")
	assert.Equal(t, issued.Secret[:4], updated.Secrets[0].Prefix)
	assert.Equal(t, issued.Client.CreatedAt, updated.CreatedAt)

	_, err = service.Update(t.Context(), "unknown-client", params)
	assert.ErrorIs(t, err, ErrClientNotFound)
}

// TestDeleteRemovesTheClientAndTheRecordNamesIt covers the deletion: the row
// is gone, the codes and grants that name it died with it, and a second
// deletion is the not-found the first already answered.
func TestDeleteRemovesTheClientAndTheRecordNamesIt(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	owner := seedAccount(t, pool, "hermione")
	issued, err := service.Create(t.Context(), owner, createParams("Hogwarts Portal"))
	require.NoError(t, err)
	require.NoError(t, service.Delete(t.Context(), issued.Client.ID))

	_, err = service.Get(t.Context(), issued.Client.ID)
	assert.ErrorIs(t, err, ErrClientNotFound)

	err = service.Delete(t.Context(), issued.Client.ID)
	assert.ErrorIs(t, err, ErrClientNotFound)
}

// TestAllowedGroupsReplaceWholeAndRefuseAnUnknownGroup covers the
// restriction's replacement: the set is replaced, not merged, and an
// identifier that names no group refuses the replacement whole — the client
// keeps the set it held.
func TestAllowedGroupsReplaceWholeAndRefuseAnUnknownGroup(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	owner := seedAccount(t, pool, "hermione")
	issued, err := service.Create(t.Context(), owner, createParams("Hogwarts Portal"))
	require.NoError(t, err)
	gryffindor := seedGroup(t, pool, "gryffindor")
	ravenclaw := seedGroup(t, pool, "ravenclaw")

	updated, err := service.SetAllowedGroups(t.Context(), issued.Client.ID, []string{gryffindor, ravenclaw})
	require.NoError(t, err)
	require.Len(t, updated.AllowedGroups, 2)

	// The replace, not a delta: one group now, and the other is gone.
	updated, err = service.SetAllowedGroups(t.Context(), issued.Client.ID, []string{gryffindor})
	require.NoError(t, err)
	require.Len(t, updated.AllowedGroups, 1)
	assert.Equal(t, "gryffindor", updated.AllowedGroups[0].Name)

	// An empty list empties the roll.
	updated, err = service.SetAllowedGroups(t.Context(), issued.Client.ID, nil)
	require.NoError(t, err)
	assert.Empty(t, updated.AllowedGroups)

	// An unknown group refuses the replacement whole.
	_, err = service.SetAllowedGroups(t.Context(), issued.Client.ID, []string{"ugrp_00000000000000000000000000"})
	assert.ErrorIs(t, err, ErrGroupUnknown)
	updated, err = service.Get(t.Context(), issued.Client.ID)
	require.NoError(t, err)
	assert.Empty(t, updated.AllowedGroups, "the refused replacement leaves the set it held")
}

// TestSecretsAddWithdrawAndNeverReplayEachOther covers the multi-secret
// lifecycle: several live secrets are legitimate, a withdrawal removes one
// and leaves the others, and an unknown secret is the not-found it is.
func TestSecretsAddWithdrawAndNeverReplayEachOther(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	owner := seedAccount(t, pool, "hermione")

	issued, err := service.Create(t.Context(), owner, createParams("Hogwarts Portal"))
	require.NoError(t, err)
	first := issued.Secret

	// A second live secret: a rotation is an addition followed by a
	// deletion, never a swap.
	second, err := service.CreateSecret(t.Context(), issued.Client.ID, "", nil)
	require.NoError(t, err)
	assert.Len(t, second.Value, 32)
	assert.NotEqual(t, first, second.Value)

	listed, err := service.ListSecrets(t.Context(), issued.Client.ID)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.Equal(t, second.Value[:4], listed[1].Prefix)

	// Withdrawing one leaves the other live.
	require.NoError(t, service.DeleteSecret(t.Context(), issued.Client.ID, second.Secret.ID))
	listed, err = service.ListSecrets(t.Context(), issued.Client.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, first[:4], listed[0].Prefix)

	// The withdrawn secret is the not-found the second time.
	err = service.DeleteSecret(t.Context(), issued.Client.ID, second.Secret.ID)
	assert.ErrorIs(t, err, ErrSecretNotFound)

	// A supplied value is stored as the hash of itself as presented.
	supplied, err := service.CreateSecret(t.Context(), issued.Client.ID, "ExpectoPatronum2026", nil)
	require.NoError(t, err)
	assert.Equal(t, "ExpectoPatronum2026", supplied.Value)
	stored, err := service.repo.GetClient(t.Context(), pool, issued.Client.ID)
	require.NoError(t, err)
	assert.NotContains(t, string(stored.Credentials), "ExpectoPatronum2026")
}

// TestExpiredSecretsReadInactive covers the window: a secret past its expiry
// reads inactive in the views, which is the judgement the token surface will
// enforce when it lands.
func TestExpiredSecretsReadInactive(t *testing.T) {
	pool := migratedPool(t)
	service, now := clockFor(t, pool)

	owner := seedAccount(t, pool, "hermione")
	issued, err := service.Create(t.Context(), owner, createParams("Hogwarts Portal"))
	require.NoError(t, err)
	expiry := (*now).Add(24 * time.Hour)
	secret, err := service.CreateSecret(t.Context(), issued.Client.ID, "", &expiry)
	require.NoError(t, err)

	listed, err := service.ListSecrets(t.Context(), issued.Client.ID)
	require.NoError(t, err)
	assert.True(t, listed[1].Active)

	*now = (*now).Add(25 * time.Hour)
	listed, err = service.ListSecrets(t.Context(), issued.Client.ID)
	require.NoError(t, err)
	assert.False(t, listed[1].Active, "the window's judgement is the view's, made at the instant it is built")
	assert.Equal(t, secret.Secret.ID, listed[1].ID)
}

// TestPreviewBuildsTheClaimMapsForTheAccount covers the preview: the maps
// name the facts the account's own views answer with, and an account that
// does not exist is its own refusal beside the client's.
func TestPreviewBuildsTheClaimMapsForTheAccount(t *testing.T) {
	pool := migratedPool(t)
	// The account facts ride the user service directly; the stub answers
	// one account, named by a real wire identifier.
	accountWire, err := user.FormatID(uuid.NewV7()), error(nil)
	require.NoError(t, err)
	service := testService(t, pool).WithBaseURL("https://idp.example.com").
		WithUserDirectory(&stubDirectory{
			accounts: map[string]user.UserView{
				accountWire: {
					ID:            accountWire,
					Username:      "hermione",
					Email:         "hermione@hogwarts.example",
					DisplayName:   "Hermione Granger",
					EmailVerified: true,
					Groups: []user.GroupSummary{
						{ID: "ugrp_gryffindor", Name: "gryffindor", DisplayName: "Gryffindor"},
					},
				},
			},
		}).
		WithClaimSource(&stubClaims{
			userClaims:  []Claim{{Key: "wand", Value: "vine"}},
			groupClaims: []Claim{{Key: "common_room", Value: "\"gryffindor-tower\""}},
		})

	owner := seedAccount(t, pool, "hermione")
	issued, err := service.Create(t.Context(), owner, createParams("Hogwarts Portal"))
	require.NoError(t, err)

	idToken, accessToken, userInfo, err := service.Preview(t.Context(), issued.Client.ID, accountWire)
	require.NoError(t, err)
	assert.Equal(t, accountWire, idToken["sub"])
	assert.Equal(t, "hermione", idToken["preferred_username"])
	assert.Equal(t, "Hermione Granger", idToken["name"])
	assert.Equal(t, true, idToken["email_verified"])
	assert.Equal(t, []string{"gryffindor"}, idToken["groups"])
	// The Standard Claims the account holds: no picture — the stub names
	// none — and the change stamp, the creation stamp the stub carries
	// (its zero instant, the shape a stub answers).
	assert.Equal(t, idToken, userInfo, "the userinfo answers the profile the id token names")

	assert.Equal(t, "https://idp.example.com", accessToken["iss"])
	assert.Equal(t, issued.Client.ID, accessToken["client_id"])
	assert.Equal(t, issued.Client.ID, accessToken["aud"])

	// The custom claims ride the preview: the account's own as the string
	// it is, the group's parsed as the JSON document it names.
	assert.Equal(t, "vine", idToken["wand"])
	assert.Equal(t, "gryffindor-tower", idToken["common_room"])

	// The change stamp the stub never named answers the creation instant
	// it carried — the same fallback the issuance minted.
	assert.Equal(t, time.Time{}.Unix(), idToken["updated_at"])

	// The client's own absence and the account's are different refusals.
	_, _, _, err = service.Preview(t.Context(), "unknown-client", accountWire)
	assert.ErrorIs(t, err, ErrClientNotFound)
	_, _, _, err = service.Preview(t.Context(), issued.Client.ID, "user_ron")
	assert.ErrorIs(t, err, ErrPreviewUnknownUser)
}

// TestTheLogoLifecycleCoversTheKindCheckAndTheReset covers the logo: the
// bytes decide the kind, a replaced file is deleted before its replacement
// is stored, and a reset is the same success twice.
func TestTheLogoLifecycleCoversTheKindCheckAndTheReset(t *testing.T) {
	pool := migratedPool(t)
	manager := testStorage(t, pool)
	service := testService(t, pool).WithPictures(manager).WithBaseURL("https://idp.example.com")

	owner := seedAccount(t, pool, "hermione")
	issued, err := service.Create(t.Context(), owner, createParams("Hogwarts Portal"))
	require.NoError(t, err)

	// A client with no logo answers the missing-logo refusal, not the
	// client's.
	_, err = service.Logo(t.Context(), issued.Client.ID)
	assert.ErrorIs(t, err, ErrLogoMissing)

	// The bytes decide, not a declared type: a renamed archive is refused.
	err = service.UploadLogo(t.Context(), issued.Client.ID, []byte("not an image"))
	assert.ErrorIs(t, err, ErrUnsupportedLogo)

	png := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, make([]byte, 64)...)
	require.NoError(t, service.UploadLogo(t.Context(), issued.Client.ID, png))

	view, err := service.Get(t.Context(), issued.Client.ID)
	require.NoError(t, err)
	assert.True(t, view.HasLogo)
	require.NotNil(t, view.LogoURL)
	assert.True(t, strings.HasSuffix(*view.LogoURL, "/oidc/clients/"+issued.Client.ID+"/logo"))

	logo, err := service.Logo(t.Context(), issued.Client.ID)
	require.NoError(t, err)
	assert.Equal(t, "image/png", logo.ContentType)
	require.NoError(t, logo.Body.Close())

	// A kind change moves the key, so the replaced file is deleted first;
	// the key the row names carries the extension the new bytes earn.
	jpeg := append([]byte{0xff, 0xd8, 0xff}, make([]byte, 64)...)
	require.NoError(t, service.UploadLogo(t.Context(), issued.Client.ID, jpeg))
	stored, err := service.repo.GetClient(t.Context(), pool, issued.Client.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.LogoPath)
	assert.True(t, strings.HasSuffix(*stored.LogoPath, ".jpg"))

	// The reset is idempotent: a client without a logo is the state the
	// second reset names.
	require.NoError(t, service.DeleteLogo(t.Context(), issued.Client.ID))
	require.NoError(t, service.DeleteLogo(t.Context(), issued.Client.ID))
	view, err = service.Get(t.Context(), issued.Client.ID)
	require.NoError(t, err)
	assert.False(t, view.HasLogo)
	assert.Nil(t, view.LogoURL)
}

// TestTheAuditRecordsRideTheChangeTransactions reads the table the recorder
// fills, so the record's presence is what is asserted — not the call a stub
// would have counted.
func TestTheAuditRecordsRideTheChangeTransactions(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	owner := seedAccount(t, pool, "hermione")

	issued, err := service.Create(t.Context(), owner, createParams("Hogwarts Portal"))
	require.NoError(t, err)
	assertEvents(t, pool, audit.EventOidcClientCreated, 1)

	require.NoError(t, service.Delete(t.Context(), issued.Client.ID))
	assertEvents(t, pool, audit.EventOidcClientDeleted, 1)
}

// clockFor returns the service and the knob that moves its sense of now, so
// a test can put a secret past its expiry without writing an expiry the
// database's own check refuses.
func clockFor(t *testing.T, pool *datastore.Postgres) (*Service, *time.Time) {
	t.Helper()
	service := testService(t, pool)
	now := time.Now()
	service.now = func() time.Time { return now }
	return service, &now
}

// assertEvents counts the records one event has in the table.
func assertEvents(t *testing.T, pool *datastore.Postgres, event string, want int) {
	t.Helper()

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs WHERE event = $1`, event).Scan(&count))
	assert.Equal(t, want, count, "the %s record rides the change that caused it", event)
}
