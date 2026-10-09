package devicelogin

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	authnv1 "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// rpcHandler is the transport mapping of the approval procedures. The
// service carries the rules; this type carries the connect codes and
// the request facts the protocol supplies on its own.
type rpcHandler struct {
	service *Service
}

func newRPCHandler(service *Service) *rpcHandler {
	return &rpcHandler{service: service}
}

// Inspect answers the request the code names.
func (h *rpcHandler) Inspect(ctx context.Context, req *authnv1.InspectDeviceLoginRequest) (*authnv1.InspectDeviceLoginResponse, error) {
	if _, ok := jwtutils.CallerFrom(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "authentication required")
	}

	inspection, err := h.service.Inspect(ctx, req.Code)
	switch {
	case errors.Is(err, ErrCodeUnknown):
		return nil, connect.NewError(connect.CodeNotFound, "no live pairing request answers this code")
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, "the inspection failed")
	}

	return &authnv1.InspectDeviceLoginResponse{
		UserCode:  inspection.UserCode,
		IpAddress: inspection.IPAddress,
		UserAgent: inspection.UserAgent,
		ExpiresAt: timestamppb.New(inspection.ExpiresAt),
	}, nil
}

// Decide stamps the answer, the deciding account named by the bearer.
func (h *rpcHandler) Decide(ctx context.Context, req *authnv1.DecideDeviceLoginRequest) (*authnv1.DecideDeviceLoginResponse, error) {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "authentication required")
	}

	// A delegated caller decides nothing: the approval names the account
	// whose browser will sign in, and a token acting for another must
	// not mint sessions for a third device.
	if caller.IsImpersonating() {
		return nil, connect.NewError(connect.CodePermissionDenied, "an impersonating session cannot approve a device login")
	}

	decision := Decision(req.Decision)
	if decision != DecisionApprove && decision != DecisionDeny {
		return nil, connect.NewError(connect.CodeInvalidArgument, "decision must be approve or deny")
	}

	err := h.service.Decide(ctx, req.Code, decision, caller)
	switch {
	case errors.Is(err, ErrCodeUnknown):
		return nil, connect.NewError(connect.CodeNotFound, "no live pairing request answers this code")
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, "the decision could not be recorded")
	}

	return &authnv1.DecideDeviceLoginResponse{
		Status:  "success",
		Message: "the decision was recorded",
	}, nil
}
