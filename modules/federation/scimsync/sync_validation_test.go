package scimsync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/fetcher"
)

// The listing contract: a remote page the sync cannot trust — a body
// that is not a well-formed SCIM list, negative or contradictory
// counts, a cursor that never advances, or a listing without end —
// fails the listing. It must never read as an empty or partial
// snapshot, because the reconcile deletes what the snapshot does not
// name.

// pagedRemote answers one scripted raw JSON body per request in order,
// recording the queries the listing sent.
type pagedRemote struct {
	t        *testing.T
	pages    []string
	requests []string
}

func (p *pagedRemote) Do(_ context.Context, req fetcher.Request) (*fetcher.Response, error) {
	p.requests = append(p.requests, req.URL)
	index := len(p.requests) - 1
	if index >= len(p.pages) {
		return &fetcher.Response{StatusCode: http.StatusInternalServerError, Body: []byte("script exhausted")}, nil
	}
	return &fetcher.Response{StatusCode: http.StatusOK, Body: []byte(p.pages[index])}, nil
}

func (p *pagedRemote) startIndexes() []string {
	out := make([]string, 0, len(p.requests))
	for _, raw := range p.requests {
		parsed, err := url.Parse(raw)
		if err != nil {
			continue
		}
		out = append(out, parsed.Query().Get("startIndex"))
	}
	return out
}

func testSnapshot() snapshot {
	return snapshot{
		provider: Provider{Endpoint: "https://sp.example/scim/v2"},
		token:    "token-1",
	}
}

func userPage(total, itemsPerPage, startIndex int, ids ...string) string {
	type row struct {
		ID         string `json:"id"`
		ExternalID string `json:"externalId"`
	}
	resources := make([]row, 0, len(ids))
	for _, id := range ids {
		resources = append(resources, row{ID: id, ExternalID: "local-" + id})
	}
	doc := map[string]any{
		"schemas":      []string{scimListSchema},
		"totalResults": total,
		"Resources":    resources,
	}
	if itemsPerPage > 0 {
		doc["itemsPerPage"] = itemsPerPage
	}
	if startIndex > 0 {
		doc["startIndex"] = startIndex
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func walkUsers(t *testing.T, remote *pagedRemote) ([]remoteUser, error) {
	t.Helper()
	svc := testService(t, migratedPool(t), remote)
	return listRemote[remoteUser](t.Context(), svc, testSnapshot(), "/Users")
}

func TestAListWalksThePagesMonotonically(t *testing.T) {
	remote := &pagedRemote{t: t, pages: []string{
		userPage(25, 10, 1, "u1", "u2", "u3", "u4", "u5", "u6", "u7", "u8", "u9", "u10"),
		userPage(25, 10, 11, "u11", "u12", "u13", "u14", "u15", "u16", "u17", "u18", "u19", "u20"),
		userPage(25, 5, 21, "u21", "u22", "u23", "u24", "u25"),
	}}

	rows, err := walkUsers(t, remote)
	require.NoError(t, err)
	assert.Len(t, rows, 25)
	assert.Equal(t, []string{"1", "11", "21"}, remote.startIndexes(), "the cursor advances by the page size the remote reported")
}

func TestAnEmptySnapshotIsAWellFormedList(t *testing.T) {
	remote := &pagedRemote{t: t, pages: []string{
		userPage(0, 0, 1),
	}}

	rows, err := walkUsers(t, remote)
	require.NoError(t, err)
	assert.Empty(t, rows, "totalResults 0 with an empty Resources array is a legitimate empty snapshot")
	assert.Len(t, remote.requests, 1)
}

func TestAMalformedPageFailsTheListing(t *testing.T) {
	for name, tc := range map[string]struct {
		body    string
		wantErr string
	}{
		"no totalResults": {`{"schemas":[],"Resources":[]}`, "well-formed"},
		"no Resources":    {`{"totalResults":0}`, "well-formed"},
		"null Resources":  {`{"totalResults":0,"Resources":null}`, "well-formed"},
		"not an object":   {`[1,2,3]`, "decode"},
	} {
		t.Run(name, func(t *testing.T) {
			remote := &pagedRemote{t: t, pages: []string{tc.body}}
			_, err := walkUsers(t, remote)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestNegativeOrContradictoryCountsAreRejected(t *testing.T) {
	t.Run("negative total", func(t *testing.T) {
		remote := &pagedRemote{t: t, pages: []string{userPage(-1, 0, 1, "u1")}}
		_, err := walkUsers(t, remote)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reports -1 total results")
	})
	t.Run("total below the page's rows", func(t *testing.T) {
		remote := &pagedRemote{t: t, pages: []string{userPage(1, 0, 1, "u1", "u2", "u3")}}
		_, err := walkUsers(t, remote)
		require.Error(t, err)
	})
	t.Run("empty page before the total is reached", func(t *testing.T) {
		remote := &pagedRemote{t: t, pages: []string{userPage(5, 0, 1)}}
		_, err := walkUsers(t, remote)
		require.Error(t, err, "the remote claims five results but serves an empty first page")
	})
}

func TestAPageWithoutACursorFailsInsteadOfTruncating(t *testing.T) {
	// Two pages' worth of results, but the second page names no
	// itemsPerPage: the listing cannot advance, and a truncated snapshot
	// would delete the rows it never saw.
	remote := &pagedRemote{t: t, pages: []string{
		userPage(25, 10, 1, "u1", "u2", "u3", "u4", "u5", "u6", "u7", "u8", "u9", "u10"),
		userPage(25, 0, 11, "u11", "u12"),
	}}

	_, err := walkUsers(t, remote)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no page size to advance by")
}

func TestTheBoundsStopARunawayListing(t *testing.T) {
	// A remote reporting endless results one row per page: the listing
	// must give up at the page bound, not accumulate forever.
	pages := make([]string, 0, scimMaxPages+1)
	for i := 1; i <= scimMaxPages+1; i++ {
		pages = append(pages, userPage(1_000_000, 1, i, fmt.Sprintf("u%d", i)))
	}
	remote := &pagedRemote{t: t, pages: pages}

	_, err := walkUsers(t, remote)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeded 100 pages")
	assert.Len(t, remote.requests, scimMaxPages, "the walk stops at the bound, one page later at most")
}

func TestScimURLJoinsTheBasePathAndEscapesTheID(t *testing.T) {
	tests := []struct {
		endpoint string
		path     string
		want     string
	}{
		{"https://sp.example/scim/v2", "/Users/alice", "https://sp.example/scim/v2/Users/alice"},
		{"https://sp.example", "/Users/alice", "https://sp.example/Users/alice"},
		{"https://sp.example/scim/v2/", "/Users/alice", "https://sp.example/scim/v2/Users/alice"},
		{"https://sp.example/tenant-42/scim", "/Groups/g1", "https://sp.example/tenant-42/scim/Groups/g1"},
		{"https://sp.example/scim/v2", "/Users/with%20space", "https://sp.example/scim/v2/Users/with%20space"},
		{"https://sp.example/scim/v2", "/Users/with%2Fslash", "https://sp.example/scim/v2/Users/with%2Fslash"},
	}
	for _, tt := range tests {
		got := scimURL(tt.endpoint, tt.path, nil)
		assert.Equal(t, tt.want, got, tt.endpoint+" + "+tt.path)
	}

	// A query rides the joined URL.
	withQuery := scimURL("https://sp.example/scim/v2", "/Users", url.Values{"startIndex": {"1"}})
	assert.Contains(t, withQuery, "startIndex=1")
}

// The pass-level guard rails, run through the full sync so the
// assertions read what would actually have hit the remote.

// sentDeletes answers the DELETE requests the stub recorded.
func sentDeletes(remote *stubRemote) []string {
	paths := make([]string, 0, 2)
	for _, r := range remote.Requests {
		if r.Method == http.MethodDelete {
			paths = append(paths, r.URL)
		}
	}
	return paths
}

func TestAMalformedListBodyFailsThePassWithoutDeleting(t *testing.T) {
	pool := migratedPool(t)
	remote := newStubRemote(t)
	clientID := "77777777-7777-5777-8777-777777777777"
	seedClient(t, pool, clientID, false)

	// The body is not a SCIM list: under the old listing it decoded into
	// an empty snapshot, and the reconcile would have wiped the remote.
	remote.answer(http.MethodGet, "https://sp.example/scim/v2/Users", http.StatusOK, map[string]any{"detail": "unexpected maintenance page"})
	remote.answer(http.MethodGet, "https://sp.example/scim/v2/Groups", http.StatusOK, groupList(
		remoteGroup{ID: "remote-group-1", ExternalID: "88888888-8888-5888-8888-888888888888", DisplayName: "Priory"},
	))

	svc := testService(t, pool, remote)
	provider, err := svc.Create(t.Context(), clientID, "https://sp.example/scim/v2", "token-1")
	require.NoError(t, err)

	_, err = svc.Sync(t.Context(), provider.ID)
	require.Error(t, err)
	assert.Empty(t, sentDeletes(remote), "a malformed listing must not reach the delete half")
}

func TestARowWithoutItsIdentifiersFailsThePassWithoutDeleting(t *testing.T) {
	pool := migratedPool(t)
	remote := newStubRemote(t)
	clientID := "99999999-9999-5999-8999-999999999999"
	seedClient(t, pool, clientID, false)

	// One row the remote answered without its externalId: the pass
	// cannot tell what it names, so it refuses the snapshot whole.
	remote.answer(http.MethodGet, "https://sp.example/scim/v2/Users", http.StatusOK, remoteList[remoteUser]{
		TotalResults: new(1),
		Resources:    &[]remoteUser{{ID: "remote-1", UserName: "Teabing"}},
	})
	remote.answer(http.MethodGet, "https://sp.example/scim/v2/Groups", http.StatusOK, groupList())

	svc := testService(t, pool, remote)
	provider, err := svc.Create(t.Context(), clientID, "https://sp.example/scim/v2", "token-1")
	require.NoError(t, err)

	_, err = svc.Sync(t.Context(), provider.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no id or externalId")
	assert.Empty(t, sentDeletes(remote))
}

func TestARestrictedClientWithNoGroupsDeprovisionsLegitimately(t *testing.T) {
	pool := migratedPool(t)
	remote := newStubRemote(t)
	// The restriction is enabled with no allowed groups: the visibility
	// roll admits nobody, and the empty snapshot is a valid instruction
	// to bring the remote in line — every previously visible row goes.
	clientID := "10101010-1010-5110-8110-101010101010"
	seedClient(t, pool, clientID, true)

	remote.answer(http.MethodGet, "https://sp.example/scim/v2/Users", http.StatusOK, userList(
		remoteUser{ID: "remote-user-1", ExternalID: "20202020-2020-5220-8220-202020202020", UserName: "Langdon"},
		remoteUser{ID: "remote-user-2", ExternalID: "30303030-3030-5330-8330-303030303030", UserName: "Neveu"},
	))
	remote.answer(http.MethodGet, "https://sp.example/scim/v2/Groups", http.StatusOK, groupList())
	remote.answer(http.MethodDelete, "https://sp.example/scim/v2/Users/remote-user-1", http.StatusNoContent, nil)
	remote.answer(http.MethodDelete, "https://sp.example/scim/v2/Users/remote-user-2", http.StatusNoContent, nil)

	svc := testService(t, pool, remote)
	provider, err := svc.Create(t.Context(), clientID, "https://sp.example/scim/v2", "token-1")
	require.NoError(t, err)

	stats, err := svc.Sync(t.Context(), provider.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.UsersDeleted, "a restriction with no groups legitimately deprovisions everyone")
	assert.Len(t, sentDeletes(remote), 2)
}
