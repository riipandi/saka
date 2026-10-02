package blocklist

import (
	"context"
	"errors"
	"math"
	"uuid"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/riipandi/tango/codegen/proto/go/tango/common/v1"
	identityv1 "github.com/riipandi/tango/codegen/proto/go/tango/identity/v1"
	identityv1connect "github.com/riipandi/tango/codegen/proto/go/tango/identity/v1/identityv1connect"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/jwtutils"
	"github.com/riipandi/tango/pkg/responder"
)

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "blocklist"

// Module serves the blocklist procedures: the RPC surface, all of it.
type Module struct {
	service *Service
}

// NewModule builds the module over the blocklist service.
func NewModule(service *Service) *Module {
	return &Module{service: service}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the module's endpoints on the router. The surface is RPC
// alone — the entries are managed through the API, not through browser
// forms — so there is nothing on the HTTP router to claim.
func (m *Module) Mount(r chi.Router) {}

// MountRPC registers the procedures on the RPC router. Each procedure is
// registered at its own path: the generated handler answers a path under its
// prefix it does not know with a plain-text 404, which a Connect client
// cannot read.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, handler := identityv1connect.NewBlocklistServiceHandler(newRPCHandler(m.service), opts...)
	r.Handle(identityv1connect.BlocklistServiceListBlocklistEntriesProcedure, handler)
	r.Handle(identityv1connect.BlocklistServiceAddBlocklistEntryProcedure, handler)
	r.Handle(identityv1connect.BlocklistServiceRemoveBlocklistEntryProcedure, handler)
}

// rpcHandler is the transport mapping of the procedures. The service carries
// the rules; this type carries the connect codes and the caller's identity.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) identityv1connect.BlocklistServiceHandler {
	return &rpcHandler{service: service}
}

// ListBlocklistEntries answers one page of the entries.
func (h *rpcHandler) ListBlocklistEntries(ctx context.Context, req *connect.Request[identityv1.ListBlocklistEntriesRequest]) (*connect.Response[identityv1.ListBlocklistEntriesResponse], error) {
	entries, pagination, err := h.service.List(ctx, req.Msg.GetSortBy(), req.Msg.GetSortOrder() == "asc", int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.ListBlocklistEntriesResponse{
		Entries:  wireEntries(entries),
		Metadata: metadataOf(pagination),
		Status:   responder.StatusSuccess,
		Message:  "the blocklist entries were listed",
	}), nil
}

// AddBlocklistEntry stores one entry in the administrator's name.
func (h *rpcHandler) AddBlocklistEntry(ctx context.Context, req *connect.Request[identityv1.AddBlocklistEntryRequest]) (*connect.Response[identityv1.AddBlocklistEntryResponse], error) {
	caller, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}

	adminID, parseErr := user.UUIDFromWire(caller.UserID)
	if parseErr != nil {
		return nil, mapError(ErrEntryNotFound)
	}
	entry, err := h.service.Add(ctx, adminID, req.Msg.GetPattern())
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.AddBlocklistEntryResponse{
		Entry:   wireEntry(entry),
		Status:  responder.StatusSuccess,
		Message: "the blocklist entry was stored",
	}), nil
}

// RemoveBlocklistEntry deletes one entry by identifier.
func (h *rpcHandler) RemoveBlocklistEntry(ctx context.Context, req *connect.Request[identityv1.RemoveBlocklistEntryRequest]) (*connect.Response[identityv1.RemoveBlocklistEntryResponse], error) {
	entryID, parseErr := uuid.Parse(req.Msg.GetId())
	if parseErr != nil {
		return nil, mapError(ErrEntryNotFound)
	}
	if err := h.service.Remove(ctx, entryID); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.RemoveBlocklistEntryResponse{
		Status:  responder.StatusSuccess,
		Message: "the blocklist entry was removed",
	}), nil
}

// mapError translates the service's failures onto the connect codes.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrEntryNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("blocklist entry not found"))
	case errors.Is(err, ErrPatternInvalid):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("the pattern must be an email address or an @domain entry"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("blocklist call failed"))
	}
}

// wireEntry maps one row onto the wire message.
func wireEntry(row EntrySchema) *identityv1.BlocklistEntry {
	entry := &identityv1.BlocklistEntry{
		Id:        row.ID.String(),
		Pattern:   row.Pattern,
		CreatedAt: timestamppb.New(row.CreatedAt),
	}
	if row.CreatedBy != nil {
		createdBy := user.FormatID(*row.CreatedBy)
		entry.CreatedBy = &createdBy
	}
	return entry
}

// wireEntries maps the page's rows.
func wireEntries(rows []EntrySchema) []*identityv1.BlocklistEntry {
	entries := make([]*identityv1.BlocklistEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, wireEntry(row))
	}
	return entries
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

// callerOf reads the caller the transport's middleware resolved.
func callerOf(ctx context.Context) (*jwtutils.Caller, error) {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok || caller == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	return caller, nil
}
