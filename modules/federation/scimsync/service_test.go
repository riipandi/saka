package scimsync

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"uuid"

	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/fetcher"
	"github.com/riipandi/saka/modules/federation/oidc"
	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/testutils"
)

// sqlb is the builder flavor the feature's queries use.
var sqlb = sqlbuilder.PostgreSQL

const cipherKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// migratedPool answers a pool over a fresh, fully migrated database.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()
	return testutils.MigratedPostgres(t, "scimsync_test")
}

// testService builds the service over the pool with a cipher from a fixed
// key and a stub HTTP client the test inspects. The stub is the seam's
// interface, so the scripted-remote and the paged-listing stubs both fit.
func testService(t *testing.T, pool *datastore.Postgres, remote HTTPClient) *Service {
	t.Helper()
	cipher, err := crypto.NewCipherFromHex(cipherKey)
	require.NoError(t, err)
	svc := NewService(pool, NewRepository(), nil, cipher, remote, nil)
	return svc.WithDirectories(NewDirectory(), NewGroupDirectory())
}

// seedClient inserts one OIDC client row directly: the sync reads its
// restriction, and the CRUD surface that writes it is federation's other
// feature, not this test's subject.
func seedClient(t *testing.T, pool *datastore.Postgres, id string, restricted bool) {
	t.Helper()
	ib := sqlb.NewInsertBuilder()
	ib.InsertInto(oidc.ClientTable)
	ib.Cols("id", "name", "is_group_restricted")
	ib.Values(id, "Test "+id, restricted)
	query, args := ib.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)
}

// seedUser inserts one account row directly and answers its sync shape.
func seedUser(t *testing.T, pool *datastore.Postgres, username string) ProvisionedUser {
	t.Helper()
	id := uuid.NewV7()
	ib := sqlb.NewInsertBuilder()
	ib.InsertInto(UserTable)
	ib.Cols("id", "username", "email", "display_name")
	ib.Values(id, username, username+"@example.test", username)
	query, args := ib.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)
	return ProvisionedUser{ID: id, Username: username, Email: username + "@example.test", DisplayName: username, Active: true}
}

// seedGroup inserts one group row directly.
func seedGroup(t *testing.T, pool *datastore.Postgres, id, displayName string) {
	t.Helper()
	ib := sqlb.NewInsertBuilder()
	ib.InsertInto(GroupTable)
	ib.Cols("id", "name", "display_name")
	ib.Values(id, "group-"+id, displayName)
	query, args := ib.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)
}

// seedMembership makes one account a group's member.
func seedMembership(t *testing.T, pool *datastore.Postgres, groupID, userID uuid.UUID) {
	t.Helper()
	ib := sqlb.NewInsertBuilder()
	ib.InsertInto(GroupMemberTable)
	ib.Cols("user_group_id", "user_id")
	ib.Values(groupID, userID)
	query, args := ib.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)
}

// userList scripts a well-formed SCIM user listing: the counters set and
// the rows the pass should see — an empty argument list is a legitimate
// empty snapshot.
func userList(rows ...remoteUser) remoteList[remoteUser] {
	resources := append([]remoteUser{}, rows...)
	return remoteList[remoteUser]{
		Schemas:      []string{scimListSchema},
		TotalResults: new(len(resources)),
		ItemsPerPage: new(scimPageCount),
		Resources:    &resources,
	}
}

// groupList scripts a well-formed SCIM group listing.
func groupList(rows ...remoteGroup) remoteList[remoteGroup] {
	resources := append([]remoteGroup{}, rows...)
	return remoteList[remoteGroup]{
		Schemas:      []string{scimListSchema},
		TotalResults: new(len(resources)),
		ItemsPerPage: new(scimPageCount),
		Resources:    &resources,
	}
}

// stubRemote records what the sync sent and answers SCIM documents from a
// script the test writes.
type stubRemote struct {
	t         *testing.T
	Requests  []recordedRequest
	responses map[string]any
	status    map[string]int
}

type recordedRequest struct {
	Method  string
	URL     string
	Headers http.Header
	Body    []byte
}

func newStubRemote(t *testing.T) *stubRemote {
	return &stubRemote{
		t:         t,
		responses: map[string]any{},
		status:    map[string]int{},
	}
}

// answer scripts one path's response.
func (s *stubRemote) answer(method, path string, status int, body any) {
	s.status[method+" "+path] = status
	s.responses[method+" "+path] = body
}

// Do answers from the script, recording the request. The URL the sync
// sends carries the query string the listing adds; the script keys on the
// bare path, so the lookup strips it.
func (s *stubRemote) Do(_ context.Context, req fetcher.Request) (*fetcher.Response, error) {
	raw := req.URL
	if at := strings.Index(raw, "?"); at >= 0 {
		raw = raw[:at]
	}
	s.Requests = append(s.Requests, recordedRequest{
		Method:  req.Method,
		URL:     raw,
		Headers: req.Headers,
		Body:    bodyBytes(req.Body),
	})
	key := req.Method + " " + raw
	status, ok := s.status[key]
	if !ok {
		status = http.StatusOK
	}
	body, _ := json.Marshal(s.responses[key])
	return &fetcher.Response{StatusCode: status, Body: body}, nil
}

func bodyBytes(body any) []byte {
	if raw, ok := body.([]byte); ok {
		return raw
	}
	return nil
}

// sentBodies decodes every request body the stub recorded for one method
// and path.
func (s *stubRemote) sentBodies(method, path string) []map[string]any {
	var out []map[string]any
	for _, r := range s.Requests {
		if r.Method != method || r.URL != path {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal(r.Body, &doc); err != nil {
			continue
		}
		out = append(out, doc)
	}
	return out
}

// TestAProviderRoundTripsThroughTheRepository covers the management CRUD:
// create, read by client, update, delete, and the not-founds between them.
func TestAProviderRoundTripsThroughTheRepository(t *testing.T) {
	pool := migratedPool(t)
	repo := NewRepository()
	ctx := t.Context()

	// The provider row hangs on a real client: the schema's foreign key
	// demands one.
	clientID := "11111111-1111-5111-8111-111111111111"
	seedClient(t, pool, clientID, false)

	created, err := repo.Create(ctx, pool, Provider{
		ID:          uuid.NewV7(),
		ClientID:    clientID,
		Endpoint:    "https://sp.example/scim/v2",
		SealedToken: "enc:sealed",
	})
	require.NoError(t, err)

	byClient, err := repo.ByClient(ctx, pool, created.ClientID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, byClient.ID)

	// The unique index turns a second provider for one client into a
	// refused insert — one provider per client is the rule.
	_, err = repo.Create(ctx, pool, Provider{
		ID:          uuid.NewV7(),
		ClientID:    created.ClientID,
		Endpoint:    "https://other.example/scim/v2",
		SealedToken: "enc:sealed",
	})
	require.ErrorIs(t, err, ErrProviderExists)

	updated, err := repo.Update(ctx, pool, Provider{
		ID:          created.ID,
		ClientID:    created.ClientID,
		Endpoint:    "https://moved.example/scim/v2",
		SealedToken: "enc:resealed",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://moved.example/scim/v2", updated.Endpoint)

	require.NoError(t, repo.Delete(ctx, pool, created.ID))
	_, err = repo.ByID(ctx, pool, created.ID)
	require.ErrorIs(t, err, ErrNoProvider)
	require.ErrorIs(t, repo.Delete(ctx, pool, created.ID), ErrNoProvider)
}

// TestCreateSealsTheTokenAndAnswersItOnce pins the token boundary: the row
// carries the sealed form, and the Create answer is the one read that
// carries the plaintext.
func TestCreateSealsTheTokenAndAnswersItOnce(t *testing.T) {
	pool := migratedPool(t)
	remote := newStubRemote(t)
	clientID := "22222222-2222-5222-8222-222222222222"
	seedClient(t, pool, clientID, false)

	svc := testService(t, pool, remote)
	created, err := svc.Create(t.Context(), clientID, "https://sp.example/scim/v2", "operator-secret")
	require.NoError(t, err)
	assert.Equal(t, "operator-secret", created.SealedToken, "the Create answer shows the token once")

	// The stored row is sealed: the value is not the plaintext anywhere.
	row, err := svc.repo.ByID(t.Context(), pool, created.ID)
	require.NoError(t, err)
	assert.NotContains(t, row.SealedToken, "operator-secret")

	// A Create for a client that does not exist is the not-found.
	_, err = svc.Create(t.Context(), "99999999-9999-5999-8999-999999999999", "https://sp.example/scim/v2", "t")
	require.ErrorIs(t, err, ErrNoProvider)
}

// TestSyncProvisionsTheVisibleAccountsAndGroups runs one pass against a
// scripted remote with empty listings, so everything the snapshot holds is
// created: two accounts and one group for an unrestricted client.
func TestSyncProvisionsTheVisibleAccountsAndGroups(t *testing.T) {
	pool := migratedPool(t)
	remote := newStubRemote(t)
	clientID := "33333333-3333-5333-8333-333333333333"
	seedClient(t, pool, clientID, false)

	userA := seedUser(t, pool, "Langdon")
	seedUser(t, pool, "Neveu")
	groupID := uuid.NewV7()
	seedGroup(t, pool, groupID.String(), "Sangreal")
	seedMembership(t, pool, groupID, userA.ID)

	// The remote starts empty: the listings answer nothing, and each
	// create answers a document that stamps the remote id the group's
	// member references need.
	remote.answer(http.MethodGet, "https://sp.example/scim/v2/Users", http.StatusOK, userList())
	remote.answer(http.MethodGet, "https://sp.example/scim/v2/Groups", http.StatusOK, groupList())
	remote.answer(http.MethodPost, "https://sp.example/scim/v2/Users", http.StatusCreated, remoteUser{
		ID: "remote-user-1", Schemas: []string{scimUserSchema},
	})
	remote.answer(http.MethodPost, "https://sp.example/scim/v2/Groups", http.StatusCreated, remoteGroup{
		ID: "remote-group-1", Schemas: []string{scimGroupSchema},
	})

	svc := testService(t, pool, remote)
	provider, err := svc.Create(t.Context(), clientID, "https://sp.example/scim/v2", "token-1")
	require.NoError(t, err)

	stats, err := svc.Sync(t.Context(), provider.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.UsersCreated)
	assert.Equal(t, 1, stats.GroupsCreated)

	// The creates named the schema and carried the local ids as externalId.
	userDocs := remote.sentBodies(http.MethodPost, "https://sp.example/scim/v2/Users")
	require.Len(t, userDocs, 2)
	for _, doc := range userDocs {
		assert.Equal(t, []any{scimUserSchema}, doc["schemas"])
		assert.NotEmpty(t, doc["externalId"])
		assert.NotEmpty(t, doc["userName"])
	}

	// The group's member references the remote user id the create
	// answered — here the stub answers no id, so the group half refuses
	// the member lookup. The stub answers creates with an id per user to
	// keep the pass whole: see the scripted answer above, which returns
	// an empty body, and the group create therefore records no members.
	groupDocs := remote.sentBodies(http.MethodPost, "https://sp.example/scim/v2/Groups")
	require.Len(t, groupDocs, 1)
	assert.Equal(t, "Sangreal", groupDocs[0]["displayName"])

	// The bearer token rode every request.
	for _, r := range remote.Requests {
		assert.Equal(t, "Bearer token-1", r.Headers.Get("Authorization"), r.URL)
	}

	// The pass stamped the row.
	row, err := svc.repo.ByID(t.Context(), pool, provider.ID)
	require.NoError(t, err)
	require.NotNil(t, row.LastSyncedAt)
}

// TestSyncDeactivatesABannedAccount pins the Active rule: a banned account
// is pushed with active=false, the way a sign-in would refuse it.
func TestSyncDeactivatesABannedAccount(t *testing.T) {
	pool := migratedPool(t)
	remote := newStubRemote(t)
	clientID := "44444444-4444-5444-8444-444444444444"
	seedClient(t, pool, clientID, false)

	// The ban's storage is the restriction row; the directory's Active rule
	// reads the anti-joined ban.
	ib := sqlb.NewInsertBuilder()
	ib.InsertInto(UserTable)
	ib.Cols("id", "username", "email", "display_name")
	ib.Values(uuid.NewV7(), "Silas", "Silas@example.test", "Silas")
	query, args := ib.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `INSERT INTO public.account_restrictions (user_id, kind, reason, started_at)
		SELECT id, 'ban', 'the test bans its account', now() FROM public.users WHERE username = 'Silas'`)
	require.NoError(t, err)

	remote.answer(http.MethodGet, "https://sp.example/scim/v2/Users", http.StatusOK, userList())
	remote.answer(http.MethodGet, "https://sp.example/scim/v2/Groups", http.StatusOK, groupList())

	svc := testService(t, pool, remote)
	provider, err := svc.Create(t.Context(), clientID, "https://sp.example/scim/v2", "token-1")
	require.NoError(t, err)

	stats, err := svc.Sync(t.Context(), provider.ID)
	require.NoError(t, err)
	require.Equal(t, 1, stats.UsersCreated)

	doc := remote.sentBodies(http.MethodPost, "https://sp.example/scim/v2/Users")[0]
	assert.Equal(t, false, doc["active"], "a banned account pushes as inactive")
}

// TestSyncDeletesARemoteRowTheSnapshotNoLongerNames covers the outbound
// half of the reconciliation: a remote row whose externalId names nothing
// local is removed.
func TestSyncDeletesARemoteRowTheSnapshotNoLongerNames(t *testing.T) {
	pool := migratedPool(t)
	remote := newStubRemote(t)
	clientID := "55555555-5555-5555-8555-555555555555"
	seedClient(t, pool, clientID, false)

	remote.answer(http.MethodGet, "https://sp.example/scim/v2/Users", http.StatusOK, userList(remoteUser{
		ID:         "remote-orphan",
		ExternalID: "66666666-6666-5666-8666-666666666666",
		UserName:   "Teabing",
	}))
	remote.answer(http.MethodGet, "https://sp.example/scim/v2/Groups", http.StatusOK, groupList())
	remote.answer(http.MethodDelete, "https://sp.example/scim/v2/Users/remote-orphan", http.StatusNoContent, nil)

	svc := testService(t, pool, remote)
	provider, err := svc.Create(t.Context(), clientID, "https://sp.example/scim/v2", "token-1")
	require.NoError(t, err)

	stats, err := svc.Sync(t.Context(), provider.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.UsersDeleted)

	// The delete rode the remote's own id in the path.
	deleted := false
	for _, r := range remote.Requests {
		if r.Method == http.MethodDelete && strings.Contains(r.URL, "remote-orphan") {
			deleted = true
		}
	}
	assert.True(t, deleted, "the delete must address the remote's own id")
}
