package oidc

import (
	"context"

	"connectrpc.com/connect/v2"

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
func (h *consentHandler) ListMyAuthorizedClients(ctx context.Context, _ *federationv1.ListMyAuthorizedClientsRequest) (*federationv1.ListMyAuthorizedClientsResponse, error) {
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
	return &federationv1.ListMyAuthorizedClientsResponse{
		Clients: clients,
		Status:  webutil.StatusSuccess,
		Message: "the authorized clients were listed",
	}, nil
}

// RevokeMyAuthorizedClient withdraws the calling account's consent for
// one client, tokens included.
func (h *consentHandler) RevokeMyAuthorizedClient(ctx context.Context, req *federationv1.RevokeMyAuthorizedClientRequest) (*federationv1.RevokeMyAuthorizedClientResponse, error) {
	caller, err := callerUUID(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.service.RevokeMyAuthorizedClient(ctx, caller.String(), req.GetClientId()); err != nil {
		return nil, mapError(err)
	}
	return &federationv1.RevokeMyAuthorizedClientResponse{
		Status:  webutil.StatusSuccess,
		Message: "the client's authorization was revoked",
	}, nil
}

// ListMyClients answers the clients the account may authorize.
func (h *consentHandler) ListMyClients(ctx context.Context, _ *federationv1.ListMyOidcClientsRequest) (*federationv1.ListMyOidcClientsResponse, error) {
	caller, err := callerUUID(ctx)
	if err != nil {
		return nil, err
	}
	views, err := h.service.MyClients(ctx, caller.String())
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.ListMyOidcClientsResponse{
		Clients: wireClients(views),
		Status:  webutil.StatusSuccess,
		Message: "the accessible clients were listed",
	}, nil
}

// ListUserAuthorizedClients answers one account's ledger — the
// administrative read.
func (h *consentHandler) ListUserAuthorizedClients(ctx context.Context, req *federationv1.ListUserAuthorizedClientsRequest) (*federationv1.ListUserAuthorizedClientsResponse, error) {
	id, err := user.UUIDFromWire(req.GetUserId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, "the account does not exist")
	}
	views, err := h.service.UserAuthorizedClients(ctx, id.String())
	if err != nil {
		return nil, mapError(err)
	}
	clients := make([]*federationv1.AuthorizedOidcClient, 0, len(views))
	for _, view := range views {
		clients = append(clients, wireAuthorizedClient(view))
	}
	return &federationv1.ListUserAuthorizedClientsResponse{
		Clients: clients,
		Status:  webutil.StatusSuccess,
		Message: "the authorized clients were listed",
	}, nil
}

// ListAllAuthorizedClients answers one page of the deployment-wide ledger.
func (h *consentHandler) ListAllAuthorizedClients(ctx context.Context, req *federationv1.ListAllAuthorizedClientsRequest) (*federationv1.ListAllAuthorizedClientsResponse, error) {
	entries, pagination, err := h.service.AllAuthorizedClients(ctx, int(req.GetPage()), int(req.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}
	rows := make([]*federationv1.AuthorizedOidcClientEntry, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, wireLedgerEntry(entry))
	}
	return &federationv1.ListAllAuthorizedClientsResponse{
		Entries:  rows,
		Metadata: metadataOf(pagination),
		Status:   webutil.StatusSuccess,
		Message:  "the authorized clients were listed",
	}, nil
}
