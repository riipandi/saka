package customclaim

import (
	"errors"

	"connectrpc.com/connect/v2"

	federationv1 "github.com/riipandi/saka/codegen/proto/go/saka/federation/v1"
)

// mapError translates the service's failures into the codes the Connect
// protocol carries. The internal ones are collapsed to one answer whose text
// names nothing a caller could aim at.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrClaimNotFound):
		return connect.NewError(connect.CodeNotFound, "custom claim not found")
	case errors.Is(err, ErrClaimExists):
		return connect.NewError(connect.CodeAlreadyExists, "the subject already carries this claim key")
	case errors.Is(err, ErrSubjectNotFound):
		return connect.NewError(connect.CodeFailedPrecondition, "the named subject does not exist")
	case errors.Is(err, ErrClaimWrongSubject):
		return connect.NewError(connect.CodeFailedPrecondition, "the claim belongs to the other subject kind")
	case errors.Is(err, ErrReservedClaim):
		return connect.NewError(connect.CodeInvalidArgument, "the claim key is reserved by the protocol and cannot be used as a custom claim")
	default:
		return connect.NewError(connect.CodeInternal, "custom claim operation failed")
	}
}

// wireClaim maps the service view onto the wire message.
func wireClaim(view ClaimView) *federationv1.CustomClaim {
	return &federationv1.CustomClaim{
		Id:    view.ID,
		Key:   view.Key,
		Value: view.Value,
	}
}

// wireClaims maps a list of views.
func wireClaims(views []ClaimView) []*federationv1.CustomClaim {
	list := make([]*federationv1.CustomClaim, 0, len(views))
	for _, view := range views {
		list = append(list, wireClaim(view))
	}
	return list
}
