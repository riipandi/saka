package oidc

import (
	"context"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/structpb"

	federationv1 "github.com/riipandi/saka/codegen/proto/go/saka/federation/v1"
	federationv1connect "github.com/riipandi/saka/codegen/proto/go/saka/federation/v1/federationv1connect"
	"github.com/riipandi/saka/framework/webutil"
)

// rpcHandler is the transport mapping of the procedures. The service carries
// the rules; this type carries the connect codes and the caller's identity.
// The wire mapping — the views onto the generated messages, and the
// service's failures onto the codes — lives in wire.go.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) federationv1connect.OidcClientServiceHandler {
	return &rpcHandler{service: service}
}

// ListClients answers one page of the clients.
func (h *rpcHandler) ListClients(ctx context.Context, req *federationv1.ListOidcClientsRequest) (*federationv1.ListOidcClientsResponse, error) {
	clients, pagination, err := h.service.List(ctx, req.GetSearch(), req.GetSortBy(), req.GetSortOrder() == "asc", int(req.GetPage()), int(req.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.ListOidcClientsResponse{
		Clients:  wireClients(clients),
		Metadata: metadataOf(pagination),
		Status:   webutil.StatusSuccess,
		Message:  "the OIDC clients were listed",
	}, nil
}

// CreateClient defines a client and mints its first secret.
func (h *rpcHandler) CreateClient(ctx context.Context, req *federationv1.CreateOidcClientRequest) (*federationv1.CreateOidcClientResponse, error) {
	callerID, err := callerUUID(ctx)
	if err != nil {
		return nil, err
	}
	issued, err := h.service.Create(ctx, callerID, CreateParams{
		ID:                                  req.GetId(),
		Name:                                req.Name,
		Description:                         req.GetDescription(),
		CallbackURLs:                        req.CallbackUrls,
		LogoutCallbackURLs:                  req.LogoutCallbackUrls,
		LaunchURL:                           optionalString(req.LaunchUrl),
		IsPublic:                            req.IsPublic,
		PkceEnabled:                         req.PkceEnabled,
		RequiresReauthentication:            req.RequiresReauthentication,
		RequiresPushedAuthorizationRequests: req.RequiresPushedAuthorizationRequests,
		SkipConsent:                         req.SkipConsent,
		AccessTokenDurationMinutes:          req.GetAccessTokenDurationMinutes(),
		RefreshTokenDurationMinutes:         req.GetRefreshTokenDurationMinutes(),
		BackchannelLogoutURI:                req.GetBackchannelLogoutUri(),
		BackchannelLogoutSessionRequired:    req.BackchannelLogoutSessionRequired,
		AllowedGrantWires:                   req.AllowedGrantTypes,
		AllowedGroupWires:                   req.AllowedUserGroups,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.CreateOidcClientResponse{
		Client:  wireClient(issued.Client),
		Secret:  issued.Secret,
		Status:  webutil.StatusSuccess,
		Message: "the OIDC client was created",
	}, nil
}

// GetClient answers one client's full view.
func (h *rpcHandler) GetClient(ctx context.Context, req *federationv1.GetOidcClientRequest) (*federationv1.GetOidcClientResponse, error) {
	client, err := h.service.Get(ctx, req.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.GetOidcClientResponse{
		Client:  wireClient(client),
		Status:  webutil.StatusSuccess,
		Message: "the OIDC client was read",
	}, nil
}

// UpdateClient replaces a client's fields.
func (h *rpcHandler) UpdateClient(ctx context.Context, req *federationv1.UpdateOidcClientRequest) (*federationv1.UpdateOidcClientResponse, error) {
	client, err := h.service.Update(ctx, req.Id, UpdateParams{
		Name:                                req.Name,
		Description:                         req.GetDescription(),
		CallbackURLs:                        req.CallbackUrls,
		LogoutCallbackURLs:                  req.LogoutCallbackUrls,
		LaunchURL:                           optionalString(req.LaunchUrl),
		IsPublic:                            req.IsPublic,
		PkceEnabled:                         req.PkceEnabled,
		RequiresReauthentication:            req.RequiresReauthentication,
		RequiresPushedAuthorizationRequests: req.RequiresPushedAuthorizationRequests,
		SkipConsent:                         req.SkipConsent,
		AccessTokenDurationMinutes:          req.GetAccessTokenDurationMinutes(),
		RefreshTokenDurationMinutes:         req.GetRefreshTokenDurationMinutes(),
		BackchannelLogoutURI:                req.GetBackchannelLogoutUri(),
		BackchannelLogoutSessionRequired:    req.BackchannelLogoutSessionRequired,
		AllowedGrantWires:                   req.AllowedGrantTypes,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.UpdateOidcClientResponse{
		Client:  wireClient(client),
		Status:  webutil.StatusSuccess,
		Message: "the OIDC client was updated",
	}, nil
}

// DeleteClient removes a client.
func (h *rpcHandler) DeleteClient(ctx context.Context, req *federationv1.DeleteOidcClientRequest) (*federationv1.DeleteOidcClientResponse, error) {
	if err := h.service.Delete(ctx, req.Id); err != nil {
		return nil, mapError(err)
	}
	return &federationv1.DeleteOidcClientResponse{
		Status:  webutil.StatusSuccess,
		Message: "the OIDC client was deleted",
	}, nil
}

// UpdateAllowedUserGroups replaces the group restriction's set.
func (h *rpcHandler) UpdateAllowedUserGroups(ctx context.Context, req *federationv1.UpdateOidcClientAllowedUserGroupsRequest) (*federationv1.UpdateOidcClientAllowedUserGroupsResponse, error) {
	client, err := h.service.SetAllowedGroups(ctx, req.Id, req.UserGroupIds)
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.UpdateOidcClientAllowedUserGroupsResponse{
		Client:  wireClient(client),
		Status:  webutil.StatusSuccess,
		Message: "the allowed user groups were updated",
	}, nil
}

// GetClientMeta answers the display facts a sign-in page renders.
func (h *rpcHandler) GetClientMeta(ctx context.Context, req *federationv1.GetOidcClientMetaRequest) (*federationv1.GetOidcClientMetaResponse, error) {
	meta, err := h.service.Meta(ctx, req.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.GetOidcClientMetaResponse{
		Meta:    wireMeta(meta),
		Status:  webutil.StatusSuccess,
		Message: "the OIDC client metadata was read",
	}, nil
}

// PreviewClient answers the claim maps one account would produce.
func (h *rpcHandler) PreviewClient(ctx context.Context, req *federationv1.PreviewOidcClientRequest) (*federationv1.PreviewOidcClientResponse, error) {
	idToken, accessToken, userInfo, err := h.service.Preview(ctx, req.Id, req.UserId)
	if err != nil {
		return nil, mapError(err)
	}
	idTokenStruct, err := structpb.NewStruct(idToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "the preview could not be rendered")
	}
	accessTokenStruct, err := structpb.NewStruct(accessToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "the preview could not be rendered")
	}
	userInfoStruct, err := structpb.NewStruct(userInfo)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "the preview could not be rendered")
	}
	return &federationv1.PreviewOidcClientResponse{
		Preview: &federationv1.OidcClientPreview{
			IdToken:     idTokenStruct,
			AccessToken: accessTokenStruct,
			UserInfo:    userInfoStruct,
		},
		Status:  webutil.StatusSuccess,
		Message: "the OIDC client preview was built",
	}, nil
}

// UploadLogo replaces a client's logo.
func (h *rpcHandler) UploadLogo(ctx context.Context, req *federationv1.UploadOidcClientLogoRequest) (*federationv1.UploadOidcClientLogoResponse, error) {
	if err := h.service.UploadLogo(ctx, req.Id, req.Logo); err != nil {
		return nil, mapError(err)
	}
	return &federationv1.UploadOidcClientLogoResponse{
		Status:  webutil.StatusSuccess,
		Message: "the client logo was updated",
	}, nil
}

// DeleteLogo removes a client's logo.
func (h *rpcHandler) DeleteLogo(ctx context.Context, req *federationv1.DeleteOidcClientLogoRequest) (*federationv1.DeleteOidcClientLogoResponse, error) {
	if err := h.service.DeleteLogo(ctx, req.Id); err != nil {
		return nil, mapError(err)
	}
	return &federationv1.DeleteOidcClientLogoResponse{
		Status:  webutil.StatusSuccess,
		Message: "the client logo was deleted",
	}, nil
}

// ListSecrets answers a client's secrets as their views.
func (h *rpcHandler) ListSecrets(ctx context.Context, req *federationv1.ListOidcClientSecretsRequest) (*federationv1.ListOidcClientSecretsResponse, error) {
	secrets, err := h.service.ListSecrets(ctx, req.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.ListOidcClientSecretsResponse{
		Credentials: wireCredentials(secrets),
		Status:      webutil.StatusSuccess,
		Message:     "the client secrets were listed",
	}, nil
}

// CreateSecret mints one more secret.
func (h *rpcHandler) CreateSecret(ctx context.Context, req *federationv1.CreateOidcClientSecretRequest) (*federationv1.CreateOidcClientSecretResponse, error) {
	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		at := req.ExpiresAt.AsTime()
		expiresAt = &at
	}
	issued, err := h.service.CreateSecret(ctx, req.Id, req.GetSecret(), expiresAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.CreateOidcClientSecretResponse{
		Secret:  wireSecret(issued.Secret),
		Value:   issued.Value,
		Status:  webutil.StatusSuccess,
		Message: "the client secret was created",
	}, nil
}

// DeleteSecret withdraws one secret.
func (h *rpcHandler) DeleteSecret(ctx context.Context, req *federationv1.DeleteOidcClientSecretRequest) (*federationv1.DeleteOidcClientSecretResponse, error) {
	if err := h.service.DeleteSecret(ctx, req.Id, req.SecretId); err != nil {
		return nil, mapError(err)
	}
	return &federationv1.DeleteOidcClientSecretResponse{
		Status:  webutil.StatusSuccess,
		Message: "the client secret was deleted",
	}, nil
}

// RefreshClient forces a CIMD client's metadata document to be re-fetched.
func (h *rpcHandler) RefreshClient(ctx context.Context, req *federationv1.RefreshOidcClientRequest) (*federationv1.RefreshOidcClientResponse, error) {
	client, err := h.service.RefreshCIMDClient(ctx, req.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.RefreshOidcClientResponse{
		Client:  wireClient(client),
		Status:  webutil.StatusSuccess,
		Message: "the client metadata document was refreshed",
	}, nil
}
