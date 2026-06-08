// Package cmsoauth implements the GitHub OAuth proxy used by Decap CMS.
//
// scope-check: skip
//
// Decap CMS runs as a static SPA at cms.${apex} but talks to GitHub to commit
// content changes. GitHub's OAuth code-for-token exchange must happen
// server-side (the client secret cannot be exposed to the browser). These two
// routes — mounted on the Lambdalith outside the Connect router and outside
// the auth middleware — implement that flow:
//
//	GET /cms-oauth/auth       — kicks off the GitHub OAuth dance.
//	GET /cms-oauth/callback   — exchanges the code for a token and posts it
//	                            back to the opener window via postMessage.
//
// See docs/subsystems/CMS_CONTENT_MODEL.md §8.3 and docs/SECURITY.md §2.6.
package cmsoauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

const (
	// StateCookieName carries the HMAC-signed state across the redirect to
	// GitHub and back. Short-lived (10 min), HttpOnly, Secure, SameSite=Lax.
	// Lax (not Strict) because the cookie must survive GitHub's top-level
	// navigation redirect back to /cms-oauth/callback.
	StateCookieName = "cms-oauth-state"

	// StateCookieMaxAge bounds the OAuth dance to ten minutes.
	StateCookieMaxAge = 10 * 60

	githubAuthorizeURL = "https://github.com/login/oauth/authorize"
	githubTokenURL     = "https://github.com/login/oauth/access_token"

	tokenExchangeTimeout = 10 * time.Second
)

// secretsLoader is the function signature used to read OAuth secrets at
// startup. Production wires this to SSM; tests inject a stub.
type secretsLoader func(ctx context.Context) (clientID, clientSecret, stateSecret string, err error)

// Handler implements the two HTTP routes that back the Decap CMS GitHub login.
//
// Construct with New; the zero value is not usable. Methods are safe for
// concurrent use after Init.
type Handler struct {
	Logger *slog.Logger

	// httpClient is the outbound client used for the token exchange. Override
	// in tests; nil means use the default ten-second-timeout client built in
	// New.
	httpClient *http.Client

	// loader is invoked at most once (via initOnce) to fetch secrets.
	loader secretsLoader

	// initOnce gates the first call to loader. After it runs, clientID /
	// clientSecret / stateSecret are populated for the lifetime of the
	// process.
	initOnce sync.Once
	initErr  error

	clientID     string
	clientSecret string
	stateSecret  []byte

	// origin overrides the postMessage target / redirect-URI host in tests.
	// In production these are computed from ROOT_DOMAIN / ENV_SUBDOMAIN.
	cmsOriginOverride string
	apiOriginOverride string
}

// New builds a Handler wired to read secrets from SSM (or env vars in dev
// mode) on first use. Reading is deferred so that constructing the handler
// during main() cannot fail the cold start before the AWS config / IAM is
// available; the first real request returns 503 if the loader still fails.
func New(_ context.Context, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		Logger: logger,
		httpClient: &http.Client{
			Timeout: tokenExchangeTimeout,
		},
		loader: defaultSecretsLoader,
	}
}

// ensureLoaded resolves the OAuth secrets on first call. Returns the cached
// error on every subsequent call if the first attempt failed — callers should
// surface that to clients as a 503.
func (h *Handler) ensureLoaded(ctx context.Context) error {
	h.initOnce.Do(func() {
		id, secret, state, err := h.loader(ctx)
		if err != nil {
			h.initErr = err
			return
		}
		h.clientID = id
		h.clientSecret = secret
		h.stateSecret = []byte(state)
	})
	return h.initErr
}

// defaultSecretsLoader reads the three OAuth params from SSM at
// /numun/${Env}/cms_oauth/{client_id,client_secret,state_secret}. In dev
// mode (DEV_MODE=true) it falls back to CMS_OAUTH_* env vars so `make dev`
// can stub them without needing AWS credentials.
func defaultSecretsLoader(ctx context.Context) (string, string, string, error) {
	dev := os.Getenv("DEV_MODE") == "true"

	if dev {
		id := os.Getenv("CMS_OAUTH_CLIENT_ID")
		secret := os.Getenv("CMS_OAUTH_CLIENT_SECRET")
		state := os.Getenv("CMS_OAUTH_STATE_SECRET")
		if id == "" || secret == "" || state == "" {
			return "", "", "", errors.New("cmsoauth: dev mode requires CMS_OAUTH_CLIENT_ID, CMS_OAUTH_CLIENT_SECRET, CMS_OAUTH_STATE_SECRET")
		}
		return id, secret, state, nil
	}

	env := os.Getenv("ENV")
	if env == "" {
		// Fall back to the convention used elsewhere in the stack — derive
		// the env qualifier from the subdomain (empty subdomain means prod).
		if sub := os.Getenv("ENV_SUBDOMAIN"); sub != "" {
			env = sub
		} else {
			env = "prod"
		}
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return "", "", "", fmt.Errorf("cmsoauth: load aws config: %w", err)
	}
	client := ssm.NewFromConfig(cfg)

	read := func(name string) (string, error) {
		out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
			Name:           aws.String(name),
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			return "", fmt.Errorf("cmsoauth: read %s: %w", name, err)
		}
		if out.Parameter == nil || out.Parameter.Value == nil {
			return "", fmt.Errorf("cmsoauth: %s is empty", name)
		}
		return *out.Parameter.Value, nil
	}

	base := fmt.Sprintf("/numun/%s/cms_oauth", env)
	id, err := read(base + "/client_id")
	if err != nil {
		return "", "", "", err
	}
	secret, err := read(base + "/client_secret")
	if err != nil {
		return "", "", "", err
	}
	state, err := read(base + "/state_secret")
	if err != nil {
		return "", "", "", err
	}
	return id, secret, state, nil
}

// Auth handles GET /cms-oauth/auth. It mints a fresh random state, signs it,
// drops a short-lived cookie, and 302s the browser to GitHub's authorize
// endpoint.
func (h *Handler) Auth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := h.ensureLoaded(ctx); err != nil {
		h.Logger.Error("cmsoauth: load secrets", "err", err)
		http.Error(w, "cms oauth unavailable", http.StatusServiceUnavailable)
		return
	}

	state, err := newRandomHex(32)
	if err != nil {
		h.Logger.Error("cmsoauth: rand", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	signed := signState(h.stateSecret, state)

	http.SetCookie(w, &http.Cookie{
		Name:     StateCookieName,
		Value:    signed,
		Path:     "/cms-oauth/",
		MaxAge:   StateCookieMaxAge,
		Secure:   secureCookies(),
		HttpOnly: true,
		// Lax — GitHub redirects back as a top-level navigation. Strict
		// would cause the browser to drop the cookie on the return trip.
		SameSite: http.SameSiteLaxMode,
	})

	q := url.Values{}
	q.Set("client_id", h.clientID)
	q.Set("scope", "repo")
	q.Set("state", signed)
	q.Set("redirect_uri", h.apiOrigin()+"/cms-oauth/callback")

	http.Redirect(w, r, githubAuthorizeURL+"?"+q.Encode(), http.StatusFound)
}

// Callback handles GET /cms-oauth/callback. It verifies the state cookie
// against the query string, exchanges the GitHub code for an access token, and
// returns an HTML page that posts the token back to the opener window in the
// exact format Decap CMS expects.
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := h.ensureLoaded(ctx); err != nil {
		h.Logger.Error("cmsoauth: load secrets", "err", err)
		http.Error(w, "cms oauth unavailable", http.StatusServiceUnavailable)
		return
	}

	// Always clear the state cookie on the way out, success or fail.
	defer h.clearStateCookie(w)

	queryState := r.URL.Query().Get("state")
	if queryState == "" {
		http.Error(w, "missing state", http.StatusBadRequest)
		return
	}

	cookie, err := r.Cookie(StateCookieName)
	if err != nil || cookie.Value == "" {
		http.Error(w, "missing state cookie", http.StatusBadRequest)
		return
	}

	if !hmac.Equal([]byte(cookie.Value), []byte(queryState)) {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	if _, ok := verifyState(h.stateSecret, cookie.Value); !ok {
		http.Error(w, "invalid state signature", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		h.writeErrorHTML(w, "missing code")
		return
	}

	token, err := h.exchangeCode(ctx, code)
	if err != nil {
		h.Logger.Warn("cmsoauth: token exchange failed", "err", err)
		// Don't leak the GitHub-side error; surface a generic message
		// to the browser and a 502 on the wire.
		w.WriteHeader(http.StatusBadGateway)
		h.writeErrorHTML(w, "token exchange failed")
		return
	}

	h.writeSuccessHTML(w, token)
}

// exchangeCode does the server-side POST to https://github.com/login/oauth/access_token.
func (h *Handler) exchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{}
	form.Set("client_id", h.clientID)
	form.Set("client_secret", h.clientSecret)
	form.Set("code", code)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "numun-cmsoauth/1.0")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("post token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github returned %d", resp.StatusCode)
	}

	var parsed struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if parsed.Error != "" {
		return "", fmt.Errorf("github oauth error: %s", parsed.Error)
	}
	if parsed.AccessToken == "" {
		return "", errors.New("github oauth: empty access token")
	}
	return parsed.AccessToken, nil
}

// writeSuccessHTML emits the postMessage page Decap CMS waits for. The
// exact message format is fixed by Decap: `authorization:github:success:<json>`.
func (h *Handler) writeSuccessHTML(w http.ResponseWriter, token string) {
	payload, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		// Practically unreachable — Marshal of a fixed-shape map cannot fail.
		h.Logger.Error("cmsoauth: marshal token payload", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	msg, err := json.Marshal("authorization:github:success:" + string(payload))
	if err != nil {
		h.Logger.Error("cmsoauth: marshal success message", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	origin, err := json.Marshal(h.cmsOrigin())
	if err != nil {
		h.Logger.Error("cmsoauth: marshal cms origin", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, callbackHTMLTemplate, string(msg), string(origin))
}

// writeErrorHTML emits the postMessage page Decap CMS expects on failure.
// Format: `authorization:github:error:<json>`.
func (h *Handler) writeErrorHTML(w http.ResponseWriter, message string) {
	payload, err := json.Marshal(map[string]string{"message": message})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	msg, err := json.Marshal("authorization:github:error:" + string(payload))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	origin, err := json.Marshal(h.cmsOrigin())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, callbackHTMLTemplate, string(msg), string(origin))
}

// callbackHTMLTemplate produces the minimal HTML page Decap CMS waits for. We
// JSON-encode both the postMessage payload and the target origin to avoid
// any unintended HTML or JS-string injection.
const callbackHTMLTemplate = `<!doctype html>
<html>
  <head><meta charset="utf-8"><title>NUMUN CMS · GitHub login</title></head>
  <body>
    <script>
      (function () {
        var message = %s;
        var origin  = %s;
        if (window.opener && typeof window.opener.postMessage === "function") {
          window.opener.postMessage(message, origin);
        }
        window.close();
      })();
    </script>
    <noscript>JavaScript is required to complete sign-in.</noscript>
  </body>
</html>`

// clearStateCookie zeroes out the state cookie. Called from Callback no matter
// the outcome.
func (h *Handler) clearStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     StateCookieName,
		Value:    "",
		Path:     "/cms-oauth/",
		MaxAge:   -1,
		Secure:   secureCookies(),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// cmsOrigin returns the absolute origin (https://cms.${apex}) that we lock
// the callback's postMessage target to. Overridable for tests.
func (h *Handler) cmsOrigin() string {
	if h.cmsOriginOverride != "" {
		return h.cmsOriginOverride
	}
	return "https://cms." + apex()
}

// apiOrigin returns the absolute origin (https://api.${apex}) used to build
// the redirect_uri handed to GitHub. Must match the registered OAuth app's
// callback URL exactly.
func (h *Handler) apiOrigin() string {
	if h.apiOriginOverride != "" {
		return h.apiOriginOverride
	}
	return "https://api." + apex()
}

// apex returns the bare apex domain (e.g. numun.org, test.numun.org) derived
// from ROOT_DOMAIN + ENV_SUBDOMAIN. Mirrors the SAM template logic.
func apex() string {
	root := os.Getenv("ROOT_DOMAIN")
	if root == "" {
		root = "numun.org"
	}
	sub := os.Getenv("ENV_SUBDOMAIN")
	if sub == "" {
		return root
	}
	return sub + "." + root
}

// signState returns "<state>.<hexHMAC>" for the given state and key.
func signState(key []byte, state string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(state))
	return state + "." + hex.EncodeToString(mac.Sum(nil))
}

// verifyState parses a signed-state string and returns the unsigned state
// when the signature checks out.
func verifyState(key []byte, signed string) (string, bool) {
	dot := strings.LastIndexByte(signed, '.')
	if dot <= 0 || dot == len(signed)-1 {
		return "", false
	}
	state := signed[:dot]
	sigHex := signed[dot+1:]
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(state))
	expected := mac.Sum(nil)
	if !hmac.Equal(sig, expected) {
		return "", false
	}
	return state, true
}

// newRandomHex returns the hex encoding of n random bytes.
func newRandomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// secureCookies returns whether the Secure flag should be set on the state
// cookie. Off when COOKIE_SECURE=false (local dev), on otherwise.
func secureCookies() bool {
	if v := os.Getenv("COOKIE_SECURE"); v != "" {
		return v == "true" || v == "1"
	}
	return os.Getenv("DEV_MODE") != "true"
}
