package transport_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/authn"
	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	notificationv1 "github.com/riipandi/tango/codegen/proto/go/tango/notification/v1"
	notificationv1connect "github.com/riipandi/tango/codegen/proto/go/tango/notification/v1/notificationv1connect"
	"github.com/riipandi/tango/database"
	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/health"
	"github.com/riipandi/tango/internal/kernel"
	"github.com/riipandi/tango/internal/transport"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/modules/notification"
	"github.com/riipandi/tango/pkg/jwtutils"
	"github.com/riipandi/tango/pkg/testutils"

	"uuid"
)

// The fixture accounts, named after the test-copywriting convention.
const (
	hermioneNotified = "01a0da3e-1111-7000-8000-000000000001"
	ronNotified      = "01a0da3e-1111-7000-8000-000000000002"
)

// notificationAuthenticator answers the caller the request's token names,
// with the subject in the wire form the account surface reads.
func notificationAuthenticator(subject string, admin bool) transport.Authenticator {
	return func(ctx context.Context, req *http.Request) (any, error) {
		if subject == "" {
			return nil, authn.Errorf("authentication required")
		}
		// The subject travels the wire form the account surface reads, the
		// way the real bearer half writes it.
		id, err := uuid.Parse(subject)
		if err != nil {
			return nil, authn.Errorf("the subject is not an account identifier")
		}
		claims := jwtutils.AccessClaims{Username: subject, IsAdmin: admin}
		return &jwtutils.Caller{UserID: user.FormatID(id), AccessClaims: claims}, nil
	}
}

// notificationPool opens a migrated database with one administrator and one
// plain account, so the guard's two audiences both have a caller.
func notificationPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	dsn := testutils.StartPostgres(t.Context(), t).NewDatabase(t)

	migrationDB, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	migrator, err := database.NewMigrator(t.Context(), migrationDB, database.MigratorOptions{})
	require.NoError(t, err)
	_, err = migrator.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, migrationDB.Close())

	pool, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:             dsn,
		ApplicationName: "transport_notification_test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(t.Context()) })

	_, err = pool.Exec(t.Context(), `
		INSERT INTO public.users (id, username, email, first_name, last_name, display_name, is_admin)
		VALUES
			($1, 'hermione', 'hermione@example.com', 'Hermione', 'Granger', 'Hermione Granger', false),
			($2, 'ron',      'ron@example.com',      'Ron',      'Weasley',  'Ron Weasley',      true)`,
		hermioneNotified, ronNotified)
	require.NoError(t, err)
	return pool
}

// newNotificationRouter mounts the real notification feature over the
// transport's guard, with the caller the test asks for.
func newNotificationRouter(t *testing.T, auth transport.Authenticator, pool *datastore.Postgres) http.Handler {
	t.Helper()

	service := notification.NewService(pool,
		audit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))
	return transport.NewRouter(transport.Options{
		Config:        config.Default(),
		Checker:       health.NewChecker(),
		Authenticator: auth,
		Modules:       []kernel.Module{notification.NewModule(service)},
	})
}

// TestTheNotificationGuardIsDeclared pins the surface's rule: the four
// administrative procedures are the administrator's, and the account
// procedures are every caller's own inbox — the account is the claims'
// subject, so a machine credential reads its inbox the same way a session
// does.
func TestTheNotificationGuardIsDeclared(t *testing.T) {
	pool := notificationPool(t)

	for name, tc := range map[string]struct {
		procedure string
		body      string
		auth      transport.Authenticator
		status    int
		code      string
	}{
		"create without a credential": {
			procedure: notificationv1connect.NotificationServiceCreateNotificationProcedure,
			body:      `{"category":"announcement","title":"t","body":"b"}`,
			auth:      notificationAuthenticator("", false),
			status:    http.StatusUnauthorized,
			code:      "unauthenticated",
		},
		"create without the role": {
			procedure: notificationv1connect.NotificationServiceCreateNotificationProcedure,
			body:      `{"category":"announcement","title":"t","body":"b"}`,
			auth:      notificationAuthenticator(hermioneNotified, false),
			status:    http.StatusNotFound,
			code:      "not_found",
		},
		"list all without the role": {
			procedure: notificationv1connect.NotificationServiceListAllNotificationsProcedure,
			body:      `{}`,
			auth:      notificationAuthenticator(hermioneNotified, false),
			status:    http.StatusNotFound,
			code:      "not_found",
		},
		"cancel without the role": {
			procedure: notificationv1connect.NotificationServiceCancelNotificationProcedure,
			body:      `{"id":"` + notification.FormatID(mustUUID(t, hermioneNotified)) + `"}`,
			auth:      notificationAuthenticator(hermioneNotified, false),
			status:    http.StatusNotFound,
			code:      "not_found",
		},
		"cancel an unknown notification": {
			procedure: notificationv1connect.NotificationServiceCancelNotificationProcedure,
			body:      `{"id":"` + notification.FormatID(mustUUID(t, hermioneNotified)) + `"}`,
			auth:      notificationAuthenticator(ronNotified, true),
			status:    http.StatusNotFound,
			code:      "notification not found",
		},
		"the inbox without a credential": {
			procedure: notificationv1connect.NotificationServiceListNotificationsProcedure,
			body:      `{}`,
			auth:      notificationAuthenticator("", false),
			status:    http.StatusUnauthorized,
			code:      "unauthenticated",
		},
	} {
		t.Run(name, func(t *testing.T) {
			router := newNotificationRouter(t, tc.auth, pool)

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, rpcRequest(t, tc.procedure, tc.body))

			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tc.code)
		})
	}
}

// TestTheNotificationSurfaceIsClaimed is the forwarding contract: every
// procedure the contract declares is answered by the module the area
// mounts, so an area whose wiring lost a procedure fails here rather than
// answering unknown procedure.
func TestTheNotificationSurfaceIsClaimed(t *testing.T) {
	pool := notificationPool(t)
	router := newNotificationRouter(t, notificationAuthenticator(ronNotified, true), pool)

	for _, procedure := range []string{
		notificationv1connect.NotificationServiceCreateNotificationProcedure,
		notificationv1connect.NotificationServiceGetNotificationProcedure,
		notificationv1connect.NotificationServiceListAllNotificationsProcedure,
		notificationv1connect.NotificationServiceCancelNotificationProcedure,
		notificationv1connect.NotificationServiceListNotificationsProcedure,
		notificationv1connect.NotificationServiceMarkNotificationReadProcedure,
		notificationv1connect.NotificationServiceMarkAllNotificationsReadProcedure,
		notificationv1connect.NotificationServiceUnreadCountProcedure,
		notificationv1connect.NotificationServiceWatchNotificationsProcedure,
	} {
		t.Run(procedure, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, rpcRequest(t, procedure, `{}`))

			body := rec.Body.String()
			assert.NotContains(t, body, "unknown procedure",
				"the procedure is not mounted: %s", body)
		})
	}
}

// TestTheInboxAnswersThroughTheSurface runs one account procedure end to
// end: the unread count the create drives is the number the wire carries,
// and the answer spells the snake_case names the shared codec writes.
func TestTheInboxAnswersThroughTheSurface(t *testing.T) {
	pool := notificationPool(t)

	service := notification.NewService(pool,
		audit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))
	router := transport.NewRouter(transport.Options{
		Config:        config.Default(),
		Checker:       health.NewChecker(),
		Authenticator: notificationAuthenticator(hermioneNotified, false),
		Modules:       []kernel.Module{notification.NewModule(service)},
	})

	_, err := service.Create(t.Context(), mustUUID(t, ronNotified), notification.CreateParams{
		Category: notification.CategoryAnnouncement,
		Title:    "The Room of Requirement",
		Body:     "b",
	})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t,
		notificationv1connect.NotificationServiceUnreadCountProcedure, `{}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var counted struct {
		Count string `json:"count"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &counted))
	assert.Equal(t, "1", counted.Count)

	// The inbox answers with the notification the create wrote, named by
	// its snake_case keys.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, rpcRequest(t,
		notificationv1connect.NotificationServiceListNotificationsProcedure, `{}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"audience_kind":"global"`)
	assert.Contains(t, rec.Body.String(), `"title":"The Room of Requirement"`)
	assert.Contains(t, rec.Body.String(), `"id":"ntf_`,
		"the inbox names its rows in the wire form")
	assert.NotContains(t, rec.Body.String(), "read_at",
		"an inbox row nobody read carries no receipt")
}

// TestTheWatchStreamDeliversThroughTheSurface runs the live tail end to
// end: a stream opened over the real router receives the notification a
// create publishes after the stream opened.
func TestTheWatchStreamDeliversThroughTheSurface(t *testing.T) {
	pool := notificationPool(t)

	service := notification.NewService(pool,
		audit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))
	router := transport.NewRouter(transport.Options{
		Config:        config.Default(),
		Checker:       health.NewChecker(),
		Authenticator: notificationAuthenticator(hermioneNotified, false),
		Modules:       []kernel.Module{notification.NewModule(service)},
	})

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	client := notificationv1connect.NewNotificationServiceClient(
		server.Client(), server.URL+transport.RPCPath)
	watchCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := client.WatchNotifications(watchCtx,
		connect.NewRequest(&notificationv1.WatchNotificationsRequest{}))
	require.NoError(t, err)
	defer stream.Close()

	// The create runs after the stream opened, the way a client experiences
	// it: the event is the message the connection exists to carry.
	created, err := service.Create(t.Context(), mustUUID(t, ronNotified), notification.CreateParams{
		Category: notification.CategoryAnnouncement,
		Title:    "The Pensieve",
		Body:     "b",
	})
	require.NoError(t, err)

	// The initial ping confirms the subscription is live; the events that
	// follow carry the notifications created after it.
	for {
		if !stream.Receive() {
			t.Fatalf("the stream closed before the notification arrived: %v", stream.Err())
		}
		event := stream.Msg()
		if event.Kind != "notification" {
			continue
		}
		require.NotNil(t, event.Notification)
		assert.Equal(t, notification.FormatID(created.ID), event.Notification.Id)
		break
	}
}
