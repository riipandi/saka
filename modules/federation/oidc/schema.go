package oidc

import (
	"errors"
	"time"
	"uuid"

	"github.com/riipandi/tango/modules/identity/user"
)

// ClientTable is the OIDC clients table. The migrations own the schema; this
// constant is how Go code names it, so a table rename touches one line.
const ClientTable = "public.oidc_clients"

// AllowedGroupsTable is the client-side group restriction: the junction that
// names the groups an account must belong to for the client to admit it.
const AllowedGroupsTable = "public.oidc_clients_allowed_user_groups"

// ResourceOidcClient is the resource type an audit record names when the
// change is about a client. The client's id — the relying party's client_id,
// not a row UUID — is what a reader of the record matches against, through
// the payload: the resource_id column is a uuid, and the client id is not.
const ResourceOidcClient = "oidc_client"

// ClientTypeStandard is the client_type of a client defined on this server.
// The `cimd` type — a client materialized from a metadata document — is not
// served yet; the schema carries its columns for that phase.
const ClientTypeStandard = "standard"

// The token windows a creation that leaves the fields unset writes. They are
// the schema's defaults, spelled here because the row is written once, whole.
const (
	DefaultAccessTokenMinutes  = 60
	DefaultRefreshTokenMinutes = 43200
)

// The failures the client procedures report. The handler maps them to
// connect codes, the way every feature's failures are mapped.
var (
	// ErrClientNotFound is a request whose identifier names no client. A
	// malformed identifier answers it too: the shape is the not-found one
	// either way.
	ErrClientNotFound = errors.New("oidc: the client does not exist")

	// ErrClientExists is a creation whose identifier another client holds.
	ErrClientExists = errors.New("oidc: the client id is already in use")

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

// ClientSchema is one row of ClientTable. The identifier is the relying
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
	CreatedByID                         *uuid.UUID
	CreatedAt                           time.Time
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
	AllowedGroups                       []GroupRef
	CreatedByID                         string
	CreatedAt                           time.Time
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
