package oauthsso

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/testutils"
)

func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()
	return testutils.MigratedPostgres(t, "oauthsso_test")
}

// testService is the container tests' service: a real recorder so the
// trail assertions read the table, a real cipher, and the discovery seam
// the test hands in.
func testService(t *testing.T, pool *datastore.Postgres, fetcher DiscoveryFetcher) *Service {
	t.Helper()
	// The builtin slugs are the wiring's, not the row's: a create names a
	// shipped provider only when the adapter set declares it, and a custom
	// slug that collides with one is reserved. The adapters themselves are
	// nil — these tests never begin a flow.
	return NewService(pool, testCipher(t), audit.NewRecorder(nil), fetcher, nil).
		WithProviders(ProviderSet{
			Builtin: map[string]Provider{"google": nil, "github": nil},
		})
}

// testCipher is the sealing key the container tests share.
func testCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	cipher, err := crypto.NewCipherFromHex(strings.Repeat("ab", 32))
	require.NoError(t, err)
	return cipher
}

// staticDiscovery is the fetch seam a test hands the service: it answers
// one document for one URL and refuses everything else, so a create that
// validated against it is provably the create that read it.
type staticDiscovery struct {
	status int
	body   string
}

func (f staticDiscovery) Do(context.Context, string) (int, []byte, error) {
	if f.status == 0 {
		return http.StatusOK, []byte(f.body), nil
	}
	return f.status, []byte(f.body), nil
}

const hogwartsDiscovery = `{
	"issuer": "https://sso.hogwarts.example",
	"authorization_endpoint": "https://sso.hogwarts.example/authorize",
	"token_endpoint": "https://sso.hogwarts.example/token",
	"jwks_uri": "https://sso.hogwarts.example/jwks.json"
}`

// customParams is a valid custom connection's payload, endpoints carried
// manually so no test needs the network unless it names the discovery
// path itself.
func customParams() ConnectionParams {
	return ConnectionParams{
		Kind:        KindCustom,
		Provider:    "hogwarts-sso",
		DisplayName: "Hogwarts SSO",
		Endpoints: Endpoints{
			Authorization: "https://sso.hogwarts.example/authorize",
			Token:         "https://sso.hogwarts.example/token",
			Jwks:          "https://sso.hogwarts.example/jwks.json",
		},
		ClientID:     "hogwarts-client-id",
		ClientSecret: "hogwarts-client-secret",
		Scopes:       "openid email",
	}
}

// builtinParams is a valid builtin connection's payload: the slug names a
// shipped provider, the endpoints ride the code.
func builtinParams() ConnectionParams {
	return ConnectionParams{
		Kind:         KindBuiltin,
		Provider:     "github",
		DisplayName:  "GitHub",
		ClientID:     "github-client-id",
		ClientSecret: "github-client-secret",
	}
}

func TestCreateStoresACustomConnectionWithASealedSecret(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool, nil)

	created, err := service.Create(t.Context(), customParams())
	require.NoError(t, err)
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, KindCustom, created.Kind)
	assert.Equal(t, "hogwarts-sso", created.Provider)
	assert.Equal(t, []string{"openid", "email"}, created.Scopes)
	assert.False(t, created.CreatedAt.IsZero())

	// The row stores the sealed form; the surface answer never carries a
	// secret at all.
	var stored string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT client_secret FROM public.oauth_connections WHERE id = $1`, created.ID).Scan(&stored))
	assert.True(t, strings.HasPrefix(stored, "enc:"), "the stored secret must rest sealed")
	// The wire message carries no secret field at all — the absence is
	// structural, not a value the handler forgets to fill.
	assert.NotEmpty(t, wireConnection(created).ClientId)
}

func TestCreateAnswersNoSecretAndTheReadsCarryNone(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool, nil)

	created, err := service.Create(t.Context(), customParams())
	require.NoError(t, err)

	// The row the service answers carries the sealed form; the wire view
	// the handler renders drops it — the wire message has no secret field
	// to fill.
	assert.True(t, strings.HasPrefix(created.ClientSecret, "enc:"))
}

func TestCreateRefusesAReservedBuiltinSlugOnACustomKind(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool, nil)

	params := customParams()
	params.Provider = "google"
	_, err := service.Create(t.Context(), params)
	require.ErrorIs(t, err, ErrInvalidConnection)
}

func TestCreateRefusesAnUnknownBuiltinSlug(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool, nil)

	params := builtinParams()
	params.Provider = "durmstrang"
	_, err := service.Create(t.Context(), params)
	require.ErrorIs(t, err, ErrInvalidConnection)
}

func TestCreateBuiltinStoresNoEndpointsOfItsOwn(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool, nil)

	created, err := service.Create(t.Context(), builtinParams())
	require.NoError(t, err)
	// An empty scope list is the adapter's own default, read at the
	// authorize request — the row stores none of the definition's.
	assert.Empty(t, created.Scopes)
	assert.Empty(t, created.Endpoints.Authorization)
	assert.Empty(t, created.Endpoints.Token)
}

func TestCreateCustomRefusesAnEndpointSourceItCannotRun(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool, nil)

	// Neither source: no discovery URL and no manual endpoints.
	params := customParams()
	params.Endpoints = Endpoints{}
	_, err := service.Create(t.Context(), params)
	require.ErrorIs(t, err, ErrInvalidConnection)

	// Both sources: the create refuses rather than guessing which the
	// operator meant.
	params.Endpoints = Endpoints{Authorization: "https://sso.hogwarts.example/authorize"}
	params.DiscoveryURL = "https://sso.hogwarts.example/.well-known/openid-configuration"
	_, err = service.Create(t.Context(), params)
	require.ErrorIs(t, err, ErrInvalidConnection)

	// A cleartext endpoint: the flow sends the client secret to these
	// hosts, so a relative or http URL is not one the feature stores.
	params.DiscoveryURL = ""
	params.Endpoints = Endpoints{
		Authorization: "http://sso.hogwarts.example/authorize",
		Token:         "https://sso.hogwarts.example/token",
		Jwks:          "https://sso.hogwarts.example/jwks.json",
	}
	_, err = service.Create(t.Context(), params)
	require.ErrorIs(t, err, ErrInvalidConnection)
}

func TestCreateResolvesTheDiscoveryDocumentIntoTheStoredEndpoints(t *testing.T) {
	pool := migratedPool(t)
	fetcher := staticDiscovery{body: hogwartsDiscovery}
	service := testService(t, pool, fetcher)

	params := customParams()
	params.Endpoints = Endpoints{}
	params.DiscoveryURL = "https://sso.hogwarts.example/.well-known/openid-configuration"

	created, err := service.Create(t.Context(), params)
	require.NoError(t, err)
	assert.Equal(t, "https://sso.hogwarts.example/authorize", created.Endpoints.Authorization)
	assert.Equal(t, "https://sso.hogwarts.example/token", created.Endpoints.Token)
	assert.Equal(t, "https://sso.hogwarts.example/jwks.json", created.Endpoints.Jwks)
	assert.Empty(t, created.Endpoints.Userinfo, "the userinfo endpoint is optional")
}

func TestCreateRefusesADiscoveryDocumentThatNamesAnotherIssuer(t *testing.T) {
	pool := migratedPool(t)
	body := strings.Replace(hogwartsDiscovery, `"issuer": "https://sso.hogwarts.example"`,
		`"issuer": "https://sso.durmstrang.example"`, 1)
	fetcher := staticDiscovery{body: body}
	service := testService(t, pool, fetcher)

	params := customParams()
	params.Endpoints = Endpoints{}
	params.DiscoveryURL = "https://sso.hogwarts.example/.well-known/openid-configuration"

	_, err := service.Create(t.Context(), params)
	require.ErrorIs(t, err, ErrDiscoveryInvalid)
}

func TestCreateRefusesADiscoveryDocumentTheNetworkCannotServe(t *testing.T) {
	pool := migratedPool(t)
	fetcher := staticDiscovery{status: http.StatusNotFound, body: "not a document"}
	service := testService(t, pool, fetcher)

	params := customParams()
	params.Endpoints = Endpoints{}
	params.DiscoveryURL = "https://sso.hogwarts.example/.well-known/openid-configuration"

	_, err := service.Create(t.Context(), params)
	require.ErrorIs(t, err, ErrDiscoveryFetch)
}

func TestCreateRefusesASealedWriteWithoutACipher(t *testing.T) {
	pool := migratedPool(t)
	// A nil cipher is the no-application-secret state: reads serve, a
	// sealed write is refused at the call site.
	service := NewService(pool, nil, audit.NewRecorder(nil), nil, nil)

	_, err := service.Create(t.Context(), customParams())
	require.ErrorIs(t, err, ErrSecretUnavailable)
}

func TestUpdateRewritesOnlyTheCarriedFields(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool, nil)

	created, err := service.Create(t.Context(), customParams())
	require.NoError(t, err)

	enabled := true
	name := "Hogwarts Identity"
	updated, err := service.Update(t.Context(), created.ID, ConnectionUpdate{
		DisplayName: &name,
		Enabled:     &enabled,
	})
	require.NoError(t, err)
	assert.Equal(t, name, updated.DisplayName)
	assert.True(t, updated.Enabled)
	// The untouched fields keep the stored values, and the secret stays
	// the sealed form the create wrote.
	assert.Equal(t, created.ClientSecret, updated.ClientSecret)
	assert.Equal(t, created.ClientID, updated.ClientID)
	assert.NotNil(t, updated.UpdatedAt)
}

func TestUpdateRewritesTheEndpointSourceAsOne(t *testing.T) {
	pool := migratedPool(t)
	fetcher := staticDiscovery{body: hogwartsDiscovery}
	service := testService(t, pool, fetcher)

	created, err := service.Create(t.Context(), customParams())
	require.NoError(t, err)

	// Moving the connection onto the discovery document clears the manual
	// set and stores the document's resolution.
	discovery := "https://sso.hogwarts.example/.well-known/openid-configuration"
	updated, err := service.Update(t.Context(), created.ID, ConnectionUpdate{
		DiscoveryURL: &discovery,
	})
	require.NoError(t, err)
	assert.Equal(t, discovery, updated.DiscoveryURL)
	assert.Equal(t, "https://sso.hogwarts.example/authorize", updated.Endpoints.Authorization)
	assert.Equal(t, "https://sso.hogwarts.example/token", updated.Endpoints.Token)
}

func TestDeleteRemovesTheRowAndRecordsTheChange(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool, nil)

	created, err := service.Create(t.Context(), customParams())
	require.NoError(t, err)
	require.NoError(t, service.Delete(t.Context(), created.ID))

	_, err = service.Get(t.Context(), created.ID)
	require.ErrorIs(t, err, ErrConnectionNotFound)

	// Both sides of the story are in the trail: the create and the
	// delete, each naming the connection.
	var records int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs
		 WHERE event IN ('oauthsso_connection_created', 'oauthsso_connection_deleted')`).Scan(&records))
	assert.Equal(t, 2, records)
}

func TestOneConnectionPerProviderSlug(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool, nil)

	_, err := service.Create(t.Context(), customParams())
	require.NoError(t, err)

	params := customParams()
	params.DisplayName = "A Second Hogwarts"
	_, err = service.Create(t.Context(), params)
	require.ErrorIs(t, err, ErrProviderTaken)
}

func TestCreateStoresTheExtendedMappingAndCustomAttributes(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool, nil)

	params := customParams()
	params.AttributeMapping = AttributeMapping{
		Email:                "mail",
		Subject:              "uuid",
		EmailVerified:        "mail_verified",
		EmailVerifiedDefault: true,
		Username:             "user_name",
		AvatarURL:            "portrait",
	}
	params.CustomAttributes = []CustomAttribute{
		{Key: "house", Claim: "hogwarts_house"},
		{Key: "patronus", Claim: "patronus"},
	}

	created, err := service.Create(t.Context(), params)
	require.NoError(t, err)

	// The round trip the surface serves: read the row back and find the
	// same mapping and attribute set the create carried.
	stored, err := service.Get(t.Context(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, params.AttributeMapping, stored.AttributeMapping)
	assert.Equal(t, params.CustomAttributes, stored.CustomAttributes)
}

func TestUpdateKeepsStoredClaimNamesAndReplacesTheAttributeSet(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool, nil)

	params := customParams()
	params.AttributeMapping = AttributeMapping{Email: "mail", GivenName: "first"}
	params.CustomAttributes = []CustomAttribute{{Key: "house", Claim: "hogwarts_house"}}
	created, err := service.Create(t.Context(), params)
	require.NoError(t, err)

	// An empty mapping field keeps the stored claim name; the attribute
	// set is rewritten as a whole.
	updated, err := service.Update(t.Context(), created.ID, ConnectionUpdate{
		AttributeMapping: &AttributeMapping{GivenName: "given"},
		CustomAttributes: &[]CustomAttribute{{Key: "patronus", Claim: "patronus"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "mail", updated.AttributeMapping.Email)
	assert.Equal(t, "given", updated.AttributeMapping.GivenName)
	assert.Equal(t, []CustomAttribute{{Key: "patronus", Claim: "patronus"}}, updated.CustomAttributes)

	// A nil attribute set keeps the stored one.
	kept, err := service.Update(t.Context(), created.ID, ConnectionUpdate{})
	require.NoError(t, err)
	assert.Equal(t, []CustomAttribute{{Key: "patronus", Claim: "patronus"}}, kept.CustomAttributes)

	// An empty set clears it.
	cleared := []CustomAttribute{}
	emptied, err := service.Update(t.Context(), created.ID, ConnectionUpdate{CustomAttributes: &cleared})
	require.NoError(t, err)
	assert.Empty(t, emptied.CustomAttributes)
}

func TestCreateRefusesCustomAttributesThatDoNotCompose(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool, nil)

	params := customParams()
	params.CustomAttributes = []CustomAttribute{{Key: "house", Claim: ""}}
	_, err := service.Create(t.Context(), params)
	require.ErrorIs(t, err, ErrInvalidConnection)

	params.CustomAttributes = []CustomAttribute{
		{Key: "house", Claim: "hogwarts_house"},
		{Key: "house", Claim: "patronus"},
	}
	_, err = service.Create(t.Context(), params)
	require.ErrorIs(t, err, ErrInvalidConnection)
}

func TestBuiltinConnectionStoresNoMappingOrCustomAttributes(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool, nil)

	params := builtinParams()
	params.AttributeMapping = AttributeMapping{Email: "mail"}
	params.CustomAttributes = []CustomAttribute{{Key: "house", Claim: "hogwarts_house"}}

	created, err := service.Create(t.Context(), params)
	require.NoError(t, err)
	// The builtin adapters read fixed claims — a create cannot name a
	// mapping or attributes for them.
	assert.Empty(t, created.AttributeMapping)
	assert.Empty(t, created.CustomAttributes)
}
