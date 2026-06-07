package store_test

// Integration tests against DDB Local. Skipped unless AWS_ENDPOINT_URL_DYNAMODB
// is set — keeps `go test ./...` cheap in CI without LocalStack/DDB Local.
//
// Run locally: `make dev` to bring containers up, then
// `AWS_ENDPOINT_URL_DYNAMODB=http://localhost:8000 AWS_REGION=us-east-2 \
//   AWS_ACCESS_KEY_ID=local AWS_SECRET_ACCESS_KEY=local go test ./internal/store/...`

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

func testClient(t *testing.T) *store.Client {
	t.Helper()
	if os.Getenv("AWS_ENDPOINT_URL_DYNAMODB") == "" {
		t.Skip("set AWS_ENDPOINT_URL_DYNAMODB to run DDB integration tests")
	}
	if os.Getenv("AWS_REGION") == "" {
		t.Setenv("AWS_REGION", "us-east-2")
	}
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		t.Setenv("AWS_ACCESS_KEY_ID", "local")
	}
	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		t.Setenv("AWS_SECRET_ACCESS_KEY", "local")
	}
	c, err := store.New(context.Background())
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

func newID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid v7: %v", err)
	}
	return id.String()
}

func TestUserCRUDOptimisticLock(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	u := domain.User{
		ID:    newID(t),
		Role:  domain.RoleAdvisor,
		Email: "advisor-" + newID(t)[:8] + "@example.com",
		Name:  "Test Advisor",
		Phone: "+15555550100",
	}

	created, err := c.CreateUser(ctx, u)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("want version=1 on create, got %d", created.Version)
	}

	// Duplicate create fails.
	if _, err := c.CreateUser(ctx, u); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("duplicate create: want ErrAlreadyExists, got %v", err)
	}

	got, err := c.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if got.Email != u.Email {
		t.Fatalf("email mismatch: want %q got %q", u.Email, got.Email)
	}

	newName := "Renamed Advisor"
	updated, err := c.UpdateUser(ctx, u.ID, 1, store.UpdateUserPatch{
		Name:      &newName,
		UpdatedBy: u.ID,
	})
	if err != nil {
		t.Fatalf("update user: %v", err)
	}
	if updated.Version != 2 || updated.Name != newName {
		t.Fatalf("after update: want version=2 name=%q, got version=%d name=%q",
			newName, updated.Version, updated.Name)
	}

	// Stale version triggers ErrVersionMismatch.
	if _, err := c.UpdateUser(ctx, u.ID, 1, store.UpdateUserPatch{Name: &newName}); !errors.Is(err, store.ErrVersionMismatch) {
		t.Fatalf("stale update: want ErrVersionMismatch, got %v", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	s := domain.Session{
		ID:                         newID(t),
		UserID:                     newID(t),
		RefreshToken:               "rt-abc",
		CachedAccessToken:          "at-abc",
		CachedAccessTokenExpiresAt: time.Now().Add(time.Hour).UTC(),
		CSRFToken:                  "csrf-abc",
		IP:                         "127.0.0.1",
		UserAgent:                  "test/1.0",
		ExpiresAt:                  time.Now().Add(24 * time.Hour).UTC(),
	}
	if err := c.PutSession(ctx, s); err != nil {
		t.Fatalf("put session: %v", err)
	}

	got, err := c.GetSession(ctx, s.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.UserID != s.UserID || got.CSRFToken != s.CSRFToken {
		t.Fatalf("session round-trip mismatch: got %+v", got)
	}

	if err := c.TouchSession(ctx, s.ID, "at-fresh", time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatalf("touch session: %v", err)
	}
	got, err = c.GetSession(ctx, s.ID)
	if err != nil {
		t.Fatalf("get session after touch: %v", err)
	}
	if got.CachedAccessToken != "at-fresh" {
		t.Fatalf("touch did not update cached access token: %q", got.CachedAccessToken)
	}

	if err := c.DeleteSession(ctx, s.ID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := c.GetSession(ctx, s.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
}

func TestExpiredSessionTreatedAsMissing(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	s := domain.Session{
		ID:        newID(t),
		UserID:    newID(t),
		ExpiresAt: time.Now().Add(-time.Hour).UTC(),
	}
	if err := c.PutSession(ctx, s); err != nil {
		t.Fatalf("put session: %v", err)
	}
	if _, err := c.GetSession(ctx, s.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound for expired session, got %v", err)
	}
}

func TestRecordAuthEvent(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	uid := newID(t)
	err := c.RecordAuthEvent(ctx, domain.AuthAuditEvent{
		UserID:    uid,
		Kind:      domain.AuthEventSignInSucceeded,
		IP:        "127.0.0.1",
		UserAgent: "test/1.0",
		Metadata:  map[string]string{"foo": "bar"},
	})
	if err != nil {
		t.Fatalf("record audit event: %v", err)
	}
}
