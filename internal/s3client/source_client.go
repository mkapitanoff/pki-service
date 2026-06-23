package s3client

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/mkapitanoff/pki-service/internal/storage"
)

// SourceS3Client делает HeadObject в произвольном S3-бакете (внешнем — там,
// куда Lovable заливает исходные PDF). Используется verification-воркером
// для прямой проверки x-amz-meta-sha256.
//
// Конфигурация — отдельная от основного storage-клиента: чтобы дать PKI
// read-only доступ к чужому бакету через выделенные креды/IAM-роль, не
// раскрывая прав на запись.
type SourceS3Client struct {
	client *s3.Client
}

// SourceS3Config описывает параметры подключения к внешнему S3.
type SourceS3Config struct {
	Endpoint     string
	Region       string
	AccessKey    string
	SecretKey    string
	UsePathStyle bool
}

func NewSourceS3Client(cfg SourceS3Config) (*SourceS3Client, error) {
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
		return nil, fmt.Errorf("source s3: load aws config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.UsePathStyle
	})
	return &SourceS3Client{client: client}, nil
}

// HeadObject — HEAD на bucket/key с возвратом нормализованных user-метаданных.
func (s *SourceS3Client) HeadObject(ctx context.Context, bucket, key string) (storage.HeadResult, error) {
	if bucket == "" || key == "" {
		return storage.HeadResult{}, fmt.Errorf("source s3: empty bucket or key")
	}
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return storage.HeadResult{}, fmt.Errorf("source s3: head %s/%s: %w", bucket, key, err)
	}
	res := storage.HeadResult{Metadata: make(map[string]string, len(out.Metadata))}
	for k, v := range out.Metadata {
		res.Metadata[strings.ToLower(k)] = v
	}
	if out.ContentType != nil {
		res.ContentType = *out.ContentType
	}
	if out.ContentLength != nil {
		res.Size = *out.ContentLength
	}
	if out.ETag != nil {
		res.ETag = *out.ETag
	}
	return res, nil
}

// MetaHashFetcher — интерфейс для verification-воркера (моки в тестах).
type MetaHashFetcher interface {
	HeadObject(ctx context.Context, bucket, key string) (storage.HeadResult, error)
}
