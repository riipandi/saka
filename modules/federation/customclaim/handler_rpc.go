package customclaim

import (
	"context"

	federationv1 "github.com/riipandi/saka/codegen/proto/go/saka/federation/v1"
	federationv1connect "github.com/riipandi/saka/codegen/proto/go/saka/federation/v1/federationv1connect"
	"github.com/riipandi/saka/framework/webutil"
)

// rpcHandler is the transport mapping of the procedures. The service carries
// the rules; this type carries the connect codes.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) federationv1connect.CustomClaimServiceHandler {
	return &rpcHandler{service: service}
}

// Suggest answers the autocomplete list.
func (h *rpcHandler) Suggest(ctx context.Context, req *federationv1.SuggestCustomClaimsRequest) (*federationv1.SuggestCustomClaimsResponse, error) {
	keys, err := h.service.Suggest(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	list := make([]*federationv1.SuggestedCustomClaimKey, 0, len(keys))
	for _, key := range keys {
		list = append(list, &federationv1.SuggestedCustomClaimKey{
			Key:        key.Key,
			UsageCount: key.UsageCount,
		})
	}
	return &federationv1.SuggestCustomClaimsResponse{
		Keys:    list,
		Status:  webutil.StatusSuccess,
		Message: "the custom claim suggestions were listed",
	}, nil
}

// ListUserClaims answers one account's claims.
func (h *rpcHandler) ListUserClaims(ctx context.Context, req *federationv1.ListUserCustomClaimsRequest) (*federationv1.ListUserCustomClaimsResponse, error) {
	claims, err := h.service.ListByUser(ctx, req.UserId)
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.ListUserCustomClaimsResponse{
		Claims:  wireClaims(claims),
		Status:  webutil.StatusSuccess,
		Message: "the user's custom claims were listed",
	}, nil
}

// CreateUserClaim hangs a claim on an account.
func (h *rpcHandler) CreateUserClaim(ctx context.Context, req *federationv1.CreateUserCustomClaimRequest) (*federationv1.CreateUserCustomClaimResponse, error) {
	claim, err := h.service.CreateByUser(ctx, req.UserId, req.Key, req.Value)
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.CreateUserCustomClaimResponse{
		Claim:   wireClaim(claim),
		Status:  webutil.StatusSuccess,
		Message: "the user's custom claim was created",
	}, nil
}

// UpdateUserClaim rewrites one of an account's claims.
func (h *rpcHandler) UpdateUserClaim(ctx context.Context, req *federationv1.UpdateUserCustomClaimRequest) (*federationv1.UpdateUserCustomClaimResponse, error) {
	claim, err := h.service.UpdateByUser(ctx, req.Id, req.Key, req.Value)
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.UpdateUserCustomClaimResponse{
		Claim:   wireClaim(claim),
		Status:  webutil.StatusSuccess,
		Message: "the user's custom claim was updated",
	}, nil
}

// DeleteUserClaim removes one of an account's claims.
func (h *rpcHandler) DeleteUserClaim(ctx context.Context, req *federationv1.DeleteUserCustomClaimRequest) (*federationv1.DeleteUserCustomClaimResponse, error) {
	if err := h.service.DeleteByUser(ctx, req.Id); err != nil {
		return nil, mapError(err)
	}
	return &federationv1.DeleteUserCustomClaimResponse{
		Status:  webutil.StatusSuccess,
		Message: "the user's custom claim was deleted",
	}, nil
}

// ListGroupClaims answers one group's claims.
func (h *rpcHandler) ListGroupClaims(ctx context.Context, req *federationv1.ListGroupCustomClaimsRequest) (*federationv1.ListGroupCustomClaimsResponse, error) {
	claims, err := h.service.ListByGroup(ctx, req.UserGroupId)
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.ListGroupCustomClaimsResponse{
		Claims:  wireClaims(claims),
		Status:  webutil.StatusSuccess,
		Message: "the group's custom claims were listed",
	}, nil
}

// CreateGroupClaim hangs a claim on a group.
func (h *rpcHandler) CreateGroupClaim(ctx context.Context, req *federationv1.CreateGroupCustomClaimRequest) (*federationv1.CreateGroupCustomClaimResponse, error) {
	claim, err := h.service.CreateByGroup(ctx, req.UserGroupId, req.Key, req.Value)
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.CreateGroupCustomClaimResponse{
		Claim:   wireClaim(claim),
		Status:  webutil.StatusSuccess,
		Message: "the group's custom claim was created",
	}, nil
}

// UpdateGroupClaim rewrites one of a group's claims.
func (h *rpcHandler) UpdateGroupClaim(ctx context.Context, req *federationv1.UpdateGroupCustomClaimRequest) (*federationv1.UpdateGroupCustomClaimResponse, error) {
	claim, err := h.service.UpdateByGroup(ctx, req.Id, req.Key, req.Value)
	if err != nil {
		return nil, mapError(err)
	}
	return &federationv1.UpdateGroupCustomClaimResponse{
		Claim:   wireClaim(claim),
		Status:  webutil.StatusSuccess,
		Message: "the group's custom claim was updated",
	}, nil
}

// DeleteGroupClaim removes one of a group's claims.
func (h *rpcHandler) DeleteGroupClaim(ctx context.Context, req *federationv1.DeleteGroupCustomClaimRequest) (*federationv1.DeleteGroupCustomClaimResponse, error) {
	if err := h.service.DeleteByGroup(ctx, req.Id); err != nil {
		return nil, mapError(err)
	}
	return &federationv1.DeleteGroupCustomClaimResponse{
		Status:  webutil.StatusSuccess,
		Message: "the group's custom claim was deleted",
	}, nil
}
