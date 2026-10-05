package oidc

import (
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/riipandi/saka/modules/identity/user"
)

// ResourceOidcClient is the resource type an audit record names when the
// change is about a client. The client's id — the relying party's client_id,
// not a row UUID — is what a reader of the record matches against, through
// the payload: the resource_id column is a uuid, and the client id is not.
const ResourceOidcClient = "oidc_client"

// ClientTypeStandard is the client_type of a client defined on this server.
const ClientTypeStandard = "standard"

// ClientTypeCIMD is the client_type of a client materialized from a
// client-id metadata document: the identifier IS the document's URL, the
// client is public with PKCE forced on, and the document changes through
// the refresh, never through an authorize request.
const ClientTypeCIMD = "cimd"

// The token windows a creation that leaves the fields unset writes. They are
// the schema's defaults, spelled here because the row is written once, whole.
const (
	DefaultAccessTokenMinutes  = 60
	DefaultRefreshTokenMinutes = 43200
)

// GrantAuthorizationCodeWire, GrantRefreshTokenWire, and the rest are the
// wire words the token endpoint judges — the registered grant types a
// client's allowed list may name, plus the client-credentials grant the
// machine-to-machine surface rides.
const (
	GrantAuthorizationCodeWire = "authorization_code"
	GrantRefreshTokenWire      = "refresh_token"
	GrantDeviceCodeWire        = "urn:ietf:params:oauth:grant-type:device_code"
	GrantClientCredentialsWire = "client_credentials"
)

// DefaultAllowedGrantTypes is the list a client whose create or update
// left the field empty rides: the registered trio. A var — a slice is no
// constant — and every reader must treat it as read-only.
var DefaultAllowedGrantTypes = []string{
	GrantAuthorizationCodeWire,
	GrantRefreshTokenWire,
	GrantDeviceCodeWire,
}

// grantWireTypes is the set a create or update may name. A CIMD
// document's list is judged against it at materialization, so the wire
// words are one vocabulary everywhere.
var grantWireTypes = map[string]struct{}{
	GrantAuthorizationCodeWire: {},
	GrantRefreshTokenWire:      {},
	GrantDeviceCodeWire:        {},
	GrantClientCredentialsWire: {},
}

// validateGrantWires judges an allowed list: every word must be one the
// provider serves. An empty list is the default, not a refusal.
func validateGrantWires(wires []string) error {
	for _, wire := range wires {
		if _, ok := grantWireTypes[wire]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownGrantType, wire)
		}
	}
	return nil
}

// grantWiresWithin judges an allowed list against a CIMD document's
// declared ceiling: the operator's list names nothing beyond it.
func grantWiresWithin(wires, ceiling []string) error {
	declared := make(map[string]struct{}, len(ceiling))
	for _, wire := range ceiling {
		declared[wire] = struct{}{}
	}
	for _, wire := range wires {
		if _, ok := declared[wire]; !ok {
			return fmt.Errorf("%w: %s is not in the metadata document's grant list", ErrUnknownGrantType, wire)
		}
	}
	return nil
}

// The failures the client procedures report. The handler maps them to
// connect codes, the way every feature's failures are mapped.
var (
	// ErrClientNotFound is a request whose identifier names no client. A
	// malformed identifier answers it too: the shape is the not-found one
	// either way.
	ErrClientNotFound = errors.New("oidc: the client does not exist")

	// ErrClientExists is a creation whose identifier another client holds.
	ErrClientExists = errors.New("oidc: the client id is already in use")

	// ErrUnknownGrantType is an allowed-grant list naming a word the
	// provider does not serve.
	ErrUnknownGrantType = errors.New("oidc: the grant type is not one the provider serves")

	// ErrGroupUnknown is a group restriction naming a group that does not
	// exist. The replacement is refused whole, so the client keeps the set
	// it held.
	ErrGroupUnknown = errors.New("oidc: a named user group does not exist")

	// ErrSecretNotFound is a secret deletion whose identifier names no
	// secret of the named client.
	ErrSecretNotFound = errors.New("oidc: the client secret does not exist")

	// ErrUnsupportedLogo is an upload whose bytes name no accepted image
	// kind.
	ErrUnsupportedLogo = errors.New("oidc: the logo is not a PNG, JPEG, or WebP image")

	// ErrLogosUnavailable is a logo procedure a run without the storage
	// engine cannot serve.
	ErrLogosUnavailable = errors.New("oidc: logo storage is not available")
)

// ClientSchema is one row of entity.TableOIDCClients. The identifier is the relying
// party's client_id, an operator's word rather than a row UUID — it is the
// credential a foreign client presents, so it travels as it is stored.
//
// The credentials column is the secrets' whole stored presence: a JSONB
// document of hashes and prefixes that secrets.go renders. The raw values
// exist in exactly one response each and are never written down.
type ClientSchema struct {
	ID                                  string
	Name                                *string
	Description                         string
	CallbackURLs                        []string
	LogoutCallbackURLs                  []string
	LaunchURL                           *string
	Credentials                         []byte
	IsPublic                            bool
	PkceEnabled                         bool
	PkceSupported                       bool
	RequiresReauthentication            bool
	RequiresPushedAuthorizationRequests bool
	SkipConsent                         bool
	IsGroupRestricted                   bool
	ClientType                          string
	LogoPath                            *string
	AccessTokenDurationMinutes          int64
	RefreshTokenDurationMinutes         int64
	// BackchannelLogoutURI is where the provider POSTs the logout token
	// when a signed-in session ends. An empty one is the client opted
	// out; BackchannelLogoutSessionRequired is the OIDC Session
	// Management signal the token carries.
	BackchannelLogoutURI             string
	BackchannelLogoutSessionRequired bool
	// AllowedGrantTypes is the wire-word list the token endpoint judges.
	// An empty list is the registered default — the code, refresh, and
	// device trio — and a CIMD client's list is its document's.
	AllowedGrantTypes []string
	// MetadataExpiresAt is when the surface may re-fetch a CIMD document
	// on its own; a registered client carries nil. MetadataGrantTypes is
	// the grant list the document declared — the capabilities the client
	// neither asked for beyond nor can be granted beyond.
	MetadataExpiresAt  *time.Time
	MetadataGrantTypes []string
	CreatedByID        *uuid.UUID
	CreatedAt          time.Time
}

// ClientView is a client as the procedures answer it: the fields an operator
// manages, the secrets as their views, and the groups the restriction names.
type ClientView struct {
	ID                                  string
	Name                                string
	Description                         string
	CallbackURLs                        []string
	LogoutCallbackURLs                  []string
	LaunchURL                           *string
	IsPublic                            bool
	PkceEnabled                         bool
	PkceSupported                       bool
	RequiresReauthentication            bool
	RequiresPushedAuthorizationRequests bool
	SkipConsent                         bool
	IsGroupRestricted                   bool
	ClientType                          string
	HasLogo                             bool
	LogoURL                             *string
	Secrets                             []SecretView
	AccessTokenDurationMinutes          int64
	RefreshTokenDurationMinutes         int64
	BackchannelLogoutURI                string
	BackchannelLogoutSessionRequired    bool
	AllowedGrantTypes                   []string
	// MetadataExpiresAt is when the surface may re-fetch a CIMD document
	// on its own; a registered client carries nil.
	MetadataExpiresAt  *time.Time
	MetadataGrantTypes []string
	AllowedGroups      []GroupRef
	CreatedByID        string
	CreatedAt          time.Time
}

// GroupRef is one group the restriction names, in the shape the account
// views carry their memberships in: the wire identifier and the two names.
type GroupRef struct {
	ID          string
	Name        string
	DisplayName string
}

// MetaView is the display facts a sign-in page renders.
type MetaView struct {
	ID                       string
	Name                     string
	Description              string
	LaunchURL                *string
	ClientType               string
	HasLogo                  bool
	RequiresReauthentication bool
}

// view renders the row at the given instant, the secrets judged against it.
func (c ClientSchema) view(now time.Time) ClientView {
	name := ""
	if c.Name != nil {
		name = *c.Name
	}
	stored := readCredentials(c.Credentials)
	secrets := make([]SecretView, 0, len(stored.Secrets))
	for _, secret := range stored.Secrets {
		secrets = append(secrets, secret.view(now))
	}
	return ClientView{
		ID:                                  c.ID,
		Name:                                name,
		Description:                         c.Description,
		CallbackURLs:                        c.CallbackURLs,
		LogoutCallbackURLs:                  c.LogoutCallbackURLs,
		LaunchURL:                           c.LaunchURL,
		IsPublic:                            c.IsPublic,
		PkceEnabled:                         c.PkceEnabled,
		PkceSupported:                       c.PkceSupported,
		RequiresReauthentication:            c.RequiresReauthentication,
		RequiresPushedAuthorizationRequests: c.RequiresPushedAuthorizationRequests,
		SkipConsent:                         c.SkipConsent,
		IsGroupRestricted:                   c.IsGroupRestricted,
		ClientType:                          c.ClientType,
		HasLogo:                             c.LogoPath != nil && *c.LogoPath != "",
		Secrets:                             secrets,
		AccessTokenDurationMinutes:          c.AccessTokenDurationMinutes,
		RefreshTokenDurationMinutes:         c.RefreshTokenDurationMinutes,
		BackchannelLogoutURI:                c.BackchannelLogoutURI,
		BackchannelLogoutSessionRequired:    c.BackchannelLogoutSessionRequired,
		AllowedGrantTypes:                   c.AllowedGrantTypes,
		MetadataExpiresAt:                   c.MetadataExpiresAt,
		MetadataGrantTypes:                  c.MetadataGrantTypes,
		CreatedByID:                         wireCreatedBy(c.CreatedByID),
		CreatedAt:                           c.CreatedAt,
	}
}

// wireCreatedBy renders the defining account's row UUID in the wire form the
// account views carry. A row the schema's default created — no operator
// behind it — answers absent.
func wireCreatedBy(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return user.FormatID(*id)
}
