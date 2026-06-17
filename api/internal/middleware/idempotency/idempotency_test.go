package idempotency

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/numun/numun/api/internal/store"
)

type fakeLocker struct {
	acquired map[string]string // key → userID
	failNext bool              // if set, return a generic error once
}

func (f *fakeLocker) AcquireIdempotencyLock(_ context.Context, key, userID string) error {
	if f.failNext {
		f.failNext = false
		return errors.New("ddb boom")
	}
	if f.acquired == nil {
		f.acquired = map[string]string{}
	}
	if _, exists := f.acquired[key]; exists {
		return store.ErrIdempotencyInFlight
	}
	f.acquired[key] = userID
	return nil
}

func TestSkipsWithoutHeader(t *testing.T) {
	f := &fakeLocker{}
	h := New(f, nil)(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/numun.v1.DelegationService/CreateDelegation", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if len(f.acquired) != 0 {
		t.Fatalf("expected no lock acquired, got %v", f.acquired)
	}
}

func TestSkipsReadRPCs(t *testing.T) {
	f := &fakeLocker{}
	h := New(f, nil)(okHandler())
	for _, path := range []string{
		"/numun.v1.DelegationService/GetDelegation",
		"/numun.v1.DelegationService/ListDelegations",
		"/numun.v1.UserService/SearchUsers",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set(HeaderName, uuid.NewString())
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("path %s: want 200, got %d", path, rr.Code)
		}
	}
	if len(f.acquired) != 0 {
		t.Fatalf("expected no lock acquired for reads, got %v", f.acquired)
	}
}

func TestSkipsNonConnectRoutes(t *testing.T) {
	f := &fakeLocker{}
	h := New(f, nil)(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/v1/email/unsubscribe", nil)
	req.Header.Set(HeaderName, uuid.NewString())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("non-connect: want 200, got %d", rr.Code)
	}
	if len(f.acquired) != 0 {
		t.Fatalf("non-connect lock acquired: %v", f.acquired)
	}
}

func TestAcquiresLockOnMutatingRPC(t *testing.T) {
	f := &fakeLocker{}
	h := New(f, nil)(okHandler())
	key := uuid.NewString()
	req := httptest.NewRequest(http.MethodPost, "/numun.v1.DelegationService/CreateDelegation", nil)
	req.Header.Set(HeaderName, key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if _, ok := f.acquired[key]; !ok {
		t.Fatalf("expected lock for key %s", key)
	}
}

func TestRejectsDuplicate(t *testing.T) {
	f := &fakeLocker{}
	h := New(f, nil)(okHandler())
	key := uuid.NewString()

	// First call wins.
	req1 := httptest.NewRequest(http.MethodPost, "/numun.v1.DelegationService/CreateDelegation", nil)
	req1.Header.Set(HeaderName, key)
	rr1 := httptest.NewRecorder()
	h.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first: want 200, got %d", rr1.Code)
	}

	// Replay loses.
	req2 := httptest.NewRequest(http.MethodPost, "/numun.v1.DelegationService/CreateDelegation", nil)
	req2.Header.Set(HeaderName, key)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusConflict {
		t.Fatalf("second: want 409, got %d (body=%s)", rr2.Code, rr2.Body.String())
	}
}

func TestRejectsMalformedKey(t *testing.T) {
	f := &fakeLocker{}
	h := New(f, nil)(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/numun.v1.DelegationService/CreateDelegation", nil)
	req.Header.Set(HeaderName, "not-a-uuid")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
	if len(f.acquired) != 0 {
		t.Fatalf("malformed key should not acquire lock")
	}
}

func TestPropagatesStoreError(t *testing.T) {
	f := &fakeLocker{failNext: true}
	h := New(f, nil)(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/numun.v1.DelegationService/CreateDelegation", nil)
	req.Header.Set(HeaderName, uuid.NewString())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rr.Code)
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}
