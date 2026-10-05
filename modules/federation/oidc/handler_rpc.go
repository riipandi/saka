package oidc

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
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
func (h *rpcHandler) ListClients(ctx context.Context, req *connect.Request[federationv1.ListOidcClientsRequest]) (*connect.Response[federationv1.ListOidcClientsResponse], error) {
	clients, pagination, err := h.service.List(ctx, req.Msg.GetSearch(), req.Msg.GetSortBy(), req.Msg.GetSortOrder() == "asc", int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&federationv1.ListOidcClientsResponse{
		Clients:  wireClients(clients),
		Metadata: metadataOf(pagination),
		Status:   webutil.StatusSuccess,
		Message:  "the OIDC clients were listed",
	}), nil
}

// CreateClient defines a client and mints its first secret.
func (h *rpcHandler) CreateClient(ctx context.Context, req *connect.Request[federationv1.CreateOidcClientRequest]) (*connect.Response[federationv1.CreateOidcClientResponse], error) {
	callerID, err := callerUUID(ctx)
	if err != nil {
		return nil, err
	}
	issued, err := h.service.Create(ctx, callerID, CreateParams{
		ID:                                  req.Msg.GetId(),
		Name:                                req.Msg.Name,
		Description:                         req.Msg.GetDescription(),
		CallbackURLs:                        req.Msg.CallbackUrls,
		LogoutCallbackURLs:                  req.Msg.LogoutCallbackUrls,
		LaunchURL:                           optionalString(req.Msg.LaunchUrl),
		IsPublic:                            req.Msg.IsPublic,
		PkceEnabled:                         req.Msg.PkceEnabled,
		RequiresReauthentication:            req.Msg.RequiresReauthentication,
		RequiresPushedAuthorizationRequests: req.Msg.RequiresPushedAuthorizationRequests,
		SkipConsent:                         req.Msg.SkipConsent,
		AccessTokenDurationMinutes:          req.Msg.GetAccessTokenDurationMinutes(),
		RefreshTokenDurationMinutes:         req.Msg.GetRefreshTokenDurationMinutes(),
		BackchannelLogoutURI:                req.Msg.GetBackchannelLogoutUri(),
		BackchannelLogoutSessionRequired:    req.Msg.BackchannelLogoutSessionRequired,
		AllowedGrantWires:                   req.Msg.AllowedGrantTypes,
		AllowedGroupWires:                   req.Msg.AllowedUserGroups,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&federationv1.CreateOidcClientResponse{
		Client:  wireClient(issued.Client),
		Secret:  issued.Secret,
		Status:  webutil.StatusSuccess,
		Message: "the OIDC client was created",
	}), nil
}

// GetClient answers one client's full view.
func (h *rpcHandler) GetClient(ctx context.Context, req *connect.Request[federationv1.GetOidcClientRequest]) (*connect.Response[federationv1.GetOidcClientResponse], error) {
	client, err := h.service.Get(ctx, req.Msg.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&federationv1.GetOidcClientResponse{
		Client:  wireClient(client),
		Status:  webutil.StatusSuccess,
		Message: "the OIDC client was read",
	}), nil
}

// UpdateClient replaces a client's fields.
func (h *rpcHandler) UpdateClient(ctx context.Context, req *connect.Request[federationv1.UpdateOidcClientRequest]) (*connect.Response[federationv1.UpdateOidcClientResponse], error) {
	client, err := h.service.Update(ctx, req.Msg.Id, UpdateParams{
		Name:                                req.Msg.Name,
		Description:                         req.Msg.GetDescription(),
		CallbackURLs:                        req.Msg.CallbackUrls,
		LogoutCallbackURLs:                  req.Msg.LogoutCallbackUrls,
		LaunchURL:                           optionalString(req.Msg.LaunchUrl),
		IsPublic:                            req.Msg.IsPublic,
		PkceEnabled:                         req.Msg.PkceEnabled,
		RequiresReauthentication:            req.Msg.RequiresReauthentication,
		RequiresPushedAuthorizationRequests: req.Msg.RequiresPushedAuthorizationRequests,
		SkipConsent:                         req.Msg.SkipConsent,
		AccessTokenDurationMinutes:          req.Msg.GetAccessTokenDurationMinutes(),
		RefreshTokenDurationMinutes:         req.Msg.GetRefreshTokenDurationMinutes(),
		BackchannelLogoutURI:                req.Msg.GetBackchannelLogoutUri(),
		BackchannelLogoutSessionRequired:    req.Msg.BackchannelLogoutSessionRequired,
		AllowedGrantWires:                   req.Msg.AllowedGrantTypes,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&federationv1.UpdateOidcClientResponse{
		Client:  wireClient(client),
		Status:  webutil.StatusSuccess,
		Message: "the OIDC client was updated",
	}), nil
}

// DeleteClient removes a client.
func (h *rpcHandler) DeleteClient(ctx context.Context, req *connect.Request[federationv1.DeleteOidcClientRequest]) (*connect.Response[federationv1.DeleteOidcClientResponse], error) {
	if err := h.service.Delete(ctx, req.Msg.Id); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&federationv1.DeleteOidcClientResponse{
		Status:  webutil.StatusSuccess,
		Message: "the OIDC client was deleted",
	}), nil
}

// UpdateAllowedUserGroups replaces the group restriction's set.
func (h *rpcHandler) UpdateAllowedUserGroups(ctx context.Context, req *connect.Request[federationv1.UpdateOidcClientAllowedUserGroupsRequest]) (*connect.Response[federationv1.UpdateOidcClientAllowedUserGroupsResponse], error) {
	client, err := h.service.SetAllowedGroups(ctx, req.Msg.Id, req.Msg.UserGroupIds)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&federationv1.UpdateOidcClientAllowedUserGroupsResponse{
		Client:  wireClient(client),
		Status:  webutil.StatusSuccess,
		Message: "the allowed user groups were updated",
	}), nil
}

// GetClientMeta answers the display facts a sign-in page renders.
func (h *rpcHandler) GetClientMeta(ctx context.Context, req *connect.Request[federationv1.GetOidcClientMetaRequest]) (*connect.Response[federationv1.GetOidcClientMetaResponse], error) {
	meta, err := h.service.Meta(ctx, req.Msg.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&federationv1.GetOidcClientMetaResponse{
		Meta:    wireMeta(meta),
		Status:  webutil.StatusSuccess,
		Message: "the OIDC client metadata was read",
	}), nil
}

// PreviewClient answers the claim maps one account would produce.
func (h *rpcHandler) PreviewClient(ctx context.Context, req *connect.Request[federationv1.PreviewOidcClientRequest]) (*connect.Response[federationv1.PreviewOidcClientResponse], error) {
	idToken, accessToken, userInfo, err := h.service.Preview(ctx, req.Msg.Id, req.Msg.UserId)
	if err != nil {
		return nil, mapError(err)
	}
	idTokenStruct, err := structpb.NewStruct(idToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("the preview could not be rendered"))
	}
	accessTokenStruct, err := structpb.NewStruct(accessToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("the preview could not be rendered"))
	}
	userInfoStruct, err := structpb.NewStruct(userInfo)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("the preview could not be rendered"))
	}
	return connect.NewResponse(&federationv1.PreviewOidcClientResponse{
		Preview: &federationv1.OidcClientPreview{
			IdToken:     idTokenStruct,
			AccessToken: accessTokenStruct,
			UserInfo:    userInfoStruct,
		},
		Status:  webutil.StatusSuccess,
		Message: "the OIDC client preview was built",
	}), nil
}

// UploadLogo replaces a client's logo.
func (h *rpcHandler) UploadLogo(ctx context.Context, req *connect.Request[federationv1.UploadOidcClientLogoRequest]) (*connect.Response[federationv1.UploadOidcClientLogoResponse], error) {
	if err := h.service.UploadLogo(ctx, req.Msg.Id, req.Msg.Logo); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&federationv1.UploadOidcClientLogoResponse{
		Status:  webutil.StatusSuccess,
		Message: "the client logo was updated",
	}), nil
}

// DeleteLogo removes a client's logo.
func (h *rpcHandler) DeleteLogo(ctx context.Context, req *connect.Request[federationv1.DeleteOidcClientLogoRequest]) (*connect.Response[federationv1.DeleteOidcClientLogoResponse], error) {
	if err := h.service.DeleteLogo(ctx, req.Msg.Id); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&federationv1.DeleteOidcClientLogoResponse{
		Status:  webutil.StatusSuccess,
		Message: "the client logo was deleted",
	}), nil
}

// ListSecrets answers a client's secrets as their views.
func (h *rpcHandler) ListSecrets(ctx context.Context, req *connect.Request[federationv1.ListOidcClientSecretsRequest]) (*connect.Response[federationv1.ListOidcClientSecretsResponse], error) {
	secrets, err := h.service.ListSecrets(ctx, req.Msg.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&federationv1.ListOidcClientSecretsResponse{
		Credentials: wireCredentials(secrets),
		Status:      webutil.StatusSuccess,
		Message:     "the client secrets were listed",
	}), nil
}

// CreateSecret mints one more secret.
func (h *rpcHandler) CreateSecret(ctx context.Context, req *connect.Request[federationv1.CreateOidcClientSecretRequest]) (*connect.Response[federationv1.CreateOidcClientSecretResponse], error) {
	var expiresAt *time.Time
	if req.Msg.ExpiresAt != nil {
		at := req.Msg.ExpiresAt.AsTime()
		expiresAt = &at
	}
	issued, err := h.service.CreateSecret(ctx, req.Msg.Id, req.Msg.GetSecret(), expiresAt)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&federationv1.CreateOidcClientSecretResponse{
		Secret:  wireSecret(issued.Secret),
		Value:   issued.Value,
		Status:  webutil.StatusSuccess,
		Message: "the client secret was created",
	}), nil
}

// DeleteSecret withdraws one secret.
func (h *rpcHandler) DeleteSecret(ctx context.Context, req *connect.Request[federationv1.DeleteOidcClientSecretRequest]) (*connect.Response[federationv1.DeleteOidcClientSecretResponse], error) {
	if err := h.service.DeleteSecret(ctx, req.Msg.Id, req.Msg.SecretId); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&federationv1.DeleteOidcClientSecretResponse{
		Status:  webutil.StatusSuccess,
		Message: "the client secret was deleted",
	}), nil
}

// RefreshClient forces a CIMD client's metadata document to be re-fetched.
func (h *rpcHandler) RefreshClient(ctx context.Context, req *connect.Request[federationv1.RefreshOidcClientRequest]) (*connect.Response[federationv1.RefreshOidcClientResponse], error) {
	client, err := h.service.RefreshCIMDClient(ctx, req.Msg.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&federationv1.RefreshOidcClientResponse{
		Client:  wireClient(client),
		Status:  webutil.StatusSuccess,
		Message: "the client metadata document was refreshed",
	}), nil
}
