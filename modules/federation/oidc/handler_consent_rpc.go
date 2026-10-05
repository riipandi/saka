package oidc

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	federationv1 "github.com/riipandi/saka/codegen/proto/go/saka/federation/v1"
	federationv1connect "github.com/riipandi/saka/codegen/proto/go/saka/federation/v1/federationv1connect"
	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/modules/identity/user"
)

// consentHandler is the transport mapping of the consent procedures. It
// shares the client service — the ledger is one feature's rows — and the
// wire mapping lives in wire.go beside the client surface's.
type consentHandler struct {
	service *Service
}

// newConsentHandler builds the consent handler over the service.
func newConsentHandler(service *Service) federationv1connect.OidcConsentServiceHandler {
	return &consentHandler{service: service}
}

// ListMyAuthorizedClients answers the calling account's ledger.
func (h *consentHandler) ListMyAuthorizedClients(ctx context.Context, _ *connect.Request[federationv1.ListMyAuthorizedClientsRequest]) (*connect.Response[federationv1.ListMyAuthorizedClientsResponse], error) {
	caller, err := callerUUID(ctx)
	if err != nil {
		return nil, err
	}
	views, err := h.service.MyAuthorizedClients(ctx, caller.String())
	if err != nil {
		return nil, mapError(err)
	}
	clients := make([]*federationv1.AuthorizedOidcClient, 0, len(views))
	for _, view := range views {
		clients = append(clients, wireAuthorizedClient(view))
	}
	return connect.NewResponse(&federationv1.ListMyAuthorizedClientsResponse{
		Clients: clients,
		Status:  webutil.StatusSuccess,
		Message: "the authorized clients were listed",
	}), nil
}

// RevokeMyAuthorizedClient withdraws the calling account's consent for
// one client, tokens included.
func (h *consentHandler) RevokeMyAuthorizedClient(ctx context.Context, req *connect.Request[federationv1.RevokeMyAuthorizedClientRequest]) (*connect.Response[federationv1.RevokeMyAuthorizedClientResponse], error) {
	caller, err := callerUUID(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.service.RevokeMyAuthorizedClient(ctx, caller.String(), req.Msg.GetClientId()); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&federationv1.RevokeMyAuthorizedClientResponse{
		Status:  webutil.StatusSuccess,
		Message: "the client's authorization was revoked",
	}), nil
}

// ListMyClients answers the clients the account may authorize.
func (h *consentHandler) ListMyClients(ctx context.Context, _ *connect.Request[federationv1.ListMyOidcClientsRequest]) (*connect.Response[federationv1.ListMyOidcClientsResponse], error) {
	caller, err := callerUUID(ctx)
	if err != nil {
		return nil, err
	}
	views, err := h.service.MyClients(ctx, caller.String())
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&federationv1.ListMyOidcClientsResponse{
		Clients: wireClients(views),
		Status:  webutil.StatusSuccess,
		Message: "the accessible clients were listed",
	}), nil
}

// ListUserAuthorizedClients answers one account's ledger — the
// administrative read.
func (h *consentHandler) ListUserAuthorizedClients(ctx context.Context, req *connect.Request[federationv1.ListUserAuthorizedClientsRequest]) (*connect.Response[federationv1.ListUserAuthorizedClientsResponse], error) {
	id, err := user.UUIDFromWire(req.Msg.GetUserId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("the account does not exist"))
	}
	views, err := h.service.UserAuthorizedClients(ctx, id.String())
	if err != nil {
		return nil, mapError(err)
	}
	clients := make([]*federationv1.AuthorizedOidcClient, 0, len(views))
	for _, view := range views {
		clients = append(clients, wireAuthorizedClient(view))
	}
	return connect.NewResponse(&federationv1.ListUserAuthorizedClientsResponse{
		Clients: clients,
		Status:  webutil.StatusSuccess,
		Message: "the authorized clients were listed",
	}), nil
}

// ListAllAuthorizedClients answers one page of the deployment-wide ledger.
func (h *consentHandler) ListAllAuthorizedClients(ctx context.Context, req *connect.Request[federationv1.ListAllAuthorizedClientsRequest]) (*connect.Response[federationv1.ListAllAuthorizedClientsResponse], error) {
	entries, pagination, err := h.service.AllAuthorizedClients(ctx, int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}
	rows := make([]*federationv1.AuthorizedOidcClientEntry, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, wireLedgerEntry(entry))
	}
	return connect.NewResponse(&federationv1.ListAllAuthorizedClientsResponse{
		Entries:  rows,
		Metadata: metadataOf(pagination),
		Status:   webutil.StatusSuccess,
		Message:  "the authorized clients were listed",
	}), nil
}
