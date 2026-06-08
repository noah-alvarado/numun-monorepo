package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

// PresignedUpload is the storage-layer return shape for an S3 PUT presign.
// URL is the presigned URL; UploadKey is the S3 object key (relative to the
// uploads bucket) that the handler echoes back to the portal; Headers are the
// request headers the portal MUST send verbatim or signature validation will
// fail; ExpiresAt is the absolute expiry instant.
type PresignedUpload struct {
	URL       string
	UploadKey string
	Headers   map[string]string
	ExpiresAt time.Time
}

// bulkDelegatesUploadTTL is the presign expiry per BULK_IMPORT.md §7.1.
const bulkDelegatesUploadTTL = 10 * time.Minute

// bulkDelegatesMaxBytes is the per-file ceiling per BULK_IMPORT.md §2.2.
const bulkDelegatesMaxBytes int64 = 5 * 1024 * 1024

// Allowed Content-Type values for bulk-delegate uploads. csv → .csv,
// xlsx → .xlsx; anything else is rejected at presign time.
const (
	ContentTypeCSV  = "text/csv"
	ContentTypeXLSX = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
)

// ErrUploadsBucketNotConfigured is returned when the storage layer is asked
// to presign without UPLOADS_BUCKET_NAME having been set at startup.
var ErrUploadsBucketNotConfigured = errors.New("store: UPLOADS_BUCKET_NAME not configured")

// ErrUnsupportedContentType is returned when the caller passes a Content-Type
// the bulk-delegates upload path doesn't allow.
var ErrUnsupportedContentType = errors.New("store: unsupported content type")

// PresignBulkDelegatesUpload generates a 10-minute PUT presign for the
// numun-org-uploads bucket under bulk-delegates/<userId>/<uuidv7>.{csv|xlsx}.
//
// The Content-Length on the signed request binds the upload size to the
// signature: the portal must PUT with EXACTLY sizeBytes or S3 will refuse the
// PUT. Per-purpose size ceiling enforcement remains the handler's job; this
// helper assumes sizeBytes has already been bounded.
//
// Per SECURITY.md §2.8 the generated key is checked for traversal sequences
// before return.
func (c *Client) PresignBulkDelegatesUpload(ctx context.Context, userID string, contentType string, sizeBytes int64) (PresignedUpload, error) {
	if c.S3 == nil || c.UploadsBucket == "" {
		return PresignedUpload{}, ErrUploadsBucketNotConfigured
	}
	if userID == "" {
		return PresignedUpload{}, fmt.Errorf("store: userID required")
	}

	ext, ok := extensionForContentType(contentType)
	if !ok {
		return PresignedUpload{}, fmt.Errorf("%w: %q", ErrUnsupportedContentType, contentType)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return PresignedUpload{}, fmt.Errorf("uuid v7: %w", err)
	}
	key := fmt.Sprintf("bulk-delegates/%s/%s.%s", userID, id.String(), ext)
	if !isSafeKey(key) {
		return PresignedUpload{}, fmt.Errorf("store: generated key %q failed traversal check", key)
	}

	ps := s3.NewPresignClient(c.S3)
	// Setting ContentLength on the PutObject input causes the SDK to add
	// `Content-Length` to the signed-headers list, so any over- or
	// under-sized PUT from the portal will fail signature validation. See
	// SECURITY.md §2.7 for the rationale and BULK_IMPORT.md §7.1 for the
	// caller contract.
	req, err := ps.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(c.UploadsBucket),
		Key:           aws.String(key),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(sizeBytes),
	}, s3.WithPresignExpires(bulkDelegatesUploadTTL))
	if err != nil {
		return PresignedUpload{}, fmt.Errorf("presign put: %w", err)
	}

	return PresignedUpload{
		URL:       req.URL,
		UploadKey: key,
		Headers: map[string]string{
			"Content-Type":   contentType,
			"Content-Length": fmt.Sprintf("%d", sizeBytes),
		},
		ExpiresAt: time.Now().UTC().Add(bulkDelegatesUploadTTL),
	}, nil
}

// extensionForContentType maps the two accepted MIME types to file
// extensions. Returns false for anything else.
func extensionForContentType(ct string) (string, bool) {
	switch ct {
	case ContentTypeCSV:
		return "csv", true
	case ContentTypeXLSX:
		return "xlsx", true
	default:
		return "", false
	}
}

// GetUploadedObject fetches an object from the uploads bucket. The caller
// must validate that the key belongs to them (the key prefix carries the
// userId). Returns ErrNotFound when the object is missing.
func (c *Client) GetUploadedObject(ctx context.Context, key string) (io.ReadCloser, error) {
	if c.S3 == nil || c.UploadsBucket == "" {
		return nil, ErrUploadsBucketNotConfigured
	}
	out, err := c.S3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.UploadsBucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk interface{ ErrorCode() string }
		if errors.As(err, &nsk) && nsk.ErrorCode() == "NoSuchKey" {
			return nil, ErrNotFound
		}
		if strings.Contains(err.Error(), "NoSuchKey") || strings.Contains(err.Error(), "status code: 404") {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get uploaded object: %w", err)
	}
	return out.Body, nil
}

// UploadKeyBelongsTo returns true if the key prefix bulk-delegates/<userId>/
// matches the caller's userId. Used by the preview handler to enforce that
// users can only parse their own uploads.
func UploadKeyBelongsTo(key, userID string) bool {
	prefix := "bulk-delegates/" + userID + "/"
	return strings.HasPrefix(key, prefix)
}

// isSafeKey defends against path traversal in the generated key per
// SECURITY.md §2.8. The key shape is server-controlled so this is
// belt-and-suspenders; failing here means a bug in the key builder, not a
// caller mistake.
func isSafeKey(k string) bool {
	if k == "" {
		return false
	}
	for i := 0; i+1 < len(k); i++ {
		if k[i] == '.' && k[i+1] == '.' {
			return false
		}
	}
	return true
}
