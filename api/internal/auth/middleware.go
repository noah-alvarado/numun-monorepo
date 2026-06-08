package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

const (
	SessionCookieName = "numun_session"
	CSRFCookieName    = "csrf_token"
	CSRFHeader        = "X-CSRF-Token"

	// DevUserIDHeader is the header consumed by the DEV_BYPASS_AUTH path. The
	// middleware refuses to honor it unless both DEV_MODE=true and
	// DEV_BYPASS_AUTH=true are set in the environment (defense in depth so a
	// misconfigured prod can't accidentally bypass auth — see plan §304).
	DevUserIDHeader = "X-Dev-User-Id"
)

// PublicPaths are Connect RPC paths that bypass the auth middleware entirely.
// Everything else requires a session. Match by exact path.
var publicPaths = map[string]struct{}{
	"/numun.v1.HealthService/Check":                      {},
	"/numun.v1.AuthService/Exchange":                     {},
	"/numun.v1.AuthService/RecordPasswordResetCompleted": {},
	// PublicService.* — added per-RPC when M3 lands; for now we leave the
	// public-service prefix as an explicit allowlist on the prefix below.
}

// Has the request path been listed as public? Also allows the plain
// `/v1/health` HTTP probe and the Decap CMS GitHub OAuth proxy routes
// (CMS_CONTENT_MODEL.md §8.3) — those are mounted on the same mux but live
// outside the Connect surface and have their own CSRF defense (HMAC-signed
// state cookie).
func isPublic(p string) bool {
	if _, ok := publicPaths[p]; ok {
		return true
	}
	switch p {
	case "/v1/health", "/":
		return true
	}
	if strings.HasPrefix(p, "/cms-oauth/") {
		return true
	}
	return strings.HasPrefix(p, "/numun.v1.PublicService/")
}

// Mutating returns whether the path is treated as a write call for CSRF
// purposes. Connect uses POST for everything, so we use a name-based heuristic:
// anything whose RPC name starts with Get/List is read; everything else is a
// write. Conservative: when in doubt, demand CSRF. Bypass paths skip this check.
func isMutating(p string) bool {
	// Path shape: /numun.v1.ServiceName/MethodName
	slash := strings.LastIndex(p, "/")
	if slash < 0 || slash == len(p)-1 {
		return true
	}
	method := p[slash+1:]
	if strings.HasPrefix(method, "Get") || strings.HasPrefix(method, "List") || strings.HasPrefix(method, "Search") {
		return false
	}
	return true
}

// MiddlewareConfig wires the dependencies the auth middleware needs.
type MiddlewareConfig struct {
	Store     *store.Client
	Cognito   *Cognito
	Logger    *slog.Logger
	DevMode   bool
	DevBypass bool
}

// New builds an http.Handler middleware that resolves the caller from the
// session cookie (or, in dev-bypass mode, the X-Dev-User-Id header) and
// attaches a Caller to the request context. Public paths are passed through
// untouched. Failure modes:
//
//   - Missing/invalid session → 401 (Connect maps to unauthenticated).
//   - CSRF mismatch on a mutating call → 403 (permission_denied).
//   - Refresh-token failure → 401, session deleted.
func New(cfg MiddlewareConfig) func(http.Handler) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	verifier := NewVerifier(cfg.cognitoRegion(), cfg.cognitoPool())
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if isPublic(path) {
				next.ServeHTTP(w, r)
				return
			}

			// DEV_BYPASS_AUTH path: synthesize a caller from X-Dev-User-Id.
			// Per plan §304: scope helpers and audit logs still run; CSRF is
			// skipped because there is no real cookie.
			if cfg.DevMode && cfg.DevBypass {
				if uid := r.Header.Get(DevUserIDHeader); uid != "" {
					user, err := cfg.Store.GetUser(r.Context(), uid)
					if err != nil {
						if errors.Is(err, store.ErrNotFound) {
							writeStatus(w, http.StatusUnauthorized, "dev bypass: user not found")
							return
						}
						cfg.Logger.Error("dev bypass: lookup user", "err", err)
						writeStatus(w, http.StatusInternalServerError, "internal error")
						return
					}
					ctx := WithCaller(r.Context(), Caller{
						UserID:    user.ID,
						Role:      user.Role,
						SessionID: "dev-bypass",
						DevBypass: true,
					})
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				// Fall through to the real session path if no header is set —
				// useful for tests that exercise the cookie flow even with
				// dev-bypass enabled.
			}

			cookie, err := r.Cookie(SessionCookieName)
			if err != nil || cookie.Value == "" {
				writeStatus(w, http.StatusUnauthorized, "missing session cookie")
				return
			}
			sess, err := cfg.Store.GetSession(r.Context(), cookie.Value)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					clearAuthCookies(w)
					writeStatus(w, http.StatusUnauthorized, "session not found")
					return
				}
				cfg.Logger.Error("session lookup", "err", err)
				writeStatus(w, http.StatusInternalServerError, "internal error")
				return
			}

			// Refresh the cached access token if expired.
			if time.Now().After(sess.CachedAccessTokenExpiresAt) {
				if cfg.Cognito == nil || cfg.Cognito.ClientID == "" {
					cfg.Logger.Error("session needs refresh but cognito is not configured")
					writeStatus(w, http.StatusInternalServerError, "cognito unavailable")
					return
				}
				at, expiresIn, refreshErr := cfg.Cognito.RefreshAccessToken(r.Context(), sess.RefreshToken)
				if refreshErr != nil {
					cfg.Logger.Warn("refresh token failed; revoking session", "err", refreshErr, "sessionId", sess.ID)
					_ = cfg.Store.DeleteSession(r.Context(), sess.ID)
					clearAuthCookies(w)
					writeStatus(w, http.StatusUnauthorized, "session invalid")
					return
				}
				newExpiry := time.Now().Add(time.Duration(expiresIn) * time.Second)
				if err := cfg.Store.TouchSession(r.Context(), sess.ID, at, newExpiry); err != nil {
					cfg.Logger.Warn("touch session", "err", err)
					// continue — request is still valid
				}
				sess.CachedAccessToken = at
				sess.CachedAccessTokenExpiresAt = newExpiry
			}

			// CSRF double-submit on mutating calls. Skipped for the dev-bypass
			// branch which already returned.
			if isMutating(path) {
				csrfCookie, err := r.Cookie(CSRFCookieName)
				header := r.Header.Get(CSRFHeader)
				if err != nil || csrfCookie.Value == "" || header == "" || header != csrfCookie.Value || header != sess.CSRFToken {
					writeStatus(w, http.StatusForbidden, "csrf mismatch")
					return
				}
			}

			user, err := cfg.Store.GetUser(r.Context(), sess.UserID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					// Session refers to a user that was deleted — kill the
					// session and force re-auth.
					_ = cfg.Store.DeleteSession(r.Context(), sess.ID)
					clearAuthCookies(w)
					writeStatus(w, http.StatusUnauthorized, "user not found")
					return
				}
				cfg.Logger.Error("user lookup", "err", err)
				writeStatus(w, http.StatusInternalServerError, "internal error")
				return
			}

			// Role mirror reconcile (AUTH.md §4.2) — if the verifier is enabled,
			// the access token's custom:role is authoritative. v1 simplification:
			// trust the DDB mirror except on Exchange. The mirror is updated by
			// Exchange and InviteStaff handlers.
			_ = verifier // referenced so the symbol stays meaningful; reserved for v1.1 active reconcile

			ctx := WithCaller(r.Context(), Caller{
				UserID:    user.ID,
				Role:      user.Role,
				SessionID: sess.ID,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeStatus(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Plain JSON body so non-Connect callers (browsers hitting /v1/* shims)
	// see a readable error. Connect clients parse the status code anyway.
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}

func clearAuthCookies(w http.ResponseWriter) {
	for _, name := range []string{SessionCookieName, CSRFCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			Domain:   cookieDomain(),
			MaxAge:   -1,
			Secure:   secureCookies(),
			HttpOnly: name == SessionCookieName,
			SameSite: http.SameSiteStrictMode,
		})
	}
}

// cookieDomain returns the cookie Domain attribute. In dev (`make dev`) we
// leave it empty so the cookie scopes to the request host; in prod it is
// `.numun.org` per AUTH.md §5.3.
func cookieDomain() string {
	if d := os.Getenv("COOKIE_DOMAIN"); d != "" {
		return d
	}
	return ""
}

func secureCookies() bool {
	// Defaults to true in prod; turn off locally where the dev API is plain HTTP.
	if v := os.Getenv("COOKIE_SECURE"); v != "" {
		return v == "true" || v == "1"
	}
	return os.Getenv("DEV_MODE") != "true"
}

func (c MiddlewareConfig) cognitoRegion() string {
	if c.Cognito != nil {
		return c.Cognito.Region
	}
	return os.Getenv("AWS_REGION")
}

func (c MiddlewareConfig) cognitoPool() string {
	if c.Cognito != nil {
		return c.Cognito.UserPoolID
	}
	return os.Getenv("COGNITO_USER_POOL_ID")
}

// CookieAttrs returns the Path/Domain/Secure/SameSite attributes the API uses
// when setting session cookies. Shared between the middleware and the
// AuthService handlers.
func CookieAttrs() (path, domain string, secure bool, sameSite http.SameSite) {
	return "/", cookieDomain(), secureCookies(), http.SameSiteStrictMode
}

// FormatUser is a small helper to project a domain.User into the canonical
// shape downstream handlers need to pass through. Kept here to avoid duplicate
// projection logic between Exchange and InviteStaff.
func FormatUser(u domain.User) domain.User { return u }
