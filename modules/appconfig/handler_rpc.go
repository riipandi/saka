package appconfig

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"

	settingsv1connect "github.com/riipandi/tango/codegen/proto/go/tango/settings/v1/settingsv1connect"
	systemv1 "github.com/riipandi/tango/codegen/proto/go/tango/system/v1"
	systemv1connect "github.com/riipandi/tango/codegen/proto/go/tango/system/v1/systemv1connect"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/jwtutils"
	"github.com/riipandi/tango/pkg/responder"
)

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "appconfig"

// Module serves the application-configuration area's procedures: the system
// configuration surface and the database-backed settings surface, all of it
// RPC. Authentication is the transport's middleware; the module reads the
// caller the context carries, it never verifies a token itself.
type Module struct {
	service  *Service
	settings *Settings
}

// NewModule builds the module over the area's two features.
func NewModule(service *Service, settings *Settings) *Module {
	return &Module{service: service, settings: settings}
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
	_, handler := systemv1connect.NewAppConfigServiceHandler(newRPCHandler(m.service), opts...)
	r.Handle(systemv1connect.AppConfigServiceGetProcedure, handler)
	r.Handle(systemv1connect.AppConfigServiceGetAllProcedure, handler)
	r.Handle(systemv1connect.AppConfigServiceTestEmailProcedure, handler)

	_, settingsHandler := settingsv1connect.NewSettingsServiceHandler(newSettingsHandler(m.settings), opts...)
	r.Handle(settingsv1connect.SettingsServiceListProcedure, settingsHandler)
	r.Handle(settingsv1connect.SettingsServiceGetProcedure, settingsHandler)
	r.Handle(settingsv1connect.SettingsServiceSetProcedure, settingsHandler)
	r.Handle(settingsv1connect.SettingsServiceDeleteProcedure, settingsHandler)
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

// Get answers the public configuration. The guard has already admitted an
// unauthenticated caller — the procedure is declared public — so there is
// nothing to read from the context, and nothing to fail on: the
// configuration was resolved at startup or the process never got here.
func (h *rpcHandler) Get(ctx context.Context, req *connect.Request[systemv1.GetRequest]) (*connect.Response[systemv1.GetResponse], error) {
	return connect.NewResponse(&systemv1.GetResponse{
		Config: h.service.GetPublic(),
	}), nil
}

// GetAll answers the full configuration to an administrator. The guard has
// already refused anyone else, so reaching here is the authorization, and a
// read of startup-resolved values cannot fail.
func (h *rpcHandler) GetAll(ctx context.Context, req *connect.Request[systemv1.GetAllRequest]) (*connect.Response[systemv1.GetAllResponse], error) {
	return connect.NewResponse(&systemv1.GetAllResponse{
		Config: h.service.GetAll(),
	}), nil
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
		Status:  responder.StatusSuccess,
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
	case errors.Is(err, ErrSealedNotPublic), errors.Is(err, ErrReservedPrefix):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("the setting is not writable as asked"))
	case errors.Is(err, ErrSealUnavailable):
		return connect.NewError(connect.CodeUnavailable, errors.New("no cipher is configured to seal a sensitive value"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("the app configuration area could not serve the call"))
	}
}
