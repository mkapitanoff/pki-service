package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"
	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested object does not exist.
var ErrNotFound = errors.New("storage: object not found")

// ObjectInfo describes a single object returned by ListObjectKeys.
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// Storage abstracts object storage (S3 / MinIO).
type Storage interface {
	UploadFile(ctx context.Context, key string, data []byte, contentType string) error
	DownloadFile(ctx context.Context, key string) ([]byte, error)
	DeleteFile(ctx context.Context, key string) error
	ListObjectKeys(ctx context.Context, prefix string) ([]ObjectInfo, error)
	BuildKey(tenantID, documentID uuid.UUID, filename string) string
}

// StorageConfig configures the S3 client. Populated from config.Config by the
// caller; this package does not read config or env directly.
type StorageConfig struct {
	Endpoint     string
	Region       string
	Bucket       string
	AccessKey    string
	SecretKey    string
	UsePathStyle bool
}

// S3Client is a Storage backed by aws-sdk-go-v2 (S3 or MinIO).
type S3Client struct {
	client *s3.Client
	bucket string
}

var _ Storage = (*S3Client)(nil)

func New(cfg StorageConfig) (*S3Client, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("storage: bucket is required")
	}

	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.UsePathStyle
	})

	return &S3Client{client: client, bucket: cfg.Bucket}, nil
}

func (s *S3Client) BuildKey(tenantID, documentID uuid.UUID, filename string) string {
	return fmt.Sprintf("%s/%s/%s", tenantID, documentID, filename)
}

func (s *S3Client) UploadFile(ctx context.Context, key string, data []byte, contentType string) error {
	in := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	if _, err := s.client.PutObject(ctx, in); err != nil {
		return fmt.Errorf("storage: put %q: %w", key, err)
	}
	return nil
}

func (s *S3Client) DownloadFile(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNoSuchKey(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: get %q: %w", key, err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("storage: read body %q: %w", key, err)
	}
	return data, nil
}

// EnsureBuckets creates each bucket if it does not already exist.
// Safe to call on every startup — ignores BucketAlreadyOwnedByYou / BucketAlreadyExists.
func (s *S3Client) EnsureBuckets(ctx context.Context, buckets ...string) error {
	for _, bucket := range buckets {
		_, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket: aws.String(bucket),
		})
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) {
				code := apiErr.ErrorCode()
				if code == "BucketAlreadyOwnedByYou" || code == "BucketAlreadyExists" {
					continue
				}
			}
			return fmt.Errorf("storage: create bucket %q: %w", bucket, err)
		}
		log.Printf("storage: created bucket %q", bucket)
	}
	return nil
}

// PingBucket returns nil if the bucket exists and is accessible, an error otherwise.
func (s *S3Client) PingBucket(ctx context.Context, bucket string) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return fmt.Errorf("storage: ping bucket %q: %w", bucket, err)
	}
	return nil
}

func (s *S3Client) DeleteFile(ctx context.Context, key string) error {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}); err != nil {
		return fmt.Errorf("storage: delete %q: %w", key, err)
	}
	return nil
}

func (s *S3Client) ListObjectKeys(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	var results []ObjectInfo
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("storage: list %q: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			info := ObjectInfo{Key: aws.ToString(obj.Key)}
			if obj.Size != nil {
				info.Size = *obj.Size
			}
			if obj.LastModified != nil {
				info.LastModified = *obj.LastModified
			}
			results = append(results, info)
		}
	}
	return results, nil
}

func isNoSuchKey(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var nf *types.NotFound
	if errors.As(err, &nf) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == "NoSuchKey" || code == "NotFound"
	}
	return false
}
