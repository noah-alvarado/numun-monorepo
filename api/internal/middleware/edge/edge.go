// Package edge holds the API's trust boundary with the CloudFront
// distribution in front of api.${Apex} (infra/api/template.yaml
// ApiDistribution). See SECURITY.md §3.6.
//
//   - VerifyOrigin rejects requests that didn't come through CloudFront.
//     CloudFront adds OriginVerifyHeader as an origin custom header
//     (overwriting any viewer-supplied value); a request that hits the raw
//     execute-api URL lacks it.
//
//   - ClientIP reads ViewerIPHeader, which the distribution's viewer-request
//     CloudFront Function overwrites with event.viewer.ip on every request,
//     so clients cannot spoof it the way they can X-Forwarded-For.
package edge

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
)

const (
	OriginVerifyHeader = "X-Origin-Verify"
	ViewerIPHeader     = "X-Numun-Viewer-Ip"
)

// VerifyOrigin returns middleware that checks OriginVerifyHeader against
// secret and then strips the header so it never reaches handlers, logs, or
// Sentry. On mismatch it 403s when enforce is true; otherwise it logs and lets
// the request through (report-only, used while rolling out). An empty secret
// disables the check (local dev, where there is no CloudFront).
func VerifyOrigin(secret string, enforce bool, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if secret == "" {
			return next
		}
		want := []byte(secret)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := r.Header.Get(OriginVerifyHeader)
			r.Header.Del(OriginVerifyHeader)
			if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
				logger.Warn("origin verify failed",
					"enforced", enforce,
					"header_present", got != "",
					"method", r.Method,
					"path", r.URL.Path,
					"ip", ClientIP(r.Header),
				)
				if enforce {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"code":"permission_denied","message":"direct origin access is not allowed"}`))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP returns the caller's IP, preferring the CloudFront-stamped
// ViewerIPHeader. The X-Forwarded-For / X-Real-Ip fallbacks only apply off
// CloudFront (local dev) and are client-controlled. Returns "" when unknown.
func ClientIP(h http.Header) string {
	if v := strings.TrimSpace(h.Get(ViewerIPHeader)); v != "" {
		return v
	}
	if xff := h.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	return strings.TrimSpace(h.Get("X-Real-Ip"))
}
