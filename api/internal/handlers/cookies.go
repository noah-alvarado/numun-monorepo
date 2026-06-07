// scope-check: skip
package handlers

import (
	"net/http"
	"os"
	"time"

	"github.com/numun/numun/api/internal/auth"
)

// setSessionCookies writes the Set-Cookie headers for a fresh session. Per
// AUTH.md §4.3:
//
//   - rememberMe=false  → session cookie (no Max-Age/Expires; cleared on browser close).
//   - rememberMe=true   → persistent cookie with Max-Age = ttl.
//
// The CSRF cookie always matches the session cookie's persistence.
func setSessionCookies(h http.Header, sessionID, csrf string, ttl time.Duration, rememberMe bool) {
	path, domain, secure, sameSite := auth.CookieAttrs()
	maxAge := 0
	if rememberMe {
		maxAge = int(ttl.Seconds())
	}

	sessCookie := &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    sessionID,
		Path:     path,
		Domain:   domain,
		Secure:   secure,
		HttpOnly: true,
		SameSite: sameSite,
		MaxAge:   maxAge,
	}
	csrfCookie := &http.Cookie{
		Name:     auth.CSRFCookieName,
		Value:    csrf,
		Path:     path,
		Domain:   domain,
		Secure:   secure,
		HttpOnly: false, // the client must read this cookie to populate the header
		SameSite: sameSite,
		MaxAge:   maxAge,
	}
	h.Add("Set-Cookie", sessCookie.String())
	h.Add("Set-Cookie", csrfCookie.String())
}

func clearAuthCookies(h http.Header) {
	path, domain, secure, sameSite := auth.CookieAttrs()
	for _, name := range []string{auth.SessionCookieName, auth.CSRFCookieName} {
		c := &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     path,
			Domain:   domain,
			Secure:   secure,
			HttpOnly: name == auth.SessionCookieName,
			SameSite: sameSite,
			MaxAge:   -1,
		}
		h.Add("Set-Cookie", c.String())
	}
}

// envGet is a thin os.Getenv pass-through used by auth.go.
func envGet(k string) string { return os.Getenv(k) }
