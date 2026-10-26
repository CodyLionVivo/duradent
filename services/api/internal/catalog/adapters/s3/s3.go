// Package s3 implements catalog media storage on S3. Browsers upload images
// straight to the bucket with presigned PUT URLs; the store serves them from
// CloudFront (MEDIA_BASE_URL). Locally LocalStack plays both roles.
package s3

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/ports"
)

type Storage struct {
	presign *awss3.PresignClient
	bucket  string
	baseURL string
}

type Options struct {
	Bucket string
	// PresignEndpoint overrides the endpoint written into upload URLs. Locally
	// the API reaches LocalStack as http://localstack:4566 but the browser
	// needs http://localhost:4566. Empty in AWS.
	PresignEndpoint string
	// BaseURL is the public prefix objects are served from (CloudFront).
	BaseURL string
}

func New(cfg aws.Config, opts Options) *Storage {
	client := awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		if opts.PresignEndpoint != "" {
			o.BaseEndpoint = aws.String(opts.PresignEndpoint)
			o.UsePathStyle = true
		}
	})
	return &Storage{
		presign: awss3.NewPresignClient(client),
		bucket:  opts.Bucket,
		baseURL: strings.TrimSuffix(opts.BaseURL, "/"),
	}
}

var _ ports.MediaStorage = (*Storage)(nil)

func (s *Storage) PresignUpload(ctx context.Context, key, contentType string, ttl time.Duration) (ports.Upload, error) {
	req, err := s.presign.PresignPutObject(ctx, &awss3.PutObjectInput{
		Bucket:       aws.String(s.bucket),
		Key:          aws.String(key),
		ContentType:  aws.String(contentType),
		CacheControl: aws.String("public, max-age=31536000, immutable"), // keys are unique per upload
	}, awss3.WithPresignExpires(ttl))
	if err != nil {
		return ports.Upload{}, fmt.Errorf("presign upload: %w", err)
	}
	headers := map[string]string{}
	for k, v := range req.SignedHeader {
		if strings.EqualFold(k, "host") || len(v) == 0 {
			continue
		}
		headers[k] = v[0]
	}
	return ports.Upload{URL: req.URL, Method: req.Method, Headers: headers}, nil
}

func (s *Storage) PublicURL(key string) string {
	return s.baseURL + "/" + key
}
