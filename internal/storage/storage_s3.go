package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	awshttp "github.com/aws/smithy-go/transport/http"

	"github.com/riipandi/tango/internal/config"
)

// S3 is the object-store backend: every key is one object under the
// configured path prefix, at the same path its key spells. Any service
// speaking the S3 protocol answers — the endpoint, the path style, and the
// credentials are the configuration's, so AWS, MinIO, and Silo need no code
// between them.
type S3 struct {
	client *s3.Client
	bucket string
	prefix string
}

// NewS3 builds the backend from the storage.s3 settings. The client is
// opened here because it names one bucket the driver alone reads: unlike
// the shared Valkey client, no second feature ever borrows it.
func NewS3(cfg config.S3) (*S3, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKey, cfg.SecretKey,
			"", // session token: a long-lived deployment key has none
		)),
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("storage: s3 client: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.EndpointURL != "" {
			o.BaseEndpoint = aws.String(cfg.EndpointURL)
		}
		o.UsePathStyle = cfg.ForcePathStyle
	})
	return &S3{
		client: client,
		bucket: cfg.BucketName,
		prefix: strings.TrimPrefix(cfg.PathPrefix, "/"),
	}, nil
}

// Get opens the key's object. The caller closes the body; the stream reads
// the object on demand, so no whole-file buffer exists on the read path.
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.path(key)),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, fmt.Errorf("storage: file %s: %w", key, ErrNotFound)
		}
		return nil, fmt.Errorf("storage: get file %s: %w", key, err)
	}
	return out.Body, nil
}

// Put stores the object whole — one PUT, whose ceiling (5 GiB on the
// services this targets) is a deployment ceiling, not a code path. The
// content type rides with the object, so a direct read — a presigned URL,
// a console preview — answers what the bytes are. A failed PUT leaves
// nothing behind: the object store arrives whole or not at all, the
// property the whole-file design leans on.
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(s.path(key)),
		Body:          r,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("storage: put file %s: %w", key, err)
	}
	return nil
}

// Delete removes the key's object. An object already gone is the state the
// caller asked for, not an error.
func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.path(key)),
	})
	if err != nil {
		return fmt.Errorf("storage: delete file %s: %w", key, err)
	}
	return nil
}

// List calls fn for every key the configured prefix holds. The prefix is
// the bucket-side namespace the deployment chose; the keys that come back
// are the engine's own, the prefix stripped.
func (s *S3) List(ctx context.Context, fn func(key string) error) error {
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(s.prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("storage: list files: %w", err)
		}
		for _, object := range page.Contents {
			if key, ok := s.objectKey(aws.ToString(object.Key)); ok {
				if err := fn(key); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// path is the object address a key maps to: the configured prefix, then the
// key itself.
func (s *S3) path(key string) string {
	return s.prefix + key
}

// objectKey is the inverse of path: a listed object name the prefix owns is
// one of the engine's keys, anything else — the prefix's own directory
// entry, a foreign object — is skipped.
func (s *S3) objectKey(name string) (string, bool) {
	key := strings.TrimPrefix(name, s.prefix)
	if key == "" || strings.HasSuffix(key, "/") {
		return "", false
	}
	return key, true
}

// isS3NotFound reads the error an object store answers a missing object
// with: the 404 status the transport wraps, or the error codes the API
// names the absence with.
func isS3NotFound(err error) bool {
	var httpErr *awshttp.ResponseError
	if errors.As(err, &httpErr) && httpErr.HTTPStatusCode() == 404 {
		return true
	}
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) &&
		(apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound")
}
