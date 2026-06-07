package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	authv1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/store"
)

// AuthService implements numunv1connect.AuthServiceHandler. Unlike most
// services, AuthService methods need direct access to the outgoing HTTP
// response so they can set cookies — Connect exposes that via the response's
// trailer/header API.
//
// scope-check: skip
type AuthService struct {
	Store         *store.Client
	Cognito       *auth.Cognito
	Verifier      *auth.Verifier
	Logger        *slog.Logger
	SessionMaxAge time.Duration // defaults to 30d when zero
}

const defaultSessionTTL = 30 * 24 * time.Hour

// Exchange — see AUTH.md §4.2. Validates the ID token, writes a Session row,
// returns Set-Cookie headers for numun_session + csrf_token.
func (s *AuthService) Exchange(ctx context.Context, req *connect.Request[authv1.ExchangeRequest]) (*connect.Response[authv1.ExchangeResponse], error) {
	id := strings.TrimSpace(req.Msg.GetIdToken())
	at := strings.TrimSpace(req.Msg.GetAccessToken())
	rt := strings.TrimSpace(req.Msg.GetRefreshToken())
	if id == "" || at == "" || rt == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id_token, access_token, refresh_token required"))
	}

	// In dev-bypass mode the portal still POSTs Exchange — but Cognito tokens
	// aren't real. The seed-users flow stamps `dev-` prefixed values; we
	// accept them and synthesize claims from the access_token field (which
	// the dev shim sets to the seed user id). Real prod path uses the verifier.
	var claims auth.IDTokenClaims
	if isDevBypassActive() && strings.HasPrefix(id, "dev-") {
		claims = auth.IDTokenClaims{Sub: strings.TrimPrefix(at, "dev-")}
	} else {
		if s.Verifier == nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("token verifier not configured"))
		}
		expectedAud := ""
		if s.Cognito != nil {
			expectedAud = s.Cognito.ClientID
		}
		c, err := s.Verifier.VerifyIDToken(ctx, id, expectedAud)
		if err != nil {
			s.logger().Warn("Exchange: invalid id token", "err", err)
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid id token"))
		}
		claims = c
	}

	// Resolve / reconcile the User mirror.
	u, err := s.Store.GetUser(ctx, claims.Sub)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.logger().Error("Exchange: load user", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if errors.Is(err, store.ErrNotFound) {
		// Lazy User row creation. The Cognito post-confirmation Lambda is the
		// canonical writer (eliminating races on first authenticated call),
		// but this path catches first-time sign-ins from federated or
		// admin-created accounts whose trigger may not have fired.
		role := domain.RoleAdvisor
		if r := domain.Role(claims.Role); r == domain.RoleStaffAdmin || r == domain.RoleStaffStaffer {
			role = r
		}
		created, cerr := s.Store.CreateUser(ctx, domain.User{
			ID:    claims.Sub,
			Role:  role,
			Email: claims.Email,
			Name:  claims.Name,
		})
		if cerr != nil && !errors.Is(cerr, store.ErrAlreadyExists) {
			s.logger().Error("Exchange: lazy user create", "err", cerr)
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
		}
		if cerr == nil {
			u = created
		} else {
			u, _ = s.Store.GetUser(ctx, claims.Sub)
		}
	}

	// Role-mirror reconcile (AUTH.md §4.2): if Cognito's custom:role differs
	// from the DDB mirror, update DDB.
	if claims.Role != "" {
		tokenRole := domain.Role(claims.Role)
		if (tokenRole == domain.RoleAdvisor || tokenRole == domain.RoleStaffAdmin || tokenRole == domain.RoleStaffStaffer) && tokenRole != u.Role {
			r := tokenRole
			updated, uerr := s.Store.UpdateUser(ctx, u.ID, u.Version, store.UpdateUserPatch{
				Role:      &r,
				UpdatedBy: u.ID,
			})
			if uerr == nil {
				u = updated
				_ = s.Store.RecordAuthEvent(ctx, domain.AuthAuditEvent{
					UserID:      u.ID,
					ActorUserID: u.ID,
					Kind:        domain.AuthEventRoleChanged,
					Metadata:    map[string]string{"newRole": string(r)},
				})
			} else {
				s.logger().Warn("Exchange: role mirror update", "err", uerr)
			}
		}
	}

	// Mint session id + CSRF token.
	sid, err := uuid.NewV7()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("session id gen"))
	}
	csrf, err := randomToken(32)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("csrf gen"))
	}

	ttl := s.SessionMaxAge
	if ttl == 0 {
		ttl = defaultSessionTTL
	}
	expiresAt := time.Now().Add(ttl).UTC()
	accessExpiresAt := time.Now().Add(time.Duration(int64Or(req.Msg.GetExpiresIn(), 3600)) * time.Second).UTC()

	sess := domain.Session{
		ID:                         sid.String(),
		UserID:                     u.ID,
		RefreshToken:               req.Msg.GetRefreshToken(),
		CachedAccessToken:          req.Msg.GetAccessToken(),
		CachedAccessTokenExpiresAt: accessExpiresAt,
		CSRFToken:                  csrf,
		IP:                         clientIP(req.Header()),
		UserAgent:                  req.Header().Get("User-Agent"),
		ExpiresAt:                  expiresAt,
	}
	if err := s.Store.PutSession(ctx, sess); err != nil {
		s.logger().Error("Exchange: put session", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}

	resp := connect.NewResponse(&authv1.ExchangeResponse{})
	setSessionCookies(resp.Header(), sess.ID, csrf, ttl, req.Msg.GetRememberMe())

	_ = s.Store.RecordAuthEvent(ctx, domain.AuthAuditEvent{
		UserID:      u.ID,
		ActorUserID: u.ID,
		Kind:        domain.AuthEventSignInSucceeded,
		IP:          sess.IP,
		UserAgent:   sess.UserAgent,
	})

	return resp, nil
}

// Logout — see AUTH.md §8.
func (s *AuthService) Logout(ctx context.Context, req *connect.Request[authv1.LogoutRequest]) (*connect.Response[authv1.LogoutResponse], error) {
	c, ok := auth.FromContext(ctx)
	if !ok {
		// Logout is best-effort even without a caller — return success and
		// clear any stale cookies the client might still hold. But because
		// the middleware would have rejected an unauthenticated call before
		// reaching here, this is a programming-only error path.
		resp := connect.NewResponse(&authv1.LogoutResponse{})
		clearAuthCookies(resp.Header())
		return resp, nil
	}

	if c.SessionID != "" && c.SessionID != "dev-bypass" {
		sess, gerr := s.Store.GetSession(ctx, c.SessionID)
		if gerr == nil && s.Cognito != nil && s.Cognito.ClientID != "" && sess.RefreshToken != "" {
			if err := s.Cognito.RevokeRefreshToken(ctx, sess.RefreshToken); err != nil {
				s.logger().Warn("Logout: revoke refresh token", "err", err)
			}
		}
		if err := s.Store.DeleteSession(ctx, c.SessionID); err != nil {
			s.logger().Warn("Logout: delete session", "err", err)
		}
	}

	_ = s.Store.RecordAuthEvent(ctx, domain.AuthAuditEvent{
		UserID:      c.UserID,
		ActorUserID: c.UserID,
		Kind:        domain.AuthEventSignOut,
	})

	resp := connect.NewResponse(&authv1.LogoutResponse{})
	clearAuthCookies(resp.Header())
	return resp, nil
}

// RecordPasswordResetCompleted — unauthenticated endpoint the portal calls
// after a successful ConfirmForgotPassword. Resolves the user via Cognito
// (in dev-bypass: looks up by email-equals-User.Email in DDB) and writes
// the audit event. Resolves IMPLEMENTATION_PLAN.md ambiguity #9.
func (s *AuthService) RecordPasswordResetCompleted(ctx context.Context, req *connect.Request[authv1.RecordPasswordResetCompletedRequest]) (*connect.Response[authv1.RecordPasswordResetCompletedResponse], error) {
	email := strings.TrimSpace(strings.ToLower(req.Msg.GetEmail()))
	if email == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("email required"))
	}

	var sub string
	if s.Cognito != nil && s.Cognito.UserPoolID != "" {
		got, err := s.Cognito.LookupUserByEmail(ctx, email)
		if err != nil {
			s.logger().Warn("RecordPasswordResetCompleted: lookup", "err", err)
		}
		sub = got
	}
	if sub == "" {
		// Anti-enumeration: return success even if the email is unknown. The
		// caller is unauthenticated — we don't want to confirm membership.
		return connect.NewResponse(&authv1.RecordPasswordResetCompletedResponse{}), nil
	}

	_ = s.Store.RecordAuthEvent(ctx, domain.AuthAuditEvent{
		UserID:      sub,
		ActorUserID: sub,
		Kind:        domain.AuthEventPasswordResetCompleted,
		IP:          clientIP(req.Header()),
		UserAgent:   req.Header().Get("User-Agent"),
	})
	return connect.NewResponse(&authv1.RecordPasswordResetCompletedResponse{}), nil
}

// ── helpers ────────────────────────────────────────────────────────────────

func (s *AuthService) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func clientIP(h http.Header) string {
	// Prefer X-Forwarded-For first hop; fall back to remote addr (unset on
	// Connect requests because we don't have the *http.Request directly).
	if xff := h.Get("X-Forwarded-For"); xff != "" {
		if comma := strings.IndexByte(xff, ','); comma > 0 {
			return strings.TrimSpace(xff[:comma])
		}
		return strings.TrimSpace(xff)
	}
	return h.Get("X-Real-Ip")
}

func int64Or(v int32, fallback int64) int64 {
	if v <= 0 {
		return fallback
	}
	return int64(v)
}

func isDevBypassActive() bool {
	return getEnv("DEV_MODE") == "true" && getEnv("DEV_BYPASS_AUTH") == "true"
}

// getEnv is a thin wrapper so tests can override behavior if needed; defined
// here rather than dragging os into every call site.
func getEnv(k string) string {
	return strings.TrimSpace(envGet(k))
}
