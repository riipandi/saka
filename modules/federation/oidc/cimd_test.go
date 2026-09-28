package oidc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// metadataServer serves one metadata document and answers its URL — the
// client id a CIMD client carries.
func metadataServer(t *testing.T, document string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(document))
	}))
	t.Cleanup(server.Close)
	return server
}

// validDocument is a document the validation accepts: public-client
// authentication, an initiating grant, one absolute redirect.
const validDocument = `{
	"client_name": "Hogwarts Portal",
	"redirect_uris": ["https://portal.hogwarts.example/callback"],
	"token_endpoint_auth_method": "none",
	"grant_types": ["authorization_code", "refresh_token"]
}`

// TestCIMDMaterializesFromTheDocument covers the first-seen write: the
// allowlist is judged before anything is fetched, the document's rules are
// held, and the materialized client is public with PKCE forced on.
func TestCIMDMaterializesFromTheDocument(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	server := metadataServer(t, validDocument)

	// An empty allowlist is the feature off: nothing is fetched.
	_, err := service.FetchCIMDClient(t.Context(), server.URL)
	assert.ErrorIs(t, err, ErrCIMDNotAllowed)

	// The allowlist is judged before the fetch: a URL it does not name
	// costs no request.
	service.WithCIMDAllowlist([]string{"https://*.hogwarts.example/*"})
	_, err = service.FetchCIMDClient(t.Context(), server.URL)
	assert.ErrorIs(t, err, ErrCIMDNotAllowed)

	// The wildcard allowlist lets the local document through.
	service.WithCIMDAllowlist([]string{"*"}).WithCIMDFetcher(stubFetcher{})
	materialized, err := service.MaterializeCIMDClient(t.Context(), server.URL)
	require.NoError(t, err)
	assert.Equal(t, server.URL, materialized.ID, "the identifier IS the document's URL")
	assert.Equal(t, ClientTypeCIMD, materialized.ClientType)
	assert.True(t, materialized.IsPublic)
	assert.True(t, materialized.PkceEnabled, "a client that cannot keep a secret authenticates with PKCE")
	assert.Equal(t, "Hogwarts Portal", materialized.Name)
	assert.Equal(t, []string{"authorization_code", "refresh_token"}, materialized.MetadataGrantTypes)
	assert.Empty(t, materialized.Secrets, "no secret ever traveled")

	// A second materialization is the same answer, not an overwrite.
	again, err := service.MaterializeCIMDClient(t.Context(), server.URL)
	require.NoError(t, err)
	assert.Equal(t, materialized.CreatedAt, again.CreatedAt)
}

// stubFetcher answers the mutable document the test owns — the same
// document the metadata server's handler serves, so both faces of the
// fetch agree.
type stubFetcher struct {
	document *string
	status   int
}

func (s stubFetcher) Do(_ context.Context, _ string) (int, []byte, error) {
	document := validDocument
	if s.document != nil {
		document = *s.document
	}
	status := s.status
	if status == 0 {
		status = 200
	}
	return status, []byte(document), nil
}

// TestRefreshRewritesTheClientFromTheDocument covers the refresh: the
// operator's force bypasses every cache and rewrites what the document
// names — and only a CIMD client has a document to re-fetch.
func TestRefreshRewritesTheClientFromTheDocument(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	// The document changes under the client: the mutable stub answers what
	// its `document` field carries, and the test swaps it between calls.
	document := validDocument
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(document))
	}))
	t.Cleanup(server.Close)

	service.WithCIMDAllowlist([]string{"*"}).WithCIMDFetcher(stubFetcher{document: &document})

	materialized, err := service.MaterializeCIMDClient(t.Context(), server.URL)
	require.NoError(t, err)
	assert.Equal(t, "Hogwarts Portal", materialized.Name)

	// A registered client has no document behind it.
	owner := seedAccount(t, pool, "hermione")
	issued, err := service.Create(t.Context(), owner, createParams("Hogwarts SPA"))
	require.NoError(t, err)
	_, err = service.RefreshCIMDClient(t.Context(), issued.Client.ID)
	assert.ErrorIs(t, err, ErrClientNotCIMD)

	// The document changed under the CIMD client; the refresh rewrites the
	// document-named fields and sets the re-fetch deadline.
	document = `{
		"client_name": "Ravenclaw Portal",
		"redirect_uris": ["https://ravenclaw.hogwarts.example/callback"],
		"token_endpoint_auth_method": "none",
		"grant_types": ["authorization_code"]
	}`
	refreshed, err := service.RefreshCIMDClient(t.Context(), materialized.ID)
	require.NoError(t, err)
	assert.Equal(t, "Ravenclaw Portal", refreshed.Name)
	assert.Equal(t, []string{"https://ravenclaw.hogwarts.example/callback"}, refreshed.CallbackURLs)
	assert.Equal(t, []string{"authorization_code"}, refreshed.MetadataGrantTypes)
	assert.NotNil(t, refreshed.MetadataExpiresAt, "the refresh sets the document's re-fetch deadline")
}

// TestTheDocumentRulesRefuseWhatTheSurfaceWouldNotSign covers the
// validation: the authentication method, the initiating grant, the
// response types, and the redirect URIs are each held where upstream holds
// them.
func TestTheDocumentRulesRefuseWhatTheSurfaceWouldNotSign(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	service.WithCIMDAllowlist([]string{"*"})

	cases := []struct {
		name     string
		document string
	}{
		{
			name:     "a confidential authentication method",
			document: `{"client_name": "X", "redirect_uris": ["https://x.example/cb"], "token_endpoint_auth_method": "client_secret_basic"}`,
		},
		{
			name:     "no initiating grant",
			document: `{"client_name": "X", "redirect_uris": ["https://x.example/cb"], "token_endpoint_auth_method": "none", "grant_types": ["client_credentials"]}`,
		},
		{
			name:     "an unsupported response type",
			document: `{"client_name": "X", "redirect_uris": ["https://x.example/cb"], "token_endpoint_auth_method": "none", "response_types": ["token"]}`,
		},
		{
			name:     "no redirect uris",
			document: `{"client_name": "X", "token_endpoint_auth_method": "none"}`,
		},
		{
			name:     "a wildcard redirect uri",
			document: `{"client_name": "X", "redirect_uris": ["https://x.example/*"], "token_endpoint_auth_method": "none"}`,
		},
		{
			name:     "a script-host scheme",
			document: `{"client_name": "X", "redirect_uris": ["javascript:alert(1)"], "token_endpoint_auth_method": "none"}`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := metadataServer(t, testCase.document)
			// The stub answers the case's own document — the server URL
			// only names the client id. The With* wiring mutates the
			// shared service in place and returns it.
			document := testCase.document
			service.WithCIMDFetcher(stubFetcher{document: &document})
			_, err := service.MaterializeCIMDClient(t.Context(), server.URL)
			assert.ErrorIs(t, err, ErrCIMDDocumentInvalid)
		})
	}
}

// TestURLPatternsMatchTheGrammarTheAllowlistIsWrittenIn covers the
// matcher: the wildcards the CIMD allowlist and the callback lists share.
func TestURLPatternsMatchTheGrammarTheAllowlistIsWrittenIn(t *testing.T) {
	assert.True(t, MatchesAnyURLPattern([]string{"*"}, "https://anything.example/anywhere"))
	assert.True(t, MatchesAnyURLPattern([]string{"https://*.hogwarts.example/*"}, "https://portal.hogwarts.example/cb"))
	assert.False(t, MatchesAnyURLPattern([]string{"https://*.hogwarts.example/*"}, "https://portal.ministry.example/cb"))
	assert.False(t, MatchesAnyURLPattern(nil, "https://portal.hogwarts.example"), "an empty allowlist never matches")
	assert.True(t, MatchesAnyURLPattern([]string{"https://x.example"}, "https://x.example"), "an exact string is its own pattern")
}
