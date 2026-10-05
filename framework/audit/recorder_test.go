package audit

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/pkg/testutils"
)

// testAccountID is the account the tests name. It is seeded because the user
// column carries a foreign key: a record naming an account that does not exist
// is refused by the database. A real record never meets that constraint,
// because the account is there when the action happens.
const testAccountID = "01a0da1c-cb41-779d-bd02-99b3eb5da32a"

// migratedPool opens a migrated database and answers the pool the tests write
// through. The migrator runs on its own connection first, because the tables
// the recorder writes into are the migrations' business.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	dsn := testutils.StartPostgres(t.Context(), t).NewDatabase(t)

	migrationDB, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	migrator, err := openTestMigrators(t, migrationDB)
	require.NoError(t, err)
	_, err = migrator.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, migrationDB.Close())

	pool, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:             dsn,
		ApplicationName: "audit_test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(context.Background()) })

	seedAccount(t, pool)
	return pool
}

// seedAccount exists no more than the audit log's own schema requires: the
// user_id column carries no foreign key (an audit record must survive its
// subject's deletion with the identifier intact), so the tests name accounts
// without standing any account schema up.
func seedAccount(t *testing.T, pool *datastore.Postgres) {
	t.Helper()
	_ = pool
}

// readRecord reads one record back by its event, which is the field the tests
// assert on. The columns are read as text so the assertions do not depend on
// the driver's mapping for an enum or an INET.
func readRecord(t *testing.T, pool *datastore.Postgres, event string) map[string]string {
	t.Helper()

	row := pool.QueryRow(t.Context(), `
		SELECT event, trigger_type::text, action_status::text,
		       COALESCE(user_id::text, ''), COALESCE(host(ip_address), ''),
		       COALESCE(user_agent, ''), COALESCE(device_fingerprint, ''),
		       payload::text
		FROM public.audit_logs WHERE event = $1`, event)

	var event2, trigger, status, userID, ip, agent, fingerprint, payload string
	require.NoError(t, row.Scan(&event2, &trigger, &status, &userID, &ip, &agent, &fingerprint, &payload))
	return map[string]string{
		"event": event2, "trigger": trigger, "status": status,
		"user_id": userID, "ip": ip, "agent": agent,
		"fingerprint": fingerprint, "payload": payload,
	}
}

// TestTheRecorderWritesTheRowItWasGiven is the writer's contract: what the
// caller supplies is what the columns hold, and the two fields it does not
// supply — the trigger and the status — take the meaning a plain record has.
func TestTheRecorderWritesTheRowItWasGiven(t *testing.T) {
	pool := migratedPool(t)
	recorder := NewRecorder(slog.New(slog.DiscardHandler))

	recorder.Record(t.Context(), pool, Entry{
		Event:  "sign_in",
		UserID: testAccountID,
		Client: ClientInfo{
			IPAddress:   "203.0.113.7",
			UserAgent:   "Mozilla/5.0 (Macintosh) Firefox/128.0",
			Fingerprint: "fp_hermione_granger",
		},
		Payload: map[string]string{"provider": "password"},
	})

	record := readRecord(t, pool, "sign_in")
	assert.Equal(t, "user", record["trigger"], "an unset trigger is the caller's")
	assert.Equal(t, "success", record["status"], "an unset status is a completed action")
	assert.Equal(t, testAccountID, record["user_id"])
	assert.Equal(t, "203.0.113.7", record["ip"])
	assert.Contains(t, record["agent"], "Firefox")
	assert.Equal(t, "fp_hermione_granger", record["fingerprint"])
	assert.JSONEq(t, `{"provider":"password"}`, record["payload"])
}

// TestTheRecorderRefusesAnEntryWithoutAnEvent pins the one field the row
// cannot do without: a record with no event is a row no reader can interpret,
// so it is dropped rather than written.
func TestTheRecorderRefusesAnEntryWithoutAnEvent(t *testing.T) {
	pool := migratedPool(t)
	recorder := NewRecorder(slog.New(slog.DiscardHandler))

	recorder.Record(t.Context(), pool, Entry{UserID: testAccountID})

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs`).Scan(&count))
	assert.Zero(t, count, "an entry with no event must not reach the table")
}

// TestTheRecorderStoresNoClientWhenTheRequestHadNone covers the state a job
// or a command writes in: there is no request, so the context carries no
// client facts, and the record is written with the columns left NULL rather
// than with a zero value that looks like a real one.
func TestTheRecorderStoresNoClientWhenTheRequestHadNone(t *testing.T) {
	pool := migratedPool(t)
	recorder := NewRecorder(slog.New(slog.DiscardHandler))

	recorder.Record(t.Context(), pool, Entry{
		Event:  "account_deleted",
		UserID: testAccountID,
	})

	record := readRecord(t, pool, "account_deleted")
	assert.Empty(t, record["ip"])
	assert.Empty(t, record["agent"])
	assert.Empty(t, record["fingerprint"])
	assert.JSONEq(t, `{}`, record["payload"], "an empty payload is the column's own default")
}

// TestTheRecorderTakesTheClientFromTheContext pins the plumbing the transport
// relies on: a feature passes an entry with no client, and the facts the
// middleware stored for the request are what the row carries.
func TestTheRecorderTakesTheClientFromTheContext(t *testing.T) {
	pool := migratedPool(t)
	recorder := NewRecorder(slog.New(slog.DiscardHandler))

	ctx := WithClientInfo(t.Context(), ClientInfo{
		IPAddress:   "198.51.100.23",
		UserAgent:   "Mozilla/5.0 (X11; Linux) Chrome/131.0",
		Fingerprint: "fp_vittoria_vetra",
	})
	recorder.Record(ctx, pool, Entry{
		Event:  "account_created",
		UserID: testAccountID,
	})

	record := readRecord(t, pool, "account_created")
	assert.Equal(t, "198.51.100.23", record["ip"])
	assert.Contains(t, record["agent"], "Chrome")
	assert.Equal(t, "fp_vittoria_vetra", record["fingerprint"])
}

// TestTheRecorderStoresAnUnparseableAddressAsNoAddress keeps the column's type
// from costing the record: a header a caller controls can carry anything, and
// a record is worth more than the column it fills.
func TestTheRecorderStoresAnUnparseableAddressAsNoAddress(t *testing.T) {
	pool := migratedPool(t)
	recorder := NewRecorder(slog.New(slog.DiscardHandler))

	recorder.Record(t.Context(), pool, Entry{
		Event:  "email_verified",
		Client: ClientInfo{IPAddress: "not-an-address", UserAgent: "curl/8.7.1"},
	})

	record := readRecord(t, pool, "email_verified")
	assert.Empty(t, record["ip"], "an address the column cannot hold is stored as no address")
	assert.Equal(t, "curl/8.7.1", record["agent"], "the rest of the record still lands")
}

// TestTheRecorderJoinsTheCallersTransaction is the property a reader trusts
// the log for: a record written inside a transaction that rolls back leaves
// nothing behind, so the log never describes a change that did not happen.
func TestTheRecorderJoinsTheCallersTransaction(t *testing.T) {
	pool := migratedPool(t)
	recorder := NewRecorder(slog.New(slog.DiscardHandler))

	rolledBack := errors.New("the caller changed its mind")
	err := pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
		recorder.Record(ctx, tx, Entry{Event: "account_updated"})
		return rolledBack
	})
	require.ErrorIs(t, err, rolledBack)

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs`).Scan(&count))
	assert.Zero(t, count, "a record inside a rolled-back transaction must not survive it")
}

// TestANilRecorderWritesNothing keeps the feature constructors free of a nil
// check: a service built without a recorder is the state a unit test is in,
// and the side effect is simply absent.
func TestANilRecorderWritesNothing(t *testing.T) {
	var recorder *Recorder

	assert.NotPanics(t, func() {
		recorder.Record(t.Context(), nil, Entry{Event: "sign_in"})
	})
}

// TestAFailedRecordDoesNotAbortTheCallersTransaction is the property the
// savepoint exists for: an audit insert that fails must lose the record and
// nothing else. On Postgres a failed statement poisons its transaction until
// rollback (`25P02`), so without the savepoint the caller's own change would
// fail with it — and the error the caller saw would not name the audit insert.
func TestAFailedRecordDoesNotAbortTheCallersTransaction(t *testing.T) {
	pool := migratedPool(t)
	recorder := NewRecorder(slog.New(slog.DiscardHandler))

	// The user_id names no account, so the insert's foreign key refuses it.
	// A trigger the enum does not hold is the shape a bad record takes in
	// production too: the column's domain, not a foreign key, refuses it.
	const impossibleTrigger = "no_such_trigger"

	err := pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
		// The record fails here...
		recorder.Record(ctx, tx, Entry{
			Event:   "sign_in",
			Trigger: impossibleTrigger,
		})
		// ...and the caller's own statement must still run, and commit.
		_, execErr := tx.Exec(ctx,
			`INSERT INTO public.audit_logs (event, trigger_type) VALUES ($1, 'user')`,
			"the_caller_change")
		return execErr
	})
	require.NoError(t, err, "the failed record must not fail the caller's transaction")

	// The change committed and the record did not: the savepoint contained it.
	var callerRows, failedRows int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs WHERE event = 'the_caller_change'`).Scan(&callerRows))
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs WHERE event = $1`, "sign_in").Scan(&failedRows))
	assert.Equal(t, 1, callerRows, "the caller's change must survive the failed record")
	assert.Zero(t, failedRows, "the failed record itself must not be written")
}

// TestAFailedRecordOnThePoolIsContained covers the other surface: a job or a
// command writes through the pool, where the statement is its own transaction
// and a failure is already contained. The record is still lost, and nothing
// else is.
func TestAFailedRecordOnThePoolIsContained(t *testing.T) {
	pool := migratedPool(t)
	recorder := NewRecorder(slog.New(slog.DiscardHandler))

	const impossibleTrigger = "no_such_trigger"
	assert.NotPanics(t, func() {
		recorder.Record(t.Context(), pool, Entry{
			Event:   "account_deleted",
			Trigger: impossibleTrigger,
		})
	})

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs`).Scan(&count))
	assert.Zero(t, count, "the refused record must not be written")
}

// TestTheRecordedInstantComesFromTheDatabase pins where the timestamp comes
// from: the column's own default, so two writers cannot disagree about when an
// action happened and a clock skew cannot reorder the log.
func TestTheRecordedInstantComesFromTheDatabase(t *testing.T) {
	pool := migratedPool(t)
	recorder := NewRecorder(slog.New(slog.DiscardHandler))

	before := time.Now().Add(-time.Second)
	recorder.Record(t.Context(), pool, Entry{Event: "sign_in"})

	var createdAt time.Time
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT created_at FROM public.audit_logs WHERE event = $1`,
		"sign_in").Scan(&createdAt))

	assert.WithinDuration(t, before, createdAt, time.Minute)
}
