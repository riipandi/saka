package appconfig

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"

	settingsv1 "github.com/riipandi/tango/codegen/proto/go/tango/settings/v1"
	settingsv1connect "github.com/riipandi/tango/codegen/proto/go/tango/settings/v1/settingsv1connect"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/jwtutils"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// timestampOf maps an instant. A row's zero is never mapped: the pointer
// carries the absence, and the wire field is omitted rather than zeroed.
func timestampOf(t time.Time) *timestamppb.Timestamp {
	return timestamppb.New(t)
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

// List answers every setting to an administrator. The guard has already
// refused anyone else, and the values are opened in the service.
func (h *settingsHandler) List(ctx context.Context, req *connect.Request[settingsv1.ListRequest]) (*connect.Response[settingsv1.ListResponse], error) {
	settings, err := h.settings.List(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&settingsv1.ListResponse{
		Settings: toProtoSettings(settings),
	}), nil
}

// Get answers one setting by key. An unknown key is a not_found.
func (h *settingsHandler) Get(ctx context.Context, req *connect.Request[settingsv1.GetRequest]) (*connect.Response[settingsv1.GetResponse], error) {
	setting, err := h.settings.GetSetting(ctx, req.Msg.GetKey())
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&settingsv1.GetResponse{Setting: toProtoSetting(setting)}), nil
}

// Set creates or replaces one setting. The guard has already refused a
// caller who is not an administrator, so reaching here means the claims
// name an account; a missing caller is the wiring defect it always is, and
// it is refused rather than dereferenced.
func (h *settingsHandler) Set(ctx context.Context, req *connect.Request[settingsv1.SetRequest]) (*connect.Response[settingsv1.SetResponse], error) {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok || caller == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("authentication state missing"))
	}
	callerID, err := user.UUIDFromWire(caller.UserID)
	if err != nil {
		return nil, mapError(ErrUnknownAccount)
	}

	setting, err := h.settings.SetFor(ctx, callerID.String(),
		req.Msg.GetKey(), req.Msg.GetValue(), req.Msg.GetSensitive(), req.Msg.GetPublic())
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&settingsv1.SetResponse{Setting: toProtoSetting(setting)}), nil
}

// Delete removes one setting. An unknown key is a not_found, not a silence.
func (h *settingsHandler) Delete(ctx context.Context, req *connect.Request[settingsv1.DeleteRequest]) (*connect.Response[settingsv1.DeleteResponse], error) {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok || caller == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("authentication state missing"))
	}
	callerID, err := user.UUIDFromWire(caller.UserID)
	if err != nil {
		return nil, mapError(ErrUnknownAccount)
	}

	if err := h.settings.DeleteFor(ctx, callerID.String(), req.Msg.GetKey()); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&settingsv1.DeleteResponse{}), nil
}

// ListPublic answers the rows an unauthenticated client may read. There is
// no caller to read and nothing that can fail past the service.
func (h *settingsHandler) ListPublic(ctx context.Context, req *connect.Request[settingsv1.ListPublicRequest]) (*connect.Response[settingsv1.ListPublicResponse], error) {
	settings, err := h.settings.ListPublic(ctx)
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

// toProtoSettings maps the rows onto the wire, newest change last.
func toProtoSettings(settings []Setting) []*settingsv1.Setting {
	out := make([]*settingsv1.Setting, 0, len(settings))
	for _, setting := range settings {
		out = append(out, toProtoSetting(setting))
	}
	return out
}

// toProtoSetting maps one row onto the wire. The value travels in the
// clear whatever the table rests, and the sealed form never crosses.
func toProtoSetting(setting Setting) *settingsv1.Setting {
	out := &settingsv1.Setting{
		Key:    setting.Key,
		Value:  setting.Value,
		Public: setting.Public,
	}
	if setting.UpdatedAt != nil {
		out.UpdatedAt = timestampOf(*setting.UpdatedAt)
	}
	return out
}
