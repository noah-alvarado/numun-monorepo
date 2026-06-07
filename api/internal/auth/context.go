// Package auth implements the authentication middleware, scope helpers, and
// the Cognito-facing client used by AuthService.
package auth

import (
	"context"

	"github.com/numun/numun/api/internal/domain"
)

// Caller carries the authenticated subject through the request lifecycle. The
// middleware attaches one to the request context; handlers read it via
// FromContext.
type Caller struct {
	UserID    string
	Role      domain.Role
	SessionID string
	// DevBypass indicates the caller is synthesized by the DEV_BYPASS_AUTH path
	// rather than coming from a real Cognito session. Used by middleware to
	// skip CSRF (no real cookie) but NOT to skip scope helpers or audit logs.
	DevBypass bool
}

type callerKey struct{}

// WithCaller returns a child context carrying the given Caller. Used by the
// middleware and by test helpers.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// FromContext returns the Caller previously attached by middleware. The second
// return is false when the request is unauthenticated — handlers that reach
// this point without a caller are a programming error (the middleware should
// have rejected the request) and should return CodeInternal.
func FromContext(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}
