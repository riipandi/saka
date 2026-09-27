package notification

import (
	"context"
	"errors"
	"math"
	"time"
	"uuid"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/riipandi/tango/codegen/proto/go/tango/common/v1"
	notificationv1 "github.com/riipandi/tango/codegen/proto/go/tango/notification/v1"
	notificationv1connect "github.com/riipandi/tango/codegen/proto/go/tango/notification/v1/notificationv1connect"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/modules/identity/usergroup"
	"github.com/riipandi/tango/pkg/jwtutils"
	"github.com/riipandi/tango/pkg/responder"
)

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "notification"

// pingInterval is how often the watch stream sends its keepalive. The event
// is empty by contract; its job is to keep an idle proxy from closing a
// connection whose next notification may be hours away.
const pingInterval = 25 * time.Second

// Module serves the notification procedures: the RPC surface, all of it.
// Authentication is the transport's middleware; the module reads the caller
// the context carries, it never verifies a token itself.
type Module struct {
	service *Service
}

// NewModule builds the module over the notification service.
func NewModule(service *Service) *Module {
	return &Module{service: service}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the module's endpoints on the router. The surface is RPC
// alone, so there is nothing on the HTTP router to claim.
func (m *Module) Mount(r chi.Router) {}

// MountRPC registers the procedures on the RPC router. The handler options
// are the transport's — the shared snake_case codec and the panic boundary —
// so the procedures answer exactly like the transport's own.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, handler := notificationv1connect.NewNotificationServiceHandler(newRPCHandler(m.service), opts...)
	r.Handle(notificationv1connect.NotificationServiceCreateNotificationProcedure, handler)
	r.Handle(notificationv1connect.NotificationServiceGetNotificationProcedure, handler)
	r.Handle(notificationv1connect.NotificationServiceListAllNotificationsProcedure, handler)
	r.Handle(notificationv1connect.NotificationServiceCancelNotificationProcedure, handler)
	r.Handle(notificationv1connect.NotificationServiceListNotificationsProcedure, handler)
	r.Handle(notificationv1connect.NotificationServiceMarkNotificationReadProcedure, handler)
	r.Handle(notificationv1connect.NotificationServiceMarkAllNotificationsReadProcedure, handler)
	r.Handle(notificationv1connect.NotificationServiceUnreadCountProcedure, handler)
	r.Handle(notificationv1connect.NotificationServiceWatchNotificationsProcedure, handler)
}

// rpcHandler is the transport mapping of the procedures. The service carries
// the rules; this type carries the connect codes and the caller's identity.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) notificationv1connect.NotificationServiceHandler {
	return &rpcHandler{service: service}
}

// CreateNotification publishes a notification to its audience.
func (h *rpcHandler) CreateNotification(ctx context.Context, req *connect.Request[notificationv1.CreateNotificationRequest]) (*connect.Response[notificationv1.CreateNotificationResponse], error) {
	caller, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}

	creator, err := callerUUID(caller)
	if err != nil {
		return nil, mapError(err)
	}

	params := CreateParams{
		Category:     req.Msg.Category,
		Topic:        req.Msg.GetTopic(),
		Title:        req.Msg.Title,
		Body:         req.Msg.Body,
		AudienceKind: req.Msg.GetAudienceKind(),
		SendEmail:    req.Msg.GetSendEmail(),
	}
	for _, id := range req.Msg.UserIds {
		parsed, parseErr := user.UUIDFromWire(id)
		if parseErr != nil {
			return nil, mapError(ErrUnknownTarget)
		}
		params.UserIDs = append(params.UserIDs, parsed)
	}
	for _, id := range req.Msg.UserGroupIds {
		parsed, parseErr := usergroup.UUIDFromWire(id)
		if parseErr != nil {
			return nil, mapError(ErrUnknownTarget)
		}
		params.GroupIDs = append(params.GroupIDs, parsed)
	}

	row, err := h.service.Create(ctx, creator, params)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&notificationv1.CreateNotificationResponse{
		Notification: wireNotification(row, params.UserIDs, params.GroupIDs, nil),
		Status:       responder.StatusSuccess,
		Message:      "the notification was created",
	}), nil
}

// GetNotification answers one notification's full view.
func (h *rpcHandler) GetNotification(ctx context.Context, req *connect.Request[notificationv1.GetNotificationRequest]) (*connect.Response[notificationv1.GetNotificationResponse], error) {
	id, err := parseID(req.Msg.Id)
	if err != nil {
		return nil, mapError(err)
	}

	row, userIDs, groupIDs, err := h.service.Get(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&notificationv1.GetNotificationResponse{
		Notification: wireNotification(row, userIDs, groupIDs, nil),
		Status:       responder.StatusSuccess,
		Message:      "the notification was read",
	}), nil
}

// ListAllNotifications answers one page of every notification the
// deployment holds.
func (h *rpcHandler) ListAllNotifications(ctx context.Context, req *connect.Request[notificationv1.ListAllNotificationsRequest]) (*connect.Response[notificationv1.ListAllNotificationsResponse], error) {
	rows, pagination, err := h.service.ListAll(ctx, req.Msg.GetCategory(), req.Msg.GetSortBy(), req.Msg.GetSortOrder() == "asc", int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&notificationv1.ListAllNotificationsResponse{
		Notifications: wireNotifications(rows),
		Metadata:      metadataOf(pagination),
		Status:        responder.StatusSuccess,
		Message:       "the notifications were listed",
	}), nil
}

// CancelNotification withdraws one notification.
func (h *rpcHandler) CancelNotification(ctx context.Context, req *connect.Request[notificationv1.CancelNotificationRequest]) (*connect.Response[notificationv1.CancelNotificationResponse], error) {
	caller, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}

	id, err := parseID(req.Msg.Id)
	if err != nil {
		return nil, mapError(err)
	}
	admin, err := callerUUID(caller)
	if err != nil {
		return nil, mapError(err)
	}
	if err := h.service.Cancel(ctx, admin, id); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&notificationv1.CancelNotificationResponse{
		Status:  responder.StatusSuccess,
		Message: "the notification was cancelled",
	}), nil
}

// ListNotifications answers one page of the caller's inbox.
func (h *rpcHandler) ListNotifications(ctx context.Context, req *connect.Request[notificationv1.ListNotificationsRequest]) (*connect.Response[notificationv1.ListNotificationsResponse], error) {
	caller, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}

	userID, err := callerUUID(caller)
	if err != nil {
		return nil, mapError(err)
	}
	rows, pagination, err := h.service.ListInbox(ctx, userID, req.Msg.GetUnreadOnly(), req.Msg.GetCategory(), req.Msg.GetSortBy(), req.Msg.GetSortOrder() == "asc", int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&notificationv1.ListNotificationsResponse{
		Notifications: wireInbox(rows),
		Metadata:      metadataOf(pagination),
		Status:        responder.StatusSuccess,
		Message:       "the notifications were listed",
	}), nil
}

// MarkNotificationRead writes the caller's receipt for one notification.
func (h *rpcHandler) MarkNotificationRead(ctx context.Context, req *connect.Request[notificationv1.MarkNotificationReadRequest]) (*connect.Response[notificationv1.MarkNotificationReadResponse], error) {
	caller, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}

	id, err := parseID(req.Msg.Id)
	if err != nil {
		return nil, mapError(err)
	}
	userID, err := callerUUID(caller)
	if err != nil {
		return nil, mapError(err)
	}
	if err := h.service.MarkRead(ctx, userID, id); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&notificationv1.MarkNotificationReadResponse{
		Status:  responder.StatusSuccess,
		Message: "the notification was marked read",
	}), nil
}

// MarkAllNotificationsRead writes the caller's missing receipts.
func (h *rpcHandler) MarkAllNotificationsRead(ctx context.Context, req *connect.Request[notificationv1.MarkAllNotificationsReadRequest]) (*connect.Response[notificationv1.MarkAllNotificationsReadResponse], error) {
	caller, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}

	userID, err := callerUUID(caller)
	if err != nil {
		return nil, mapError(err)
	}
	if _, err := h.service.MarkAllRead(ctx, userID); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&notificationv1.MarkAllNotificationsReadResponse{
		Status:  responder.StatusSuccess,
		Message: "the notifications were marked read",
	}), nil
}

// UnreadCount answers the number the bell badge shows.
func (h *rpcHandler) UnreadCount(ctx context.Context, req *connect.Request[notificationv1.UnreadCountRequest]) (*connect.Response[notificationv1.UnreadCountResponse], error) {
	caller, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}

	userID, err := callerUUID(caller)
	if err != nil {
		return nil, mapError(err)
	}
	count, err := h.service.UnreadCount(ctx, userID)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&notificationv1.UnreadCountResponse{
		Count:   int64(count),
		Status:  responder.StatusSuccess,
		Message: "the unread count was read",
	}), nil
}

// WatchNotifications tails the notifications created while the stream stays
// open. The stream carries no history: a reconnecting client catches up
// through the list, which is the durable record the stream tail is not.
func (h *rpcHandler) WatchNotifications(ctx context.Context, req *connect.Request[notificationv1.WatchNotificationsRequest], stream *connect.ServerStream[notificationv1.WatchEvent]) error {
	caller, err := callerOf(ctx)
	if err != nil {
		return err
	}

	userID, err := callerUUID(caller)
	if err != nil {
		return mapError(err)
	}

	events, cancel := h.service.Subscribe(userID)
	defer cancel()

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	// The first ping goes out before anything else, so a client that waits
	// for the stream to open knows the subscription is live — and a create
	// that follows the open reaches the stream, not the gap before it.
	if err := stream.Send(&notificationv1.WatchEvent{Kind: "ping"}); err != nil {
		return err
	}

	for {
		select {
		case row, ok := <-events:
			if !ok {
				return nil
			}
			if err := stream.Send(&notificationv1.WatchEvent{
				Kind:         "notification",
				Notification: wireNotification(row, nil, nil, nil),
			}); err != nil {
				return err
			}
		case <-ticker.C:
			if err := stream.Send(&notificationv1.WatchEvent{Kind: "ping"}); err != nil {
				return err
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// wireNotification maps the stored row onto the wire message. The audience
// lists are the caller's to pass — the create answers the audience it was
// given, the read answers the junctions the row holds, and the live tail
// carries neither, because a stream event names its reader by arriving.
func wireNotification(row Notification, userIDs, groupIDs []uuid.UUID, readAt *time.Time) *notificationv1.Notification {
	n := &notificationv1.Notification{
		Id:           FormatID(row.ID),
		Category:     row.Category,
		Title:        row.Title,
		Body:         row.Body,
		AudienceKind: row.AudienceKind,
		EmailSent:    row.EmailSentAt != nil,
		CreatedAt:    timestamppb.New(row.CreatedAt),
	}
	if row.Topic != nil {
		n.Topic = row.Topic
	}
	if row.CreatedBy != nil {
		createdBy := user.FormatID(*row.CreatedBy)
		n.CreatedBy = &createdBy
	}
	if row.CancelledAt != nil {
		n.CancelledAt = timestamppb.New(*row.CancelledAt)
	}
	if readAt != nil {
		n.ReadAt = timestamppb.New(*readAt)
	}
	for _, id := range userIDs {
		n.UserIds = append(n.UserIds, user.FormatID(id))
	}
	for _, id := range groupIDs {
		n.UserGroupIds = append(n.UserGroupIds, usergroup.FormatID(id))
	}
	return n
}

// wireNotifications maps a page of administrative rows.
func wireNotifications(rows []Notification) []*notificationv1.Notification {
	out := make([]*notificationv1.Notification, 0, len(rows))
	for _, row := range rows {
		out = append(out, wireNotification(row, nil, nil, nil))
	}
	return out
}

// wireInbox maps a page of the caller's inbox, each carrying the receipt
// the caller holds.
func wireInbox(rows []InboxRow) []*notificationv1.Notification {
	out := make([]*notificationv1.Notification, 0, len(rows))
	for _, row := range rows {
		out = append(out, wireNotification(row.Notification, nil, nil, row.ReadAt))
	}
	return out
}

// parseID turns the request's wire-form identifier into the identifier the
// rows carry. A malformed one — prefix missing, payload wrong — names no
// notification, so it is the not-found failure the same as an unknown one.
func parseID(id string) (uuid.UUID, error) {
	parsed, err := UUIDFromWire(id)
	if err != nil {
		return uuid.Nil(), ErrNotFound
	}
	return parsed, nil
}

// callerOf reads the caller the transport's middleware resolved. The
// guard's rules have already refused the caller a procedure is not for, so
// reaching here means the claims name an account; a missing caller is the
// wiring defect it always is, and it is refused rather than dereferenced.
func callerOf(ctx context.Context) (*jwtutils.Caller, error) {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok || caller == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("authentication state missing"))
	}
	return caller, nil
}

// callerUUID turns the caller's subject into the identifier the rows
// carry. The subject travels in the wire form — the TypeID the caller's
// owner is named by — and the rows keep their UUID, so the boundary is
// this one function. A malformed identifier names no caller, the
// not-found the surface answers.
func callerUUID(caller *jwtutils.Caller) (uuid.UUID, error) {
	id, err := user.UUIDFromWire(caller.UserID)
	if err != nil {
		return uuid.Nil(), ErrNotFound
	}
	return id, nil
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
// protocol carries. The internal ones are collapsed to one answer whose
// text names nothing a caller could aim at.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("notification not found"))
	case errors.Is(err, ErrInvalidAudience):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("the audience does not fit the category"))
	case errors.Is(err, ErrUnknownTarget):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("the audience names an unknown account or group"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("notification operation failed"))
	}
}
