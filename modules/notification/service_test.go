package notification

import (
	"log/slog"
	"testing"
	"time"
	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/internal/testutils"
)

// migratedPool opens a database the migrations have built, so the
// notification tables exist.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	return testutils.MigratedPostgres(t, "notification_test")
}

// testService builds the service with a recorder that writes for real — a
// record is part of the transaction it describes, so the assertions read
// the table the writer fills.
func testService(t *testing.T, pool *datastore.Postgres) *Service {
	t.Helper()

	return NewService(pool, fwaudit.NewRecorder(slog.New(slog.DiscardHandler)), nil)
}

// seedAccount inserts an account directly and answers its identifier. A
// notification targets accounts, so some must exist before one does.
func seedAccount(t *testing.T, pool *datastore.Postgres, username string, disabled bool) uuid.UUID {
	t.Helper()

	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.users (username, email, first_name, last_name, display_name, disabled)
		VALUES ($1::citext, $1::text || '@example.com', 'Hermione', 'Granger', 'Hermione Granger', $2)
		ON CONFLICT (username) DO NOTHING`,
		username, disabled)
	require.NoError(t, err)

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From(entity.TableUsers)
	sb.Where(sb.Equal("username", username))

	query, args := sb.Build()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&id))
	return id
}

// seedGroup inserts a group with one member and answers the pair. The
// group's audience resolves through the membership, so the row the
// audience names must have someone in it.
func seedGroup(t *testing.T, pool *datastore.Postgres, name string, member uuid.UUID) uuid.UUID {
	t.Helper()

	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.user_groups (name, display_name) VALUES ($1, $1)`, name)
	require.NoError(t, err)

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From(entity.TableUserGroups)
	sb.Where(sb.Equal("name", name))

	query, args := sb.Build()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&id))

	mb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	mb.InsertInto(entity.TableUserGroupsUsers)
	mb.Cols("user_group_id", "user_id")
	mb.Values(id, member)

	mquery, margs := mb.Build()
	_, err = pool.Exec(t.Context(), mquery, margs...)
	require.NoError(t, err)
	return id
}

// created is a fixture announcement aimed at everyone, recorded against
// the administrator the fixture seeds.
func created(t *testing.T, service *Service, title string) Notification {
	t.Helper()

	row, err := service.Create(t.Context(), mustCreate(t, service), CreateParams{
		Category: CategoryAnnouncement,
		Title:    title,
		Body:     "The service will be briefly unavailable.",
	})
	require.NoError(t, err)
	return row
}

// mustCreate is the administrator every create is recorded against. The
// column is a foreign key, so the account has to exist; the helper seeds
// it once per database and answers the same identifier afterwards.
func mustCreate(t *testing.T, service *Service) uuid.UUID {
	t.Helper()

	return seedAccount(t, service.pool, "dumbledore", false)
}

// TestACreatedAnnouncementReachesEveryInbox pins the whole account side:
// the global audience answers every account, the read state starts empty,
// and the row the create answers is the row the list reads.
func TestACreatedAnnouncementReachesEveryInbox(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	hermione := seedAccount(t, pool, "hermione", false)
	ron := seedAccount(t, pool, "ron", false)

	row := created(t, service, "Expecto Patronum")

	for _, who := range []uuid.UUID{hermione, ron} {
		inbox, _, err := service.ListInbox(t.Context(), who, false, "", "", false, 1, 20)
		require.NoError(t, err)
		require.Len(t, inbox, 1)
		assert.Equal(t, row.ID, inbox[0].ID)
		assert.Equal(t, CategoryAnnouncement, inbox[0].Category)
		assert.Nil(t, inbox[0].ReadAt, "an inbox row nobody read carries no receipt")

		count, err := service.UnreadCount(t.Context(), who)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	}

	all, _, err := service.ListAll(t.Context(), "", "", false, 1, 20)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, row.ID, all[0].ID)
}

// TestAUserAudienceNamesItsReaders pins the targeted audience: the named
// accounts read the notice, the accounts it does not name do not — and a
// disabled account is not a recipient of the email pass, though the
// audience is the query the inbox shares.
func TestAUserAudienceNamesItsReaders(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	hermione := seedAccount(t, pool, "hermione", false)
	ron := seedAccount(t, pool, "ron", false)

	row, err := service.Create(t.Context(), mustCreate(t, service), CreateParams{
		Category:     CategorySystem,
		Title:        "Digital Fortress",
		Body:         "Your account was signed in from a new device.",
		AudienceKind: AudienceUsers,
		UserIDs:      []uuid.UUID{hermione},
	})
	require.NoError(t, err)
	assert.Equal(t, AudienceUsers, row.AudienceKind)

	inbox, _, err := service.ListInbox(t.Context(), hermione, false, "", "", false, 1, 20)
	require.NoError(t, err)
	require.Len(t, inbox, 1)

	inbox, _, err = service.ListInbox(t.Context(), ron, false, "", "", false, 1, 20)
	require.NoError(t, err)
	assert.Empty(t, inbox)
}

// TestAGroupAudienceReachesTheMembers pins the group audience: the members
// read the notice and the accounts outside the group do not.
func TestAGroupAudienceReachesTheMembers(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	hermione := seedAccount(t, pool, "hermione", false)
	ron := seedAccount(t, pool, "ron", false)
	group := seedGroup(t, pool, "gryffindor", hermione)

	_, err := service.Create(t.Context(), mustCreate(t, service), CreateParams{
		Category:     CategoryAnnouncement,
		Topic:        "House cup",
		Title:        "Gryffindor wins the house cup",
		Body:         "Ten points to Gryffindor.",
		AudienceKind: AudienceGroups,
		GroupIDs:     []uuid.UUID{group},
	})
	require.NoError(t, err)

	inbox, _, err := service.ListInbox(t.Context(), hermione, false, "", "", false, 1, 20)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	assert.Equal(t, "House cup", *inbox[0].Topic)

	inbox, _, err = service.ListInbox(t.Context(), ron, false, "", "", false, 1, 20)
	require.NoError(t, err)
	assert.Empty(t, inbox)
}

// TestCreateRefusesTheAudiencesTheContractCannotExpress pins the rules the
// wire constraints cannot spell: a system notice carries a topic never and
// a global audience never, an audience that names nobody is refused, and
// the audience that names an account or group that does not exist is
// refused without naming which.
func TestCreateRefusesTheAudiencesTheContractCannotExpress(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	hermione := seedAccount(t, pool, "hermione", false)
	unknown := uuid.NewV7()

	cases := map[string]struct {
		params CreateParams
		want   error
	}{
		"a system notice with a topic": {
			params: CreateParams{
				Category:     CategorySystem,
				Topic:        "House cup",
				Title:        "t",
				Body:         "b",
				AudienceKind: AudienceUsers,
				UserIDs:      []uuid.UUID{hermione},
			},
			want: ErrInvalidAudience,
		},
		"a system notice for everyone": {
			params: CreateParams{
				Category:     CategorySystem,
				Title:        "t",
				Body:         "b",
				AudienceKind: AudienceGlobal,
			},
			want: ErrInvalidAudience,
		},
		"an audience that names nobody": {
			params: CreateParams{
				Category:     CategorySystem,
				Title:        "t",
				Body:         "b",
				AudienceKind: AudienceUsers,
			},
			want: ErrInvalidAudience,
		},
		"an unknown account": {
			params: CreateParams{
				Category:     CategorySystem,
				Title:        "t",
				Body:         "b",
				AudienceKind: AudienceUsers,
				UserIDs:      []uuid.UUID{unknown},
			},
			want: ErrUnknownTarget,
		},
		"an unknown group": {
			params: CreateParams{
				Category:     CategoryAnnouncement,
				Title:        "t",
				Body:         "b",
				AudienceKind: AudienceGroups,
				GroupIDs:     []uuid.UUID{unknown},
			},
			want: ErrUnknownTarget,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := service.Create(t.Context(), mustCreate(t, service), tc.params)
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

// TestAReceiptIsWrittenOnceAndOnlyForWhatIsVisible pins the read state:
// the receipt is written for a notification the caller is targeted by, the
// second mark does not move the instant, and a notification the caller is
// not targeted by answers the not-found instead of gaining a receipt.
func TestAReceiptIsWrittenOnceAndOnlyForWhatIsVisible(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	hermione := seedAccount(t, pool, "hermione", false)
	ron := seedAccount(t, pool, "ron", false)
	row := created(t, service, "The Invisible Book")

	require.NoError(t, service.MarkRead(t.Context(), hermione, row.ID))

	inbox, _, err := service.ListInbox(t.Context(), hermione, false, "", "", false, 1, 20)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	require.NotNil(t, inbox[0].ReadAt)

	firstRead := *inbox[0].ReadAt
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, service.MarkRead(t.Context(), hermione, row.ID))

	inbox, _, err = service.ListInbox(t.Context(), hermione, false, "", "", false, 1, 20)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	assert.WithinDuration(t, firstRead, *inbox[0].ReadAt, time.Millisecond,
		"the second mark does not move the instant")

	count, err := service.UnreadCount(t.Context(), hermione)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	unread, _, err := service.ListInbox(t.Context(), hermione, true, "", "", false, 1, 20)
	require.NoError(t, err)
	assert.Empty(t, unread)

	// A notice hermione alone is targeted by is the not-found for ron: the
	// receipt is never written for a notification the caller cannot see.
	private, err := service.Create(t.Context(), mustCreate(t, service), CreateParams{
		Category:     CategorySystem,
		Title:        "For hermione only",
		Body:         "b",
		AudienceKind: AudienceUsers,
		UserIDs:      []uuid.UUID{hermione},
	})
	require.NoError(t, err)
	assert.ErrorIs(t, service.MarkRead(t.Context(), ron, private.ID), ErrNotFound,
		"a notification the caller is not targeted by is the not-found")
}

// TestMarkAllReadMarksTheWholeInbox pins the sweep: the receipts are
// written for every live notification the caller is targeted by, and the
// answer counts what it marked.
func TestMarkAllReadMarksTheWholeInbox(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	hermione := seedAccount(t, pool, "hermione", false)
	first := created(t, service, "The First Oath")
	second := created(t, service, "The Second Oath")

	marked, err := service.MarkAllRead(t.Context(), hermione)
	require.NoError(t, err)
	assert.Equal(t, int64(2), marked)

	count, err := service.UnreadCount(t.Context(), hermione)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	again, err := service.MarkAllRead(t.Context(), hermione)
	require.NoError(t, err)
	assert.Equal(t, int64(0), again)

	inbox, _, err := service.ListInbox(t.Context(), hermione, false, "", "", false, 1, 20)
	require.NoError(t, err)
	require.Len(t, inbox, 2)
	assert.NotNil(t, inbox[0].ReadAt)
	assert.NotNil(t, inbox[1].ReadAt)
	assert.Contains(t, []uuid.UUID{first.ID, second.ID}, inbox[0].ID)
}

// TestACancelledAnnouncementLeavesTheInboxes pins the withdrawal: the
// notification stops being visible to every account, stays listed in the
// administrative view, and no longer counts as unread.
func TestACancelledAnnouncementLeavesTheInboxes(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	hermione := seedAccount(t, pool, "hermione", false)
	row := created(t, service, "Parseltongue")
	other := created(t, service, "Marauder's Map")

	require.NoError(t, service.Cancel(t.Context(), mustCreate(t, service), row.ID))

	// A second cancellation is the success the state already names.
	require.NoError(t, service.Cancel(t.Context(), mustCreate(t, service), row.ID))

	inbox, _, err := service.ListInbox(t.Context(), hermione, false, "", "", false, 1, 20)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	assert.Equal(t, other.ID, inbox[0].ID)

	count, err := service.UnreadCount(t.Context(), hermione)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	all, _, err := service.ListAll(t.Context(), "", "", false, 1, 20)
	require.NoError(t, err)
	require.Len(t, all, 2)
}

// TestTheBrokerDeliversToTheAudienceItNames pins the live tail: a global
// announcement reaches an open stream for any account, a targeted one
// reaches only the accounts it names, and the broker drops nothing on the
// floor when nobody listens.
func TestTheBrokerDeliversToTheAudienceItNames(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	hermione := seedAccount(t, pool, "hermione", false)
	ron := seedAccount(t, pool, "ron", false)

	events, cancel := service.Subscribe(hermione)
	defer cancel()

	global := created(t, service, "The Marauder's Map")
	select {
	case got := <-events:
		assert.Equal(t, global.ID, got.ID)
	case <-time.After(time.Second):
		t.Fatal("the global announcement never reached the open stream")
	}

	targeted, err := service.Create(t.Context(), mustCreate(t, service), CreateParams{
		Category:     CategorySystem,
		Title:        "For hermione only",
		Body:         "b",
		AudienceKind: AudienceUsers,
		UserIDs:      []uuid.UUID{hermione},
	})
	require.NoError(t, err)
	select {
	case got := <-events:
		assert.Equal(t, targeted.ID, got.ID)
	case <-time.After(time.Second):
		t.Fatal("the targeted notice never reached its reader's stream")
	}

	// The same create must not have leaked to a stream it did not name.
	ronEvents, ronCancel := service.Subscribe(ron)
	defer ronCancel()
	select {
	case got := <-ronEvents:
		t.Fatalf("ron's stream received %s for a notice that does not target him", got.ID)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestTheInboxSortsAndFiltersByItsOwnColumns pins the account list's
// ordering and filtering: the category filter narrows the page, and the
// sort key orders it against the whitelist the contract names.
func TestTheInboxSortsAndFiltersByItsOwnColumns(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	hermione := seedAccount(t, pool, "hermione", false)
	admin := mustCreate(t, service)

	system, err := service.Create(t.Context(), admin, CreateParams{
		Category:     CategorySystem,
		Title:        "Marauder's Map",
		Body:         "The map names its reader.",
		AudienceKind: AudienceUsers,
		UserIDs:      []uuid.UUID{hermione},
	})
	require.NoError(t, err)
	created(t, service, "Expecto Patronum")
	created(t, service, "The Invisible Book")

	inbox, _, err := service.ListInbox(t.Context(), hermione, false, CategorySystem, "", false, 1, 20)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	assert.Equal(t, system.ID, inbox[0].ID)

	inbox, _, err = service.ListInbox(t.Context(), hermione, false, "", "title", true, 1, 20)
	require.NoError(t, err)
	require.Len(t, inbox, 3)
	assert.Equal(t, "Expecto Patronum", inbox[0].Title)
	assert.Equal(t, "Marauder's Map", inbox[1].Title)
	assert.Equal(t, "The Invisible Book", inbox[2].Title)

	inbox, _, err = service.ListInbox(t.Context(), hermione, false, "", "audience_kind", false, 1, 20)
	require.NoError(t, err)
	require.Len(t, inbox, 3)
	assert.Equal(t, "The Invisible Book", inbox[0].Title, "a sort key the contract does not name falls back to the creation order")
}

// TestCreateSystemNoticeAddressesOneAccountWithoutACreator pins the
// automated notice: the row lands in exactly the addressee's inbox, no
// creator is named, and the live tail carries it — the shape the upload
// finished notice rides.
func TestCreateSystemNoticeAddressesOneAccountWithoutACreator(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	hermione := seedAccount(t, pool, "hermione", false)
	ron := seedAccount(t, pool, "ron", false)

	require.NoError(t, service.CreateSystemNotice(t.Context(), hermione,
		"Upload complete", "Your file pictures/hermione.png is ready."))

	inbox, _, err := service.ListInbox(t.Context(), hermione, false, CategorySystem, "", false, 1, 20)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	assert.Equal(t, "Upload complete", inbox[0].Title)
	assert.Empty(t, inbox[0].CreatedBy,
		"the system is the author; no operator's identifier travels on it")

	inbox, _, err = service.ListInbox(t.Context(), ron, false, CategorySystem, "", false, 1, 20)
	require.NoError(t, err)
	assert.Empty(t, inbox,
		"the notice is addressed, not broadcast: another account reads none")
}
