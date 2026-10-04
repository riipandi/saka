package webhook

import (
	"context"
	"encoding/json/v2"
	"errors"
	"math"

	"uuid"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/riipandi/saka/codegen/proto/go/saka/common/v1"
	webhookv1 "github.com/riipandi/saka/codegen/proto/go/saka/webhook/v1"
	webhookv1connect "github.com/riipandi/saka/codegen/proto/go/saka/webhook/v1/webhookv1connect"
	"github.com/riipandi/saka/framework/webutil"
)

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "webhook"

// Module serves the webhook procedures: the RPC surface, all of it. An
// endpoint is managed through the API, and the deliveries it receives are
// read there too.
type Module struct {
	service *Service
}

// NewModule builds the module over the webhook service.
func NewModule(service *Service) *Module {
	return &Module{service: service}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the module's endpoints on the router. The surface is RPC
// alone, so there is nothing on the HTTP router to claim.
func (m *Module) Mount(r chi.Router) {}

// MountRPC registers the procedures on the RPC router. Each procedure is
// registered at its own path: the generated handler answers a path under its
// prefix it does not know with a plain-text 404, which a Connect client
// cannot read.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, handler := webhookv1connect.NewWebhookServiceHandler(newRPCHandler(m.service), opts...)
	r.Handle(webhookv1connect.WebhookServiceListProcedure, handler)
	r.Handle(webhookv1connect.WebhookServiceCreateProcedure, handler)
	r.Handle(webhookv1connect.WebhookServiceGetProcedure, handler)
	r.Handle(webhookv1connect.WebhookServiceUpdateProcedure, handler)
	r.Handle(webhookv1connect.WebhookServiceDeleteProcedure, handler)
	r.Handle(webhookv1connect.WebhookServiceRotateSecretProcedure, handler)
	r.Handle(webhookv1connect.WebhookServiceTestProcedure, handler)
	r.Handle(webhookv1connect.WebhookServiceListDeliveriesProcedure, handler)
	r.Handle(webhookv1connect.WebhookServiceListAllDeliveriesProcedure, handler)
	r.Handle(webhookv1connect.WebhookServiceListEventTypesProcedure, handler)
}

// rpcHandler is the transport mapping of the procedures. The service carries
// the rules; this type carries the connect codes and the wire shapes.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) webhookv1connect.WebhookServiceHandler {
	return &rpcHandler{service: service}
}

// List answers one page of the endpoints.
func (h *rpcHandler) List(ctx context.Context, req *connect.Request[webhookv1.ListWebhooksRequest]) (*connect.Response[webhookv1.ListWebhooksResponse], error) {
	enabled := (*bool)(req.Msg.Enabled)
	rows, pagination, err := h.service.List(ctx, enabled, req.Msg.GetEvent(), int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&webhookv1.ListWebhooksResponse{
		Webhooks: wireEndpoints(rows),
		Metadata: metadataOf(pagination),
		Status:   webutil.StatusSuccess,
		Message:  "the webhooks were listed",
	}), nil
}

// Create registers an endpoint and shows its signing secret once.
func (h *rpcHandler) Create(ctx context.Context, req *connect.Request[webhookv1.CreateWebhookRequest]) (*connect.Response[webhookv1.CreateWebhookResponse], error) {
	params := CreateParams{
		Name:       req.Msg.Name,
		Endpoint:   req.Msg.Endpoint,
		Method:     req.Msg.Method,
		Headers:    req.Msg.Headers,
		EventTypes: req.Msg.EventTypes,
	}
	if req.Msg.Description != nil {
		params.Description = *req.Msg.Description
	}
	row, secret, err := h.service.Create(ctx, params)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&webhookv1.CreateWebhookResponse{
		Webhook: wireEndpoint(row),
		Secret:  secret,
		Status:  webutil.StatusSuccess,
		Message: "the webhook was created",
	}), nil
}

// Get answers one endpoint's view.
func (h *rpcHandler) Get(ctx context.Context, req *connect.Request[webhookv1.GetWebhookRequest]) (*connect.Response[webhookv1.GetWebhookResponse], error) {
	id, idErr := parseID(req.Msg.Id)
	if idErr != nil {
		return nil, mapError(idErr)
	}
	row, err := h.service.Get(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&webhookv1.GetWebhookResponse{
		Webhook: wireEndpoint(row),
		Status:  webutil.StatusSuccess,
		Message: "the webhook was read",
	}), nil
}

// Update rewrites the fields the caller named.
func (h *rpcHandler) Update(ctx context.Context, req *connect.Request[webhookv1.UpdateWebhookRequest]) (*connect.Response[webhookv1.UpdateWebhookResponse], error) {
	id, idErr := parseID(req.Msg.Id)
	if idErr != nil {
		return nil, mapError(idErr)
	}
	params := UpdateParams{
		Description: req.Msg.Description,
		Endpoint:    req.Msg.Endpoint,
		Method:      req.Msg.Method,
		Enabled:     req.Msg.Enabled,
	}
	if req.Msg.Headers != nil {
		params.Headers = &req.Msg.Headers.Headers
	}
	if req.Msg.EventTypes != nil {
		params.EventTypes = &req.Msg.EventTypes.EventTypes
	}
	row, err := h.service.Update(ctx, id, params)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&webhookv1.UpdateWebhookResponse{
		Webhook: wireEndpoint(row),
		Status:  webutil.StatusSuccess,
		Message: "the webhook was updated",
	}), nil
}

// Delete removes one endpoint.
func (h *rpcHandler) Delete(ctx context.Context, req *connect.Request[webhookv1.DeleteWebhookRequest]) (*connect.Response[webhookv1.DeleteWebhookResponse], error) {
	id, idErr := parseID(req.Msg.Id)
	if idErr != nil {
		return nil, mapError(idErr)
	}
	if err := h.service.Delete(ctx, id); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&webhookv1.DeleteWebhookResponse{
		Status:  webutil.StatusSuccess,
		Message: "the webhook was deleted",
	}), nil
}

// RotateSecret replaces an endpoint's signing secret and shows it once.
func (h *rpcHandler) RotateSecret(ctx context.Context, req *connect.Request[webhookv1.RotateWebhookSecretRequest]) (*connect.Response[webhookv1.RotateWebhookSecretResponse], error) {
	id, idErr := parseID(req.Msg.Id)
	if idErr != nil {
		return nil, mapError(idErr)
	}
	row, secret, err := h.service.RotateSecret(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&webhookv1.RotateWebhookSecretResponse{
		Webhook: wireEndpoint(row),
		Secret:  secret,
		Status:  webutil.StatusSuccess,
		Message: "the webhook secret was rotated",
	}), nil
}

// Test queues one test delivery.
func (h *rpcHandler) Test(ctx context.Context, req *connect.Request[webhookv1.TestWebhookRequest]) (*connect.Response[webhookv1.TestWebhookResponse], error) {
	id, idErr := parseID(req.Msg.Id)
	if idErr != nil {
		return nil, mapError(idErr)
	}
	if err := h.service.Test(ctx, id); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&webhookv1.TestWebhookResponse{
		Status:  webutil.StatusSuccess,
		Message: "the test delivery was queued",
	}), nil
}

// ListDeliveries answers one page of an endpoint's deliveries.
func (h *rpcHandler) ListDeliveries(ctx context.Context, req *connect.Request[webhookv1.ListWebhookDeliveriesRequest]) (*connect.Response[webhookv1.ListWebhookDeliveriesResponse], error) {
	id, idErr := parseID(req.Msg.WebhookId)
	if idErr != nil {
		return nil, mapError(idErr)
	}
	views, pagination, err := h.service.ListDeliveries(ctx, id, int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&webhookv1.ListWebhookDeliveriesResponse{
		Deliveries: wireDeliveries(views),
		Metadata:   metadataOf(pagination),
		Status:     webutil.StatusSuccess,
		Message:    "the webhook deliveries were listed",
	}), nil
}

// ListAllDeliveries answers one page of every delivery the deployment holds.
func (h *rpcHandler) ListAllDeliveries(ctx context.Context, req *connect.Request[webhookv1.ListAllWebhookDeliveriesRequest]) (*connect.Response[webhookv1.ListAllWebhookDeliveriesResponse], error) {
	views, pagination, err := h.service.ListAllDeliveries(ctx, req.Msg.GetEvent(), int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&webhookv1.ListAllWebhookDeliveriesResponse{
		Deliveries: wireDeliveries(views),
		Metadata:   metadataOf(pagination),
		Status:     webutil.StatusSuccess,
		Message:    "the webhook deliveries were listed",
	}), nil
}

// ListEventTypes answers the webhook event catalog: every event a receiver
// can subscribe to, with the sentence that says what happened.
func (h *rpcHandler) ListEventTypes(ctx context.Context, req *connect.Request[webhookv1.ListWebhookEventTypesRequest]) (*connect.Response[webhookv1.ListWebhookEventTypesResponse], error) {
	events := h.service.EventCatalog()
	types := make([]*webhookv1.WebhookEventType, 0, len(events))
	for _, event := range events {
		types = append(types, &webhookv1.WebhookEventType{
			Name:        event.Name,
			Description: event.Description,
		})
	}
	return connect.NewResponse(&webhookv1.ListWebhookEventTypesResponse{
		EventTypes: types,
		Status:     webutil.StatusSuccess,
		Message:    "the webhook event types were listed",
	}), nil
}

// wireEndpoint maps the stored row onto the wire message. The sealed secret
// never travels: the raw value is the create and rotate responses' `secret`
// field alone.
func wireEndpoint(row EndpointSchema) *webhookv1.Webhook {
	webhook := &webhookv1.Webhook{
		Id:         FormatEndpointID(row.ID),
		Name:       row.Name,
		Endpoint:   deref(row.Endpoint),
		Method:     row.Method,
		Headers:    row.CustomHeaders(),
		Enabled:    row.Enabled,
		EventTypes: row.EventTypes,
		CreatedAt:  timestamppb.New(row.CreatedAt),
	}
	if row.Descr != nil {
		webhook.Description = row.Descr
	}
	if row.UpdatedAt != nil {
		webhook.UpdatedAt = timestamppb.New(*row.UpdatedAt)
	}
	if webhook.Headers == nil {
		webhook.Headers = map[string]string{}
	}
	if webhook.EventTypes == nil {
		webhook.EventTypes = []string{}
	}
	return webhook
}

// wireEndpoints maps a page of rows.
func wireEndpoints(rows []EndpointSchema) []*webhookv1.Webhook {
	webhooks := make([]*webhookv1.Webhook, 0, len(rows))
	for _, row := range rows {
		webhooks = append(webhooks, wireEndpoint(row))
	}
	return webhooks
}

// narrowed answers the int32 the wire field carries, saturating at the
// bound the field cannot hold. The counts and durations the rows carry are
// far below it; the guard is for the shape, not the expectation.
func narrowed(value int) int32 {
	if value > math.MaxInt32 || value < math.MinInt32 {
		return math.MaxInt32
	}
	return int32(value)
}

// wireDelivery maps one delivery and its latest attempt onto the wire.
func wireDelivery(view DeliveryView) *webhookv1.WebhookDelivery {
	row := view.Delivery
	delivery := &webhookv1.WebhookDelivery{
		Id:           FormatDeliveryID(row.ID),
		Event:        row.Event,
		Status:       row.Status,
		AttemptCount: narrowed(row.AttemptCount),
		CreatedAt:    timestamppb.New(row.CreatedAt),
	}
	if row.WebhookID != nil {
		id := FormatEndpointID(*row.WebhookID)
		delivery.WebhookId = &id
	}
	if row.DeliveredAt != nil {
		delivery.DeliveredAt = timestamppb.New(*row.DeliveredAt)
	}
	if view.Attempt != nil {
		delivery.LatestAttempt = wireAttempt(*view.Attempt)
	}
	return delivery
}

// wireDeliveries maps a page of views.
func wireDeliveries(views []DeliveryView) []*webhookv1.WebhookDelivery {
	deliveries := make([]*webhookv1.WebhookDelivery, 0, len(views))
	for _, view := range views {
		deliveries = append(deliveries, wireDelivery(view))
	}
	return deliveries
}

// wireAttempt maps one attempt row onto the wire. The response metadata is
// what an operator needs; the response body was never stored.
func wireAttempt(row AttemptSchema) *webhookv1.WebhookDeliveryAttempt {
	attempt := &webhookv1.WebhookDeliveryAttempt{
		Id:            FormatAttemptID(row.ID),
		AttemptNumber: narrowed(row.AttemptNumber),
		CreatedAt:     timestamppb.New(row.CreatedAt),
	}
	if row.ResponseStatus != nil {
		status := narrowed(*row.ResponseStatus)
		attempt.ResponseStatus = &status
	}
	if row.Error != nil {
		attempt.Error = row.Error
	}
	if row.DurationMS != nil {
		duration := narrowed(*row.DurationMS)
		attempt.DurationMs = &duration
	}
	if len(row.Response) > 0 {
		metadata := map[string]string{}
		if json.Unmarshal(row.Response, &metadata) == nil && metadata != nil {
			attempt.Response = metadata
		}
	}
	if attempt.Response == nil {
		attempt.Response = map[string]string{}
	}
	return attempt
}

// parseID turns the request's identifier into the identifier the rows carry.
// The wire form is the TypeID the endpoint is named by — the prefix is what
// makes the identifier self-describing on a receiver's log line — and a
// malformed one names no endpoint, so it is the not-found failure the same
// as an unknown one.
func parseID(id string) (uuid.UUID, error) {
	parsed, err := ParseEndpointID(id)
	if err != nil {
		return uuid.Nil(), ErrEndpointNotFound
	}
	return parsed, nil
}

// metadataOf maps the responder's pagination onto the shared block. The wire
// fields are optional, so an unknown range is absent rather than zero.
func metadataOf(p webutil.Pagination) *commonv1.ListMetadata {
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
// names nothing a caller could aim at.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrEndpointNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("webhook not found"))
	case errors.Is(err, ErrEndpointExists):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("webhook name already in use"))
	case errors.Is(err, ErrReservedHeader):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("the headers cannot carry the signature set's names"))
	case errors.Is(err, ErrSecretUnavailable):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("the application secret is not configured"))
	case errors.Is(err, ErrUnknownEvent):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("the event names must be catalog events, or the wildcard"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("webhook operation failed"))
	}
}
