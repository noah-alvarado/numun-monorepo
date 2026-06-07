package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

func testStore(t *testing.T) *store.Client {
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
		t.Fatalf("new store: %v", err)
	}
	return c
}

func TestPublicPathBypassesAuth(t *testing.T) {
	mw := auth.New(auth.MiddlewareConfig{})

	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(204)
	}))

	for _, path := range []string{
		"/v1/health",
		"/numun.v1.HealthService/Check",
		"/numun.v1.AuthService/Exchange",
		"/numun.v1.AuthService/RecordPasswordResetCompleted",
		"/numun.v1.PublicService/GetActiveConference",
	} {
		called = false
		req := httptest.NewRequest("POST", path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if !called {
			t.Errorf("expected handler called for public path %q (status=%d)", path, rec.Code)
		}
	}
}

func TestMissingSessionUnauthenticated(t *testing.T) {
	mw := auth.New(auth.MiddlewareConfig{Store: nil})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest("POST", "/numun.v1.UserService/GetMe", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestDevBypassPath(t *testing.T) {
	c := testStore(t)
	ctx := context.Background()

	uid := "00000000-0000-7000-8000-000000000abc"
	if _, err := c.CreateUser(ctx, domain.User{
		ID:    uid,
		Role:  domain.RoleAdvisor,
		Email: "dev-bypass-" + uid + "@test",
		Name:  "Dev Bypass",
	}); err != nil && err.Error() != "store: already exists" {
		t.Fatalf("create user: %v", err)
	}

	mw := auth.New(auth.MiddlewareConfig{
		Store:     c,
		DevMode:   true,
		DevBypass: true,
	})

	var seen auth.Caller
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, _ := auth.FromContext(r.Context())
		seen = caller
		w.WriteHeader(204)
	}))

	req := httptest.NewRequest("POST", "/numun.v1.UserService/GetMe", nil)
	req.Header.Set(auth.DevUserIDHeader, uid)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("want 204, got %d body=%q", rec.Code, rec.Body.String())
	}
	if !seen.DevBypass || seen.UserID != uid || seen.Role != domain.RoleAdvisor {
		t.Fatalf("unexpected caller: %+v", seen)
	}
}

func TestSessionHappyPath(t *testing.T) {
	c := testStore(t)
	ctx := context.Background()

	uid := "00000000-0000-7000-8000-000000000def"
	_, _ = c.CreateUser(ctx, domain.User{
		ID:    uid,
		Role:  domain.RoleAdvisor,
		Email: "session-" + uid + "@test",
		Name:  "Session User",
	})

	sessID := "11111111-1111-7111-8111-111111111111"
	csrf := "csrf-aaa-bbb"
	if err := c.PutSession(ctx, domain.Session{
		ID:                         sessID,
		UserID:                     uid,
		RefreshToken:               "rt",
		CachedAccessToken:          "at",
		CachedAccessTokenExpiresAt: time.Now().Add(time.Hour),
		CSRFToken:                  csrf,
		ExpiresAt:                  time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("put session: %v", err)
	}

	mw := auth.New(auth.MiddlewareConfig{Store: c})

	var seen auth.Caller
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, _ := auth.FromContext(r.Context())
		seen = caller
		w.WriteHeader(204)
	}))

	// Read RPC: no CSRF required.
	req := httptest.NewRequest("POST", "/numun.v1.UserService/GetMe", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessID})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("read RPC: want 204 got %d body=%q", rec.Code, rec.Body.String())
	}
	if seen.UserID != uid || seen.SessionID != sessID {
		t.Fatalf("unexpected caller: %+v", seen)
	}

	// Mutating RPC: CSRF required.
	req = httptest.NewRequest("POST", "/numun.v1.UserService/UpdateUser", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessID})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF: want 403, got %d", rec.Code)
	}

	// With CSRF cookie + header.
	req = httptest.NewRequest("POST", "/numun.v1.UserService/UpdateUser", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessID})
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
	req.Header.Set(auth.CSRFHeader, csrf)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("with CSRF: want 204, got %d body=%q", rec.Code, rec.Body.String())
	}

	// Mismatched CSRF.
	req = httptest.NewRequest("POST", "/numun.v1.UserService/UpdateUser", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessID})
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
	req.Header.Set(auth.CSRFHeader, "different")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("CSRF mismatch: want 403, got %d", rec.Code)
	}
}
