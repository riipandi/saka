package transport_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/queue"
	"github.com/riipandi/saka/internal/transport"
	"github.com/riipandi/saka/pkg/testutils"
)

// TestRPCRefusesTheEngineProceduresWithoutAToken proves the guard on the
// queue and scheduler surface: an anonymous caller never reaches the
// handler, the same 401 every administrative procedure answers.
func TestRPCRefusesTheEngineProceduresWithoutAToken(t *testing.T) {
	router := newRPCRouterWithAuth(t, stubAuthenticator())

	for _, procedure := range []string{
		"/saka.system.v1.QueueService/ListQueues",
		"/saka.system.v1.QueueService/ListTasks",
		"/saka.system.v1.SchedulerService/ListJobs",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, rpcRequest(t, procedure, "{}"))
		require.Equal(t, http.StatusUnauthorized, rec.Code, procedure)
	}
}

// TestRPCQueueProceduresAnswerWithoutTheEngine proves the wiring answer: an
// administrator reaches the handler, and a router composed without the
// queue client says the engine is unavailable rather than pretending the
// queue is empty — the state a bare test router is in.
func TestRPCQueueProceduresAnswerWithoutTheEngine(t *testing.T) {
	router := newRPCRouterWithAuth(t, stubAuthenticator())

	req := rpcRequest(t, "/saka.system.v1.QueueService/ListQueues", "{}")
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())

	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "unavailable", body.Code)
	assert.Contains(t, body.Message, "queue engine")
}

// TestRPCSchedulerProceduresAnswerWithoutTheEngine is the scheduler's half
// of the wiring answer.
func TestRPCSchedulerProceduresAnswerWithoutTheEngine(t *testing.T) {
	router := newRPCRouterWithAuth(t, stubAuthenticator())

	req := rpcRequest(t, "/saka.system.v1.SchedulerService/ListJobs", "{}")
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())

	var body struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "unavailable", body.Code)
}

// adminAuditProbeTask is a task the audit test enqueues to have something to
// cancel. The queue never runs it: the dispatcher stays stopped.
type adminAuditProbeTask struct{}

func (adminAuditProbeTask) Config() queue.QueueConfig {
	return queue.QueueConfig{Name: "admin_audit_probe", MaxAttempts: 1}
}

// adminAuditRouter mounts the queue and scheduler procedures over a real
// engine, the pool and the recorder a run wires — the destructive actions'
// records are the subject of the tests below.
func adminAuditRouter(t *testing.T) (http.Handler, *datastore.Postgres, *queue.Client) {
	t.Helper()
	testutils.SkipWithoutDocker(t)

	pool := sessionPool(t)
	client, err := queue.NewClient(queue.ClientConfig{
		Store:        pool,
		NumWorkers:   1,
		ReleaseAfter: time.Hour,
	})
	require.NoError(t, err)

	router := transport.NewRouter(transport.Options{
		Config:        config.Default(),
		Logger:        slog.Default(),
		Authenticator: callerAuthenticator(wireID(t, langdonAccount), true, false),
		QueueClient:   client,
		DB:            pool,
		Audit:         audit.NewRecorder(slog.Default()),
	})
	return router, pool, client
}

// adminAuditRecordCount counts the audit rows one event name left behind.
// A replay or a flush names no single resource, so the count is the fact.
func adminAuditRecordCount(t *testing.T, pool *datastore.Postgres, event string) int {
	t.Helper()
	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs WHERE event = $1`, event).Scan(&count))
	return count
}

// adminAuditRecordID reads the resource id the one record an event left
// behind — the cancelled task's, the only one of these actions that names a
// row.
func adminAuditRecordID(t *testing.T, pool *datastore.Postgres, event string) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT resource_id FROM public.audit_logs WHERE event = $1 ORDER BY id`, event)
	require.NoError(t, err)
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id *string
		require.NoError(t, rows.Scan(&id))
		if id != nil {
			ids = append(ids, *id)
		}
	}
	require.NoError(t, rows.Err())
	return ids
}

// TestADestructiveQueueActionLeavesAnAuditRecord covers the trail the
// engine's own administrative surface owes: a cancelled task, a replay, and
// both flushes each write their event, with the counts the reader audits for.
func TestADestructiveQueueActionLeavesAnAuditRecord(t *testing.T) {
	router, pool, client := adminAuditRouter(t)

	saved, err := client.Add(adminAuditProbeTask{}).Save()
	require.NoError(t, err)
	require.Len(t, saved, 1)

	// Save answers the raw UUID; the wire carries the `que_` TypeID the
	// handler decodes.
	raw, err := uuid.Parse(saved[0])
	require.NoError(t, err)
	wire := queue.FormatID(raw)

	call := func(procedure, body string) {
		rec := httptest.NewRecorder()
		req := rpcRequest(t, procedure, body)
		req.Header.Set("Authorization", "Bearer secret")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}

	call("/saka.system.v1.QueueService/CancelTask", `{"id":"`+wire+`"}`)
	call("/saka.system.v1.QueueService/ReplayDeadTasks", `{"queue":"admin_audit_probe"}`)
	call("/saka.system.v1.QueueService/FlushPendingTasks", `{}`)
	call("/saka.system.v1.QueueService/FlushCompletedTasks", `{}`)

	// The cancelled record names the task by the row's own identifier — the
	// raw UUID, the form a UUID column holds — not the wire form.
	assert.Equal(t, []string{saved[0]},
		adminAuditRecordID(t, pool, audit.EventQueueTaskCancelled))
	assert.Equal(t, 1, adminAuditRecordCount(t, pool, audit.EventQueueDeadReplayed))
	assert.Equal(t, 1, adminAuditRecordCount(t, pool, audit.EventQueuePendingFlushed))
	assert.Equal(t, 1, adminAuditRecordCount(t, pool, audit.EventQueueCompletedFlushed))
}

// refusingStore is a queue.Store whose every answer is the same failure —
// the stand-in for a database the engine cannot reach, so a test can prove
// the wire rule without closing a real pool.
type refusingStore struct{}

func (refusingStore) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("closed pool")
}

func (refusingStore) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("closed pool")
}

func (refusingStore) QueryRow(context.Context, string, ...any) pgx.Row {
	return failingRow{}
}

func (refusingStore) WithTx(context.Context, func(context.Context, datastore.Querier) error) error {
	return errors.New("closed pool")
}

// failingRow is the QueryRow answer of the refusing store.
type failingRow struct{}

func (failingRow) Scan(...any) error { return errors.New("closed pool") }

// TestTheStaticInternalAnswerCarriesNoDriverText pins the wire rule: an
// engine failure answers the one static message, and the cause lives only in
// the process log.
func TestTheStaticInternalAnswerCarriesNoDriverText(t *testing.T) {
	client, err := queue.NewClient(queue.ClientConfig{
		Store:        refusingStore{},
		NumWorkers:   1,
		ReleaseAfter: time.Hour,
	})
	require.NoError(t, err)
	// A registered queue is what makes the counts run: without one the page
	// is empty and never touches the store.
	client.Register(queue.NewQueue[adminAuditProbeTask](func(context.Context, adminAuditProbeTask) error {
		return nil
	}))

	router := transport.NewRouter(transport.Options{
		Config:        config.Default(),
		Authenticator: callerAuthenticator(wireID(t, langdonAccount), true, false),
		QueueClient:   client,
		Audit:         audit.NewRecorder(nil),
	})

	rec := httptest.NewRecorder()
	req := rpcRequest(t, "/saka.system.v1.QueueService/ListQueues", "{}")
	req.Header.Set("Authorization", "Bearer secret")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "internal", body.Code)
	assert.Equal(t, "internal error", body.Message)
}
