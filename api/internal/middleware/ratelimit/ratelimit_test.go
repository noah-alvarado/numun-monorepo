package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
)

func TestPerIPSeparatesAnonymousFromAuthenticated(t *testing.T) {
	// Anonymous cap is much lower; an anonymous-path burst should trip
	// before the authenticated cap would.
	limits := Limits{PerUserPerMin: 0, PerIPAuthedPerMin: 6000, PerIPAnonymousPerMin: 5}
	h := PerIP(limits)(okHandler())

	// Five OK on the anonymous path, sixth rejected.
	for i := 0; i < 5; i++ {
		rr := callIP(h, "/numun.v1.PublicService/GetActiveConference", "1.2.3.4")
		if rr.Code != http.StatusOK {
			t.Fatalf("anon req %d: want 200, got %d", i+1, rr.Code)
		}
	}
	rr := callIP(h, "/numun.v1.PublicService/GetActiveConference", "1.2.3.4")
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("anon req 6: want 429, got %d", rr.Code)
	}

	// Same IP on an authenticated path: still allowed (different bucket
	// with a much higher cap).
	rr = callIP(h, "/numun.v1.DelegationService/GetDelegation", "1.2.3.4")
	if rr.Code != http.StatusOK {
		t.Fatalf("authed req on rate-limited anon IP: want 200, got %d", rr.Code)
	}
}

func TestPerIPDifferentIPsIndependent(t *testing.T) {
	limits := Limits{PerIPAuthedPerMin: 6000, PerIPAnonymousPerMin: 2}
	h := PerIP(limits)(okHandler())

	for i := 0; i < 2; i++ {
		if rr := callIP(h, "/numun.v1.PublicService/X", "10.0.0.1"); rr.Code != http.StatusOK {
			t.Fatalf("IP A req %d: %d", i+1, rr.Code)
		}
	}
	if rr := callIP(h, "/numun.v1.PublicService/X", "10.0.0.1"); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("IP A burst: want 429, got %d", rr.Code)
	}
	// Different IP, same path: independent bucket.
	if rr := callIP(h, "/numun.v1.PublicService/X", "10.0.0.2"); rr.Code != http.StatusOK {
		t.Fatalf("IP B: want 200, got %d", rr.Code)
	}
}

func TestPerIPNoXFFPasses(t *testing.T) {
	// API Gateway always sets X-Forwarded-For; absence means local dev call
	// and we don't rate limit.
	limits := Limits{PerIPAuthedPerMin: 1, PerIPAnonymousPerMin: 1}
	h := PerIP(limits)(okHandler())
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/numun.v1.PublicService/X", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("no-xff req %d: want 200, got %d", i+1, rr.Code)
		}
	}
}

func TestPerUserEnforcesPerUserCap(t *testing.T) {
	limits := Limits{PerUserPerMin: 3}
	h := PerUser(limits)(okHandler())

	for i := 0; i < 3; i++ {
		rr := callAsUser(h, "/numun.v1.DelegationService/X", "user-A")
		if rr.Code != http.StatusOK {
			t.Fatalf("user-A req %d: %d", i+1, rr.Code)
		}
	}
	if rr := callAsUser(h, "/numun.v1.DelegationService/X", "user-A"); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("user-A burst: want 429, got %d", rr.Code)
	}
	// Different user: independent.
	if rr := callAsUser(h, "/numun.v1.DelegationService/X", "user-B"); rr.Code != http.StatusOK {
		t.Fatalf("user-B: want 200, got %d", rr.Code)
	}
}

func TestPerUserUnauthenticatedPasses(t *testing.T) {
	// No Caller on context → middleware passes through; PerIP handles
	// anonymous traffic.
	limits := Limits{PerUserPerMin: 1}
	h := PerUser(limits)(okHandler())
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("unauth req %d: %d", i+1, rr.Code)
		}
	}
}

func TestIsAnonymousPath(t *testing.T) {
	cases := map[string]bool{
		"/v1/health":            true,
		"/v1/email/unsubscribe": true,
		"/cms-oauth/auth":       true,
		"/cms-oauth/callback":   true,
		"/numun.v1.PublicService/GetActiveConference": true,
		"/numun.v1.AuthService/Exchange":              true,
		"/numun.v1.DelegationService/GetDelegation":   false,
		"/numun.v1.AuthService/Logout":                false,
		"/numun.v1.UserService/GetMe":                 false,
	}
	for path, want := range cases {
		if got := isAnonymousPath(path); got != want {
			t.Errorf("isAnonymousPath(%q) = %v, want %v", path, got, want)
		}
	}
}

// helpers

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func callIP(h http.Handler, path, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("X-Forwarded-For", ip)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func callAsUser(h http.Handler, path, userID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	ctx := auth.WithCaller(req.Context(), auth.Caller{UserID: userID, Role: domain.RoleAdvisor})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req.WithContext(ctx))
	return rr
}
