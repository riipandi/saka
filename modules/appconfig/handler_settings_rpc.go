package appconfig

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	settingsv1 "github.com/riipandi/tango/codegen/proto/go/tango/settings/v1"
	settingsv1connect "github.com/riipandi/tango/codegen/proto/go/tango/settings/v1/settingsv1connect"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/jwtutils"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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
