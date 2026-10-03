package testutils

import (
	"context"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
)

// MinIO is a running S3-compatible object store container.
type MinIO struct {
	// Endpoint is the S3 API address (http://host:port).
	Endpoint string

	// AccessKey and Secret are the root credentials.
	AccessKey string
	Secret    string
}

// Client builds a path-style S3 client over the container's endpoint, the
// same shape the storage driver addresses it with — a test's raw reads and
// listings speak the protocol, not the engine.
func (m *MinIO) Client(ctx context.Context) (*s3.Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			m.AccessKey, m.Secret, "")),
	)
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(m.Endpoint)
		o.UsePathStyle = true
	}), nil
}

// minioImage is the S3-compatible object store used by integration tests.
// pgsty/silo is a drop-in MinIO replacement.
const minioImage = "docker.io/pgsty/silo:latest"

var (
	minioOnce   sync.Once
	sharedMinio *MinIO
	minioErr    error
)

// StartMinIO returns the shared object store container for the test binary.
func StartMinIO(ctx context.Context, t testing.TB) *MinIO {
	t.Helper()

	SkipWithoutDocker(t)

	minioOnce.Do(func() {
		container, startErr := tcminio.Run(ctx, minioImage,
			tcminio.WithUsername("minioadmin"),
			tcminio.WithPassword("minioadmin"),
		)
		if startErr != nil {
			minioErr = startErr
			return
		}
		endpoint, endpointErr := container.ConnectionString(ctx)
		if endpointErr != nil {
			minioErr = endpointErr
			return
		}
		// ConnectionString is host:port; consumers expect a full HTTP URL.
		sharedMinio = &MinIO{Endpoint: "http://" + endpoint, AccessKey: "minioadmin", Secret: "minioadmin"}
	})

	require.NoError(t, minioErr, "start minio container (docker daemon required)")
	return sharedMinio
}
