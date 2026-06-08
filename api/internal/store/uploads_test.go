package store_test

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/numun/numun/api/internal/store"
)

// uploadsClient brings up a store.Client wired to LocalStack S3 if available.
// Skips when AWS_ENDPOINT_URL_S3 is unset (mirrors testClient's policy).
func uploadsClient(t *testing.T) *store.Client {
	t.Helper()
	if os.Getenv("AWS_ENDPOINT_URL_S3") == "" {
		t.Skip("set AWS_ENDPOINT_URL_S3 to run S3 integration tests")
	}
	if os.Getenv("UPLOADS_BUCKET_NAME") == "" {
		t.Setenv("UPLOADS_BUCKET_NAME", "numun-test-uploads")
	}
	return testClient(t)
}

// keyShape matches the BULK_IMPORT.md §7.1 contract:
//
//	bulk-delegates/<userId>/<uuid>.{csv|xlsx}
//
// uuidv7 is a hex-with-dashes string, and userId comes from the caller. We
// accept any non-`/` userId so the regex can verify both LocalStack and unit
// inputs.
var keyShape = regexp.MustCompile(`^bulk-delegates/[^/]+/[0-9a-f-]+\.(csv|xlsx)$`)

func TestPresignBulkDelegatesUpload_KeyShape(t *testing.T) {
	c := uploadsClient(t)
	ctx := context.Background()

	cases := []struct {
		name        string
		contentType string
		ext         string
	}{
		{"csv", store.ContentTypeCSV, "csv"},
		{"xlsx", store.ContentTypeXLSX, "xlsx"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := c.PresignBulkDelegatesUpload(ctx, "user-abc", tc.contentType, 1024)
			if err != nil {
				t.Fatalf("presign: %v", err)
			}
			if !keyShape.MatchString(out.UploadKey) {
				t.Fatalf("key %q does not match expected shape", out.UploadKey)
			}
			if !strings.HasSuffix(out.UploadKey, "."+tc.ext) {
				t.Fatalf("key %q missing .%s extension", out.UploadKey, tc.ext)
			}
			if out.URL == "" {
				t.Fatal("expected non-empty URL")
			}
			if got := out.Headers["Content-Type"]; got != tc.contentType {
				t.Fatalf("Content-Type header = %q, want %q", got, tc.contentType)
			}
			if got := out.Headers["Content-Length"]; got != "1024" {
				t.Fatalf("Content-Length header = %q, want 1024", got)
			}
			if out.ExpiresAt.IsZero() {
				t.Fatal("ExpiresAt unset")
			}
		})
	}
}

func TestPresignBulkDelegatesUpload_RejectsUnsupportedContentType(t *testing.T) {
	c := uploadsClient(t)
	_, err := c.PresignBulkDelegatesUpload(context.Background(), "user-abc", "application/octet-stream", 1024)
	if !errors.Is(err, store.ErrUnsupportedContentType) {
		t.Fatalf("expected ErrUnsupportedContentType, got %v", err)
	}
}

func TestPresignBulkDelegatesUpload_RequiresBucketConfigured(t *testing.T) {
	// Standalone test that doesn't require LocalStack — uses a zero-value
	// store.Client (no S3, no bucket) to verify the no-config error path.
	c := &store.Client{}
	_, err := c.PresignBulkDelegatesUpload(context.Background(), "user-abc", store.ContentTypeCSV, 1024)
	if !errors.Is(err, store.ErrUploadsBucketNotConfigured) {
		t.Fatalf("expected ErrUploadsBucketNotConfigured, got %v", err)
	}
}
