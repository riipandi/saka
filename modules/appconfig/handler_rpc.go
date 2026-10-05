package appconfig

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"

	"google.golang.org/protobuf/types/known/timestamppb"

	settingsv1 "github.com/riipandi/saka/codegen/proto/go/saka/settings/v1"
	settingsv1connect "github.com/riipandi/saka/codegen/proto/go/saka/settings/v1/settingsv1connect"
	systemv1 "github.com/riipandi/saka/codegen/proto/go/saka/system/v1"
	systemv1connect "github.com/riipandi/saka/codegen/proto/go/saka/system/v1/systemv1connect"
	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "appconfig"

// Module serves the application-configuration area's surfaces: the system
// configuration REST read, the settings RPC, and the test-email RPC.
// Authentication is the transport's middleware; the module reads the caller
// the context carries, it never verifies a token itself.
type Module struct {
	config   config.Config
	service  *Service
	settings *Settings
}

// NewModule builds the module over the area's features.
func NewModule(cfg config.Config, service *Service, settings *Settings) *Module {
	return &Module{config: cfg, service: service, settings: settings}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the endpoints on the HTTP router. The configuration read
// is the area's one REST route; the settings are RPC alone.
func (m *Module) Mount(r chi.Router) {
	r.Get("/api/configuration", m.serveConfiguration)
}

// MountRPC registers the procedures on the RPC router. The handler options
// are the transport's — the shared snake_case codec and the panic boundary —
// so the procedures answer exactly like the transport's own.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, handler := systemv1connect.NewAppConfigServiceHandler(newRPCHandler(m.service), opts...)
	r.Handle(systemv1connect.AppConfigServiceTestEmailProcedure, handler)

	_, settingsHandler := settingsv1connect.NewSettingsServiceHandler(newSettingsHandler(m.settings), opts...)
	r.Handle(settingsv1connect.SettingsServiceListProcedure, settingsHandler)
	r.Handle(settingsv1connect.SettingsServiceUpdateProcedure, settingsHandler)
	r.Handle(settingsv1connect.SettingsServiceResetProcedure, settingsHandler)
	r.Handle(settingsv1connect.SettingsServiceListPublicProcedure, settingsHandler)
}

// rpcHandler is the transport mapping of the procedures. The service carries
// the rules; this type carries the connect codes and the caller's identity.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) systemv1connect.AppConfigServiceHandler {
	return &rpcHandler{service: service}
}

// TestEmail sends one test message through the configured mailer. The guard
// has already refused a caller who is not an administrator, so reaching here
// means the claims name an account; a missing caller is the wiring defect it
// always is, and it is refused rather than dereferenced.
func (h *rpcHandler) TestEmail(ctx context.Context, req *connect.Request[systemv1.TestEmailRequest]) (*connect.Response[systemv1.TestEmailResponse], error) {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok || caller == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("authentication state missing"))
	}

	callerID, err := user.UUIDFromWire(caller.UserID)
	if err != nil {
		return nil, mapError(ErrUnknownAccount)
	}

	if err := h.service.SendTestEmail(ctx, callerID, req.Msg.GetTo()); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&systemv1.TestEmailResponse{
		Status:  webutil.StatusSuccess,
		Message: "the test email was sent",
	}), nil
}

// mapError translates the service's failures into the codes the Connect
// protocol carries. The internal ones are collapsed to one answer whose
// text names nothing a caller could aim at.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrMailUnavailable):
		return connect.NewError(connect.CodeUnavailable, errors.New("mailer is not configured"))
	case errors.Is(err, ErrUnknownAccount):
		return connect.NewError(connect.CodeNotFound, errors.New("account not found"))
	case errors.Is(err, ErrUnknownSetting):
		return connect.NewError(connect.CodeNotFound, errors.New("setting not found"))
	case errors.Is(err, ErrReservedPrefix):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("the setting is not writable as asked"))
	case errors.Is(err, ErrSealUnavailable):
		return connect.NewError(connect.CodeUnavailable, errors.New("no cipher is configured to seal a sensitive value"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("the app configuration area could not serve the call"))
	}
}

// settingsHandler is the transport mapping of the settings procedures. The
// service carries the rules; this type carries the connect codes and the
// caller's identity.
type settingsHandler struct {
	settings *Settings
}

// newSettingsHandler builds the handler over the settings feature.
func newSettingsHandler(settings *Settings) settingsv1connect.SettingsServiceHandler {
	return &settingsHandler{settings: settings}
}

// List answers every catalog item with its effective values to an
// administrator. The guard has already refused anyone else, and the sealed
// values are opened in the service.
func (h *settingsHandler) List(ctx context.Context, req *connect.Request[settingsv1.ListRequest]) (*connect.Response[settingsv1.ListResponse], error) {
	settings, err := h.settings.List(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&settingsv1.ListResponse{
		Settings: toProtoSettings(settings),
	}), nil
}

// Update replaces one setting's value. The guard has already refused a
// caller who is not an administrator, so reaching here means the claims
// name an account; a missing caller is the wiring defect it always is, and
// it is refused rather than dereferenced.
func (h *settingsHandler) Update(ctx context.Context, req *connect.Request[settingsv1.UpdateRequest]) (*connect.Response[settingsv1.UpdateResponse], error) {
	callerID, err := callerUUID(ctx)
	if err != nil {
		return nil, err
	}

	setting, err := h.settings.UpdateFor(ctx, callerID, req.Msg.GetKey(), req.Msg.GetValue())
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&settingsv1.UpdateResponse{Setting: toProtoSetting(setting)}), nil
}

// Reset removes one setting's override, so the item answers its catalog
// default again.
func (h *settingsHandler) Reset(ctx context.Context, req *connect.Request[settingsv1.ResetRequest]) (*connect.Response[settingsv1.ResetResponse], error) {
	callerID, err := callerUUID(ctx)
	if err != nil {
		return nil, err
	}

	setting, err := h.settings.ResetFor(ctx, callerID, req.Msg.GetKey())
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&settingsv1.ResetResponse{Setting: toProtoSetting(setting)}), nil
}

// ListPublic answers the items an unauthenticated client may read. There is
// no caller to read and nothing that can fail past the service. A request
// that carries the bypass flag reads the source and leaves the cached
// listing alone.
func (h *settingsHandler) ListPublic(ctx context.Context, req *connect.Request[settingsv1.ListPublicRequest]) (*connect.Response[settingsv1.ListPublicResponse], error) {
	settings, err := h.settings.ListPublicBypassingCache(ctx, req.Msg.GetNocache())
	if err != nil {
		return nil, mapError(err)
	}

	public := make([]*settingsv1.PublicSetting, 0, len(settings))
	for _, setting := range settings {
		public = append(public, &settingsv1.PublicSetting{
			Key:   setting.Key,
			Value: setting.Value,
		})
	}
	return connect.NewResponse(&settingsv1.ListPublicResponse{Settings: public}), nil
}

// callerUUID converts the caller's wire identifier into the UUID the
// service writes into the audit record.
func callerUUID(ctx context.Context) (string, error) {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok || caller == nil {
		return "", connect.NewError(connect.CodeInternal, errors.New("authentication state missing"))
	}
	callerID, err := user.UUIDFromWire(caller.UserID)
	if err != nil {
		return "", mapError(ErrUnknownAccount)
	}
	return callerID.String(), nil
}

// toProtoSettings maps the items onto the wire, ordered by key.
func toProtoSettings(settings []Setting) []*settingsv1.Setting {
	out := make([]*settingsv1.Setting, 0, len(settings))
	for _, setting := range settings {
		out = append(out, toProtoSetting(setting))
	}
	return out
}

// toProtoSetting maps one item onto the wire. The value travels in the
// clear whatever the table rests, and the sealed form never crosses.
func toProtoSetting(setting Setting) *settingsv1.Setting {
	out := &settingsv1.Setting{
		Key:          setting.Key,
		Value:        setting.Value,
		DefaultValue: setting.Default,
		Sealed:       setting.Sealed,
		Public:       setting.Public,
	}
	if setting.UpdatedAt != nil {
		out.UpdatedAt = timestamppb.New(*setting.UpdatedAt)
	}
	return out
}
