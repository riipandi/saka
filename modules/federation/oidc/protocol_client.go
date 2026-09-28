package oidc

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/luikyv/go-oidc/pkg/goidc"

	"github.com/riipandi/tango/internal/datastore"
)

// clientStore is the goidc.DCRManager over the oidc_clients rows — the
// lookup every protocol flow resolves clients by. The registration
// endpoint the same interface serves stays unmounted: tango registers
// clients through the management surface, never dynamically.
type clientStore struct {
	pool    *datastore.Postgres
	repo    *Repository
	baseURL string
	// materialize turns an unseen CIMD identifier into a client row. It
	// is the service's protocol-time hook; nil answers not-found.
	materialize func(ctx context.Context, id string) error
}

func (s clientStore) Client(ctx context.Context, id string) (*goidc.Client, error) {
	repo := s.repo
	row, err := repo.GetClient(ctx, s.pool, id)
	if errors.Is(err, datastore.ErrNoRows) && strings.HasPrefix(id, "https://") && s.materialize != nil {
		// An identifier that names a metadata document inside the
		// allowlist becomes a client on first sight; the next lookup
		// reads the row like any registered one.
		if matErr := s.materialize(ctx, id); matErr != nil {
			return nil, goidc.ErrNotFound
		}
		row, err = repo.GetClient(ctx, s.pool, id)
	}
	if err != nil {
		if errors.Is(err, datastore.ErrNoRows) {
			return nil, goidc.ErrNotFound
		}
		return nil, err
	}
	client := protocolClient(row, s.baseURL)
	return &client, nil
}

func (s clientStore) SaveClient(ctx context.Context, client *goidc.Client) error {
	// Unreachable through the mounted surface; the write the management
	// surface owns is the repository's.
	return goidc.ErrNotFound
}

func (s clientStore) DeleteClient(ctx context.Context, id string) error {
	return goidc.ErrNotFound
}

// protocolClient maps one stored client onto the shape the provider
// reads. The grant list is the metadata document's word for a CIMD
// client and the standard code+refresh pair for a registered one; the
// secrets ride the credentials document, every hash a key the verifier
// may match.
func protocolClient(row ClientSchema, baseURL string) goidc.Client {
	client := goidc.Client{
		ID: row.ID,
		// The verifier splits this document; one client may hold
		// several live secrets, and the newline never occurs in a PHC
		// hash.
		Secret:     storedSecretHashes(row.Credentials),
		ClientMeta: goidc.ClientMeta{},
	}
	if row.Name != nil {
		client.Name = *row.Name
	}
	if row.LogoPath != nil {
		client.LogoURI = baseURL + "/oidc/clients/" + row.ID + "/logo"
	}
	client.RedirectURIs = row.CallbackURLs
	client.PostLogoutRedirectURIs = row.LogoutCallbackURLs
	client.ResponseTypes = []goidc.ResponseType{goidc.ResponseTypeCode}
	// The scopes a client may ask for are all the provider's — the
	// consent question and the group restriction are what govern them,
	// not a per-client list the management surface does not keep.
	client.ScopeIDs = "openid profile email groups"
	if len(row.MetadataGrantTypes) > 0 {
		client.GrantTypes = make([]goidc.GrantType, 0, len(row.MetadataGrantTypes))
		for _, grant := range row.MetadataGrantTypes {
			client.GrantTypes = append(client.GrantTypes, goidc.GrantType(grant))
		}
	} else {
		client.GrantTypes = []goidc.GrantType{goidc.GrantAuthorizationCode, goidc.GrantRefreshToken}
	}
	if row.IsPublic {
		client.TokenAuthnMethod = goidc.AuthnMethodNone
	} else {
		client.TokenAuthnMethod = goidc.AuthnMethodSecretPost
	}
	return client
}

// storedSecretHashes renders the credentials document as the secret
// string the client authn verifier receives.
func storedSecretHashes(raw []byte) string {
	stored := readCredentials(raw)
	hashes := make([]string, 0, len(stored.Secrets))
	now := time.Now()
	for _, secret := range stored.Secrets {
		if secret.ExpiresAt == nil || secret.ExpiresAt.After(now) {
			hashes = append(hashes, secret.Hash)
		}
	}
	return strings.Join(hashes, "\n")
}
