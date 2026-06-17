// Package idempotency implements the Idempotency-Key in-flight lock middleware
// described in API.md §8.
//
// Behavior:
//   - Only inspects requests carrying an `Idempotency-Key` header.
//   - Only acts on mutating RPC paths (same Get/List/Search heuristic the auth
//     middleware uses for CSRF).
//   - On match: writes a 60-second TTL marker on `IDEMPOTENCY#<key>` via a
//     conditional PutItem. A concurrent retry that hits the same key sees
//     ErrIdempotencyInFlight and is rejected with HTTP 409 + Connect
//     `already_exists` shape.
//   - On lock acquisition the request flows to the handler. The marker
//     expires naturally — no second write on success.
//
// Keys must parse as UUIDs (we don't strictly require v7; client conformance
// is checked via the lint rule on the portal side). Malformed keys are
// rejected with 400.
package idempotency

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/store"
)

// HeaderName is the canonical request header. Lowercased for net/http's
// case-insensitive lookup; Connect clients always send `Idempotency-Key`.
const HeaderName = "Idempotency-Key"

// Locker is the minimal interface the middleware needs. *store.Client
// satisfies it; tests can pass a fake.
type Locker interface {
	AcquireIdempotencyLock(ctx context.Context, key, userID string) error
}

// New returns the middleware.
func New(locker Locker, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(HeaderName)
			if key == "" || !isMutating(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if _, err := uuid.Parse(key); err != nil {
				writeErr(w, http.StatusBadRequest, "invalid_argument", "Idempotency-Key must be a UUID")
				return
			}
			// Take the caller userId when present — useful for forensics on
			// the lock row. Anonymous mutating paths (the only one in v1 is
			// AuthService.Exchange) get an empty userId.
			var userID string
			if caller, ok := auth.FromContext(r.Context()); ok {
				userID = caller.UserID
			}
			if err := locker.AcquireIdempotencyLock(r.Context(), key, userID); err != nil {
				if errors.Is(err, store.ErrIdempotencyInFlight) {
					writeErr(w, http.StatusConflict, "already_exists", "duplicate request in flight")
					return
				}
				logger.Error("idempotency: acquire lock", "err", err, "key", key)
				writeErr(w, http.StatusServiceUnavailable, "unavailable", "store unavailable")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isMutating mirrors auth.isMutating: Connect uses POST for everything, so we
// classify by the trailing method name. Get/List/Search are reads.
func isMutating(p string) bool {
	slash := strings.LastIndex(p, "/")
	if slash < 0 || slash == len(p)-1 {
		return false
	}
	method := p[slash+1:]
	if strings.HasPrefix(method, "Get") || strings.HasPrefix(method, "List") || strings.HasPrefix(method, "Search") {
		return false
	}
	// Also skip plain HTTP shims (no Connect service prefix → not a mutating
	// RPC even if not Get/List). The idempotency contract is RPC-scoped.
	if !strings.HasPrefix(p, "/numun.v1.") {
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"code":"` + code + `","message":"` + msg + `"}`))
}
