package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type R2Config struct {
	Endpoint      string
	Bucket        string
	AccessKeyID   string
	SecretKey     string
	PublicBaseURL string
}

type R2Store struct {
	client        *s3.Client
	bucket        string
	publicBaseURL string
}

func NewR2Store(ctx context.Context, cfg R2Config) (*R2Store, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKeyID == "" || cfg.SecretKey == "" || cfg.PublicBaseURL == "" {
		return nil, fmt.Errorf("incomplete r2 configuration")
	}
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("auto"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretKey, "")),
	)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.UsePathStyle = true
	})
	return &R2Store{
		client:        client,
		bucket:        cfg.Bucket,
		publicBaseURL: strings.TrimRight(cfg.PublicBaseURL, "/"),
	}, nil
}

func (s *R2Store) Put(ctx context.Context, key string, contentType string, data []byte) (string, error) {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        readSeek(data),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", err
	}
	return s.publicBaseURL + "/" + key, nil
}

func (s *R2Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err
}

func (s *R2Store) KeyFromURL(rawURL string) (string, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	base, err := url.Parse(s.publicBaseURL)
	if err != nil {
		return "", false
	}
	if parsed.Host != base.Host {
		return "", false
	}
	key := strings.TrimPrefix(parsed.Path, strings.TrimRight(base.Path, "/")+"/")
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") {
		return "", false
	}
	return key, true
}
