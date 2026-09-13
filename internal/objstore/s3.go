package objstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Config mirrors the S3-compatible object storage settings carried by
// internal/config.Config. It is a separate, minimal struct (rather than an
// import of that package) so internal/objstore has no dependency on the rest
// of the service and can be built and tested in isolation.
type Config struct {
	// Endpoint overrides the default AWS endpoint, e.g. "http://localhost:55900"
	// for a local MinIO instance. Empty means talk to real AWS S3.
	Endpoint string
	Region   string
	Bucket   string

	AccessKeyID string
	SecretKey   string

	// ForcePathStyle selects "http://endpoint/bucket/key" addressing instead
	// of virtual-hosted "http://bucket.endpoint/key" addressing. MinIO and
	// most S3-compatible services outside AWS require this.
	ForcePathStyle bool
}

type s3Store struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// New builds a Store backed by an S3-compatible service (AWS S3 or a
// self-hosted service such as MinIO). Credentials are static and explicit:
// this deliberately never falls back to ambient AWS credential discovery
// (environment variables, ~/.aws/credentials, EC2/ECS instance metadata),
// because that fallback would let a misconfigured process silently start
// talking to the wrong account with the wrong permissions instead of failing
// to start.
func New(cfg Config) (Store, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("objstore: Bucket is required")
	}
	if cfg.AccessKeyID == "" || cfg.SecretKey == "" {
		return nil, errors.New("objstore: AccessKeyID and SecretKey are required")
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	awsCfg := aws.Config{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretKey, ""),
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.ForcePathStyle
	})

	return &s3Store{
		client:  client,
		presign: s3.NewPresignClient(client),
		bucket:  cfg.Bucket,
	}, nil
}

func (s *s3Store) Put(ctx context.Context, key, contentType string, r io.Reader, size int64) error {
	if key == "" {
		return errors.New("objstore: key is required")
	}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          r,
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(size),
	})
	if err != nil {
		return fmt.Errorf("objstore: put %q: %w", key, err)
	}
	return nil
}

func (s *s3Store) Get(ctx context.Context, key string, rangeHeader string) (*Object, error) {
	in := &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}
	if rangeHeader != "" {
		in.Range = aws.String(rangeHeader)
	}
	out, err := s.client.GetObject(ctx, in)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotExist
		}
		return nil, fmt.Errorf("objstore: get %q: %w", key, err)
	}
	status := http.StatusOK
	obj := &Object{
		Body:          out.Body,
		ContentType:   aws.ToString(out.ContentType),
		ContentLength: aws.ToInt64(out.ContentLength),
		ETag:          strings.Trim(aws.ToString(out.ETag), `"`),
	}
	if out.ContentRange != nil {
		obj.ContentRange = aws.ToString(out.ContentRange)
		status = http.StatusPartialContent
	}
	obj.StatusCode = status
	return obj, nil
}

func (s *s3Store) Head(ctx context.Context, key string) (*Object, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotExist
		}
		return nil, fmt.Errorf("objstore: head %q: %w", key, err)
	}
	return &Object{
		ContentType:   aws.ToString(out.ContentType),
		ContentLength: aws.ToInt64(out.ContentLength),
		ETag:          strings.Trim(aws.ToString(out.ETag), `"`),
		StatusCode:    http.StatusOK,
	}, nil
}

// Delete removes keys in batches of up to 1000 (the S3 DeleteObjects limit).
// A key that does not exist is not reported as an error by S3's batch delete
// API, which is exactly the idempotent behavior cleanup jobs need.
func (s *s3Store) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	const batchSize = 1000
	for start := 0; start < len(keys); start += batchSize {
		end := min(start+batchSize, len(keys))
		chunk := keys[start:end]
		objs := make([]types.ObjectIdentifier, len(chunk))
		for i, k := range chunk {
			objs[i] = types.ObjectIdentifier{Key: aws.String(k)}
		}
		out, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(s.bucket),
			Delete: &types.Delete{Objects: objs, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return fmt.Errorf("objstore: delete objects: %w", err)
		}
		if len(out.Errors) > 0 {
			e := out.Errors[0]
			return fmt.Errorf("objstore: delete %q: %s", aws.ToString(e.Key), aws.ToString(e.Message))
		}
	}
	return nil
}

// PresignPut authorizes one client PUT to exactly one key. Binding the
// content type and content length into the signed request (rather than
// issuing a bare presigned URL) means a client cannot reuse the URL to write
// a different content type, and any attempt to send a body of a different
// declared length fails signature verification before the bytes are stored.
func (s *s3Store) PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (string, map[string]string, error) {
	if key == "" {
		return "", nil, errors.New("objstore: key is required")
	}
	req, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(size),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", nil, fmt.Errorf("objstore: presign put %q: %w", key, err)
	}
	headers := make(map[string]string, len(req.SignedHeader)+2)
	for k, v := range req.SignedHeader {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	// Belt and suspenders: the client must send exactly these headers for the
	// signature to verify, but state them explicitly so a caller building the
	// upload request never has to guess which headers were signed.
	headers["Content-Type"] = contentType
	headers["Content-Length"] = fmt.Sprintf("%d", size)
	return req.URL, headers, nil
}

func (s *s3Store) Healthy(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	if err != nil {
		return fmt.Errorf("objstore: bucket %q unhealthy: %w", s.bucket, err)
	}
	return nil
}

func isNotFound(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var nf *types.NotFound
	if errors.As(err, &nf) {
		return true
	}
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) && re.HTTPStatusCode() == http.StatusNotFound {
		return true
	}
	return false
}
