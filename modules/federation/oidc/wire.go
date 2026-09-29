package oidc

import (
	"context"
	"errors"
	"math"
	"uuid"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/riipandi/tango/codegen/proto/go/tango/common/v1"
	federationv1 "github.com/riipandi/tango/codegen/proto/go/tango/federation/v1"
	identityv1 "github.com/riipandi/tango/codegen/proto/go/tango/identity/v1"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/jwtutils"
	"github.com/riipandi/tango/pkg/responder"
)

// wireClient maps the service view onto the wire message. The hashes never
// travel: the credentials block carries the views, and the raw values are
// the create-secret responses' field alone.
func wireClient(view ClientView) *federationv1.OidcClient {
	client := &federationv1.OidcClient{
		Id:                                  view.ID,
		Name:                                view.Name,
		Description:                         &view.Description,
		CallbackUrls:                        view.CallbackURLs,
		LogoutCallbackUrls:                  view.LogoutCallbackURLs,
		IsPublic:                            view.IsPublic,
		PkceEnabled:                         view.PkceEnabled,
		PkceSupported:                       view.PkceSupported,
		RequiresReauthentication:            view.RequiresReauthentication,
		RequiresPushedAuthorizationRequests: view.RequiresPushedAuthorizationRequests,
		SkipConsent:                         view.SkipConsent,
		IsGroupRestricted:                   view.IsGroupRestricted,
		ClientType:                          view.ClientType,
		HasLogo:                             view.HasLogo,
		Credentials:                         wireCredentials(view.Secrets),
		AccessTokenDurationMinutes:          view.AccessTokenDurationMinutes,
		RefreshTokenDurationMinutes:         view.RefreshTokenDurationMinutes,
		BackchannelLogoutSessionRequired:    view.BackchannelLogoutSessionRequired,
		AllowedGrantTypes:                   view.AllowedGrantTypes,
		CreatedById:                         &view.CreatedByID,
		CreatedAt:                           timestamppb.New(view.CreatedAt),
	}
	if view.BackchannelLogoutURI != "" {
		client.BackchannelLogoutUri = &view.BackchannelLogoutURI
	}
	if view.Description == "" {
		client.Description = nil
	}
	if view.LaunchURL != nil {
		client.LaunchUrl = view.LaunchURL
	}
	if view.LogoURL != nil {
		client.LogoUrl = view.LogoURL
	}
	if view.CreatedByID == "" {
		client.CreatedById = nil
	}
	for _, group := range view.AllowedGroups {
		client.AllowedUserGroups = append(client.AllowedUserGroups, &identityv1.UserGroup{
			Id:          group.ID,
			Name:        group.Name,
			DisplayName: group.DisplayName,
		})
	}
	return client
}

// wireClients maps a page of views.
func wireClients(views []ClientView) []*federationv1.OidcClient {
	clients := make([]*federationv1.OidcClient, 0, len(views))
	for _, view := range views {
		clients = append(clients, wireClient(view))
	}
	return clients
}

// wireAuthorizedClient maps one ledger row: the client's view, the scopes
// the consent covers, and the last-use instant when a grant rode it.
func wireAuthorizedClient(view AuthorizedClientView) *federationv1.AuthorizedOidcClient {
	entry := &federationv1.AuthorizedOidcClient{
		Client: wireClient(view.Client),
		Scopes: view.Scopes,
	}
	if view.LastUsedAt != nil {
		entry.LastUsedAt = timestamppb.New(*view.LastUsedAt)
	}
	return entry
}

// wireLedgerEntry maps one ledger row of the deployment-wide read, the
// account named in its wire form.
func wireLedgerEntry(entry LedgerEntry) *federationv1.AuthorizedOidcClientEntry {
	wide := &federationv1.AuthorizedOidcClientEntry{
		UserId: entry.UserWire,
		Client: wireClient(entry.Client),
		Scopes: entry.Scopes,
	}
	if entry.LastUsedAt != nil {
		wide.LastUsedAt = timestamppb.New(*entry.LastUsedAt)
	}
	return wide
}

// wireCredentials maps a page of secret views.
func wireCredentials(secrets []SecretView) *federationv1.OidcClientCredentials {
	list := make([]*federationv1.OidcClientSecret, 0, len(secrets))
	for _, secret := range secrets {
		list = append(list, wireSecret(secret))
	}
	return &federationv1.OidcClientCredentials{Secrets: list}
}

// wireSecret maps one secret view. The hash is not a field of the wire
// message, so it cannot travel by accident.
func wireSecret(view SecretView) *federationv1.OidcClientSecret {
	secret := &federationv1.OidcClientSecret{
		Id:        view.ID,
		Prefix:    view.Prefix,
		IsActive:  view.Active,
		CreatedAt: timestamppb.New(view.CreatedAt),
	}
	if view.ExpiresAt != nil {
		secret.ExpiresAt = timestamppb.New(*view.ExpiresAt)
	}
	return secret
}

// wireMeta maps the display facts.
func wireMeta(view MetaView) *federationv1.OidcClientMeta {
	meta := &federationv1.OidcClientMeta{
		Id:                       view.ID,
		Name:                     view.Name,
		Description:              view.Description,
		ClientType:               view.ClientType,
		HasLogo:                  view.HasLogo,
		RequiresReauthentication: view.RequiresReauthentication,
	}
	if view.LaunchURL != nil {
		meta.LaunchUrl = view.LaunchURL
	}
	return meta
}

// optionalString turns an optional wire field into the pointer the service
// carries. The optional field's presence is the distinction: a value that
// names clearing and one that names unset are the same here, because the
// fields it serves are strings an empty value clears.
func optionalString(value *string) *string {
	if value == nil {
		return nil
	}
	return value
}

// mapError translates the service's failures into the codes the Connect
// protocol carries. The internal ones are collapsed to one answer whose text
// names nothing a caller could aim at.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrClientNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("OIDC client not found"))
	case errors.Is(err, ErrPreviewUnknownUser):
		return connect.NewError(connect.CodeNotFound, errors.New("the account does not exist"))
	case errors.Is(err, ErrSecretNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("client secret not found"))
	case errors.Is(err, ErrClientExists):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("the client id is already in use"))
	case errors.Is(err, ErrGroupUnknown):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("a named user group does not exist"))
	case errors.Is(err, ErrUnsupportedLogo):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("the logo must be a PNG, JPEG, or WebP image"))
	case errors.Is(err, ErrLogosUnavailable):
		return connect.NewError(connect.CodeUnavailable, errors.New("logo storage is not available"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("OIDC client operation failed"))
	}
}

// callerUUID turns the caller the guard established into the account the
// rows carry. The subject travels in the wire form — the TypeID the caller's
// owner is named by — and the rows keep their UUID, so the boundary is this
// one function.
func callerUUID(ctx context.Context) (uuid.UUID, error) {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok || caller == nil {
		return uuid.Nil(), connect.NewError(connect.CodeInternal, errors.New("authentication state missing"))
	}
	id, err := user.UUIDFromWire(caller.UserID)
	if err != nil {
		return uuid.Nil(), connect.NewError(connect.CodeInternal, errors.New("authentication state missing"))
	}
	return id, nil
}

// metadataOf maps the responder's pagination onto the shared block. The wire
// fields are optional, so an unknown range is absent rather than zero.
func metadataOf(p responder.Pagination) *commonv1.ListMetadata {
	meta := &commonv1.ListMetadata{}
	set := func(dst **int32, src *int) {
		if src == nil {
			return
		}
		// The wire field is int32; a total beyond it saturates rather than
		// wrapping, and no page the rules allow can reach the bound.
		value := *src
		if value > math.MaxInt32 || value < math.MinInt32 {
			value = math.MaxInt32
		}
		narrowed := int32(value)
		*dst = &narrowed
	}
	set(&meta.Page, p.Page)
	set(&meta.Limit, p.Limit)
	set(&meta.TotalPages, p.TotalPages)
	set(&meta.TotalItems, p.TotalItems)
	set(&meta.FirstItemIndex, p.FirstItemIndex)
	set(&meta.LastItemIndex, p.LastItemIndex)
	return meta
}
