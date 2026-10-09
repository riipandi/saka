package storage

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	storagev1 "github.com/riipandi/saka/codegen/proto/go/saka/storage/v1"
	storagev1connect "github.com/riipandi/saka/codegen/proto/go/saka/storage/v1/storagev1connect"
	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// AreaName is the name the area reports under in the registry.
const AreaName = "storage"

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "bucket"

// Module serves the bucket management procedures: the RPC surface, all of
// it. A bucket is managed through the API, not through browser forms, so
// there is nothing on the HTTP router to claim.
type Module struct {
	service *Service
}

// NewModule builds the module over the bucket service.
func NewModule(service *Service) *Module {
	return &Module{service: service}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the module's endpoints on the router. The surface is RPC
// alone.
func (m *Module) Mount(r chi.Router) {}

// MountRPC registers the procedures on the RPC server. The server carries the
// transport's interceptors and the mount the shared snake_case codec, so the
// procedures answer exactly like the transport's own.
func (m *Module) MountRPC(server *connect.Server) {
	storagev1connect.RegisterBucketServiceHandler(server, newRPCHandler(m.service))
}

// rpcHandler is the transport mapping of the procedures. The service carries
// the rules; this type carries the connect codes and the caller's identity:
// the surface is administrative, so every write names the administrator the
// context carries.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) storagev1connect.BucketServiceHandler {
	return &rpcHandler{service: service}
}

// CreateBucket registers a new bucket.
func (h *rpcHandler) CreateBucket(ctx context.Context, req *storagev1.CreateBucketRequest) (*storagev1.CreateBucketResponse, error) {
	actor, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	params := CreateParams{
		Name:          req.Name,
		FileSizeLimit: req.FileSizeLimit,
	}
	if req.AllowedMimeTypes != nil {
		params.AllowedMimeTypes = req.AllowedMimeTypes.Types
	}
	created, err := h.service.Create(ctx, actor, params)
	if err != nil {
		return nil, mapError(err)
	}
	return &storagev1.CreateBucketResponse{
		Bucket:  wireBucket(created),
		Status:  webutil.StatusSuccess,
		Message: "the bucket was created",
	}, nil
}

// UpdateBucket rewrites a bucket's limits.
func (h *rpcHandler) UpdateBucket(ctx context.Context, req *storagev1.UpdateBucketRequest) (*storagev1.UpdateBucketResponse, error) {
	actor, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	params := UpdateParams{
		FileSizeLimit: req.FileSizeLimit,
	}
	if req.AllowedMimeTypes != nil {
		params.AllowedMimeTypes = &req.AllowedMimeTypes.Types
	}
	updated, err := h.service.Update(ctx, actor, req.Name, params)
	if err != nil {
		return nil, mapError(err)
	}
	return &storagev1.UpdateBucketResponse{
		Bucket:  wireBucket(updated),
		Status:  webutil.StatusSuccess,
		Message: "the bucket was updated",
	}, nil
}

// DeleteBucket removes an empty bucket.
func (h *rpcHandler) DeleteBucket(ctx context.Context, req *storagev1.DeleteBucketRequest) (*storagev1.DeleteBucketResponse, error) {
	actor, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	if err := h.service.Delete(ctx, actor, req.Name); err != nil {
		return nil, mapError(err)
	}
	return &storagev1.DeleteBucketResponse{
		Status:  webutil.StatusSuccess,
		Message: "the bucket was deleted",
	}, nil
}

// ListBuckets answers every bucket the deployment holds.
func (h *rpcHandler) ListBuckets(ctx context.Context, req *storagev1.ListBucketsRequest) (*storagev1.ListBucketsResponse, error) {
	buckets, err := h.service.List(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	return &storagev1.ListBucketsResponse{
		Buckets: wireBuckets(buckets),
		Status:  webutil.StatusSuccess,
		Message: "the buckets were listed",
	}, nil
}

// GetBucket answers one bucket by name.
func (h *rpcHandler) GetBucket(ctx context.Context, req *storagev1.GetBucketRequest) (*storagev1.GetBucketResponse, error) {
	bucket, err := h.service.Get(ctx, req.Name)
	if err != nil {
		return nil, mapError(err)
	}
	return &storagev1.GetBucketResponse{
		Bucket:  wireBucket(bucket),
		Status:  webutil.StatusSuccess,
		Message: "the bucket was read",
	}, nil
}

// wireBucket maps the stored row onto the wire message. A NULL limit column
// is the unlimited state, so its wire field stays absent; the identifier
// leaves in the wire form its TypeID prefix spells.
func wireBucket(row BucketSchema) *storagev1.Bucket {
	bucket := &storagev1.Bucket{
		Id:               FormatID(row.ID),
		Name:             row.Name,
		AllowedMimeTypes: row.AllowedMimeTypes,
		CreatedAt:        timestamppb.New(row.CreatedAt),
	}
	if bucket.AllowedMimeTypes == nil {
		bucket.AllowedMimeTypes = []string{}
	}
	if row.FileSizeLimit != nil {
		bucket.FileSizeLimit = row.FileSizeLimit
	}
	bucket.UpdatedAt = timestamppb.New(row.UpdatedAt)
	return bucket
}

// wireBuckets maps every row.
func wireBuckets(rows []BucketSchema) []*storagev1.Bucket {
	buckets := make([]*storagev1.Bucket, 0, len(rows))
	for _, row := range rows {
		buckets = append(buckets, wireBucket(row))
	}
	return buckets
}

// callerID reads the caller the transport's middleware resolved. The
// guard's admin rule has already refused an unauthenticated caller, so
// reaching here means the claims name an account; a missing caller is the
// wiring defect it always is, and it is refused rather than dereferenced.
// The claims carry the wire form — the TypeID — and the audit record names
// the account by the UUID its column stores, so the boundary is here.
func callerID(ctx context.Context) (string, error) {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok || caller == nil {
		return "", connect.NewError(connect.CodeInternal, "authentication state missing")
	}
	id, err := user.UUIDFromWire(caller.UserID)
	if err != nil {
		return "", connect.NewError(connect.CodeInternal, "authentication state missing")
	}
	return id.String(), nil
}

// mapError translates the service's failures into the codes the Connect
// protocol carries. The internal ones are collapsed to one answer whose
// text names nothing a caller could aim at.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrBucketNotFound):
		return connect.NewError(connect.CodeNotFound, "bucket not found")
	case errors.Is(err, ErrBucketExists):
		return connect.NewError(connect.CodeAlreadyExists, "bucket name already in use")
	case errors.Is(err, ErrBucketNotEmpty):
		return connect.NewError(connect.CodeFailedPrecondition, "the bucket is not empty")
	case errors.Is(err, ErrBucketIsDefault):
		return connect.NewError(connect.CodeFailedPrecondition, "the bucket is the default bucket")
	case errors.Is(err, ErrInvalidBucketName):
		return connect.NewError(connect.CodeInvalidArgument, "invalid bucket name")
	default:
		return connect.NewError(connect.CodeInternal, "bucket operation failed")
	}
}
