package storage

// Options is the storage engine's configuration. It is the caller's resolved
// values — this package reads nothing itself.
type Options struct {
	// Driver selects the backend: DriverFS (the local filesystem) or
	// DriverS3 (an S3-compatible object store).
	Driver string
	// LocalPath is the data directory the filesystem driver roots at.
	LocalPath string
	// S3 carries the object-store settings, used when Driver is DriverS3.
	S3 S3Options
	// Scope is the instrumentation scope name the meter carries. Empty
	// means this package's own word ("storage").
	Scope string
	// TelemetryNamespace prefixes the metric instrument names. Empty leaves
	// the domain name bare: the series name is the caller's identity
	// decision, not the framework's.
	TelemetryNamespace string
	// LinkPathPrefix is the mount the signed links compose against: the
	// path segment of a signed link whose base names no path of its own.
	// The transport must mount the serving handler there.
	LinkPathPrefix string
}

// S3Options carries the object-store connection the S3 driver runs on.
type S3Options struct {
	Region    string
	AccessKey string
	SecretKey string
	// EndpointURL is a non-AWS endpoint (MinIO, a compatible store). Empty
	// means the AWS defaults.
	EndpointURL string
	// ForcePathStyle addresses the bucket in the path rather than the
	// host, which is what everything but AWS needs.
	ForcePathStyle bool
}

// The drivers the engine ships. The values are the schema's own vocabulary.
const (
	DriverFS = "local"
	DriverS3 = "s3"
)

// defaultLinkPathPrefix is the mount a signer without an explicit prefix
// composes against.
const defaultLinkPathPrefix = "/storage"
