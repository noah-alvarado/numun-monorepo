package store_test

// Integration test for the bulk-import hourly counter. Skipped unless
// AWS_ENDPOINT_URL_DYNAMODB is set (matches store_test.go gating).

import (
	"context"
	"errors"
	"testing"

	"github.com/numun/numun/api/internal/store"
)

func TestIncrBulkImportHourlyCounter_PerKindCap(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	userID := "rl-" + newID(t)

	// 10 attempts at preview pass; 11th fails.
	for i := 0; i < 10; i++ {
		if err := c.IncrBulkImportHourlyCounter(ctx, userID, store.BulkImportRLPreview); err != nil {
			t.Fatalf("preview attempt %d: %v", i+1, err)
		}
	}
	if err := c.IncrBulkImportHourlyCounter(ctx, userID, store.BulkImportRLPreview); !errors.Is(err, store.ErrBulkImportRateLimitExceeded) {
		t.Fatalf("11th preview: want ErrBulkImportRateLimitExceeded, got %v", err)
	}

	// Commit kind is independent — fresh 10 budget.
	for i := 0; i < 10; i++ {
		if err := c.IncrBulkImportHourlyCounter(ctx, userID, store.BulkImportRLCommit); err != nil {
			t.Fatalf("commit attempt %d (after preview cap): %v", i+1, err)
		}
	}
	if err := c.IncrBulkImportHourlyCounter(ctx, userID, store.BulkImportRLCommit); !errors.Is(err, store.ErrBulkImportRateLimitExceeded) {
		t.Fatalf("11th commit: want ErrBulkImportRateLimitExceeded, got %v", err)
	}
}

func TestIncrBulkImportHourlyCounter_PerUserIndependent(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	userA := "rl-" + newID(t)
	userB := "rl-" + newID(t)

	for i := 0; i < 10; i++ {
		if err := c.IncrBulkImportHourlyCounter(ctx, userA, store.BulkImportRLPreview); err != nil {
			t.Fatalf("userA attempt %d: %v", i+1, err)
		}
	}
	// userA tripped — userB still fresh.
	if err := c.IncrBulkImportHourlyCounter(ctx, userB, store.BulkImportRLPreview); err != nil {
		t.Fatalf("userB first attempt: %v", err)
	}
}
