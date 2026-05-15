package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

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

// Storage abstracts object storage (S3 / MinIO).
type Storage interface {
	UploadFile(ctx context.Context, key string, data []byte, contentType string) error
	DownloadFile(ctx context.Context, key string) ([]byte, error)
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
