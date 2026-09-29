package user

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"connectrpc.com/connect"

	"log/slog"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	awshttp "github.com/aws/smithy-go/transport/http"
	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/storage"
	"github.com/riipandi/tango/pkg/testutils"

	"uuid"
)

func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	return testutils.MigratedPostgres(t, "user_test")
}

// readPicture opens the account's picture through the service's read, so a
// test reads the body the transport would stream.
func (s *Service) readPicture(ctx context.Context, id string) (io.ReadCloser, string, error) {
	view, err := s.ProfilePicture(ctx, id)
	if err != nil {
		return nil, "", err
	}
	return view.Body, view.ContentType, nil
}

func testService(t *testing.T, pool *datastore.Postgres) *Service {
	t.Helper()
	return NewService(pool, nil, nil, nil)
}

// testPictureService builds the service over the real storage engine — the
// local driver in a throwaway directory — so a picture procedure runs the
// stage, sync, and read the production path runs. The engine and its
// directory travel with the test through the returned cleanup.
func testPictureService(t *testing.T, pool *datastore.Postgres) (*Service, *storage.Manager) {
	t.Helper()

	manager := storage.NewManager(storage.NewFS(t.TempDir()), pool,
		t.TempDir(), slog.New(slog.DiscardHandler))
	return NewService(pool, nil, nil, manager), manager
}

// passwordCount reads how many credentials an account carries. An account
// created without a password carries none.
// rowID decodes the wire identifier the views carry into the UUID the rows
// and the storage keys are built from.
func rowID(t *testing.T, wire string) string {
	t.Helper()
	id, err := ParseID(wire)
	require.NoError(t, err)
	return id.UUID()
}

func passwordCount(t *testing.T, pool *datastore.Postgres, userID string) int {
	userID = rowID(t, userID)
	t.Helper()

	id, err := uuid.Parse(userID)
	require.NoError(t, err)

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From("public.user_passwords")
	sb.Where(sb.Equal("user_id", id))

	query, args := sb.Build()
	var count int
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&count))
	return count
}

func TestCreateUserStoresTheAccountAndTheCredential(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	created, err := service.CreateUser(t.Context(), CreateParams{
		Username:      "hermione",
		Email:         "hermione@example.com",
		Password:      "expecto-patronum",
		FirstName:     "Hermione",
		LastName:      "Granger",
		EmailVerified: true,
	})
	require.NoError(t, err)

	assert.NotEmpty(t, created.ID)
	assert.Equal(t, "hermione", created.Username)
	assert.Equal(t, "hermione@example.com", created.Email)
	assert.Equal(t, "Hermione Granger", created.DisplayName)
	assert.True(t, created.EmailVerified)
	assert.False(t, created.CreatedAt.IsZero())
	assert.Equal(t, 1, passwordCount(t, pool, created.ID))

	read, err := service.GetUser(t.Context(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Hermione Granger", read.DisplayName)
	require.NotNil(t, read.FirstName)
	assert.Equal(t, "Hermione", *read.FirstName)
}

func TestCreateUserWithoutAPasswordCarriesNoCredential(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	created, err := service.CreateUser(t.Context(), CreateParams{FirstName: "Robert", LastName: "Langdon",
		Username: "langdon",
		Email:    "langdon@example.com",
	})
	require.NoError(t, err)

	// The display name is composed from the mandatory names, and no
	// credential row exists to sign in with.
	assert.Equal(t, "Robert Langdon", created.DisplayName)
	assert.False(t, created.EmailVerified)
	assert.Equal(t, 0, passwordCount(t, pool, created.ID))
}

func TestCreateUserRefusesADuplicateAccount(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	_, err := service.CreateUser(t.Context(), CreateParams{FirstName: "Hermione", LastName: "Granger", Username: "hermione", Email: "hermione@example.com"})
	require.NoError(t, err)

	// The username matches case-insensitively, the way its unique index does.
	_, err = service.CreateUser(t.Context(), CreateParams{FirstName: "Hermione", LastName: "Granger", Username: "HERMIONE", Email: "other@example.com"})
	assert.ErrorIs(t, err, ErrAccountExists)

	_, err = service.CreateUser(t.Context(), CreateParams{FirstName: "Hermione", LastName: "Granger", Username: "other", Email: "hermione@example.com"})
	assert.ErrorIs(t, err, ErrAccountExists)
}

// mustID parses a wire identifier in a test helper. A view identifier is
// always valid, so the failure is the test's own bug.
func mustID(t *testing.T, wire string) UserID {
	t.Helper()
	parsed, err := ParseID(wire)
	require.NoError(t, err)
	return parsed
}

// TestGetCurrentUserAnswersTheSubject reads the account the caller is: the
// subject travels as the wire identifier, and the answer is the same view
// the identifier-addressed read produces.
func TestGetCurrentUserAnswersTheSubject(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	ctx := t.Context()

	created, err := service.CreateUser(ctx, CreateParams{
		Username:  "sophie_neveu",
		Email:     "sophie.neveu@holy.grail",
		FirstName: "Sophie",
		LastName:  "Neveu",
	})
	require.NoError(t, err)

	view, err := service.GetCurrentUser(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "sophie_neveu", view.Username)
	assert.Equal(t, created.ID, view.ID)
}

// TestGetCurrentUserRefusesAnUnknownSubject keeps the boundary honest: an
// identifier that names no account is the not-found failure, whatever
// surface read it.
func TestGetCurrentUserRefusesAnUnknownSubject(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	service := testService(t, migratedPool(t))

	_, err := service.GetCurrentUser(t.Context(), "user_00000000000000000000000000")
	assert.ErrorIs(t, err, ErrUserNotFound)
}

// TestUpdateCurrentUserTouchesOnlyTheProfile pins the self-service
// boundary: the names and the locale travel, and everything the
// administrative surface owns — the username, the email, the role, the
// disabled flag — rides through untouched.
func TestUpdateCurrentUserTouchesOnlyTheProfile(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	ctx := t.Context()

	created, err := service.CreateUser(ctx, CreateParams{
		Username:  "vittoria_vetra",
		Email:     "vittoria.vetra@infinite.bound",
		FirstName: "Vittoria",
		LastName:  "Vetra",
		Locale:    "id-ID",
	})
	require.NoError(t, err)

	updated, err := service.UpdateCurrentUser(ctx, created.ID, ProfileParams{
		FirstName:   "Vittoria",
		LastName:    "Vetra",
		DisplayName: "V. Vetra",
		Locale:      "en-US",
	})
	require.NoError(t, err)
	assert.Equal(t, "V. Vetra", updated.DisplayName)
	assert.Equal(t, "en-US", *updated.Locale)

	// The identity fields are not the caller's to rewrite: read them back
	// from the store the administrative procedures would.
	row, err := service.repo.GetUser(ctx, pool, IDToUUID(mustID(t, created.ID)))
	require.NoError(t, err)
	assert.Equal(t, "vittoria_vetra", row.Username)
	assert.Equal(t, "vittoria.vetra@infinite.bound", row.Email)
}

func TestUpdateCurrentUserSetsTheTimezoneItIsGiven(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	ctx := t.Context()

	created, err := service.CreateUser(ctx, CreateParams{
		Username:    "hermione_granger",
		Email:       "hermione.granger@hogwarts.edu",
		DisplayName: "Hermione",
		Password:    "Expecto-Patronum-9",
	})
	require.NoError(t, err)
	assert.Equal(t, DefaultTimezone, created.Timezone,
		"an account that never chose a zone starts on the default, not on an empty string")

	updated, err := service.UpdateCurrentUser(ctx, created.ID, ProfileParams{
		FirstName:   "Hermione",
		LastName:    "Granger",
		DisplayName: "Hermione",
		Timezone:    "Asia/Jakarta",
	})
	require.NoError(t, err)
	assert.Equal(t, "Asia/Jakarta", updated.Timezone)

	// Clearing the field is the default, not an unparseable zone: the
	// frontend falls back to UTC the way it would for a fresh account.
	cleared, err := service.UpdateCurrentUser(ctx, created.ID, ProfileParams{
		FirstName:   "Hermione",
		LastName:    "Granger",
		DisplayName: "Hermione",
	})
	require.NoError(t, err)
	assert.Equal(t, DefaultTimezone, cleared.Timezone)

	_, err = service.UpdateCurrentUser(ctx, created.ID, ProfileParams{
		FirstName:   "Hermione",
		LastName:    "Granger",
		DisplayName: "Hermione",
		Timezone:    "Mars/Olympus",
	})
	assert.ErrorIs(t, err, ErrTimezoneInvalid,
		"a zone the tz database does not carry is refused, not stored")
}

func TestUpdateUserReplacesTheTimezoneAndRefusesAnUnknownZone(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	ctx := t.Context()

	created, err := service.CreateUser(ctx, CreateParams{
		Username:    "robert_langdon",
		Email:       "robert.langdon@harvard.edu",
		FirstName:   "Robert",
		LastName:    "Langdon",
		DisplayName: "Robert Langdon",
	})
	require.NoError(t, err)

	existing, err := service.GetUser(ctx, created.ID)
	require.NoError(t, err)

	updated, err := service.UpdateUser(ctx, created.ID, UpdateParams{
		Username:    existing.Username,
		Email:       existing.Email,
		FirstName:   *existing.FirstName,
		LastName:    *existing.LastName,
		DisplayName: existing.DisplayName,
		Timezone:    "Europe/Rome",
	})
	require.NoError(t, err)
	assert.Equal(t, "Europe/Rome", updated.Timezone)

	_, err = service.UpdateUser(ctx, created.ID, UpdateParams{
		Username:    existing.Username,
		Email:       existing.Email,
		FirstName:   *existing.FirstName,
		LastName:    *existing.LastName,
		DisplayName: existing.DisplayName,
		Timezone:    "Not/AZone",
	})
	assert.ErrorIs(t, err, ErrTimezoneInvalid)
}

func TestGetUserRefusesAnUnknownIdentifier(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	_, err := service.GetUser(t.Context(), uuid.NewV7().String())
	assert.ErrorIs(t, err, ErrUserNotFound)

	_, err = service.GetUser(t.Context(), "not-a-uuid")
	assert.ErrorIs(t, err, ErrUserNotFound)
}

func TestListUsersSearchesAndPaginates(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	// The four accounts carry distinct names, so the search's one match is
	// observable: the Granger name is hers alone.
	for _, account := range []struct{ username, first, last string }{
		{"hermione", "Hermione", "Granger"},
		{"langdon", "Robert", "Langdon"},
		{"sophie", "Sophie", "Neveu"},
		{"vittoria", "Vittoria", "Vetra"},
	} {
		_, err := service.CreateUser(t.Context(), CreateParams{
			Username: account.username, Email: account.username + "@example.com",
			FirstName: account.first, LastName: account.last,
		})
		require.NoError(t, err)
	}

	users, pagination, err := service.ListUsers(t.Context(), "", "", false, 1, 2)
	require.NoError(t, err)
	assert.Len(t, users, 2)
	require.NotNil(t, pagination.TotalItems)
	assert.Equal(t, 4, *pagination.TotalItems)

	// The search matches the username and the name parts, case-insensitively,
	// and answers only the accounts it names.
	users, _, err = service.ListUsers(t.Context(), "GRA", "", false, 1, 10)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "hermione", users[0].Username)
}

func TestUpdateUserReplacesTheFields(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	created, err := service.CreateUser(t.Context(), CreateParams{
		Username:  "hermione",
		Email:     "hermione@example.com",
		FirstName: "Hermione",
	})
	require.NoError(t, err)

	// The full replace is the whole shape: every field is specified, and the
	// names are mandatory, so a name part left empty is a contract refusal
	// the transport answers before the service runs.
	updated, err := service.UpdateUser(t.Context(), created.ID, UpdateParams{
		Username:    "hermione",
		Email:       "grey.lady@example.com",
		FirstName:   "Hermione",
		LastName:    "King",
		DisplayName: "The Grey Lady",
		Locale:      "en-GB",
		Disabled:    true,
	})
	require.NoError(t, err)
	assert.Equal(t, "The Grey Lady", updated.DisplayName)
	assert.Equal(t, "grey.lady@example.com", updated.Email)
	assert.True(t, updated.Disabled)
	require.NotNil(t, updated.FirstName)
	assert.Equal(t, "Hermione", *updated.FirstName)
	require.NotNil(t, updated.LastName)
	assert.Equal(t, "King", *updated.LastName)
	require.NotNil(t, updated.Locale)
	assert.Equal(t, "en-GB", *updated.Locale)

	read, err := service.GetUser(t.Context(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, "King", *read.LastName)
}

func TestUpdateUserAppliesAndLiftsTheBan(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	service.now = func() time.Time { return time.Unix(2000000000, 0) }

	created, err := service.CreateUser(t.Context(), CreateParams{FirstName: "Hermione", LastName: "Granger", Username: "hermione", Email: "hermione@example.com"})
	require.NoError(t, err)

	expires := service.now().Add(24 * time.Hour)
	banned, err := service.UpdateUser(t.Context(), created.ID, UpdateParams{
		Username:     "hermione",
		Email:        "hermione@example.com",
		FirstName:    "Hermione",
		LastName:     "Granger",
		DisplayName:  "hermione",
		BanExpiresAt: &expires,
		BanReason:    new("unruly behaviour"),
	})
	require.NoError(t, err)
	require.NotNil(t, banned.BannedAt)
	assert.Equal(t, service.now(), *banned.BannedAt)
	require.NotNil(t, banned.BanExpires)
	require.NotNil(t, banned.BanReason)

	// A re-ban keeps the start instant on record: the field answers "since
	// when", so applying a new expiry does not move it.
	later := expires.Add(24 * time.Hour)
	rebanned, err := service.UpdateUser(t.Context(), created.ID, UpdateParams{
		Username:     "hermione",
		Email:        "hermione@example.com",
		FirstName:    "Hermione",
		LastName:     "Granger",
		DisplayName:  "hermione",
		BanExpiresAt: &later,
	})
	require.NoError(t, err)
	assert.Equal(t, service.now(), *rebanned.BannedAt)

	lifted, err := service.UpdateUser(t.Context(), created.ID, UpdateParams{
		Username:    "hermione",
		Email:       "hermione@example.com",
		DisplayName: "hermione",
	})
	require.NoError(t, err)
	assert.Nil(t, lifted.BannedAt)
	assert.Nil(t, lifted.BanExpires)
	assert.Nil(t, lifted.BanReason)
}

func TestUpdateUserRefusesAnUnknownIdentifierAndADuplicate(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	_, err := service.CreateUser(t.Context(), CreateParams{FirstName: "Hermione", LastName: "Granger", Username: "hermione", Email: "hermione@example.com"})
	require.NoError(t, err)

	_, err = service.UpdateUser(t.Context(), uuid.NewV7().String(), UpdateParams{
		Username: "hermione", Email: "ron@example.com", DisplayName: "x", FirstName: "Hermione", LastName: "Granger",
	})
	assert.ErrorIs(t, err, ErrUserNotFound)

	other, err := service.CreateUser(t.Context(), CreateParams{FirstName: "Hermione", LastName: "Granger", Username: "langdon", Email: "langdon@example.com"})
	require.NoError(t, err)

	_, err = service.UpdateUser(t.Context(), other.ID, UpdateParams{
		Username: "hermione", Email: "langdon@example.com", DisplayName: "langdon", FirstName: "Hermione", LastName: "Granger",
	})
	assert.ErrorIs(t, err, ErrAccountExists)
}

func TestDeleteUserRefusesTheSignedInAccount(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	created, err := service.CreateUser(t.Context(), CreateParams{FirstName: "Hermione", LastName: "Granger", Username: "hermione", Email: "hermione@example.com"})
	require.NoError(t, err)

	// The signed-in account is refused, whatever case the claims carry it in.
	err = service.DeleteUser(t.Context(), created.ID, "HERMIONE")
	assert.ErrorIs(t, err, ErrSelfDeletion)

	other, err := service.CreateUser(t.Context(), CreateParams{FirstName: "Hermione", LastName: "Granger", Username: "langdon", Email: "langdon@example.com"})
	require.NoError(t, err)
	require.NoError(t, service.DeleteUser(t.Context(), other.ID, "hermione"))

	_, err = service.GetUser(t.Context(), other.ID)
	assert.ErrorIs(t, err, ErrUserNotFound)

	err = service.DeleteUser(t.Context(), other.ID, "hermione")
	assert.ErrorIs(t, err, ErrUserNotFound)
}

func TestMapErrorCarriesTheConnectCodes(t *testing.T) {
	cases := []struct {
		err  error
		code connect.Code
	}{
		{ErrUserNotFound, connect.CodeNotFound},
		{ErrAccountExists, connect.CodeAlreadyExists},
		{ErrSelfDeletion, connect.CodeFailedPrecondition},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.code, connect.CodeOf(mapError(tc.err)), "%v", tc.err)
	}
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(mapError(errors.New("boom"))))
}

// TestThePictureFlowStagesSyncsAndReadsBack runs the update through the real
// engine — stage, inline sync, row pointer — and reads the bytes back with
// the content type the update recorded.
func TestThePictureFlowStagesSyncsAndReadsBack(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, pictures := testPictureService(t, pool)
	created, err := service.CreateUser(t.Context(), CreateParams{
		Username: "hermione", Email: "hermione@example.com", Password: "expecto-patronum",
		FirstName: "Hermione", LastName: "Granger",
	})
	require.NoError(t, err)

	picture := bytes.Repeat([]byte("A"), 80) // PNG magic + filler
	picture[0], picture[3] = 0x89, 'N'
	copy(picture, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	require.NoError(t, service.UpdateProfilePicture(t.Context(), created.ID, picture))

	body, mime, err := service.readPicture(t.Context(), created.ID)
	require.NoError(t, err)
	defer body.Close()
	assert.Equal(t, "image/png", mime)
	read, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, picture, read)

	// The engine holds the file whole under the key the row names — the
	// same tree of keys both drivers keep.
	stored, err := pictures.Open(t.Context(), "avatars/"+rowID(t, created.ID)+".png")
	require.NoError(t, err)
	storedBody, err := io.ReadAll(stored)
	require.NoError(t, err)
	require.NoError(t, stored.Close())
	assert.Equal(t, picture, storedBody)

	// The row names the key the engine holds the file under.
	var storedPath *string
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("avatar_url")
	sb.From(UserTable)
	sb.Where(sb.Equal("id", rowID(t, created.ID)))
	query, args := sb.Build()
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&storedPath))
	require.NotNil(t, storedPath)
	assert.Equal(t, "avatars/"+rowID(t, created.ID)+".png", *storedPath)
}

// TestPictureUpdateMovesTheKeyWhenTheKindChanges pins the naming contract: the
// key is `avatars/<id>.<ext>` with the extension the sniffed bytes earned, so
// an upload of another kind moves the picture rather than leaving the old one
// under a name that lies about its content.
func TestPictureUpdateMovesTheKeyWhenTheKindChanges(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, pictures := testPictureService(t, pool)
	created, err := service.CreateUser(t.Context(), CreateParams{
		Username: "hermione", Email: "hermione@example.com", Password: "expecto-patronum",
		FirstName: "Hermione", LastName: "Granger",
	})
	require.NoError(t, err)

	png := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("first")...)
	require.NoError(t, service.UpdateProfilePicture(t.Context(), created.ID, png))
	pngKey := "avatars/" + rowID(t, created.ID) + ".png"
	assert.Equal(t, pngKey, storedPictureKey(t, pool, created.ID))

	// The second upload is another kind, so it lands under another key.
	jpeg := append([]byte{0xff, 0xd8, 0xff}, []byte("second")...)
	require.NoError(t, service.UpdateProfilePicture(t.Context(), created.ID, jpeg))
	jpegKey := "avatars/" + rowID(t, created.ID) + ".jpg"
	assert.Equal(t, jpegKey, storedPictureKey(t, pool, created.ID))

	// The replaced picture left the engine whole: no object answers its key,
	// and no manifest row keeps the garbage collection from ever sweeping it.
	_, err = pictures.Open(t.Context(), pngKey)
	assert.ErrorIs(t, err, storage.ErrNotFound)
	// The manifest row left with the object: a row without a file is what
	// the garbage collection keeps a key for, so one that lingered would
	// make the old name unsweepable forever.
	_, err = storage.NewManifests().Load(t.Context(), pool, pngKey)
	assert.ErrorIs(t, err, storage.ErrNoManifest)

	// The read answers the picture the row now names.
	body, mime, err := service.readPicture(t.Context(), created.ID)
	require.NoError(t, err)
	defer body.Close()
	assert.Equal(t, "image/jpeg", mime)
	read, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, jpeg, read)

	// The same kind again keeps its key: an update is not a move when the
	// extension does not change.
	require.NoError(t, service.UpdateProfilePicture(t.Context(), created.ID, jpeg))
	assert.Equal(t, jpegKey, storedPictureKey(t, pool, created.ID))
}

// storedPictureKey reads the key the account's row names.
func storedPictureKey(t *testing.T, pool *datastore.Postgres, userID string) string {
	userID = rowID(t, userID)
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("avatar_url")
	sb.From(UserTable)
	sb.Where(sb.Equal("id", userID))
	query, args := sb.Build()

	var key *string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&key))
	require.NotNil(t, key, "the row names the picture it stored")
	return *key
}

// TestPictureUpdateSniffsTheBytesRatherThanTheDeclaration refuses a payload
// no accepted image kind claims, whatever its name says.
func TestPictureUpdateSniffsTheBytesRatherThanTheDeclaration(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testPictureService(t, pool)
	created, err := service.CreateUser(t.Context(), CreateParams{
		Username: "hermione", Email: "hermione@example.com", Password: "expecto-patronum",
		FirstName: "Hermione", LastName: "Granger",
	})
	require.NoError(t, err)

	err = service.UpdateProfilePicture(t.Context(), created.ID, []byte("definitely not an image"))
	assert.ErrorIs(t, err, ErrUnsupportedPicture)

	// The WAV file shares the RIFF form with WebP; the format field tells
	// them apart.
	wav := append([]byte("RIFF"), make([]byte, 8)...)
	copy(wav[8:], "WAVE")
	err = service.UpdateProfilePicture(t.Context(), created.ID, wav)
	assert.ErrorIs(t, err, ErrUnsupportedPicture)
}

// TestPictureResetFallsBackToTheDefault clears the row and removes the file,
// so the read answers the bundled default the frontend ships.
func TestPictureResetFallsBackToTheDefault(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, pictures := testPictureService(t, pool)
	created, err := service.CreateUser(t.Context(), CreateParams{
		Username: "hermione", Email: "hermione@example.com", Password: "expecto-patronum",
		FirstName: "Hermione", LastName: "Granger",
	})
	require.NoError(t, err)
	picture := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 1, 2}
	require.NoError(t, service.UpdateProfilePicture(t.Context(), created.ID, picture))

	// The read before the reset answers the stored bytes.
	body, _, readErr := service.readPicture(t.Context(), created.ID)
	require.NoError(t, readErr)
	body.Close()

	require.NoError(t, service.ResetProfilePicture(t.Context(), created.ID))

	var storedPath *string
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("avatar_url")
	sb.From(UserTable)
	sb.Where(sb.Equal("id", rowID(t, created.ID)))
	query, args := sb.Build()
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&storedPath))
	assert.Nil(t, storedPath)

	// The file left the engine: no read answers the key anymore.
	_, err = pictures.Open(t.Context(), "avatars/"+rowID(t, created.ID)+".png")
	assert.ErrorIs(t, err, storage.ErrNotFound)

	view, err := service.ProfilePicture(t.Context(), created.ID)
	require.NoError(t, err)
	defer view.Body.Close()
	assert.True(t, view.Default)
	empty, err := io.ReadAll(view.Body)
	require.NoError(t, err)
	assert.Empty(t, empty)

	// The file left the engine: the sync the update ran holds no copy the
	// reset forgot.
	assert.True(t, view.Default, "the read after the reset answers the default")
}

// TestPictureReadFallsBackWhenTheBytesAreGone covers the manifest that
// outlives its object — a deployment that switched storage drivers, a bucket
// emptied underneath the engine. The account has no readable picture, so the
// read answers the bundled default; a 500 would tell the client its request
// was wrong when the request was fine.
func TestPictureReadFallsBackWhenTheBytesAreGone(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	// The backend's root is the test's own, so the object can be taken away
	// from underneath the engine — the state another driver's tree leaves.
	root := t.TempDir()
	manager := storage.NewManager(storage.NewFS(root), pool,
		t.TempDir(), slog.New(slog.DiscardHandler))
	service := NewService(pool, nil, nil, manager)
	created, err := service.CreateUser(t.Context(), CreateParams{
		Username: "hermione", Email: "hermione@example.com", Password: "expecto-patronum",
		FirstName: "Hermione", LastName: "Granger",
	})
	require.NoError(t, err)

	picture := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 4, 4, 4}
	require.NoError(t, service.UpdateProfilePicture(t.Context(), created.ID, picture))

	// The object leaves the backend while the manifest row stays: exactly
	// the state another driver's tree produces.
	key := storedPictureKey(t, pool, created.ID)
	require.NoError(t, os.Remove(filepath.Join(root, "files", filepath.FromSlash(key))))
	_, err = storage.NewManifests().Load(t.Context(), pool, key)
	require.NoError(t, err, "the manifest row outlives the object")

	view, err := service.ProfilePicture(t.Context(), created.ID)
	require.NoError(t, err)
	defer view.Body.Close()
	assert.True(t, view.Default, "an account whose bytes are gone reads as one without a picture")
	empty, err := io.ReadAll(view.Body)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// TestPictureRefusesAnUnknownAccount keeps the not-found boundary on every
// picture procedure.
func TestPictureRefusesAnUnknownAccount(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, _ := testPictureService(t, pool)

	id := "00000000-0000-0000-0000-000000000000"
	err := service.UpdateProfilePicture(t.Context(), id, []byte("x"))
	assert.ErrorIs(t, err, ErrUserNotFound)
	err = service.ResetProfilePicture(t.Context(), id)
	assert.ErrorIs(t, err, ErrUserNotFound)
	_, err = service.ProfilePicture(t.Context(), id)
	assert.ErrorIs(t, err, ErrUserNotFound)
}

// TestThePictureFlowLandsOnS3 runs the same update through the S3-compatible
// backend — the local driver's twin — and reads the bytes back through the
// engine, so the picture procedures are indifferent to the store they are
// given.
func TestThePictureFlowLandsOnS3(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	backend := testutils.StartMinIO(t.Context(), t)
	store, err := storage.NewS3(config.S3{
		AccessKey:      backend.AccessKey,
		SecretKey:      backend.Secret,
		BucketName:     "tango-user-test",
		EndpointURL:    backend.Endpoint,
		ForcePathStyle: true,
		Region:         "us-east-1",
		PathPrefix:     "tango-user-test/",
	})
	require.NoError(t, err)
	// The shared container starts empty: the bucket is this test's to make,
	// and an existing one is fine. The client is built over the same
	// endpoint the store addresses — the store itself owns no bucket.
	awsCfg, err := awsconfig.LoadDefaultConfig(t.Context(), awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			backend.AccessKey, backend.Secret, "")),
		awsconfig.WithBaseEndpoint(backend.Endpoint))
	require.NoError(t, err)
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) { o.UsePathStyle = true })
	if _, bucketErr := client.CreateBucket(t.Context(), &s3.CreateBucketInput{
		Bucket: awssdk.String("tango-user-test"),
	}); bucketErr != nil {
		var apiErr *awshttp.ResponseError
		if !errors.As(bucketErr, &apiErr) || apiErr.HTTPStatusCode() != 409 {
			require.NoError(t, bucketErr)
		}
	}

	manager := storage.NewManager(store, pool, t.TempDir(), slog.New(slog.DiscardHandler))
	service := NewService(pool, nil, nil, manager)

	created, err := service.CreateUser(t.Context(), CreateParams{
		Username: "hermione", Email: "hermione@example.com", Password: "expecto-patronum",
		FirstName: "Hermione", LastName: "Granger",
	})
	require.NoError(t, err)

	picture := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 9, 8, 7}
	require.NoError(t, service.UpdateProfilePicture(t.Context(), created.ID, picture))

	// The read streams the file the backend holds — the same flow the
	// local driver answers, no S3-specific branch anywhere in the feature.
	view, mime, err := service.readPicture(t.Context(), created.ID)
	require.NoError(t, err)
	defer view.Close()
	assert.Equal(t, "image/png", mime)
	read, err := io.ReadAll(view)
	require.NoError(t, err)
	assert.Equal(t, picture, read)

	// The bucket holds the final file at the key — the same tree the local
	// driver keeps — and nothing else: no chunk-shaped object ever lands
	// beside it.
	listed, err := client.ListObjectsV2(t.Context(), &s3.ListObjectsV2Input{
		Bucket: awssdk.String("tango-user-test"),
		Prefix: awssdk.String("tango-user-test/"),
	})
	require.NoError(t, err)
	var keys []string
	for _, item := range listed.Contents {
		keys = append(keys, awssdk.ToString(item.Key))
	}
	assert.Equal(t,
		[]string{"tango-user-test/avatars/" + rowID(t, created.ID) + ".png"}, keys)

	// The object carries the feature's content type: a direct read of the
	// bucket — a presigned URL, a console preview — answers what the
	// bytes are without consulting the manifest.
	headed, err := client.HeadObject(t.Context(), &s3.HeadObjectInput{
		Bucket: awssdk.String("tango-user-test"),
		Key:    awssdk.String("tango-user-test/avatars/" + rowID(t, created.ID) + ".png"),
	})
	require.NoError(t, err)
	assert.Equal(t, "image/png", awssdk.ToString(headed.ContentType))

	// The reset removes the object the backend holds, so the account falls
	// back to the bundled default the same way it does on the local driver.
	require.NoError(t, service.ResetProfilePicture(t.Context(), created.ID))
	fallback, err := service.ProfilePicture(t.Context(), created.ID)
	require.NoError(t, err)
	defer fallback.Body.Close()
	assert.True(t, fallback.Default)

	// The reset emptied the bucket: the object left with the account's
	// key, the same state a local reset lands in.
	listed, err = client.ListObjectsV2(t.Context(), &s3.ListObjectsV2Input{
		Bucket: awssdk.String("tango-user-test"),
		Prefix: awssdk.String("tango-user-test/"),
	})
	require.NoError(t, err)
	assert.Empty(t, listed.Contents)
}

// TestPictureProceduresRefuseARunWithoutTheEngine covers the deployment that
// serves accounts without picture storage: the account procedures answer as
// usual, the picture procedures refuse.
func TestPictureProceduresRefuseARunWithoutTheEngine(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	created, err := service.CreateUser(t.Context(), CreateParams{
		Username: "hermione", Email: "hermione@example.com", Password: "expecto-patronum",
		FirstName: "Hermione", LastName: "Granger",
	})
	require.NoError(t, err)

	err = service.UpdateProfilePicture(t.Context(), created.ID, []byte("x"))
	assert.ErrorIs(t, err, ErrPicturesUnavailable)
	err = service.ResetProfilePicture(t.Context(), created.ID)
	assert.ErrorIs(t, err, ErrPicturesUnavailable)
}

// recordingEnder is the session ender a test observes: it counts the calls
// and ends nothing, because the ban's transaction is the thing under test.
type recordingEnder struct {
	calls []uuid.UUID
}

func (r *recordingEnder) RevokeAllForUser(_ context.Context, _ datastore.Querier, userID uuid.UUID) (int, error) {
	r.calls = append(r.calls, userID)
	return 2, nil
}

// silentNotifier records the notices a ban and its lift queue.
type silentNotifier struct {
	banned, unbanned int
}

func (s *silentNotifier) UserBanned(_ context.Context, _ string, _ UserView, _ *time.Time) {
	s.banned++
}

func (s *silentNotifier) UserUnbanned(_ context.Context, _ string, _ UserView) {
	s.unbanned++
}

func TestBanUserAppliesTheTermAndEndsTheSessions(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	service.now = func() time.Time { return time.Unix(2000000000, 0) }

	ender := &recordingEnder{}
	notifier := &silentNotifier{}
	service.sessions = ender
	service.notify = notifier

	created, err := service.CreateUser(t.Context(), CreateParams{FirstName: "Sophie", LastName: "Neveu", Username: "sophie", Email: "sophie@example.com"})
	require.NoError(t, err)

	expires := service.now().Add(24 * time.Hour)
	outcome, err := service.BanUser(t.Context(), created.ID, BanParams{
		Reason:    "unruly behaviour",
		ExpiresAt: &expires,
	})
	require.NoError(t, err)

	require.NotNil(t, outcome.User.BannedAt)
	assert.Equal(t, service.now(), *outcome.User.BannedAt)
	require.NotNil(t, outcome.User.BanExpires)
	assert.Equal(t, expires, *outcome.User.BanExpires)
	require.NotNil(t, outcome.User.BanReason)
	assert.Equal(t, "unruly behaviour", *outcome.User.BanReason)
	assert.Equal(t, 2, outcome.EndedSessions)
	assert.Equal(t, 1, len(ender.calls), "the ban ended the account's live sessions")

	// A re-ban replaces the terms and keeps the start instant: the field
	// answers "since when", so applying a new expiry does not move it.
	later := service.now().Add(48 * time.Hour)
	rebanned, err := service.BanUser(t.Context(), created.ID, BanParams{
		Reason:    "still unruly",
		ExpiresAt: &later,
	})
	require.NoError(t, err)
	assert.Equal(t, service.now(), *rebanned.User.BannedAt)
	assert.Equal(t, "still unruly", *rebanned.User.BanReason)
	assert.Equal(t, 2, notifier.banned, "the re-ban queued its notice too")
	assert.Equal(t, 2, len(ender.calls), "the re-ban ended the live sessions again")
}

func TestBanUserRefusesAPastExpiryAndAnUnknownTarget(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)

	past := service.now().Add(-time.Hour)
	_, err := service.BanUser(t.Context(), "user_01a0da1ccb4177900000000000", BanParams{
		Reason:    "anything",
		ExpiresAt: &past,
	})
	assert.ErrorIs(t, err, ErrBanInPast)

	_, err = service.BanUser(t.Context(), "user_01a0da1ccb4177900000000000", BanParams{
		Reason: "anything",
	})
	assert.ErrorIs(t, err, ErrUserNotFound)
}

func TestUnbanUserLiftsTheBanAsAUnitAndAnswersTheQuiet(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	service.now = func() time.Time { return time.Unix(2000000000, 0) }

	notifier := &silentNotifier{}
	service.notify = notifier

	created, err := service.CreateUser(t.Context(), CreateParams{FirstName: "Vittoria", LastName: "Vetra", Username: "vittoria", Email: "vittoria@example.com"})
	require.NoError(t, err)

	_, err = service.BanUser(t.Context(), created.ID, BanParams{Reason: "cooling off"})
	require.NoError(t, err)

	lifted, err := service.UnbanUser(t.Context(), created.ID)
	require.NoError(t, err)
	assert.Nil(t, lifted.User.BannedAt)
	assert.Nil(t, lifted.User.BanExpires)
	assert.Nil(t, lifted.User.BanReason)
	assert.Equal(t, 1, notifier.unbanned)

	// An account that is not banned is the same success: the state the
	// caller asked for is the state the row is in.
	again, err := service.UnbanUser(t.Context(), created.ID)
	require.NoError(t, err)
	assert.Nil(t, again.User.BannedAt)
	assert.Equal(t, 2, notifier.unbanned)
}
