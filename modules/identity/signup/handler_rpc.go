package signup

import (
	"context"
	"errors"
	"math"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/go-chi/chi/v5"

	commonv1 "github.com/riipandi/saka/codegen/proto/go/saka/common/v1"
	identityv1 "github.com/riipandi/saka/codegen/proto/go/saka/identity/v1"
	identityv1connect "github.com/riipandi/saka/codegen/proto/go/saka/identity/v1/identityv1connect"
	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/modules/identity/password"
	"github.com/riipandi/saka/modules/identity/user"
)

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "signup"

// Module serves the sign-up and signup-token procedures. Everything it
// answers is an RPC procedure, so its HTTP mount is empty by construction.
type Module struct {
	service *Service
}

// NewModule builds the module over the sign-up service.
func NewModule(service *Service) *Module {
	return &Module{service: service}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the endpoints on the HTTP router. The feature serves no
// plain HTTP route: a procedure is POST-only on the RPC surface.
func (m *Module) Mount(r chi.Router) {}

// MountRPC registers the procedures on the RPC server. The server carries the
// transport's interceptors and the mount the shared snake_case codec, so the
// procedures answer exactly like the transport's own.
func (m *Module) MountRPC(server *connect.Server) {
	identityv1connect.RegisterSignupServiceHandler(server, newRPCHandler(m.service))
}

// rpcHandler is the transport mapping of the procedures. The service carries
// the rules; this type carries the connect codes.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) identityv1connect.SignupServiceHandler {
	return &rpcHandler{service: service}
}

// Signup creates an account from a signup token.
func (h *rpcHandler) Signup(ctx context.Context, req *identityv1.SignupRequest) (*identityv1.SignupResponse, error) {
	body := req

	account, err := h.service.Signup(ctx, Params{
		Username:  body.Username,
		Email:     body.Email,
		Password:  body.Password,
		Token:     body.Token,
		FirstName: body.GetFirstName(),
		LastName:  body.GetLastName(),
	})
	if err != nil {
		return nil, mapError(err)
	}

	return &identityv1.SignupResponse{
		User: user.WireView(account),

		Status:  webutil.StatusSuccess,
		Message: "the account was created",
	}, nil
}

// CreateSignupToken issues a signup token. The procedure is administrative:
// the transport authenticated the caller, and the claims decide the role.
func (h *rpcHandler) CreateSignupToken(ctx context.Context, req *identityv1.CreateSignupTokenRequest) (*identityv1.CreateSignupTokenResponse, error) {
	body := req
	created, err := h.service.CreateSignupToken(ctx, CreateTokenParams{
		TTL:        time.Duration(body.TtlSeconds) * time.Second,
		UsageLimit: body.GetUsageLimit(),
		GroupIDs:   body.GetUserGroupIds(),
	})
	if err != nil {
		return nil, mapError(err)
	}

	return &identityv1.CreateSignupTokenResponse{
		Token:    tokenView(created.Token),
		RawToken: created.RawToken,

		Status:  webutil.StatusSuccess,
		Message: "the signup token was created",
	}, nil
}

// ListSignupTokens answers the issued tokens with their pagination block.
func (h *rpcHandler) ListSignupTokens(ctx context.Context, req *identityv1.ListSignupTokensRequest) (*identityv1.ListSignupTokensResponse, error) {
	tokens, pagination, err := h.service.ListSignupTokens(ctx, req.GetSortBy(), req.GetSortOrder() == "asc", int(req.GetPage()), int(req.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}

	views := make([]*identityv1.SignupToken, 0, len(tokens))
	for _, token := range tokens {
		views = append(views, tokenView(token))
	}
	return &identityv1.ListSignupTokensResponse{
		Tokens:   views,
		Metadata: listMetadata(pagination),

		Status:  webutil.StatusSuccess,
		Message: "the signup tokens were listed",
	}, nil
}

// DeleteSignupToken revokes an issued token.
func (h *rpcHandler) DeleteSignupToken(ctx context.Context, req *identityv1.DeleteSignupTokenRequest) (*identityv1.DeleteSignupTokenResponse, error) {
	if err := h.service.DeleteSignupToken(ctx, req.Id); err != nil {
		return nil, mapError(err)
	}
	return &identityv1.DeleteSignupTokenResponse{
		Status:  webutil.StatusSuccess,
		Message: "the signup token was deleted",
	}, nil
}

// tokenView maps the service's token view onto the wire message.
func tokenView(token TokenView) *identityv1.SignupToken {
	return &identityv1.SignupToken{
		Id:           token.ID,
		UsageLimit:   token.UsageLimit,
		UsageCount:   token.UsageCount,
		CreatedAt:    token.CreatedAt.Format(time.RFC3339),
		ExpiresAt:    token.ExpiresAt.Format(time.RFC3339),
		UserGroupIds: token.GroupIDs,
	}
}

// listMetadata maps the responder's pagination onto the shared block. The
// wire fields are optional, so an unknown range is absent rather than zero.
func listMetadata(p webutil.Pagination) *commonv1.ListMetadata {
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
		*dst = ptr(int32(value))
	}
	set(&meta.Page, p.Page)
	set(&meta.Limit, p.Limit)
	set(&meta.TotalPages, p.TotalPages)
	set(&meta.TotalItems, p.TotalItems)
	set(&meta.FirstItemIndex, p.FirstItemIndex)
	set(&meta.LastItemIndex, p.LastItemIndex)
	return meta
}

// ptr hands the setters an addressable value.
func ptr[T any](value T) *T {
	return &value
}

// mapError translates the service's failures into the codes the Connect
// protocol carries. The internal ones are collapsed to one answer whose text
// names nothing a caller could aim at. A malformed field never reaches the
// service: the transport's validate interceptor refuses it with the typed
// violation details the contracts carry.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidToken):
		return connect.NewError(connect.CodePermissionDenied, "signup token is invalid or expired")
	case errors.Is(err, ErrAccountExists):
		return connect.NewError(connect.CodeAlreadyExists, "account already exists")
	case errors.Is(err, ErrSignupNotAllowed):
		return connect.NewError(connect.CodeNotFound, "sign-up is not available")
	case errors.Is(err, ErrUsernameRequired):
		return connect.NewError(connect.CodeInvalidArgument, "username is required")
	case errors.Is(err, ErrUsernameInvalid):
		return connect.NewError(connect.CodeInvalidArgument, "username is invalid")
	case errors.Is(err, ErrTokenNotFound):
		return connect.NewError(connect.CodeNotFound, "signup token not found")
	case isPasswordPolicy(err):
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	default:
		return connect.NewError(connect.CodeInternal, "sign-up failed")
	}
}

// isPasswordPolicy reports whether the failure is the credential policy's
// refusal. The rule lives in the password package, so the check does too —
// the handler maps the answer without learning the policy's rules.
func isPasswordPolicy(err error) bool {
	return errors.Is(err, password.ErrWeakPassword) || errors.Is(err, password.ErrBreachedPassword)
}
