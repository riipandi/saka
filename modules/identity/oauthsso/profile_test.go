package oauthsso

import (
	"context"
	"log/slog"
	"testing"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/modules/identity/user"
)

// stubProfiles records the account writes the resolution's refresh made:
// the profile applications, the attribute merges, and the pictures.
type stubProfiles struct {
	applied  map[uuid.UUID][]user.ProviderProfile
	merged   map[uuid.UUID][]map[string]any
	pictures map[uuid.UUID][]byte
}

func newStubProfiles() *stubProfiles {
	return &stubProfiles{
		applied:  map[uuid.UUID][]user.ProviderProfile{},
		merged:   map[uuid.UUID][]map[string]any{},
		pictures: map[uuid.UUID][]byte{},
	}
}

func (p *stubProfiles) ApplyProviderProfile(_ context.Context, _ datastore.Querier, userID uuid.UUID, profile user.ProviderProfile) error {
	p.applied[userID] = append(p.applied[userID], profile)
	return nil
}

func (p *stubProfiles) MergeCustomAttributes(_ context.Context, _ datastore.Querier, userID uuid.UUID, attrs map[string]any) error {
	p.merged[userID] = append(p.merged[userID], attrs)
	return nil
}

func (p *stubProfiles) RefreshProviderPicture(_ context.Context, userID uuid.UUID, data []byte) error {
	p.pictures[userID] = data
	return nil
}

// pictureRecorder wraps the real account service and records the bytes
// its picture writer receives — the storage engine is the picture
// pipeline's own test.
type pictureRecorder struct {
	*user.Service
	records map[uuid.UUID][]byte
}

func (p *pictureRecorder) RefreshProviderPicture(_ context.Context, userID uuid.UUID, data []byte) error {
	p.records[userID] = data
	return nil
}

// customAttrs is the connection's attribute set one test needs.
var customAttrs = []CustomAttribute{{Key: "house", Claim: "hogwarts_house"}}

func TestCustomAttributeDocumentKeepsTheAnswersTheAccountMayHold(t *testing.T) {
	profile := []byte(`{
		"hogwarts_house": "gryffindor",
		"years": [1991, 1998],
		"mixed": ["charms", 7, true],
		"empty": "",
		"null_claim": null,
		"no_members": [],
		"nested": {"a": 1},
		"objects_in": ["charms", {"b": 2}]
	}`)

	attrs := customAttributeDocument(profile, []CustomAttribute{
		{Key: "house", Claim: "hogwarts_house"},
		{Key: "years", Claim: "years"},
		{Key: "mixed", Claim: "mixed"},
		{Key: "empty", Claim: "empty"},
		{Key: "null_claim", Claim: "null_claim"},
		{Key: "no_members", Claim: "no_members"},
		{Key: "nested", Claim: "nested"},
		{Key: "objects_in", Claim: "objects_in"},
		{Key: "absent", Claim: "absent"},
	}, slog.New(slog.DiscardHandler))

	require.Len(t, attrs, 3, "blank, structured, and absent answers land nothing")
	assert.Equal(t, "gryffindor", attrs["house"])
	assert.Equal(t, []any{float64(1991), float64(1998)}, attrs["years"])
	assert.Equal(t, []any{"charms", float64(7), true}, attrs["mixed"])
}

func TestTheSecondSignInRefreshesTheProfileAndTheAttributes(t *testing.T) {
	pool := migratedPool(t)
	accounts := user.NewService(pool, fwaudit.NewRecorder(nil), nil, nil)
	recorder := &pictureRecorder{Service: accounts, records: map[uuid.UUID][]byte{}}
	service := resolutionService(t, pool, func(s *Service) { s.WithProfiles(recorder) })
	// The outbound client answers a PNG: the picture the flow resolved is
	// carried to the picture writer beside the transaction.
	service.fetcher = DiscoveryFetcherFunc(func(_ context.Context, _ string) (int, []byte, error) {
		return 200, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}, nil
	})
	identity := ExternalIdentity{
		ProviderAccountID: "prov-20",
		Email:             "hermione@hogwarts.example",
		EmailVerified:     true,
		GivenName:         "Hermione",
		FamilyName:        "Granger",
	}
	provider := &fakeProvider{identity: identity}
	service.providers.Custom = provider
	params := customParams()
	params.Enabled = true
	params.CustomAttributes = customAttrs
	created, err := service.Create(t.Context(), params)
	require.NoError(t, err)

	// The first sign-in binds the provider identity to the seeded
	// account.
	authorizeURL, err := service.Begin(t.Context(), created.Provider)
	require.NoError(t, err)
	firstToken, err := service.Callback(t.Context(), created.Provider, "the-code", stateOf(t, authorizeURL))
	require.NoError(t, err)
	userID := seedAccount(t, pool, "hermione@hogwarts.example", true)
	mustExec(t, pool,
		`INSERT INTO public.oauth_accounts (user_id, connection_id, provider_account_id, email, email_verified)
		 VALUES ($1, $2, 'prov-20', 'hermione@hogwarts.example', true)`, userID, created.ID)

	answer, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: firstToken})
	require.NoError(t, err)
	require.NotNil(t, answer.Session)

	// The second sign-in resolves new names, a username claim the
	// mapping answered, an picture, and a custom attribute — the account
	// takes the answers, and its username stays.
	provider.identity = ExternalIdentity{
		ProviderAccountID: "prov-20",
		Email:             "hermione@hogwarts.example",
		EmailVerified:     true,
		GivenName:         "Minerva",
		FamilyName:        "McGonagall",
		Username:          "head-of-house",
		Picture:           "https://cdn.hogwarts.example/minerva.png",
		Profile:           []byte(`{"hogwarts_house":"gryffindor"}`),
	}
	authorizeURL, err = service.Begin(t.Context(), created.Provider)
	require.NoError(t, err)
	secondToken, err := service.Callback(t.Context(), created.Provider, "the-code", stateOf(t, authorizeURL))
	require.NoError(t, err)
	answer, err = service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: secondToken})
	require.NoError(t, err)
	require.NotNil(t, answer.Session)

	// The account took the refresh: the names moved, the display name
	// composed after them, the custom attribute landed, and the
	// username — written once at the JIT creation — stayed.
	var firstName, lastName, displayName, username string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT coalesce(first_name, ''), coalesce(last_name, ''), display_name, coalesce(username, '')
		 FROM public.users WHERE id = $1`, userID).
		Scan(&firstName, &lastName, &displayName, &username))
	assert.Equal(t, "Minerva", firstName)
	assert.Equal(t, "McGonagall", lastName)
	assert.Equal(t, "Minerva McGonagall", displayName)
	assert.Equal(t, "hermione", username)

	var house string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT custom_attributes->>'house' FROM public.users WHERE id = $1`, userID).Scan(&house))
	assert.Equal(t, "gryffindor", house)

	// The picture the flow resolved is fetched beside the transaction and
	// carried to the picture writer.
	assert.NotEmpty(t, recorder.records[userID], "the picture is carried to the picture writer")
}

func TestTheMappedUsernameIsTheJITCandidateAndATakenOneSuffixes(t *testing.T) {
	pool := migratedPool(t)
	service := resolutionService(t, pool, func(s *Service) {
		s.WithSettings(settingsFor(map[string]any{SettingAccessMode: "open"}))
		s.WithProfiles(newStubProfiles())
	})
	identity := ExternalIdentity{
		ProviderAccountID: "prov-21",
		Email:             "luna@hogwarts.example",
		EmailVerified:     true,
		GivenName:         "Luna",
		FamilyName:        "Lovegood",
	}
	provider := &fakeProvider{identity: identity}
	service.providers.Custom = provider
	params := customParams()
	params.Enabled = true
	created, err := service.Create(t.Context(), params)
	require.NoError(t, err)

	// The mapped claim is the candidate, cleaned to the column's shape.
	provider.identity.Username = "Loony-Lovegood!"
	authorizeURL, err := service.Begin(t.Context(), created.Provider)
	require.NoError(t, err)
	firstToken, err := service.Callback(t.Context(), created.Provider, "the-code", stateOf(t, authorizeURL))
	require.NoError(t, err)
	answer, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: firstToken})
	require.NoError(t, err)
	require.NotNil(t, answer.Session)
	assert.Equal(t, "loonylovegood", answer.Session.User.Username)

	// A taken candidate retries with a suffix, the derivation's rule.
	provider.identity = ExternalIdentity{
		ProviderAccountID: "prov-22",
		Email:             "luna2@hogwarts.example",
		EmailVerified:     true,
		GivenName:         "Luna",
		FamilyName:        "Lovegood",
		Username:          "loonylovegood",
	}
	authorizeURL, err = service.Begin(t.Context(), created.Provider)
	require.NoError(t, err)
	secondToken, err := service.Callback(t.Context(), created.Provider, "the-code", stateOf(t, authorizeURL))
	require.NoError(t, err)
	answer, err = service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: secondToken})
	require.NoError(t, err)
	require.NotNil(t, answer.Session)
	assert.Equal(t, "loonylovegood_1", answer.Session.User.Username)
}

func TestThePictureFailureKeepsTheSignIn(t *testing.T) {
	pool := migratedPool(t)
	profiles := newStubProfiles()
	service := resolutionService(t, pool, func(s *Service) { s.WithProfiles(profiles) })
	// The outbound client answers a refusal: the refresh is the run this
	// run could not make, the sign-in never fails on it.
	service.fetcher = DiscoveryFetcherFunc(func(_ context.Context, _ string) (int, []byte, error) {
		return 0, nil, context.DeadlineExceeded
	})
	flow, flowToken := resolvedFlow(t, service, ExternalIdentity{
		ProviderAccountID: "prov-23",
		Email:             "hannah@hogwarts.example",
		EmailVerified:     true,
		GivenName:         "Hannah",
		FamilyName:        "Abbott",
		Picture:           "https://cdn.hogwarts.example/hannah.png",
	})
	userID := seedAccount(t, pool, "hannah@hogwarts.example", true)
	mustExec(t, pool,
		`INSERT INTO public.oauth_accounts (user_id, connection_id, provider_account_id, email, email_verified)
		 VALUES ($1, $2, 'prov-23', 'hannah@hogwarts.example', true)`, userID, flow.ConnectionID)

	answer, err := service.ContinueSignIn(t.Context(), ContinueParams{FlowToken: flowToken})
	require.NoError(t, err)
	require.NotNil(t, answer.Session, "a failed download is not a failed sign-in")
	assert.Empty(t, profiles.pictures, "no bytes reached the picture writer")
}
