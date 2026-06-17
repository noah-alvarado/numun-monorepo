// Package ratelimit implements the HTTP rate-limit middleware described in
// SECURITY.md §2.10.
//
// Two layers cooperate:
//
//   - PerIP wraps the outermost handler. It classifies the request path as
//     either an anonymous surface (PublicService, AuthService.Exchange, the
//     CMS OAuth proxy) — 60 req/min/IP — or an authenticated surface —
//     600 req/min/IP, intentionally loose because the conference venue NATs
//     every advisor and staffer through one egress IP and a tight per-IP cap
//     would false-trip at peak.
//
//   - PerUser is mounted *inside* the auth middleware so it sees the Caller
//     attached to the request context. 300 req/min/user, the primary cap on
//     authenticated misuse.
//
// Excess returns HTTP 429 with a small JSON body. Connect clients map 429 to
// `resource_exhausted` regardless of body shape.
//
// Backing store is in-memory per Lambda env (sync.Map of bucket-key →
// *rate.Limiter, swept on a goroutine). At our concurrency profile this is
// "approximate but adequate": a misbehaving client that fans out across cold
// envs is bounded by the API Gateway account-level throttle, which is what
// actually protects the wallet. See SECURITY.md §2.10 for the full rationale.
package ratelimit

import (
	"net/http"
	"strings"

	"github.com/numun/numun/api/internal/auth"
)

// Limits the per-minute caps the package applies. Exposed for tests + callers
// that want to tune them; defaults match SECURITY.md §2.10.
type Limits struct {
	PerUserPerMin        int
	PerIPAuthedPerMin    int
	PerIPAnonymousPerMin int
}

// DefaultLimits returns the SECURITY.md §2.10 values.
func DefaultLimits() Limits {
	return Limits{
		PerUserPerMin:        300,
		PerIPAuthedPerMin:    600,
		PerIPAnonymousPerMin: 60,
	}
}

// PerIP wraps the outermost handler and rejects when a single client IP
// exceeds the appropriate per-minute cap for the request path. Path
// classification: anonymous surfaces get the tight cap, everything else the
// loose defense-in-depth cap.
func PerIP(limits Limits) func(http.Handler) http.Handler {
	anon := newStorePerMin(limits.PerIPAnonymousPerMin)
	authed := newStorePerMin(limits.PerIPAuthedPerMin)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if ip == "" {
				// Couldn't identify the caller's IP. API Gateway always sets
				// X-Forwarded-For, so absence indicates a local dev call —
				// don't rate limit.
				next.ServeHTTP(w, r)
				return
			}
			store := authed
			if isAnonymousPath(r.URL.Path) {
				store = anon
			}
			if !store.allow(ip) {
				write429(w, "ip rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// PerUser is mounted inside the auth middleware. It reads the Caller from the
// request context and enforces the per-user cap. Unauthenticated requests
// (no Caller on context) pass through — PerIP is responsible for them.
func PerUser(limits Limits) func(http.Handler) http.Handler {
	store := newStorePerMin(limits.PerUserPerMin)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller, ok := auth.FromContext(r.Context())
			if !ok || caller.UserID == "" {
				next.ServeHTTP(w, r)
				return
			}
			if !store.allow(caller.UserID) {
				write429(w, "user rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isAnonymousPath classifies a path as one of the anonymous-surface buckets.
// Kept in sync with auth.isPublic but here we only care about whether to
// apply the tight or loose per-IP cap, not whether auth runs.
func isAnonymousPath(p string) bool {
	switch p {
	case "/v1/health", "/v1/email/unsubscribe":
		return true
	}
	if strings.HasPrefix(p, "/numun.v1.PublicService/") {
		return true
	}
	if strings.HasPrefix(p, "/cms-oauth/") {
		return true
	}
	if p == "/numun.v1.AuthService/Exchange" {
		return true
	}
	return false
}

// clientIP extracts the caller's IP. API Gateway HTTP API sets
// X-Forwarded-For with the client first; we take the first hop.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if comma := strings.IndexByte(xff, ','); comma > 0 {
			return strings.TrimSpace(xff[:comma])
		}
		return strings.TrimSpace(xff)
	}
	if v := r.Header.Get("X-Real-Ip"); v != "" {
		return v
	}
	return ""
}

func write429(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "60")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"code":"resource_exhausted","message":"` + msg + `"}`))
}
