package oidc

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/fetcher"
)

// The client-id metadata document (CIMD): a client whose identifier is the
// URL of a metadata document it hosts, materialized here from what the
// document declares. The draft the feature implements is
// draft-ietf-oauth-client-id-metadata-document; the validation rules are
// upstream Pocket ID's, read off its cimd.go.

// The constraints a metadata document is held to. Only the public-client
// authentication is accepted — the document's key material is never
// persisted — and the grant list must name a flow that initiates at the
// authorization endpoint.
const (
	cimdAuthMethodNone = "none"

	cimdMaxDocumentBytes = 1 << 20
	cimdFetchTimeout     = 15 * time.Second
)

// ErrCIMDRefused wraps every refusal the CIMD path produces: the fetcher's
// typed failures beside the document's own, named so the operator can tell
// a network answer from a policy one.
var (
	// ErrCIMDNotAllowed is a metadata URL the allowlist does not name. An
	// empty allowlist answers it for every URL — the feature is off.
	ErrCIMDNotAllowed = errors.New("oidc: the metadata URL is not in the allowlist")

	// ErrCIMDDocumentInvalid is a document that fetched but does not name
	// a client this server would sign accounts into.
	ErrCIMDDocumentInvalid = errors.New("oidc: the metadata document is not acceptable")

	// ErrCIMDUnavailable is a materialization a run without the fetcher
	// cannot serve.
	ErrCIMDUnavailable = errors.New("oidc: metadata fetching is not available")
)

// metadataDocument is the fields of a CIMD document this server reads. The
// rest of the document — logos, contacts, key material — is ignored: the
// draft lets a document say more than an issuer must honor.
type metadataDocument struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	PostLogoutRedirectURIs  []string `json:"post_logout_redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

// CIMDFetcher is the outbound fetch the materialization runs. The
// shared fetcher client satisfies it; the tests hand a stub.
type CIMDFetcher interface {
	Do(ctx context.Context, url string) (status int, body []byte, err error)
}

// FetcherAdapter adapts the shared fetcher client onto the CIMD fetch seam.
func FetcherAdapter(client *fetcher.Client) CIMDFetcher {
	return CIMDFetcherFunc(func(ctx context.Context, url string) (int, []byte, error) {
		return DoCIMDFetch(ctx, client, url)
	})
}

// CIMDFetcherFunc adapts a function onto the fetch seam.
type CIMDFetcherFunc func(ctx context.Context, url string) (int, []byte, error)

// Do runs the function.
func (f CIMDFetcherFunc) Do(ctx context.Context, url string) (int, []byte, error) {
	return f(ctx, url)
}

// DoCIMDFetch runs one metadata fetch through the shared client. Exported
// for the area's adapter, which bridges the concrete client onto the seam.
func DoCIMDFetch(ctx context.Context, client *fetcher.Client, rawurl string) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, cimdFetchTimeout)
	defer cancel()

	res, err := client.Do(ctx, fetcher.Request{Method: http.MethodGet, URL: rawurl})
	if err != nil {
		return 0, nil, err
	}
	if len(res.Body) > cimdMaxDocumentBytes {
		return res.StatusCode, nil, fmt.Errorf("%w: the document is larger than %d bytes",
			ErrCIMDDocumentInvalid, cimdMaxDocumentBytes)
	}
	return res.StatusCode, res.Body, nil
}

// cimdAllows reports whether the allowlist names the URL. An empty
// allowlist is the feature off: nothing is ever fetched.
func cimdAllows(patterns []string, metadataURL string) bool {
	if len(patterns) == 0 {
		return false
	}
	return MatchesAnyURLPattern(patterns, metadataURL)
}

// validateCIMDDocument holds a fetched document to what this server
// implements: public-client authentication only, a grant list that
// initiates at the authorization endpoint, and redirect URIs that assert
// nothing dangerous.
func validateCIMDDocument(doc *metadataDocument) error {
	if doc.TokenEndpointAuthMethod != "" && doc.TokenEndpointAuthMethod != cimdAuthMethodNone {
		return fmt.Errorf("%w: token_endpoint_auth_method must be %q, got %q",
			ErrCIMDDocumentInvalid, cimdAuthMethodNone, doc.TokenEndpointAuthMethod)
	}

	grants := cimdSupportedGrantTypes(doc.GrantTypes)
	if !cimdHasInitiatingGrant(grants) {
		return fmt.Errorf("%w: the document must enable authorization_code or device_code",
			ErrCIMDDocumentInvalid)
	}

	for _, responseType := range doc.ResponseTypes {
		if responseType != "code" {
			return fmt.Errorf("%w: response_type %q is not supported", ErrCIMDDocumentInvalid, responseType)
		}
	}

	if len(doc.RedirectURIs) == 0 {
		return fmt.Errorf("%w: redirect_uris must name at least one URI", ErrCIMDDocumentInvalid)
	}
	for _, uri := range doc.RedirectURIs {
		if err := validateCIMDRedirectURI(uri); err != nil {
			return err
		}
	}
	for _, uri := range doc.PostLogoutRedirectURIs {
		if err := validateCIMDRedirectURI(uri); err != nil {
			return err
		}
	}
	return nil
}

// validateCIMDRedirectURI rejects the self-asserted redirect URIs a
// metadata document must not get away with: wildcards (a document cannot
// widen its own match), relative URIs, and the schemes that are script
// hosts.
func validateCIMDRedirectURI(raw string) error {
	if strings.Contains(raw, "*") {
		return fmt.Errorf("%w: a redirect URI must not contain a wildcard", ErrCIMDDocumentInvalid)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %s is not a valid URL", ErrCIMDDocumentInvalid, raw)
	}
	if !u.IsAbs() {
		return fmt.Errorf("%w: %s must be an absolute URL", ErrCIMDDocumentInvalid, raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "javascript", "data":
		return fmt.Errorf("%w: %s uses a disallowed scheme", ErrCIMDDocumentInvalid, raw)
	}
	return nil
}

// cimdSupportedGrantTypes keeps only the grant types the surface
// implements and drops the rest; an omitted list defaults to
// authorization_code, per RFC 7591 §2.
func cimdSupportedGrantTypes(grantTypes []string) []string {
	supported := make([]string, 0, len(grantTypes))
	for _, grant := range grantTypes {
		switch grant {
		case "authorization_code", "device_code", "refresh_token":
			supported = append(supported, grant)
		}
	}
	if len(supported) == 0 && len(grantTypes) == 0 {
		return []string{"authorization_code"}
	}
	return supported
}

// cimdHasInitiatingGrant reports whether the grant list can start a flow:
// client_credentials alone initiates nothing a user consents to.
func cimdHasInitiatingGrant(grants []string) bool {
	for _, grant := range grants {
		if grant == "authorization_code" || grant == "device_code" {
			return true
		}
	}
	return false
}

// materializeCIMDClient projects a validated document onto the client row
// the surface stores: the identifier IS the metadata URL, the client is
// public with PKCE forced on (no secret ever traveled), and the grant list
// the document declared is recorded beside it.
func materializeCIMDClient(doc *metadataDocument, metadataURL string) ClientSchema {
	name := doc.ClientName
	if name == "" {
		if u, err := url.Parse(metadataURL); err == nil {
			name = u.Host
		}
	}

	return ClientSchema{
		ID:                          metadataURL,
		Name:                        &name,
		CallbackURLs:                doc.RedirectURIs,
		LogoutCallbackURLs:          doc.PostLogoutRedirectURIs,
		IsPublic:                    true,
		PkceEnabled:                 true,
		ClientType:                  ClientTypeCIMD,
		MetadataGrantTypes:          cimdSupportedGrantTypes(doc.GrantTypes),
		AccessTokenDurationMinutes:  DefaultAccessTokenMinutes,
		RefreshTokenDurationMinutes: DefaultRefreshTokenMinutes,
	}
}

// FetchCIMDClient fetches one metadata document and answers the client row
// it materializes to. The allowlist is judged before anything is fetched —
// a URL the operator did not name costs no request, and the refusal does
// not depend on the fetcher being wired.
func (s *Service) FetchCIMDClient(ctx context.Context, metadataURL string) (ClientSchema, error) {
	if !cimdAllows(s.cimdAllowlist, metadataURL) {
		return ClientSchema{}, ErrCIMDNotAllowed
	}
	if s.fetcher == nil {
		return ClientSchema{}, ErrCIMDUnavailable
	}
	if err := ValidateCallbackURLPattern(metadataURL); err != nil {
		return ClientSchema{}, ErrCIMDDocumentInvalid
	}

	status, body, err := s.fetcher.Do(ctx, metadataURL)
	if err != nil {
		return ClientSchema{}, fmt.Errorf("oidc: fetch metadata document: %w", err)
	}
	if status != 200 {
		return ClientSchema{}, fmt.Errorf("%w: the document answered %d", ErrCIMDDocumentInvalid, status)
	}

	var doc metadataDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return ClientSchema{}, fmt.Errorf("%w: the document is not a JSON object", ErrCIMDDocumentInvalid)
	}
	if err := validateCIMDDocument(&doc); err != nil {
		return ClientSchema{}, err
	}
	return materializeCIMDClient(&doc, metadataURL), nil
}

// MaterializeCIMDClient fetches a metadata document and stores the client
// it names. A client the URL already names is left as it is — the
// materialization is a first-seen write, never an overwrite: the document
// changes through RefreshClient, not through an authorize request.
func (s *Service) MaterializeCIMDClient(ctx context.Context, metadataURL string) (ClientView, error) {
	row, err := s.FetchCIMDClient(ctx, metadataURL)
	if err != nil {
		return ClientView{}, err
	}

	// The client the URL already names is the caller's answer — an
	// authorize request must not rewrite what an operator administers.
	if _, existingErr := s.repo.GetClient(ctx, s.pool, metadataURL); existingErr == nil {
		return s.Get(ctx, metadataURL)
	}

	stored, encodeErr := json.Marshal(credentials{Secrets: []Secret{}})
	if encodeErr != nil {
		return ClientView{}, fmt.Errorf("oidc: encode credentials: %w", encodeErr)
	}
	row.Credentials = stored

	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if createErr := s.repo.CreateClient(ctx, tx, row); createErr != nil {
			if errUniqueViolation(createErr) {
				// A concurrent authorization materialized it first.
				return nil
			}
			return createErr
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventOidcClientCreated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceOidcClient,
			Payload: map[string]string{
				"client_id":   row.ID,
				"name":        derefName(row.Name),
				"client_type": row.ClientType,
			},
		})
		return nil
	})
	if err != nil {
		return ClientView{}, err
	}
	return s.Get(ctx, metadataURL)
}

// RefreshCIMDClient forces a re-fetch of one stored metadata document,
// bypassing every cache: the operator's answer to a document that changed
// under it. Only a CIMD client refreshes — a registered client has no
// document to re-fetch.
func (s *Service) RefreshCIMDClient(ctx context.Context, clientID string) (ClientView, error) {
	row, err := s.repo.GetClient(ctx, s.pool, clientID)
	if errors.Is(err, datastore.ErrNoRows) {
		return ClientView{}, ErrClientNotFound
	}
	if err != nil {
		return ClientView{}, err
	}
	if row.ClientType != ClientTypeCIMD {
		return ClientView{}, ErrClientNotCIMD
	}

	fetched, fetchErr := s.FetchCIMDClient(ctx, clientID)
	if fetchErr != nil {
		return ClientView{}, fetchErr
	}

	var updated ClientView
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		row.Name = fetched.Name
		row.CallbackURLs = fetched.CallbackURLs
		row.LogoutCallbackURLs = fetched.LogoutCallbackURLs
		row.MetadataGrantTypes = fetched.MetadataGrantTypes
		expiresAt := s.now().Add(cimdRefreshTTL)
		row.MetadataExpiresAt = &expiresAt

		if _, updateErr := s.repo.UpdateCIMDClient(ctx, tx, row); updateErr != nil {
			return updateErr
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventOidcClientMetadataRefreshed,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceOidcClient,
			Payload:      map[string]string{"client_id": clientID},
		})
		updated = row.view(s.now())
		return nil
	})
	if err != nil {
		return ClientView{}, err
	}
	updated.AllowedGroups, err = s.repo.ListAllowedGroups(ctx, s.pool, clientID)
	if err != nil {
		return ClientView{}, err
	}
	return updated, nil
}

// cimdRefreshTTL is how long a materialized document stands before the
// protocol surface may re-fetch it on its own. The operator's refresh
// bypasses the window entirely.
const cimdRefreshTTL = 24 * time.Hour

// ErrClientNotCIMD is a refresh whose client is a registered one — there
// is no document behind it to re-fetch.
var ErrClientNotCIMD = errors.New("oidc: the client is not a metadata-document client")

// derefName reads the nullable name for the audit payload.
func derefName(name *string) string {
	if name == nil {
		return ""
	}
	return *name
}
