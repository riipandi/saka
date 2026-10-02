package oauthsso

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	authnv1 "github.com/riipandi/tango/codegen/proto/go/tango/authn/v1"
)

// rpcHandler is the transport mapping of the OAuth SSO procedures. The
// service carries the rules; this type carries the connect codes and the
// wire forms the contract answers in.
type rpcHandler struct {
	service *Service
}

func newRPCHandler(service *Service) *rpcHandler {
	return &rpcHandler{service: service}
}

// BeginSignIn opens the authorization-code flow. The procedure rides the
// flow phase; until it lands, the contract answers unimplemented rather
// than a half-run flow.
func (h *rpcHandler) BeginSignIn(context.Context, *connect.Request[authnv1.BeginOAuthSignInRequest]) (*connect.Response[authnv1.BeginOAuthSignInResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("the OAuth sign-in flow is not served yet"))
}

// ContinueSignIn completes a paused flow. The procedure rides the flow
// phase; until it lands, the contract answers unimplemented.
func (h *rpcHandler) ContinueSignIn(context.Context, *connect.Request[authnv1.ContinueOAuthSignInRequest]) (*connect.Response[authnv1.CompleteSignInResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("the OAuth sign-in flow is not served yet"))
}

// ListConnections answers the operator's listing, secrets never included.
func (h *rpcHandler) ListConnections(ctx context.Context, req *connect.Request[authnv1.ListOAuthConnectionsRequest]) (*connect.Response[authnv1.ListOAuthConnectionsResponse], error) {
	connections, err := h.service.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("the connections could not be read"))
	}

	out := make([]*authnv1.OAuthConnection, 0, len(connections))
	for _, conn := range connections {
		out = append(out, wireConnection(conn))
	}
	return connect.NewResponse(&authnv1.ListOAuthConnectionsResponse{
		Connections: out,
		Status:      "success",
		Message:     "the connections were read",
	}), nil
}

// GetConnection answers one connection by its identifier, secrets never
// included.
func (h *rpcHandler) GetConnection(ctx context.Context, req *connect.Request[authnv1.GetOAuthConnectionRequest]) (*connect.Response[authnv1.GetOAuthConnectionResponse], error) {
	wireID, err := ParseID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no connection answers this identifier"))
	}
	id := IDToUUID(wireID)

	conn, err := h.service.Get(ctx, id)
	switch {
	case errors.Is(err, ErrConnectionNotFound):
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no connection answers this identifier"))
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, errors.New("the connection could not be read"))
	}

	return connect.NewResponse(&authnv1.GetOAuthConnectionResponse{
		Connection: wireConnection(conn),
		Status:     "success",
		Message:    "the connection was read",
	}), nil
}

// CreateConnection validates, seals, and stores a connection.
func (h *rpcHandler) CreateConnection(ctx context.Context, req *connect.Request[authnv1.CreateOAuthConnectionRequest]) (*connect.Response[authnv1.CreateOAuthConnectionResponse], error) {
	conn, err := h.service.Create(ctx, paramsOf(req.Msg))
	switch {
	case errors.Is(err, ErrInvalidConnection):
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("the connection's fields do not compose into a runnable connection"))
	case errors.Is(err, ErrProviderTaken):
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("a connection with this provider slug already exists"))
	case errors.Is(err, ErrSecretUnavailable):
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("the application secret is not configured to seal the client secret"))
	case errors.Is(err, ErrDiscoveryUnavailable):
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("the discovery document cannot be fetched from this process"))
	case errors.Is(err, ErrDiscoveryFetch), errors.Is(err, ErrDiscoveryInvalid):
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("the discovery document is not a usable OIDC document"))
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, errors.New("the connection could not be stored"))
	}

	return connect.NewResponse(&authnv1.CreateOAuthConnectionResponse{
		Connection: wireConnection(conn),
		Status:     "success",
		Message:    "the connection was stored",
	}), nil
}

// UpdateConnection rewrites one connection's editable fields.
func (h *rpcHandler) UpdateConnection(ctx context.Context, req *connect.Request[authnv1.UpdateOAuthConnectionRequest]) (*connect.Response[authnv1.UpdateOAuthConnectionResponse], error) {
	wireID, err := ParseID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no connection answers this identifier"))
	}
	id := IDToUUID(wireID)

	conn, err := h.service.Update(ctx, id, updateOf(req.Msg))
	switch {
	case errors.Is(err, ErrConnectionNotFound):
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no connection answers this identifier"))
	case errors.Is(err, ErrInvalidConnection):
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("the connection's fields do not compose into a runnable connection"))
	case errors.Is(err, ErrProviderTaken):
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("a connection with this provider slug already exists"))
	case errors.Is(err, ErrSecretUnavailable):
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("the application secret is not configured to seal the client secret"))
	case errors.Is(err, ErrDiscoveryUnavailable):
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("the discovery document cannot be fetched from this process"))
	case errors.Is(err, ErrDiscoveryFetch), errors.Is(err, ErrDiscoveryInvalid):
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("the discovery document is not a usable OIDC document"))
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, errors.New("the connection could not be rewritten"))
	}

	return connect.NewResponse(&authnv1.UpdateOAuthConnectionResponse{
		Connection: wireConnection(conn),
		Status:     "success",
		Message:    "the connection was rewritten",
	}), nil
}

// DeleteConnection removes one connection and everything that rode it.
func (h *rpcHandler) DeleteConnection(ctx context.Context, req *connect.Request[authnv1.DeleteOAuthConnectionRequest]) (*connect.Response[authnv1.DeleteOAuthConnectionResponse], error) {
	wireID, err := ParseID(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no connection answers this identifier"))
	}
	id := IDToUUID(wireID)

	err = h.service.Delete(ctx, id)
	switch {
	case errors.Is(err, ErrConnectionNotFound):
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no connection answers this identifier"))
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, errors.New("the connection could not be removed"))
	}

	return connect.NewResponse(&authnv1.DeleteOAuthConnectionResponse{
		Status:  "success",
		Message: "the connection was removed",
	}), nil
}

// ListLinkedConnections answers the holder's ledger. The procedure rides
// the account-surface phase; until it lands, the contract answers
// unimplemented.
func (h *rpcHandler) ListLinkedConnections(context.Context, *connect.Request[authnv1.ListLinkedConnectionsRequest]) (*connect.Response[authnv1.ListLinkedConnectionsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("the linked-account surface is not served yet"))
}

// UnlinkConnection removes one linked account. The procedure rides the
// account-surface phase; until it lands, the contract answers
// unimplemented.
func (h *rpcHandler) UnlinkConnection(context.Context, *connect.Request[authnv1.UnlinkConnectionRequest]) (*connect.Response[authnv1.UnlinkConnectionResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("the linked-account surface is not served yet"))
}

// paramsOf maps a create's wire fields onto the service's words.
func paramsOf(msg *authnv1.CreateOAuthConnectionRequest) ConnectionParams {
	params := ConnectionParams{
		Kind:         ConnectionKind(msg.Kind),
		Provider:     msg.Provider,
		DisplayName:  msg.DisplayName,
		DiscoveryURL: msg.DiscoveryUrl,
		ClientID:     msg.ClientId,
		ClientSecret: msg.ClientSecret,
		Scopes:       msg.Scopes,
		Enabled:      msg.GetEnabled(),
	}
	if msg.Endpoints != nil {
		params.Endpoints = wireEndpoints(msg.Endpoints)
	}
	if msg.AttributeMapping != nil {
		params.AttributeMapping = wireMapping(msg.AttributeMapping)
	}
	return params
}

// updateOf maps an update's wire fields onto the service's optional
// words; an unset field is a nil, which the service reads as "keep".
func updateOf(msg *authnv1.UpdateOAuthConnectionRequest) ConnectionUpdate {
	update := ConnectionUpdate{}
	if msg.DisplayName != nil {
		update.DisplayName = msg.DisplayName
	}
	if msg.DiscoveryUrl != nil {
		update.DiscoveryURL = msg.DiscoveryUrl
	}
	if msg.Endpoints != nil {
		endpoints := wireEndpoints(msg.Endpoints)
		update.Endpoints = &endpoints
	}
	if msg.ClientId != nil {
		update.ClientID = msg.ClientId
	}
	if msg.ClientSecret != nil {
		update.ClientSecret = msg.ClientSecret
	}
	if msg.Scopes != nil {
		update.Scopes = msg.Scopes
	}
	if msg.AttributeMapping != nil {
		mapping := wireMapping(msg.AttributeMapping)
		update.AttributeMapping = &mapping
	}
	if msg.Enabled != nil {
		update.Enabled = msg.Enabled
	}
	return update
}

// wireEndpoints maps the wire endpoint set onto the service's.
func wireEndpoints(msg *authnv1.OAuthEndpoints) Endpoints {
	return Endpoints{
		Authorization: msg.Authorization,
		Token:         msg.Token,
		Userinfo:      msg.Userinfo,
		Jwks:          msg.Jwks,
	}
}

// wireMapping maps the wire attribute mapping onto the service's.
func wireMapping(msg *authnv1.OAuthAttributeMapping) AttributeMapping {
	return AttributeMapping{
		Email:      msg.Email,
		GivenName:  msg.GivenName,
		FamilyName: msg.FamilyName,
	}
}

// wireConnection renders the row's wire form. The client secret is
// dropped here, at the one boundary every read crosses — the row carries
// it sealed, and no answer ever carries it at all.
func wireConnection(conn Connection) *authnv1.OAuthConnection {
	out := &authnv1.OAuthConnection{
		Id:           FormatID(conn.ID),
		Kind:         string(conn.Kind),
		Provider:     conn.Provider,
		DisplayName:  conn.DisplayName,
		DiscoveryUrl: conn.DiscoveryURL,
		ClientId:     conn.ClientID,
		Enabled:      conn.Enabled,
	}
	if conn.Endpoints.Authorization != "" || conn.Endpoints.Token != "" ||
		conn.Endpoints.Userinfo != "" || conn.Endpoints.Jwks != "" {
		out.Endpoints = &authnv1.OAuthEndpoints{
			Authorization: conn.Endpoints.Authorization,
			Token:         conn.Endpoints.Token,
			Userinfo:      conn.Endpoints.Userinfo,
			Jwks:          conn.Endpoints.Jwks,
		}
	}
	if len(conn.Scopes) > 0 {
		out.Scopes = strings.Join(conn.Scopes, " ")
	}
	if conn.AttributeMapping.Email != "" || conn.AttributeMapping.GivenName != "" ||
		conn.AttributeMapping.FamilyName != "" {
		out.AttributeMapping = &authnv1.OAuthAttributeMapping{
			Email:      conn.AttributeMapping.Email,
			GivenName:  conn.AttributeMapping.GivenName,
			FamilyName: conn.AttributeMapping.FamilyName,
		}
	}
	if !conn.CreatedAt.IsZero() {
		out.CreatedAt = timestamppb.New(conn.CreatedAt)
	}
	if conn.UpdatedAt != nil {
		out.UpdatedAt = timestamppb.New(*conn.UpdatedAt)
	}
	return out
}
