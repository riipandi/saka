package oauthsso

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/riipandi/tango/internal/fetcher"
)

// The discovery read's fixed terms: one attempt within ten seconds, a
// document no larger than 256 KiB. A discovery document is a few
// kilobytes; anything larger is not one.
const (
	discoveryFetchTimeout = 10 * time.Second
	discoveryMaxBytes     = 256 * 1024
)

// The failures the discovery read reports. The handler maps them onto the
// codes the Connect protocol carries; the service defines what happened,
// not how it is answered.
var (
	// ErrDiscoveryUnavailable is a discovery validation on a process with
	// no outbound client wired: the row is refused rather than stored
	// unvalidated.
	ErrDiscoveryUnavailable = errors.New("oauthsso: no outbound client is configured to fetch a discovery document")

	// ErrDiscoveryFetch is a discovery URL the outbound client could not
	// read — an unreachable host, a non-200 answer, a body over the cap.
	ErrDiscoveryFetch = errors.New("oauthsso: the discovery document could not be fetched")

	// ErrDiscoveryInvalid is a body that is not a usable OIDC discovery
	// document: a malformed document, or one missing an endpoint the
	// flow needs.
	ErrDiscoveryInvalid = errors.New("oauthsso: the discovery document is not a usable OIDC document")
)

// DiscoveryFetcher is the outbound fetch the discovery validation runs.
// The shared fetcher client satisfies it; the tests hand a stub.
type DiscoveryFetcher interface {
	Do(ctx context.Context, url string) (status int, body []byte, err error)
}

// DiscoveryFetcherFunc adapts a function onto the fetch seam.
type DiscoveryFetcherFunc func(ctx context.Context, url string) (int, []byte, error)

// Do runs the function.
func (f DiscoveryFetcherFunc) Do(ctx context.Context, url string) (int, []byte, error) {
	return f(ctx, url)
}

// FetcherAdapter adapts the shared fetcher client onto the discovery
// fetch seam.
func FetcherAdapter(client *fetcher.Client) DiscoveryFetcher {
	return DiscoveryFetcherFunc(func(ctx context.Context, rawurl string) (int, []byte, error) {
		ctx, cancel := context.WithTimeout(ctx, discoveryFetchTimeout)
		defer cancel()

		res, err := client.Do(ctx, fetcher.Request{Method: http.MethodGet, URL: rawurl})
		if err != nil {
			return 0, nil, err
		}
		if len(res.Body) > discoveryMaxBytes {
			return res.StatusCode, nil, fmt.Errorf("%w: the document is larger than %d bytes",
				ErrDiscoveryFetch, discoveryMaxBytes)
		}
		return res.StatusCode, res.Body, nil
	})
}

// discoveryDocument is the subset of an OIDC discovery document the flow
// reads. The issuer must match the URL the document was fetched from —
// the one check that stops a document minted for another issuer from
// standing in — and the three endpoints the flow needs must be absolute
// https URLs.
type discoveryDocument struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	JwksURI               string `json:"jwks_uri"`
}

// ResolveDiscovery fetches the discovery URL and renders the endpoints it
// declares. The userinfo endpoint is optional — a provider whose
// id_token carries the claims serves none — and a missing one is stored
// as empty, which the identity resolution reads as "the id_token speaks".
func ResolveDiscovery(ctx context.Context, fetcher DiscoveryFetcher, discoveryURL string) (Endpoints, error) {
	if fetcher == nil {
		return Endpoints{}, ErrDiscoveryUnavailable
	}

	status, body, err := fetcher.Do(ctx, discoveryURL)
	if err != nil {
		return Endpoints{}, fmt.Errorf("%w: %s: %v", ErrDiscoveryFetch, strings.TrimPrefix(discoveryURL, "https://"), err)
	}
	if status != http.StatusOK {
		return Endpoints{}, fmt.Errorf("%w: %s answered %d", ErrDiscoveryFetch, strings.TrimPrefix(discoveryURL, "https://"), status)
	}

	var doc discoveryDocument
	if decodeErr := json.Unmarshal(body, &doc); decodeErr != nil {
		return Endpoints{}, fmt.Errorf("%w: %v", ErrDiscoveryInvalid, decodeErr)
	}

	// The issuer must equal the URL the document was fetched from, with
	// no query or fragment — RFC 8414 §3.3 makes the identifier the
	// issuer's, and a document that names another issuer is not this
	// provider's.
	issuer, err := url.Parse(doc.Issuer)
	if err != nil || !issuer.IsAbs() || issuer.RawQuery != "" || issuer.RawFragment != "" {
		return Endpoints{}, fmt.Errorf("%w: issuer %q is not an absolute issuer identifier", ErrDiscoveryInvalid, doc.Issuer)
	}
	authority, err := url.Parse(discoveryURL)
	if err != nil {
		return Endpoints{}, fmt.Errorf("%w: %s is not a valid URL", ErrDiscoveryInvalid, discoveryURL)
	}
	if issuer.Scheme != authority.Scheme || issuer.Host != authority.Host {
		return Endpoints{}, fmt.Errorf("%w: the document's issuer %q does not match the host it was fetched from", ErrDiscoveryInvalid, doc.Issuer)
	}

	endpoints := Endpoints{
		Authorization: doc.AuthorizationEndpoint,
		Token:         doc.TokenEndpoint,
		Userinfo:      doc.UserinfoEndpoint,
		Jwks:          doc.JwksURI,
	}
	for name, raw := range map[string]string{
		"authorization_endpoint": endpoints.Authorization,
		"token_endpoint":         endpoints.Token,
		"jwks_uri":               endpoints.Jwks,
	} {
		if err := requireHTTPSURL(raw); err != nil {
			return Endpoints{}, fmt.Errorf("%w: %s: %v", ErrDiscoveryInvalid, name, err)
		}
	}
	if endpoints.Userinfo != "" {
		if err := requireHTTPSURL(endpoints.Userinfo); err != nil {
			return Endpoints{}, fmt.Errorf("%w: userinfo_endpoint: %v", ErrDiscoveryInvalid, err)
		}
	}
	return endpoints, nil
}

// requireHTTPSURL holds an endpoint to an absolute https URL: the flow
// sends the account's authorization redirect and the client secret to
// these hosts, so a relative or cleartext URL is not one the feature
// stores.
func requireHTTPSURL(raw string) error {
	if raw == "" {
		return errors.New("is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() {
		return fmt.Errorf("%q is not an absolute URL", raw)
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("%q must be an https URL", raw)
	}
	return nil
}

// validateManualEndpoints holds an operator-supplied endpoint set to the
// same bar a discovery document is held to.
func validateManualEndpoints(endpoints Endpoints) error {
	for name, raw := range map[string]string{
		"authorization": endpoints.Authorization,
		"token":         endpoints.Token,
		"jwks":          endpoints.Jwks,
	} {
		if err := requireHTTPSURL(raw); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if endpoints.Userinfo != "" {
		if err := requireHTTPSURL(endpoints.Userinfo); err != nil {
			return fmt.Errorf("userinfo: %w", err)
		}
	}
	return nil
}
