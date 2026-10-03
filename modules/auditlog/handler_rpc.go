package auditlog

import (
	"context"
	"errors"
	"log/slog"
	"math"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	auditlogv1 "github.com/riipandi/saka/codegen/proto/go/saka/auditlog/v1"
	auditlogv1connect "github.com/riipandi/saka/codegen/proto/go/saka/auditlog/v1/auditlogv1connect"
	commonv1 "github.com/riipandi/saka/codegen/proto/go/saka/common/v1"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/jwtutils"
	"github.com/riipandi/saka/pkg/responder"
)

// Module serves the audit-log procedures. Everything it answers is an RPC
// procedure, so its HTTP mount is empty by construction.
type Module struct {
	service *Service
	log     *slog.Logger
}

// NewModule builds the module over the service.
func NewModule(service *Service, log *slog.Logger) *Module {
	return &Module{service: service, log: log}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the endpoints on the HTTP router. The area serves no plain
// HTTP route: every procedure is POST-only on the RPC surface.
func (m *Module) Mount(r chi.Router) {}

// MountRPC registers the procedures on the RPC router.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, handler := auditlogv1connect.NewAuditLogServiceHandler(newRPCHandler(m.service), opts...)
	r.Handle(auditlogv1connect.AuditLogServiceListProcedure, handler)
	r.Handle(auditlogv1connect.AuditLogServiceListAllProcedure, handler)
	r.Handle(auditlogv1connect.AuditLogServiceListForUserProcedure, handler)
	r.Handle(auditlogv1connect.AuditLogServiceFilterOptionsProcedure, handler)
}

// rpcHandler is the transport mapping of the audit-log procedures. The service
// carries the rules; this type carries the connect codes and the mapping from
// a request to a scope.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) auditlogv1connect.AuditLogServiceHandler {
	return &rpcHandler{service: service}
}

// List answers the caller's own activity.
//
// The account is read from the caller's token rather than from the request:
// the procedure takes no target, so there is nothing for a caller to point at
// somebody else's records with. The guard refuses an impersonated caller
// before this runs, which is what keeps a delegated session from reading the
// account's own history as if it were the account.
func (h *rpcHandler) List(ctx context.Context, req *connect.Request[auditlogv1.ListRequest]) (*connect.Response[auditlogv1.ListResponse], error) {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errNoCaller)
	}

	// Absent a sort order the page answers newest first.
	ascending := req.Msg.GetSortOrder() == "asc"
	views, metadata, err := h.service.List(ctx, Scope{UserID: caller.UserID},
		req.Msg.GetSortBy(), ascending, int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}

	return connect.NewResponse(&auditlogv1.ListResponse{
		Logs:     wireLogs(views),
		Metadata: wireMetadata(metadata),
		Status:   responder.StatusSuccess,
		Message:  "the audit records were read",
	}), nil
}

// ListAll answers every record the filters admit. It is an administrative
// procedure: the guard refuses a caller who is not an administrator before
// this runs.
func (h *rpcHandler) ListAll(ctx context.Context, req *connect.Request[auditlogv1.ListAllRequest]) (*connect.Response[auditlogv1.ListAllResponse], error) {
	views, metadata, err := h.service.List(ctx, Scope{
		UserID: req.Msg.GetUserId(),
		Event:  req.Msg.GetEvent(),
		Search: req.Msg.GetSearch(),
	}, req.Msg.GetSortBy(), req.Msg.GetSortOrder() == "asc", int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}

	return connect.NewResponse(&auditlogv1.ListAllResponse{
		Logs:     wireLogs(views),
		Metadata: wireMetadata(metadata),
		Status:   responder.StatusSuccess,
		Message:  "the audit records were read",
	}), nil
}

// ListForUser answers one account's records, which is the administrative view
// of the list `List` gives an account of its own.
func (h *rpcHandler) ListForUser(ctx context.Context, req *connect.Request[auditlogv1.ListForUserRequest]) (*connect.Response[auditlogv1.ListForUserResponse], error) {
	views, metadata, err := h.service.List(ctx, Scope{UserID: req.Msg.GetUserId()},
		"", false, int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}

	return connect.NewResponse(&auditlogv1.ListForUserResponse{
		Logs:     wireLogs(views),
		Metadata: wireMetadata(metadata),
		Status:   responder.StatusSuccess,
		Message:  "the audit records were read",
	}), nil
}

// FilterOptions answers the facets a filter control is built from.
func (h *rpcHandler) FilterOptions(ctx context.Context, _ *connect.Request[auditlogv1.FilterOptionsRequest]) (*connect.Response[auditlogv1.FilterOptionsResponse], error) {
	events, users, err := h.service.Options(ctx)
	if err != nil {
		return nil, mapError(err)
	}

	options := make([]*auditlogv1.UserOption, 0, len(users))
	for _, user := range users {
		options = append(options, &auditlogv1.UserOption{Id: user.ID, Username: user.Username})
	}

	return connect.NewResponse(&auditlogv1.FilterOptionsResponse{
		Events:  events,
		Users:   options,
		Status:  responder.StatusSuccess,
		Message: "the filter options were read",
	}), nil
}

// wireLogs maps the views onto the contract's shape.
func wireLogs(views []View) []*auditlogv1.AuditLog {
	logs := make([]*auditlogv1.AuditLog, 0, len(views))
	for _, view := range views {
		logs = append(logs, &auditlogv1.AuditLog{
			Id:                view.ID,
			CreatedAt:         timestamppb.New(view.CreatedAt),
			Event:             view.Event,
			TriggerType:       view.TriggerType,
			ActionStatus:      view.ActionStatus,
			UserId:            wireUserID(view.UserID),
			Username:          view.Username,
			ActorId:           view.ActorID,
			ActorUsername:     view.ActorUsername,
			IpAddress:         view.IPAddress,
			UserAgent:         view.UserAgent,
			DeviceFingerprint: view.Fingerprint,
			Country:           view.Country,
			City:              view.City,
			ResourceType:      view.ResourceType,
			ResourceId:        view.ResourceID,
			Payload:           view.Payload,
		})
	}
	return logs
}

// wireMetadata maps the pagination block onto the shared contract's shape.
func wireMetadata(metadata responder.Pagination) *commonv1.ListMetadata {
	return &commonv1.ListMetadata{
		Page:           int32Ptr(metadata.Page),
		Limit:          int32Ptr(metadata.Limit),
		TotalPages:     int32Ptr(metadata.TotalPages),
		TotalItems:     int32Ptr(metadata.TotalItems),
		FirstItemIndex: int32Ptr(metadata.FirstItemIndex),
		LastItemIndex:  int32Ptr(metadata.LastItemIndex),
	}
}

// int32Ptr narrows a pagination count.
//
// The narrowing is safe by construction: every count comes from
// responder.NewPagination over a page size the contract caps at 100, so the
// value is far inside the range. The bound is asserted rather than assumed,
// because a future page size that reached past it would silently wrap into a
// negative page number.
func int32Ptr(value *int) *int32 {
	if value == nil {
		return nil
	}
	if *value > math.MaxInt32 || *value < math.MinInt32 {
		return nil
	}
	narrowed := int32(*value)
	return &narrowed
}

// errNoCaller is the answer to a request that reached the handler without a
// caller. The guard refuses such a request before this runs, so this is the
// defensive branch a test can reach directly rather than a live state.
var errNoCaller = errors.New("authentication required")

// mapError translates the service's failures into the codes the Connect
// protocol carries. Every failure here is a read the database refused, so the
// caller's answer is the same and the detail stays in the log: a read failure
// names a table and a query, which is nothing a client can act on.
func mapError(err error) error {
	return connect.NewError(connect.CodeInternal, errors.New("the audit records could not be read"))
}

// wireUserID renders an audit record's subject in the wire form. A record
// whose account was deleted carries no subject, and the absent stays absent.
func wireUserID(raw string) string {
	if raw == "" {
		return ""
	}
	wire, err := user.IDFromUUIDString(raw)
	if err != nil {
		return ""
	}
	return wire.String()
}
