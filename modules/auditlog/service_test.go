package auditlog_test

import (
	"log/slog"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/samber/do/v2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	auditlogv1connect "github.com/riipandi/tango/codegen/proto/go/tango/auditlog/v1/auditlogv1connect"
	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/auditlog"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/testutils"
)

// The fixture accounts. Their names come from the test-copywriting convention;
// the identifiers are fixed so a record can name one without reading it back.
const (
	hermioneID = "01a0da1c-cb41-779d-bd02-99b3eb5da32a"
	langdonID  = "01a0da2e-1111-7aaa-8bbb-000000000001"
	vetraID    = "01a0da3f-2222-7ccc-8ddd-000000000002"
)

// migratedPool opens a migrated database with the fixture accounts, which the
// records' foreign key requires.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	pool := testutils.MigratedPostgres(t, "auditlog_test")

	for id, name := range map[string]string{
		hermioneID: "hermione",
		langdonID:  "langdon",
		vetraID:    "vetra",
	} {
		_, err := pool.Exec(t.Context(), `
			INSERT INTO public.users (id, username, email, first_name, last_name, display_name)
			VALUES ($1, $2::citext, $2::text || '@example.com', 'Hermione', 'Granger', 'Hermione Granger')`, id, name)
		require.NoError(t, err)
	}
	return pool
}

// record writes one record through the shared recorder, the way a feature
// does, so the tests read what the writer actually produces.
func record(t *testing.T, pool *datastore.Postgres, entry audit.Entry) {
	t.Helper()

	audit.NewRecorder(slog.New(slog.DiscardHandler)).Record(t.Context(), pool, entry)
}

// wireOf renders an account's row identifier in the wire form the contract's
// filter carries, the shape the real caller presents.
func wireOf(raw string) string {
	id, err := user.IDFromUUIDString(raw)
	if err != nil {
		panic(err)
	}
	return id.String()
}

func newService(pool *datastore.Postgres) *auditlog.Service {
	return auditlog.NewService(pool, auditlog.NewRepository(), slog.New(slog.DiscardHandler))
}

// TestListAnswersTheCallersOwnActivityOnly is the self-service view's whole
// contract: the scope is the account, so another account's records are not in
// the page however recent they are.
func TestListAnswersTheCallersOwnActivityOnly(t *testing.T) {
	pool := migratedPool(t)
	record(t, pool, audit.Entry{Event: audit.EventSignIn, UserID: hermioneID})
	record(t, pool, audit.Entry{Event: audit.EventSignIn, UserID: langdonID})

	logs, metadata, err := newService(pool).List(t.Context(),
		auditlog.Scope{UserID: wireOf(hermioneID)}, "", false, 1, 20)
	require.NoError(t, err)

	require.Len(t, logs, 1)
	assert.Equal(t, "hermione", logs[0].Username)
	assert.Equal(t, "sign_in", logs[0].Event)
	require.NotNil(t, metadata.TotalItems)
	assert.Equal(t, 1, *metadata.TotalItems, "the count must be scoped the way the page is")
}

// TestListOrdersNewestFirst pins the ordering a reader relies on. The
// identifier breaks a tie, so the page is stable across calls.
func TestListOrdersNewestFirst(t *testing.T) {
	pool := migratedPool(t)
	for _, event := range []string{
		audit.EventAccountCreated,
		audit.EventAccountUpdated,
		audit.EventAccountDeleted,
	} {
		record(t, pool, audit.Entry{Event: event, UserID: hermioneID})
	}

	logs, _, err := newService(pool).List(t.Context(), auditlog.Scope{UserID: wireOf(hermioneID)}, "", false, 1, 20)
	require.NoError(t, err)

	require.Len(t, logs, 3)
	for i := 1; i < len(logs); i++ {
		assert.False(t, logs[i].CreatedAt.After(logs[i-1].CreatedAt),
			"record %d is newer than the one before it", i)
	}
}

// TestListPagesTheWindow keeps the pagination honest: the second page holds
// what the first did not, and the total counts every record rather than the
// page.
func TestListPagesTheWindow(t *testing.T) {
	pool := migratedPool(t)
	for range 5 {
		record(t, pool, audit.Entry{Event: audit.EventSignIn, UserID: hermioneID})
	}

	first, metadata, err := newService(pool).List(t.Context(), auditlog.Scope{UserID: wireOf(hermioneID)}, "", false, 1, 2)
	require.NoError(t, err)
	require.Len(t, first, 2)
	require.NotNil(t, metadata.TotalItems)
	assert.Equal(t, 5, *metadata.TotalItems)
	require.NotNil(t, metadata.TotalPages)
	assert.Equal(t, 3, *metadata.TotalPages)

	second, _, err := newService(pool).List(t.Context(), auditlog.Scope{UserID: wireOf(hermioneID)}, "", false, 2, 2)
	require.NoError(t, err)
	require.Len(t, second, 2)

	firstIDs := map[string]bool{first[0].ID: true, first[1].ID: true}
	for _, log := range second {
		assert.False(t, firstIDs[log.ID], "page two repeated a record from page one")
	}
}

// TestTheAdministrativeFiltersNarrowTheList covers the three filters the
// administrator's view offers, one at a time.
func TestTheAdministrativeFiltersNarrowTheList(t *testing.T) {
	pool := migratedPool(t)
	record(t, pool, audit.Entry{Event: audit.EventSignIn, UserID: hermioneID})
	record(t, pool, audit.Entry{Event: audit.EventAccountUpdated, UserID: hermioneID})
	record(t, pool, audit.Entry{Event: audit.EventSignIn, UserID: langdonID})

	service := newService(pool)

	byEvent, _, err := service.List(t.Context(), auditlog.Scope{Event: audit.EventSignIn}, "", false, 1, 20)
	require.NoError(t, err)
	assert.Len(t, byEvent, 2, "the event filter must narrow to one event")

	byUser, _, err := service.List(t.Context(), auditlog.Scope{UserID: wireOf(langdonID)}, "", false, 1, 20)
	require.NoError(t, err)
	assert.Len(t, byUser, 1, "the account filter must narrow to one account")

	bySearch, _, err := service.List(t.Context(), auditlog.Scope{Search: "herm"}, "", false, 1, 20)
	require.NoError(t, err)
	assert.Len(t, bySearch, 2, "the search must match the account's name")

	byEmail, _, err := service.List(t.Context(), auditlog.Scope{Search: "langdon@example"}, "", false, 1, 20)
	require.NoError(t, err)
	assert.Len(t, byEmail, 1, "the search must match the account's address too")

	every, _, err := service.List(t.Context(), auditlog.Scope{}, "", false, 1, 20)
	require.NoError(t, err)
	assert.Len(t, every, 3, "an unfiltered scope is every record")
}

// TestTheSearchCountsWhatThePageShows pins the join the search needs: the
// count must narrow the same way the page does, or the metadata would describe
// a set the caller cannot page to.
func TestTheSearchCountsWhatThePageShows(t *testing.T) {
	pool := migratedPool(t)
	record(t, pool, audit.Entry{Event: audit.EventSignIn, UserID: hermioneID})
	record(t, pool, audit.Entry{Event: audit.EventSignIn, UserID: langdonID})
	// A record with no account at all, which the search's join would drop.
	record(t, pool, audit.Entry{Event: audit.EventSignIn})

	_, metadata, err := newService(pool).List(t.Context(), auditlog.Scope{Search: "hermione"}, "", false, 1, 20)
	require.NoError(t, err)

	require.NotNil(t, metadata.TotalItems)
	assert.Equal(t, 1, *metadata.TotalItems)
}

// TestTheRecordCarriesTheActorTheWriterStored pins the payload read: the
// administrator behind a delegated action travels in the payload, and the
// service lifts it into fields of its own so a client does not have to know
// how the writer spelled it.
func TestTheRecordCarriesTheActorTheWriterStored(t *testing.T) {
	pool := migratedPool(t)
	record(t, pool, audit.Entry{
		Event:  audit.EventAccountUpdated,
		UserID: hermioneID,
		Payload: map[string]string{
			audit.PayloadActorID:       langdonID,
			audit.PayloadActorUsername: "langdon",
			"changed":                  "display_name",
		},
	})

	logs, _, err := newService(pool).List(t.Context(), auditlog.Scope{UserID: wireOf(hermioneID)}, "", false, 1, 20)
	require.NoError(t, err)

	require.Len(t, logs, 1)
	assert.Equal(t, langdonID, logs[0].ActorID)
	assert.Equal(t, "langdon", logs[0].ActorUsername)
	// The payload travels as it was stored: the actor is lifted out for
	// convenience, not removed.
	assert.Equal(t, "display_name", logs[0].Payload["changed"])
}

// TestTheAddressIsStoredWithoutItsMask keeps the INET column's own rendering
// out of the response: a cast would append `/32`, which is not the address a
// client asked for.
func TestTheAddressIsStoredWithoutItsMask(t *testing.T) {
	pool := migratedPool(t)
	record(t, pool, audit.Entry{
		Event:  audit.EventSignIn,
		UserID: hermioneID,
		Client: audit.ClientInfo{IPAddress: "203.0.113.7", UserAgent: "Mozilla/5.0 Firefox/128.0"},
	})

	logs, _, err := newService(pool).List(t.Context(), auditlog.Scope{UserID: wireOf(hermioneID)}, "", false, 1, 20)
	require.NoError(t, err)

	require.Len(t, logs, 1)
	assert.Equal(t, "203.0.113.7", logs[0].IPAddress)
	assert.Contains(t, logs[0].UserAgent, "Firefox")
}

// TestTheUsernameIsEmptyOnceTheAccountIsGone pins the join's left side: a
// record outlives its account — the column is `ON DELETE SET NULL` — so it
// stays in the list with no name rather than disappearing with the account.
func TestTheUsernameIsEmptyOnceTheAccountIsGone(t *testing.T) {
	pool := migratedPool(t)
	record(t, pool, audit.Entry{Event: audit.EventSignIn, UserID: vetraID})

	_, err := pool.Exec(t.Context(), `DELETE FROM public.users WHERE id = $1`, vetraID)
	require.NoError(t, err)

	logs, _, err := newService(pool).List(t.Context(), auditlog.Scope{}, "", false, 1, 20)
	require.NoError(t, err)

	require.Len(t, logs, 1, "the record must survive its account")
	assert.Empty(t, logs[0].Username)
	assert.Empty(t, logs[0].UserID, "the column was set to NULL, so the record names no account")
}

// TestFilterOptionsAnswersWhatTheTableHolds is the facets' contract: the
// events and accounts come from the records, not from a list the server keeps
// in step with a contract.
func TestFilterOptionsAnswersWhatTheTableHolds(t *testing.T) {
	pool := migratedPool(t)
	record(t, pool, audit.Entry{Event: audit.EventSignIn, UserID: hermioneID})
	record(t, pool, audit.Entry{Event: audit.EventAccountCreated, UserID: langdonID})
	record(t, pool, audit.Entry{Event: audit.EventSignIn, UserID: langdonID})
	// A record with no account contributes no user option.
	record(t, pool, audit.Entry{Event: audit.EventSignIn})

	events, users, err := newService(pool).Options(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []string{audit.EventAccountCreated, audit.EventSignIn}, events,
		"the events are distinct and sorted")
	require.Len(t, users, 2)
	// Sorted by username, so the order is the one a filter control renders.
	assert.Equal(t, "hermione", users[0].Username)
	assert.Equal(t, hermioneID, users[0].ID)
	assert.Equal(t, "langdon", users[1].Username)
}

// TestAnEmptyTableAnswersEmptyFacets keeps a fresh deployment from answering
// null where a client expects a list.
func TestAnEmptyTableAnswersEmptyFacets(t *testing.T) {
	pool := migratedPool(t)

	events, users, err := newService(pool).Options(t.Context())
	require.NoError(t, err)

	assert.Empty(t, events)
	assert.Empty(t, users)
}

// TestAnUnsetPageAnswersTheDefaultWindow keeps a client that omits the page
// from getting nothing: the window is a display choice, and its default is
// the first page.
func TestAnUnsetPageAnswersTheDefaultWindow(t *testing.T) {
	pool := migratedPool(t)
	record(t, pool, audit.Entry{Event: audit.EventSignIn, UserID: hermioneID})

	logs, metadata, err := newService(pool).List(t.Context(), auditlog.Scope{UserID: wireOf(hermioneID)}, "", false, 0, 0)
	require.NoError(t, err)

	assert.Len(t, logs, 1)
	require.NotNil(t, metadata.Page)
	assert.Equal(t, auditlog.DefaultPage, *metadata.Page)
	require.NotNil(t, metadata.Limit)
	assert.Equal(t, auditlog.DefaultLimit, *metadata.Limit)
}

// TestTheModuleClaimsEveryProcedureTheContractDeclares is the forwarding
// check: the module mounts each procedure the generated handler answers, so a
// procedure added to the contract without a mount is caught here rather than
// answering "unknown procedure" in a running server.
func TestTheModuleClaimsEveryProcedureTheContractDeclares(t *testing.T) {
	module := auditlog.NewModule(nil, slog.New(slog.DiscardHandler))

	router := chi.NewRouter()
	module.MountRPC(router)

	claimed := map[string]bool{}
	for _, route := range router.Routes() {
		claimed[route.Pattern] = true
	}

	for _, procedure := range []string{
		auditlogv1connect.AuditLogServiceListProcedure,
		auditlogv1connect.AuditLogServiceListAllProcedure,
		auditlogv1connect.AuditLogServiceListForUserProcedure,
		auditlogv1connect.AuditLogServiceFilterOptionsProcedure,
	} {
		assert.True(t, claimed[procedure], "the module must claim %s", procedure)
	}
	assert.Len(t, claimed, 4, "the module must claim the contract's procedures and no more")
}

// TestTheAreaForwardsTheServiceThroughTheContainer is the wiring check the
// registry relies on: the area's Package registers the service and its Mount
// resolves it, so a provider missed there leaves the procedures answering
// "unknown procedure" — a hand-built Deps would pass while the wiring is
// broken.
func TestTheAreaForwardsTheServiceThroughTheContainer(t *testing.T) {
	pool := migratedPool(t)
	cfg := config.Config{}
	logger := slog.New(slog.DiscardHandler)

	i := do.New(
		do.Eager(&cfg),
		do.Eager(logger),
		do.Eager(pool),
	)
	auditlog.Package(i)

	module, err := auditlog.Mount(i)
	require.NoError(t, err)
	assert.Equal(t, auditlog.ModuleName, module.Name())
}
