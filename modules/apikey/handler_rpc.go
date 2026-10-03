package apikey

import (
	"context"
	"errors"
	"math"
	"time"
	"uuid"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	apikeyv1 "github.com/riipandi/saka/codegen/proto/go/saka/apikey/v1"
	apikeyv1connect "github.com/riipandi/saka/codegen/proto/go/saka/apikey/v1/apikeyv1connect"
	commonv1 "github.com/riipandi/saka/codegen/proto/go/saka/common/v1"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/jwtutils"
	"github.com/riipandi/saka/pkg/responder"
)

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "apikey"

// Module serves the machine-credential procedures: the RPC surface, all of it.
// Authentication is the transport's middleware for both credentials — the
// module reads the caller the context carries, it never verifies a key itself.
type Module struct {
	service *Service
}

// NewModule builds the module over the key service.
func NewModule(service *Service) *Module {
	return &Module{service: service}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the module's endpoints on the router. The surface is RPC
// alone — a key is managed through the API, not through browser forms — so
// there is nothing on the HTTP router to claim.
func (m *Module) Mount(r chi.Router) {}

// MountRPC registers the procedures on the RPC router. The handler options
// are the transport's — the shared snake_case codec and the panic boundary —
// so the procedures answer exactly like the transport's own. Each procedure
// is registered at its own path: the generated handler answers a path under
// its prefix it does not know with a plain-text 404, which a Connect client
// cannot read.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, handler := apikeyv1connect.NewApiKeyServiceHandler(newRPCHandler(m.service), opts...)
	r.Handle(apikeyv1connect.ApiKeyServiceCreateAPIKeyProcedure, handler)
	r.Handle(apikeyv1connect.ApiKeyServiceListAPIKeysProcedure, handler)
	r.Handle(apikeyv1connect.ApiKeyServiceRenewAPIKeyProcedure, handler)
	r.Handle(apikeyv1connect.ApiKeyServiceRevokeAPIKeyProcedure, handler)
	r.Handle(apikeyv1connect.ApiKeyServiceListAllAPIKeysProcedure, handler)
}

// rpcHandler is the transport mapping of the procedures. The service carries
// the rules; this type carries the connect codes and the caller's identity:
// the owner's own surface reads the account from the claims the context
// carries, the way every self-scoped feature does.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) apikeyv1connect.ApiKeyServiceHandler {
	return &rpcHandler{service: service}
}

// CreateApiKey issues a key for the caller's own account.
func (h *rpcHandler) CreateAPIKey(ctx context.Context, req *connect.Request[apikeyv1.CreateAPIKeyRequest]) (*connect.Response[apikeyv1.CreateAPIKeyResponse], error) {
	caller, err := sessionCaller(ctx)
	if err != nil {
		return nil, err
	}

	params := CreateParams{
		Name:        req.Msg.Name,
		Description: req.Msg.GetDescription(),
	}
	if req.Msg.ExpiresAt != nil {
		params.ExpiresAt = req.Msg.ExpiresAt.AsTime()
	}
	ownerID, ownerErr := parseCallerUUID(caller.UserID)
	if ownerErr != nil {
		return nil, mapError(ErrKeyNotFound)
	}
	issued, err := h.service.Create(ctx, ownerID, params)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&apikeyv1.CreateAPIKeyResponse{
		ApiKey:  wireKey(issued.Key),
		Key:     issued.Raw,
		Status:  responder.StatusSuccess,
		Message: "the API key was created",
	}), nil
}

// ListApiKeys answers one page of the caller's own keys.
func (h *rpcHandler) ListAPIKeys(ctx context.Context, req *connect.Request[apikeyv1.ListAPIKeysRequest]) (*connect.Response[apikeyv1.ListAPIKeysResponse], error) {
	caller, err := sessionCaller(ctx)
	if err != nil {
		return nil, err
	}

	ownerID, ownerErr := parseCallerUUID(caller.UserID)
	if ownerErr != nil {
		return nil, mapError(ErrKeyNotFound)
	}
	keys, pagination, err := h.service.ListOwn(ctx, ownerID, req.Msg.GetSortBy(), req.Msg.GetSortOrder() == "asc", int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&apikeyv1.ListAPIKeysResponse{
		ApiKeys:  wireKeys(keys),
		Metadata: metadataOf(pagination),
		Status:   responder.StatusSuccess,
		Message:  "the API keys were listed",
	}), nil
}

// RenewApiKey replaces an expired key's secret and window.
func (h *rpcHandler) RenewAPIKey(ctx context.Context, req *connect.Request[apikeyv1.RenewAPIKeyRequest]) (*connect.Response[apikeyv1.RenewAPIKeyResponse], error) {
	caller, err := sessionCaller(ctx)
	if err != nil {
		return nil, err
	}

	keyID, keyErr := parseKeyID(req.Msg.Id)
	if keyErr != nil {
		return nil, mapError(keyErr)
	}
	var expiresAt time.Time
	if req.Msg.ExpiresAt != nil {
		expiresAt = req.Msg.ExpiresAt.AsTime()
	}
	ownerID, ownerErr := parseCallerUUID(caller.UserID)
	if ownerErr != nil {
		return nil, mapError(ErrKeyNotFound)
	}
	issued, err := h.service.Renew(ctx, ownerID, keyID, expiresAt)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&apikeyv1.RenewAPIKeyResponse{
		ApiKey:  wireKey(issued.Key),
		Key:     issued.Raw,
		Status:  responder.StatusSuccess,
		Message: "the API key was renewed",
	}), nil
}

// RevokeApiKey stamps one of the caller's own keys revoked.
func (h *rpcHandler) RevokeAPIKey(ctx context.Context, req *connect.Request[apikeyv1.RevokeAPIKeyRequest]) (*connect.Response[apikeyv1.RevokeAPIKeyResponse], error) {
	caller, err := sessionCaller(ctx)
	if err != nil {
		return nil, err
	}

	keyID, keyErr := parseKeyID(req.Msg.Id)
	if keyErr != nil {
		return nil, mapError(keyErr)
	}
	ownerID, ownerErr := parseCallerUUID(caller.UserID)
	if ownerErr != nil {
		return nil, mapError(ErrKeyNotFound)
	}
	if err := h.service.Revoke(ctx, ownerID, keyID); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&apikeyv1.RevokeAPIKeyResponse{
		Status:  responder.StatusSuccess,
		Message: "the API key was revoked",
	}), nil
}

// ListAllApiKeys answers one page of every key the deployment holds.
func (h *rpcHandler) ListAllAPIKeys(ctx context.Context, req *connect.Request[apikeyv1.ListAllAPIKeysRequest]) (*connect.Response[apikeyv1.ListAllAPIKeysResponse], error) {
	keys, pagination, err := h.service.ListAll(ctx, req.Msg.GetSortBy(), req.Msg.GetSortOrder() == "asc", int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&apikeyv1.ListAllAPIKeysResponse{
		ApiKeys:  wireKeys(keys),
		Metadata: metadataOf(pagination),
		Status:   responder.StatusSuccess,
		Message:  "the API keys were listed",
	}), nil
}

// wireKey maps the stored row onto the wire message. The hash never travels:
// the wire view is the shape a list answers with, and the raw credential is
// the create and renew responses' `key` field alone.
func wireKey(row KeySchema) *apikeyv1.ApiKey {
	key := &apikeyv1.ApiKey{
		Id:                  row.ID.String(),
		Name:                row.Name,
		Prefix:              row.Prefix,
		ExpiresAt:           timestamppb.New(row.ExpiresAt),
		CreatedAt:           timestamppb.New(row.CreatedAt),
		OwnerId:             row.UserID.String(),
		ExpirationEmailSent: row.EmailSent != nil,
	}
	if row.Descr != nil {
		key.Description = row.Descr
	}
	if row.LastUsed != nil {
		key.LastUsedAt = timestamppb.New(*row.LastUsed)
	}
	if row.UpdatedAt != nil {
		key.UpdatedAt = timestamppb.New(*row.UpdatedAt)
	}
	if row.RevokedAt != nil {
		key.RevokedAt = timestamppb.New(*row.RevokedAt)
	}
	return key
}

// wireKeys maps a page of rows.
func wireKeys(rows []KeySchema) []*apikeyv1.ApiKey {
	keys := make([]*apikeyv1.ApiKey, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, wireKey(row))
	}
	return keys
}

// parseKeyID turns the request's identifier into the key the rows carry. A
// malformed identifier names no key, so it is the not-found failure the same
// as an unknown one.
func parseKeyID(id string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil(), ErrKeyNotFound
	}
	return parsed, nil
}

// sessionCaller reads the caller the transport's middleware resolved. The
// guard's session rule has already refused a machine credential and an
// unauthenticated caller, so reaching here means the claims name an account;
// a missing caller is the wiring defect it always is, and it is refused
// rather than dereferenced.
func sessionCaller(ctx context.Context) (*jwtutils.Caller, error) {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok || caller == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("authentication state missing"))
	}
	return caller, nil
}

// metadataOf maps the responder's pagination onto the shared block. The
// wire fields are optional, so an unknown range is absent rather than zero.
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

// mapError translates the service's failures into the codes the Connect
// protocol carries. The internal ones are collapsed to one answer whose text
// names nothing a caller could aim at. A malformed field never reaches the
// service: the transport's validate interceptor refuses it with the typed
// violation details the contracts carry.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrKeyNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("API key not found"))
	case errors.Is(err, ErrKeyExists):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("API key name already in use"))
	case errors.Is(err, ErrKeyNotExpired):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("the API key has not expired yet"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("API key operation failed"))
	}
}

// parseCallerUUID turns the machine caller's owner into the key the rows
// carry. The subject travels in the wire form — the TypeID the caller's
// owner is named by — and the rows keep their UUID, so the boundary is this
// one function. A malformed identifier names no owner, the not-found the
// surface answers.
func parseCallerUUID(wire string) (uuid.UUID, error) {
	id, err := user.UUIDFromWire(wire)
	if err != nil {
		return uuid.Nil(), ErrKeyNotFound
	}
	return id, nil
}
