package oauthsso

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	authnv1 "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1"
	authnv1connect "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1/authnv1connect"
	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// ModuleName names the feature in composition reports and logs.
const ModuleName = "oauthsso"

// Module serves the OAuth SSO feature: the connection procedures and the
// flow's REST routes. The type lives with the handlers — the area's
// features list is the only registration the feature needs.
type Module struct {
	service    *Service
	rpcHandler *rpcHandler
	rest       *handler
}

// NewModule builds the module over the connection service.
func NewModule(service *Service) *Module {
	return &Module{
		service:    service,
		rpcHandler: newRPCHandler(service),
		rest:       newHandler(service),
	}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the flow's REST routes. Both sit outside the bearer
// group — the browser crossing them holds no token, that being the point
// of the feature — and at the root's own level: `/oauth` is the outbound
// flow's prefix, the sibling of the inbound protocol's `/oidc`.
func (m *Module) Mount(r chi.Router) {
	m.rest.Mount(r)
}

// MountRPC registers the OAuth SSO procedures on the RPC server. The server
// carries the transport's interceptors and the mount the shared snake_case
// codec, so the procedures answer exactly like the rest.
func (m *Module) MountRPC(server *connect.Server) {
	authnv1connect.RegisterOAuthSSOServiceHandler(server, m.rpcHandler)
}

// rpcHandler is the transport mapping of the OAuth SSO procedures. The
// service carries the rules; this type carries the connect codes and the
// wire forms the contract answers in.
type rpcHandler struct {
	service *Service
}

func newRPCHandler(service *Service) *rpcHandler {
	return &rpcHandler{service: service}
}

// callerID reads the calling account's identifier out of the guard's
// claims. A Session-ruled procedure always carries one; an unreadable
// claim is an internal failure, not a client fault.
func callerID(ctx context.Context) (uuid.UUID, error) {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok {
		return uuid.UUID{}, errors.New("oauthsso: the caller is unnamed")
	}
	id, err := user.UUIDFromWire(caller.UserID)
	if err != nil {
		return uuid.UUID{}, errors.New("oauthsso: the caller's identity is unreadable")
	}
	return id, nil
}

// BeginSignIn opens the authorization-code flow: the pending row is
// written, and the answer is the authorize URL the browser navigates to.
func (h *rpcHandler) BeginSignIn(ctx context.Context, req *authnv1.BeginOAuthSignInRequest) (*authnv1.BeginOAuthSignInResponse, error) {
	authorizeURL, err := h.service.Begin(ctx, req.Connection)
	switch {
	case errors.Is(err, ErrConnectionUnavailable):
		return nil, connect.NewError(connect.CodeNotFound, "no enabled connection answers this provider")
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, "the sign-in flow could not be opened")
	}

	return &authnv1.BeginOAuthSignInResponse{
		AuthorizeUrl: authorizeURL,
		Status:       "success",
		Message:      "the sign-in flow was opened",
	}, nil
}

// ContinueSignIn completes a paused flow: the resolution binds the
// identity, the fork answers the bridge or the session, and the flow
// spends. The client facts ride the request's context, the way the
// password sign-in records them.
func (h *rpcHandler) ContinueSignIn(ctx context.Context, req *authnv1.ContinueOAuthSignInRequest) (*authnv1.ContinueOAuthSignInResponse, error) {
	client := fwaudit.ClientFromContext(ctx)
	answer, err := h.service.ContinueSignIn(ctx, ContinueParams{
		FlowToken:   req.FlowToken,
		GivenName:   req.GivenName,
		FamilyName:  req.FamilyName,
		UserAgent:   client.UserAgent,
		IPAddress:   client.IPAddress,
		Fingerprint: client.Fingerprint,
	})
	if err != nil {
		return nil, continueError(ctx, err)
	}

	out := &authnv1.ContinueOAuthSignInResponse{
		Status:  webutil.StatusSuccess,
		Message: "the sign-in flow completed",
		Stage:   string(answer.Stage),
	}
	switch {
	case answer.Bridge != nil:
		out.MfaRequired = true
		out.MfaPendingToken = answer.Bridge.Token
		out.MfaPendingExpiresAt = timestamppb.New(answer.Bridge.ExpiresAt)
		out.Stage = string(StageCompleted)
		out.Message = "the account keeps a confirmed second factor"
	case answer.Session != nil:
		out.AccessToken = answer.Session.AccessToken
		out.TokenType = answer.Session.TokenType
		out.AccessExpiresIn = answer.Session.AccessExpiresIn
		out.RefreshExpiresIn = answer.Session.RefreshExpiresIn
		out.RefreshToken = answer.Session.RefreshToken
		out.SessionId = answer.Session.SessionID
		out.User = &authnv1.AuthenticatedUser{
			Id:          answer.Session.User.ID,
			Username:    answer.Session.User.Username,
			Email:       answer.Session.User.Email,
			DisplayName: answer.Session.User.DisplayName,
		}
		out.Stage = string(StageCompleted)
	default:
		out.Message = "the sign-in flow moved to the " + string(answer.Stage) + " stage"
	}
	return out, nil
}

// continueError maps the resolution's failures onto the connect codes.
// The unknown-flow answer is the same not_found a replay earns: a
// refused continue learns nothing about which half failed.
func continueError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, ErrFlowUnknown), errors.Is(err, ErrConnectionUnavailable):
		return connect.NewError(connect.CodeNotFound, "no live flow answers this handle")
	case errors.Is(err, ErrNamesRequired):
		return connect.NewError(connect.CodeInvalidArgument, "the flow waits for a given and a family name")
	case errors.Is(err, ErrInvalidCode):
		return connect.NewError(connect.CodeInvalidArgument, "the code does not answer the flow's request")
	case errors.Is(err, ErrFlowEnded):
		return connect.NewError(connect.CodeFailedPrecondition, "the flow answered wrong too many times and ended")
	case errors.Is(err, ErrLinkingDisabled):
		return connect.NewError(connect.CodeFailedPrecondition, "account linking is disabled")
	case errors.Is(err, ErrSignUpRefused):
		return connect.NewError(connect.CodePermissionDenied, "the sign-up is not allowed")
	default:
		// The mapped failures all name themselves; whatever lands here is
		// the service's internals, and the wire answer says nothing about
		// it. The log is where it speaks.
		slog.ErrorContext(ctx, "oauthsso: the flow's completion failed", "error", err)
		return connect.NewError(connect.CodeInternal, "the sign-in flow could not be completed")
	}
}

// ListEnabledConnections answers the connections an unauthenticated
// caller may sign in with — the slug and the display name, the button's
// label and its destination and nothing else.
func (h *rpcHandler) ListEnabledConnections(ctx context.Context, req *authnv1.ListEnabledConnectionsRequest) (*authnv1.ListEnabledConnectionsResponse, error) {
	connections, err := h.service.EnabledConnections(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "the connections could not be read")
	}

	out := make([]*authnv1.EnabledConnection, 0, len(connections))
	for _, conn := range connections {
		out = append(out, &authnv1.EnabledConnection{
			Provider:    conn.Provider,
			DisplayName: conn.DisplayName,
		})
	}
	return &authnv1.ListEnabledConnectionsResponse{Connections: out}, nil
}

// ListConnections answers the operator's listing, secrets never included.
func (h *rpcHandler) ListConnections(ctx context.Context, req *authnv1.ListOAuthConnectionsRequest) (*authnv1.ListOAuthConnectionsResponse, error) {
	connections, err := h.service.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "the connections could not be read")
	}

	out := make([]*authnv1.OAuthConnection, 0, len(connections))
	for _, conn := range connections {
		out = append(out, wireConnection(conn))
	}
	return &authnv1.ListOAuthConnectionsResponse{
		Connections: out,
		Status:      "success",
		Message:     "the connections were read",
	}, nil
}

// GetConnection answers one connection by its identifier, secrets never
// included.
func (h *rpcHandler) GetConnection(ctx context.Context, req *authnv1.GetOAuthConnectionRequest) (*authnv1.GetOAuthConnectionResponse, error) {
	wireID, err := ParseID(req.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, "no connection answers this identifier")
	}
	id := IDToUUID(wireID)

	conn, err := h.service.Get(ctx, id)
	switch {
	case errors.Is(err, ErrConnectionNotFound):
		return nil, connect.NewError(connect.CodeNotFound, "no connection answers this identifier")
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, "the connection could not be read")
	}

	return &authnv1.GetOAuthConnectionResponse{
		Connection: wireConnection(conn),
		Status:     "success",
		Message:    "the connection was read",
	}, nil
}

// CreateConnection validates, seals, and stores a connection.
func (h *rpcHandler) CreateConnection(ctx context.Context, req *authnv1.CreateOAuthConnectionRequest) (*authnv1.CreateOAuthConnectionResponse, error) {
	conn, err := h.service.Create(ctx, paramsOf(req))
	switch {
	case errors.Is(err, ErrInvalidConnection):
		return nil, connect.NewError(connect.CodeInvalidArgument, "the connection's fields do not compose into a runnable connection")
	case errors.Is(err, ErrProviderTaken):
		return nil, connect.NewError(connect.CodeAlreadyExists, "a connection with this provider slug already exists")
	case errors.Is(err, ErrSecretUnavailable):
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the application secret is not configured to seal the client secret")
	case errors.Is(err, ErrDiscoveryUnavailable):
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the discovery document cannot be fetched from this process")
	case errors.Is(err, ErrDiscoveryFetch), errors.Is(err, ErrDiscoveryInvalid):
		return nil, connect.NewError(connect.CodeInvalidArgument, "the discovery document is not a usable OIDC document")
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, "the connection could not be stored")
	}

	return &authnv1.CreateOAuthConnectionResponse{
		Connection: wireConnection(conn),
		Status:     "success",
		Message:    "the connection was stored",
	}, nil
}

// UpdateConnection rewrites one connection's editable fields.
func (h *rpcHandler) UpdateConnection(ctx context.Context, req *authnv1.UpdateOAuthConnectionRequest) (*authnv1.UpdateOAuthConnectionResponse, error) {
	wireID, err := ParseID(req.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, "no connection answers this identifier")
	}
	id := IDToUUID(wireID)

	conn, err := h.service.Update(ctx, id, updateOf(req))
	switch {
	case errors.Is(err, ErrConnectionNotFound):
		return nil, connect.NewError(connect.CodeNotFound, "no connection answers this identifier")
	case errors.Is(err, ErrInvalidConnection):
		return nil, connect.NewError(connect.CodeInvalidArgument, "the connection's fields do not compose into a runnable connection")
	case errors.Is(err, ErrProviderTaken):
		return nil, connect.NewError(connect.CodeAlreadyExists, "a connection with this provider slug already exists")
	case errors.Is(err, ErrSecretUnavailable):
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the application secret is not configured to seal the client secret")
	case errors.Is(err, ErrDiscoveryUnavailable):
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the discovery document cannot be fetched from this process")
	case errors.Is(err, ErrDiscoveryFetch), errors.Is(err, ErrDiscoveryInvalid):
		return nil, connect.NewError(connect.CodeInvalidArgument, "the discovery document is not a usable OIDC document")
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, "the connection could not be rewritten")
	}

	return &authnv1.UpdateOAuthConnectionResponse{
		Connection: wireConnection(conn),
		Status:     "success",
		Message:    "the connection was rewritten",
	}, nil
}

// DeleteConnection removes one connection and everything that rode it.
func (h *rpcHandler) DeleteConnection(ctx context.Context, req *authnv1.DeleteOAuthConnectionRequest) (*authnv1.DeleteOAuthConnectionResponse, error) {
	wireID, err := ParseID(req.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, "no connection answers this identifier")
	}
	id := IDToUUID(wireID)

	err = h.service.Delete(ctx, id)
	switch {
	case errors.Is(err, ErrConnectionNotFound):
		return nil, connect.NewError(connect.CodeNotFound, "no connection answers this identifier")
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, "the connection could not be removed")
	}

	return &authnv1.DeleteOAuthConnectionResponse{
		Status:  "success",
		Message: "the connection was removed",
	}, nil
}

// ListLinkedConnections answers the calling account's bindings, oldest
// first. The account is the claims' subject — the guard's Session rule
// has already admitted a caller holding a session, and this procedure
// reads no other account's rows.
func (h *rpcHandler) ListLinkedConnections(ctx context.Context, _ *authnv1.ListLinkedConnectionsRequest) (*authnv1.ListLinkedConnectionsResponse, error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "internal error")
	}
	linked, err := h.service.ListLinkedAccounts(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "the linked accounts could not be read")
	}

	out := make([]*authnv1.OAuthLinkedAccount, 0, len(linked))
	for _, view := range linked {
		entry := &authnv1.OAuthLinkedAccount{
			Id:           FormatLinkedAccountID(view.LinkedAccount.ID),
			ConnectionId: FormatID(view.LinkedAccount.ConnectionID),
			Provider:     view.Provider,
			Email:        view.LinkedAccount.Email,
		}
		if !view.LinkedAccount.CreatedAt.IsZero() {
			entry.CreatedAt = timestamppb.New(view.LinkedAccount.CreatedAt)
		}
		out = append(out, entry)
	}
	return &authnv1.ListLinkedConnectionsResponse{
		LinkedAccounts: out,
		Status:         webutil.StatusSuccess,
		Message:        "the linked accounts were read",
	}, nil
}

// VerifySignInEmail spends the email code a verify_email-stage flow waits
// for. The answer names the stage the flow moved to; the sign-in itself
// completes through ContinueSignIn.
func (h *rpcHandler) VerifySignInEmail(ctx context.Context, req *authnv1.VerifyOAuthSignInEmailRequest) (*authnv1.VerifyOAuthSignInEmailResponse, error) {
	stage, err := h.service.VerifySignInEmail(ctx, req.FlowToken, req.Code)
	if err != nil {
		return nil, continueError(ctx, err)
	}
	return &authnv1.VerifyOAuthSignInEmailResponse{
		Stage:   string(stage),
		Status:  webutil.StatusSuccess,
		Message: "the address is proven and the sign-in flow moved on",
	}, nil
}

// UnlinkConnection removes one of the calling account's bindings. The
// stranding refusal is a failed_precondition whose message names the
// way back in; a foreign or unknown binding answers not_found, the
// same answer either way.
func (h *rpcHandler) UnlinkConnection(ctx context.Context, req *authnv1.UnlinkConnectionRequest) (*authnv1.UnlinkConnectionResponse, error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "internal error")
	}
	linkedID, err := ParseLinkedAccountID(req.LinkedAccountId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, "no linked account answers this identifier")
	}

	switch err := h.service.UnlinkLinkedAccount(ctx, userID, linkedID); {
	case errors.Is(err, ErrLinkedAccountNotFound):
		return nil, connect.NewError(connect.CodeNotFound, "no linked account answers this identifier")
	case errors.Is(err, ErrLastCredential):
		return nil, connect.NewError(connect.CodeFailedPrecondition, "set a password before unlinking the last provider")
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, "the linked account could not be removed")
	}

	return &authnv1.UnlinkConnectionResponse{
		Status:  webutil.StatusSuccess,
		Message: "the linked account was removed",
	}, nil
}

// GetLinkedAccountTokens answers the calling account's binding with the
// provider tokens it carries, opened from their seal. A foreign or
// unknown binding answers not_found, the same answer either way.
func (h *rpcHandler) GetLinkedAccountTokens(ctx context.Context, req *authnv1.GetLinkedAccountTokensRequest) (*authnv1.GetLinkedAccountTokensResponse, error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "internal error")
	}
	linkedID, err := ParseLinkedAccountID(req.LinkedAccountId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, "no linked account answers this identifier")
	}

	tokens, err := h.service.GetLinkedAccountTokens(ctx, userID, linkedID)
	switch {
	case errors.Is(err, ErrLinkedAccountNotFound):
		return nil, connect.NewError(connect.CodeNotFound, "no linked account answers this identifier")
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, "the provider tokens could not be read")
	}

	answer := &authnv1.GetLinkedAccountTokensResponse{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ConnectionId: FormatID(tokens.ConnectionID),
		Provider:     tokens.Provider,
		Status:       webutil.StatusSuccess,
		Message:      "the provider tokens were read",
	}
	if tokens.ExpiresAt != nil {
		answer.ExpiresAt = timestamppb.New(*tokens.ExpiresAt)
	}
	return answer, nil
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
	if msg.CustomAttributes != nil {
		params.CustomAttributes = wireCustomAttributes(msg.CustomAttributes)
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
	if msg.CustomAttributes != nil {
		attrs := wireCustomAttributes(msg.CustomAttributes)
		update.CustomAttributes = &attrs
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
		Email:                msg.Email,
		GivenName:            msg.GivenName,
		FamilyName:           msg.FamilyName,
		Subject:              msg.Subject,
		EmailVerified:        msg.EmailVerified,
		EmailVerifiedDefault: msg.GetEmailVerifiedDefault(),
		Username:             msg.Username,
		Picture:              msg.Picture,
	}
}

// wireCustomAttributes maps the wire attribute set onto the service's.
func wireCustomAttributes(msgs []*authnv1.OAuthCustomAttribute) []CustomAttribute {
	attrs := make([]CustomAttribute, 0, len(msgs))
	for _, msg := range msgs {
		attrs = append(attrs, CustomAttribute{Key: msg.Key, Claim: msg.Claim})
	}
	return attrs
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
		conn.AttributeMapping.FamilyName != "" || conn.AttributeMapping.Subject != "" ||
		conn.AttributeMapping.EmailVerified != "" || conn.AttributeMapping.EmailVerifiedDefault ||
		conn.AttributeMapping.Username != "" || conn.AttributeMapping.Picture != "" {
		out.AttributeMapping = &authnv1.OAuthAttributeMapping{
			Email:                conn.AttributeMapping.Email,
			GivenName:            conn.AttributeMapping.GivenName,
			FamilyName:           conn.AttributeMapping.FamilyName,
			Subject:              conn.AttributeMapping.Subject,
			EmailVerified:        conn.AttributeMapping.EmailVerified,
			EmailVerifiedDefault: &conn.AttributeMapping.EmailVerifiedDefault,
			Username:             conn.AttributeMapping.Username,
			Picture:              conn.AttributeMapping.Picture,
		}
	}
	if len(conn.CustomAttributes) > 0 {
		attrs := make([]*authnv1.OAuthCustomAttribute, 0, len(conn.CustomAttributes))
		for _, attr := range conn.CustomAttributes {
			attrs = append(attrs, &authnv1.OAuthCustomAttribute{Key: attr.Key, Claim: attr.Claim})
		}
		out.CustomAttributes = attrs
	}
	if !conn.CreatedAt.IsZero() {
		out.CreatedAt = timestamppb.New(conn.CreatedAt)
	}
	if conn.UpdatedAt != nil {
		out.UpdatedAt = timestamppb.New(*conn.UpdatedAt)
	}
	return out
}
