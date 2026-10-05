package seeders

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"uuid"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/database/entity"
	oidc "github.com/riipandi/saka/modules/federation/oidc"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/crypto"
)

// ConformanceSuiteSeederName is the name this seeder reports under.
const ConformanceSuiteSeederName = "ConformanceSuiteSeeder"

// SuiteBaseURL is the conformance suite's own front — the hostname the
// prebuilt image serves its test instances on, aliased inside the compose
// network and reached from a browser through a hosts mapping.
const SuiteBaseURL = "https://localhost.emobix.co.uk:8443"

// SuiteAlias is the one alias every plan runs under. The suite derives its
// own receiver URLs — callback, post-logout redirect, back-channel — from
// the alias the test configuration names, so the seeded clients'
// registrations must name the same one. One alias serves all four
// profiles: the plans tell each other apart by the config block they read
// (client, client2, client_secret_post), not by the URL.
const SuiteAlias = "saka"

// SuiteAccount is the account the conformance driver signs the SPA in as.
// The values are public on purpose: they bootstrap a local rehearsal, the
// same posture the default user's take.
var SuiteAccount = UserCredentials{
	Email:     "suite@localhost.test",
	Username:  "suite",
	Password:  "Expecto-Suite-9",
	FirstName: "Conformance",
	LastName:  "Suite",
}

// suiteClient is one OIDC client the rehearsal drives. The redirect URIs
// name the shared alias the runner's test configuration uses — the suite
// derives its receiver URLs from that alias, so the registration matches
// whatever plan drives the client. The callback, post-logout, and
// back-channel paths are the ones the suite's OP testing instructions
// name.
type suiteClient struct {
	ID             string
	Name           string
	Secret         string
	BackchannelURI string
}

// suiteClients are the three clients the four profiles ride: the basic
// profile authenticates with client_secret_basic, the config profile with
// client_secret_post (the provider accepts both for every confidential
// client — the plan's client_auth_type decides which the suite presents),
// and the logout pair rides the third. The back-channel URI is registered
// on the logout client alone.
var suiteClients = []suiteClient{
	{
		ID:     "suite-basic",
		Name:   "Conformance Suite Basic OP",
		Secret: "saka-suite-basic",
	},
	{
		ID:     "suite-config",
		Name:   "Conformance Suite Config OP",
		Secret: "saka-suite-config",
	},
	{
		ID:     "suite-logout",
		Name:   "Conformance Suite Logout OP",
		Secret: "saka-suite-logout",
		// The suite's back-channel receiver: the profile asks the OP to
		// POST its logout token there, and the session-required flag is
		// what puts the sid member into the delivered token.
		BackchannelURI: SuiteBaseURL + "/test/a/" + SuiteAlias + "/backchannel_logout",
	},
}

// suiteCallbackURLs answers the paths a client of the shared alias needs:
// the authorization callback and the post-logout redirect. The alias is
// the one the runner's test configuration names — see SuiteAlias.
func suiteCallbackURLs() (callback, postLogout string) {
	return SuiteBaseURL + "/test/a/" + SuiteAlias + "/callback",
		SuiteBaseURL + "/test/a/" + SuiteAlias + "/post_logout_redirect"
}

// ConformanceSuite returns the seeder for the local certification
// rehearsal: the account the driver signs in and the clients the four
// profiles ride. It is a development seed — the rows are rehearsal
// fixtures, not something a deployment serves on purpose — so it joins
// the migrate:seed list, never the system one.
func ConformanceSuite() Seeder {
	return Seeder{
		Name:  ConformanceSuiteSeederName,
		Apply: applyConformanceSuite,
	}
}

func applyConformanceSuite(ctx context.Context, q datastore.Querier, dryRun bool) (created, skipped []string, err error) {
	if dryRun {
		return nil, []string{SuiteAccount.Email}, nil
	}

	userID, err := suiteAccount(ctx, q, &created, &skipped)
	if err != nil {
		return created, skipped, err
	}

	for _, client := range suiteClients {
		createdKey, skippedKey, insertErr := suiteOIDCClient(ctx, q, client, userID)
		if insertErr != nil {
			return created, skipped, insertErr
		}
		created = append(created, createdKey...)
		skipped = append(skipped, skippedKey...)
	}
	return created, skipped, nil
}

// suiteAccount creates the account the driver signs in as. The insert is
// the default user's — ON CONFLICT DO NOTHING, the password only written
// when the account is new — so a re-seed never touches an account whose
// password the operator may already have changed.
func suiteAccount(ctx context.Context, q datastore.Querier, created, skipped *[]string) (uuid.UUID, error) {
	hash, err := crypto.NewPasswordHasher().Hash(SuiteAccount.Password)
	if err != nil {
		return uuid.Nil(), err
	}

	now := time.Now().UTC()
	row := user.UserSchema{
		ID:              uuid.NewV7(),
		Username:        SuiteAccount.Username,
		Email:           SuiteAccount.Email,
		FirstName:       SuiteAccount.FirstName,
		LastName:        SuiteAccount.LastName,
		DisplayName:     SuiteAccount.DisplayName(),
		CreatedAt:       now,
		EmailVerifiedAt: &now,
	}

	inserted, err := insertUser(ctx, q, row)
	if err != nil {
		return uuid.Nil(), err
	}
	if !inserted {
		*skipped = append(*skipped, SuiteAccount.Email)
		// The account exists; its id is what the clients name as their
		// owner, so the lookup reads it back.
		return lookupSuiteAccount(ctx, q)
	}
	if err := insertPassword(ctx, q, row.ID, hash); err != nil {
		return uuid.Nil(), err
	}
	*created = append(*created, SuiteAccount.Email)
	return row.ID, nil
}

// lookupSuiteAccount reads an existing suite account's id back — the owner
// id the client inserts name.
func lookupSuiteAccount(ctx context.Context, q datastore.Querier) (uuid.UUID, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id").From(entity.TableUsers).Where(sb.Equal("email", SuiteAccount.Email))
	query, args := sb.Build()

	var id uuid.UUID
	if err := q.QueryRow(ctx, query, args...).Scan(&id); err != nil {
		return uuid.Nil(), fmt.Errorf("seeders: read the suite account back: %w", err)
	}
	return id, nil
}

// suiteOIDCClient writes one client row. The upsert keeps the secret
// document and the identifiers stable across re-seeds — a rotation is the
// secret surface's job — and refreshes the registration the suite's plans
// point at, so a changed alias or destination lands on the next seed.
func suiteOIDCClient(ctx context.Context, q datastore.Querier, client suiteClient, ownerID uuid.UUID) (created, skipped []string, err error) {
	hash, err := crypto.NewPasswordHasher().Hash(client.Secret)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()
	document, err := json.Marshal(map[string]any{
		"secrets": []oidc.Secret{{
			ID:        uuid.NewV7().String(),
			Algorithm: "phc",
			Hash:      hash,
			Prefix:    client.Secret[:4],
			CreatedAt: now,
		}},
	})
	if err != nil {
		return nil, nil, err
	}

	callback, postLogout := suiteCallbackURLs()
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableOIDCClients)
	ib.Cols("id", "name", "callback_urls", "logout_callback_urls", "credentials",
		"is_public", "pkce_enabled", "pkce_supported", "skip_consent", "client_type",
		"backchannel_logout_uri", "backchannel_logout_session_required",
		"allowed_grant_types", "created_by_id", "created_at")
	ib.Values(client.ID, client.Name,
		jsonList(callback), jsonList(postLogout), string(document),
		false, true, true, false, oidc.ClientTypeStandard,
		client.BackchannelURI, client.BackchannelURI != "",
		[]string{"authorization_code", "refresh_token"}, ownerID, now).
		SQL("ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, " +
			"callback_urls = EXCLUDED.callback_urls, " +
			"logout_callback_urls = EXCLUDED.logout_callback_urls, " +
			"backchannel_logout_uri = EXCLUDED.backchannel_logout_uri, " +
			"backchannel_logout_session_required = EXCLUDED.backchannel_logout_session_required, " +
			"created_by_id = EXCLUDED.created_by_id RETURNING (xmax = 0) AS inserted")

	query, args := ib.Build()
	var inserted bool
	if err := q.QueryRow(ctx, query, args...).Scan(&inserted); err != nil {
		return nil, nil, fmt.Errorf("seeders: write %s: %w", client.ID, err)
	}
	if inserted {
		return []string{client.ID}, nil, nil
	}
	return nil, []string{client.ID}, nil
}

// jsonList shapes a string list the JSONB columns store.
func jsonList(urls ...string) []byte {
	raw, _ := json.Marshal(urls)
	return raw
}
