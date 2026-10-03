package oidc

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/luikyv/go-oidc/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Third-party initiated login: the relying party links the browser here
// with its own parameters, and the answers name the provider back.

func TestTheAuthorizationResponseCarriesTheIssParameter(t *testing.T) {
	rp := &goidc.Client{
		ID:            "rp",
		RedirectURIs:  []string{"https://rp.example/cb"},
		GrantTypes:    []goidc.GrantType{goidc.GrantAuthorizationCode},
		ResponseTypes: []goidc.ResponseType{goidc.ResponseTypeCode},
		ScopeIDs:      "openid",
	}
	p, err := provider.New(provider.Config{
		Issuer: "https://saka.example",
		JWKS: func(context.Context) (goidc.JSONWebKeySet, error) {
			return goidc.JSONWebKeySet{}, nil
		},
		IDTokenAlgs: []goidc.SignatureAlgorithm{goidc.SigAlgES256},
	},
		provider.WithPathPrefix(protocolPrefix),
		provider.WithStaticClients(rp),
		provider.WithAuthCodeGrant(provider.AuthCodeGrantConfig{
			ResponseTypes: []goidc.ResponseType{goidc.ResponseTypeCode},
		}, provider.WithIssuerResponseParameter()),
	)
	require.NoError(t, err)

	// The discovery document names the support so a relying party can
	// rely on the parameter before it ever sees one.
	rec := httptest.NewRecorder()
	p.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/openid-configuration", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var doc struct {
		IssParameterSupported bool `json:"authorization_response_iss_parameter_supported"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
	assert.True(t, doc.IssParameterSupported,
		"the discovery document names the iss response parameter")

	// The error redirect — the first response an incomplete request
	// earns — carries the parameter in its query.
	rec = httptest.NewRecorder()
	p.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		protocolPrefix+protocolAuthorizeEndpoint+"?client_id=rp&response_type=code&scope=openid&redirect_uri=https://rp.example/cb&state=xyz", nil))
	require.Equal(t, http.StatusSeeOther, rec.Code)
	location, err := url.Parse(rec.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "https://saka.example", location.Query().Get("iss"),
		"the redirect names the issuer, whatever the answer is")
	assert.Equal(t, "xyz", location.Query().Get("state"))
}

func TestTheInteractionDocumentCarriesTheLoginHint(t *testing.T) {
	hint := "sophie.neveu@saka.example"
	session := &goidc.AuthnSession{
		ID:       "sess_1",
		ClientID: "client_1",
		Status:   goidc.StatusPending,
		AuthorizationParameters: goidc.AuthorizationParameters{
			Scopes:    "openid profile",
			LoginHint: hint,
		},
	}

	rec := httptest.NewRecorder()
	writeInteraction(rec, http.StatusOK, map[string]any{
		"interaction_id":   session.ID,
		"client_id":        session.ClientID,
		"requested_scopes": requestedScopes(session),
		"consent_required": true,
		"login_hint":       session.LoginHint,
	})
	var doc map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
	assert.Equal(t, hint, doc["login_hint"])

	// A request without a hint answers an empty one, never an omitted
	// field — the SPA reads the key either way.
	rec = httptest.NewRecorder()
	session.LoginHint = ""
	writeInteraction(rec, http.StatusOK, map[string]any{
		"login_hint": session.LoginHint,
	})
	assert.Contains(t, rec.Body.String(), `"login_hint":""`)
	assert.False(t, strings.Contains(rec.Body.String(), "sophie"))
}
