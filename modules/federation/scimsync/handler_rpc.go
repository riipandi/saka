package scimsync

import (
	"context"
	"errors"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	federationv1 "github.com/riipandi/saka/codegen/proto/go/saka/federation/v1"
	federationv1connect "github.com/riipandi/saka/codegen/proto/go/saka/federation/v1/federationv1connect"
	"github.com/riipandi/saka/framework/webutil"
)

// rpcHandler is the transport mapping of the procedures. The service
// carries the rules; this type carries the connect codes.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) federationv1connect.ScimProviderServiceHandler {
	return &rpcHandler{service: service}
}

// GetByClient answers the provider one client syncs to.
func (h *rpcHandler) GetByClient(ctx context.Context, req *federationv1.GetScimProviderRequest) (*federationv1.GetScimProviderResponse, error) {
	provider, err := h.service.GetByClient(ctx, req.ClientId)
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.GetScimProviderResponse{
		Provider: wireProvider(provider, false),
		Status:   webutil.StatusSuccess,
		Message:  "the client's SCIM service provider was read",
	}, nil
}

// Create attaches a provisioning target to a client. The token rides this
// one answer.
func (h *rpcHandler) Create(ctx context.Context, req *federationv1.CreateScimProviderRequest) (*federationv1.CreateScimProviderResponse, error) {
	provider, err := h.service.Create(ctx, req.ClientId, req.Endpoint, req.Token)
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.CreateScimProviderResponse{
		Provider: wireProvider(provider, true),
		Status:   webutil.StatusSuccess,
		Message:  "the SCIM service provider was created; the token is shown once",
	}, nil
}

// Update replaces a provider's endpoint and token.
func (h *rpcHandler) Update(ctx context.Context, req *federationv1.UpdateScimProviderRequest) (*federationv1.UpdateScimProviderResponse, error) {
	id, err := providerIDFromWire(req.Id)
	if err != nil {
		return nil, mapError(err)
	}
	provider, err := h.service.Update(ctx, id, req.Endpoint, req.Token)
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.UpdateScimProviderResponse{
		Provider: wireProvider(provider, false),
		Status:   webutil.StatusSuccess,
		Message:  "the SCIM service provider was updated",
	}, nil
}

// Delete removes the provisioning target.
func (h *rpcHandler) Delete(ctx context.Context, req *federationv1.DeleteScimProviderRequest) (*federationv1.DeleteScimProviderResponse, error) {
	id, err := providerIDFromWire(req.Id)
	if err != nil {
		return nil, mapError(err)
	}
	if err := h.service.Delete(ctx, id); err != nil {
		return nil, mapError(err)
	}
	return &federationv1.DeleteScimProviderResponse{
		Status:  webutil.StatusSuccess,
		Message: "the SCIM service provider was deleted",
	}, nil
}

// Sync runs one provisioning pass now.
func (h *rpcHandler) Sync(ctx context.Context, req *federationv1.SyncScimProviderRequest) (*federationv1.SyncScimProviderResponse, error) {
	id, err := providerIDFromWire(req.Id)
	if err != nil {
		return nil, mapError(err)
	}
	stats, err := h.service.Sync(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.SyncScimProviderResponse{
		UsersCreated:  clampToInt32(stats.UsersCreated),
		UsersUpdated:  clampToInt32(stats.UsersUpdated),
		UsersDeleted:  clampToInt32(stats.UsersDeleted),
		GroupsCreated: clampToInt32(stats.GroupsCreated),
		GroupsUpdated: clampToInt32(stats.GroupsUpdated),
		GroupsDeleted: clampToInt32(stats.GroupsDeleted),
		Status:        webutil.StatusSuccess,
		Message:       "the SCIM sync pass completed",
	}, nil
}

// clampToInt32 narrows a count to the wire type. A pass that touched more
// than two billion rows of one kind is not a number the wire type can
// carry; the cap answers the largest value instead of wrapping.
func clampToInt32(count int) int32 {
	const maxInt32 = int32(^uint32(0) >> 1) // 2147483647
	if count > int(maxInt32) {
		return maxInt32
	}
	//nolint:gosec // the comparison above bounds count to the int32 range
	return int32(count)
}

// providerIDFromWire reads a raw UUID form into the row's key.
func providerIDFromWire(wire string) (uuid.UUID, error) {
	return uuid.Parse(wire)
}

// mapError translates the service's errors into connect codes. The
// internal text never reaches the wire: the code is the contract, the
// message is the operator's hint.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrNoProvider):
		return connect.NewError(connect.CodeNotFound, "SCIM service provider not found")
	case errors.Is(err, ErrProviderExists):
		return connect.NewError(connect.CodeFailedPrecondition, "the client already has a SCIM service provider")
	case errors.Is(err, ErrNoDirectory):
		return connect.NewError(connect.CodeFailedPrecondition, "no account directory is wired")
	default:
		return connect.NewError(connect.CodeInternal, "SCIM operation failed")
	}
}

// wireProvider maps the row onto the wire message. withToken controls the
// one read the Create answer makes; every other read answers the token
// empty — it is sealed at rest and nothing reads it back.
func wireProvider(p Provider, withToken bool) *federationv1.ScimProvider {
	wire := &federationv1.ScimProvider{
		Id:        p.ID.String(),
		ClientId:  p.ClientID,
		Endpoint:  p.Endpoint,
		CreatedAt: timestampOf(p.CreatedAt),
	}
	if withToken {
		wire.Token = p.SealedToken
	}
	if p.LastSyncedAt != nil {
		wire.LastSyncedAt = timestampOf(*p.LastSyncedAt)
	}
	return wire
}

func timestampOf(t time.Time) *timestamppb.Timestamp {
	return timestamppb.New(t)
}
