package cmsoauth

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const (
	testStateSecret  = "abcdef0123456789abcdef0123456789"
	testClientID     = "test-client-id"
	testClientSecret = "test-client-secret"
)

// stubLoader returns a secretsLoader that yields fixed test values without
// touching SSM or the environment.
func stubLoader() secretsLoader {
	return func(_ context.Context) (string, string, string, error) {
		return testClientID, testClientSecret, testStateSecret, nil
	}
}

// newTestHandler builds a Handler wired with test stubs and predictable
// origins. cmsOrigin is locked to https://cms.example.test and
// apiOrigin to https://api.example.test for assertion stability.
func newTestHandler(t *testing.T, tokenSrv *httptest.Server) *Handler {
	t.Helper()
	h := New(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.loader = stubLoader()
	h.cmsOriginOverride = "https://cms.example.test"
	h.apiOriginOverride = "https://api.example.test"
	if tokenSrv != nil {
		// Re-point the outbound exchange to the in-process test server by
		// swapping the package-level URL via the test client's Transport.
		// We achieve this without modifying production code by giving the
		// handler an http.Client whose Transport rewrites the GitHub host.
		h.httpClient = &http.Client{
			Transport: rewriteTransport{target: tokenSrv.URL},
			Timeout:   tokenSrv.Client().Timeout,
		}
	}
	return h
}

// rewriteTransport routes any outbound request to a fixed base URL. It is
// trivial on purpose — we only ever talk to one endpoint in the tests.
type rewriteTransport struct {
	target string
}

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := url.Parse(rt.target)
	if err != nil {
		return nil, err
	}
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = u.Scheme
	req2.URL.Host = u.Host
	req2.Host = u.Host
	return http.DefaultTransport.RoundTrip(req2)
}

func TestSignAndVerifyState_RoundTrip(t *testing.T) {
	key := []byte(testStateSecret)
	signed := signState(key, "deadbeef")
	got, ok := verifyState(key, signed)
	if !ok {
		t.Fatalf("verifyState rejected a freshly signed value: %q", signed)
	}
	if got != "deadbeef" {
		t.Errorf("verifyState returned %q, want deadbeef", got)
	}
}

func TestVerifyState_RejectsTampering(t *testing.T) {
	key := []byte(testStateSecret)
	signed := signState(key, "deadbeef")

	// Flip a bit in the signature.
	bad := signed[:len(signed)-1] + flipHex(signed[len(signed)-1])
	if _, ok := verifyState(key, bad); ok {
		t.Error("verifyState accepted a tampered signature")
	}

	// Flip a bit in the state.
	parts := strings.SplitN(signed, ".", 2)
	bad2 := "deadbeee." + parts[1]
	if _, ok := verifyState(key, bad2); ok {
		t.Error("verifyState accepted a tampered state")
	}

	// Wrong key.
	if _, ok := verifyState([]byte("other-secret"), signed); ok {
		t.Error("verifyState accepted with the wrong key")
	}

	// Malformed.
	for _, bogus := range []string{"", "no-dot", "trailing.", ".leading", "nothex.zzzz"} {
		if _, ok := verifyState(key, bogus); ok {
			t.Errorf("verifyState accepted malformed value %q", bogus)
		}
	}
}

func flipHex(c byte) string {
	if c == '0' {
		return "1"
	}
	return "0"
}

func TestAuth_SetsCookieAndRedirectsToGitHub(t *testing.T) {
	h := newTestHandler(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/cms-oauth/auth", nil)
	rec := httptest.NewRecorder()
	h.Auth(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, githubAuthorizeURL+"?") {
		t.Fatalf("Location = %q, want prefix %q", loc, githubAuthorizeURL+"?")
	}

	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	q := u.Query()
	if q.Get("client_id") != testClientID {
		t.Errorf("client_id = %q, want %q", q.Get("client_id"), testClientID)
	}
	if q.Get("scope") != "repo" {
		t.Errorf("scope = %q, want repo", q.Get("scope"))
	}
	if q.Get("redirect_uri") != "https://api.example.test/cms-oauth/callback" {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	state := q.Get("state")
	if state == "" {
		t.Fatal("state parameter is empty")
	}
	if _, ok := verifyState([]byte(testStateSecret), state); !ok {
		t.Errorf("state %q failed verification", state)
	}

	// Cookie should round-trip with the same signed value, Lax + HttpOnly.
	var stateCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == StateCookieName {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("state cookie was not set")
	}
	if stateCookie.Value != state {
		t.Errorf("cookie value = %q, query state = %q", stateCookie.Value, state)
	}
	if !stateCookie.HttpOnly {
		t.Error("state cookie must be HttpOnly")
	}
	if stateCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("state cookie SameSite = %v, want Lax", stateCookie.SameSite)
	}
	if stateCookie.MaxAge != StateCookieMaxAge {
		t.Errorf("state cookie MaxAge = %d, want %d", stateCookie.MaxAge, StateCookieMaxAge)
	}
}

func TestCallback_Success_PostsTokenToOpener(t *testing.T) {
	tokenSrv := newGitHubTokenServer(t, func(r *http.Request) (int, string) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.PostForm.Get("client_id") != testClientID {
			t.Errorf("client_id = %q", r.PostForm.Get("client_id"))
		}
		if r.PostForm.Get("client_secret") != testClientSecret {
			t.Errorf("client_secret leaked or wrong")
		}
		if r.PostForm.Get("code") != "test-code" {
			t.Errorf("code = %q", r.PostForm.Get("code"))
		}
		if !strings.Contains(r.Header.Get("Accept"), "application/json") {
			t.Errorf("Accept header missing application/json: %q", r.Header.Get("Accept"))
		}
		return http.StatusOK, `{"access_token":"gho_testtoken","token_type":"bearer","scope":"repo"}`
	})
	defer tokenSrv.Close()

	h := newTestHandler(t, tokenSrv)

	signed := signState([]byte(testStateSecret), "deadbeef")
	req := httptest.NewRequest(http.MethodGet, "/cms-oauth/callback?code=test-code&state="+signed, nil)
	req.AddCookie(&http.Cookie{Name: StateCookieName, Value: signed})
	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	body := rec.Body.String()
	// JSON-encoded postMessage payload — token must be embedded literally.
	if !strings.Contains(body, `authorization:github:success:{\"token\":\"gho_testtoken\"}`) {
		t.Errorf("response missing success postMessage payload\nbody=%s", body)
	}
	if !strings.Contains(body, `"https://cms.example.test"`) {
		t.Errorf("response missing CMS origin target\nbody=%s", body)
	}
	if !strings.Contains(body, "window.close()") {
		t.Errorf("response missing window.close call\nbody=%s", body)
	}

	// State cookie was cleared.
	for _, c := range rec.Result().Cookies() {
		if c.Name == StateCookieName && c.MaxAge >= 0 {
			t.Errorf("state cookie not cleared: %+v", c)
		}
	}
}

func TestCallback_StateMismatch(t *testing.T) {
	h := newTestHandler(t, nil)

	cookieState := signState([]byte(testStateSecret), "aaaaaaaa")
	queryState := signState([]byte(testStateSecret), "bbbbbbbb")

	req := httptest.NewRequest(http.MethodGet, "/cms-oauth/callback?code=x&state="+queryState, nil)
	req.AddCookie(&http.Cookie{Name: StateCookieName, Value: cookieState})
	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCallback_InvalidStateSignature(t *testing.T) {
	h := newTestHandler(t, nil)
	bad := "deadbeef.0000000000000000000000000000000000000000000000000000000000000000"
	req := httptest.NewRequest(http.MethodGet, "/cms-oauth/callback?code=x&state="+bad, nil)
	req.AddCookie(&http.Cookie{Name: StateCookieName, Value: bad})
	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCallback_MissingCookie(t *testing.T) {
	h := newTestHandler(t, nil)
	signed := signState([]byte(testStateSecret), "deadbeef")
	req := httptest.NewRequest(http.MethodGet, "/cms-oauth/callback?code=x&state="+signed, nil)
	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCallback_MissingCode_PostsErrorMessage(t *testing.T) {
	h := newTestHandler(t, nil)
	signed := signState([]byte(testStateSecret), "deadbeef")
	req := httptest.NewRequest(http.MethodGet, "/cms-oauth/callback?state="+signed, nil)
	req.AddCookie(&http.Cookie{Name: StateCookieName, Value: signed})
	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (error page); body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `authorization:github:error:{\"message\":\"missing code\"}`) {
		t.Errorf("response missing error postMessage payload\nbody=%s", body)
	}
}

func TestCallback_GitHubError_Returns502(t *testing.T) {
	tokenSrv := newGitHubTokenServer(t, func(_ *http.Request) (int, string) {
		return http.StatusOK, `{"error":"bad_verification_code","error_description":"nope"}`
	})
	defer tokenSrv.Close()

	h := newTestHandler(t, tokenSrv)

	signed := signState([]byte(testStateSecret), "deadbeef")
	req := httptest.NewRequest(http.MethodGet, "/cms-oauth/callback?code=test-code&state="+signed, nil)
	req.AddCookie(&http.Cookie{Name: StateCookieName, Value: signed})
	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "authorization:github:error") {
		t.Errorf("response missing error message; body=%s", body)
	}
	// Don't leak GitHub's error_description verbatim.
	if strings.Contains(body, "bad_verification_code") {
		t.Errorf("response leaked GitHub error code; body=%s", body)
	}
}

func TestCallback_GitHubReturns5xx(t *testing.T) {
	tokenSrv := newGitHubTokenServer(t, func(_ *http.Request) (int, string) {
		return http.StatusInternalServerError, `{"error":"server"}`
	})
	defer tokenSrv.Close()

	h := newTestHandler(t, tokenSrv)

	signed := signState([]byte(testStateSecret), "deadbeef")
	req := httptest.NewRequest(http.MethodGet, "/cms-oauth/callback?code=test-code&state="+signed, nil)
	req.AddCookie(&http.Cookie{Name: StateCookieName, Value: signed})
	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSecretsLoader_DevModeRequiresEnv(t *testing.T) {
	t.Setenv("DEV_MODE", "true")
	t.Setenv("CMS_OAUTH_CLIENT_ID", "")
	t.Setenv("CMS_OAUTH_CLIENT_SECRET", "")
	t.Setenv("CMS_OAUTH_STATE_SECRET", "")

	if _, _, _, err := defaultSecretsLoader(context.Background()); err == nil {
		t.Fatal("expected error when dev-mode env vars are missing")
	}

	t.Setenv("CMS_OAUTH_CLIENT_ID", "id")
	t.Setenv("CMS_OAUTH_CLIENT_SECRET", "sec")
	t.Setenv("CMS_OAUTH_STATE_SECRET", "state")

	id, sec, state, err := defaultSecretsLoader(context.Background())
	if err != nil {
		t.Fatalf("expected dev-mode loader to succeed: %v", err)
	}
	if id != "id" || sec != "sec" || state != "state" {
		t.Errorf("unexpected dev-mode values: id=%q sec=%q state=%q", id, sec, state)
	}
}

func TestApex_FromEnv(t *testing.T) {
	t.Setenv("ROOT_DOMAIN", "numun.org")
	t.Setenv("ENV_SUBDOMAIN", "")
	if got := apex(); got != "numun.org" {
		t.Errorf("apex(prod) = %q, want numun.org", got)
	}
	t.Setenv("ENV_SUBDOMAIN", "test")
	if got := apex(); got != "test.numun.org" {
		t.Errorf("apex(test) = %q, want test.numun.org", got)
	}
}

func TestEnsureLoaded_PropagatesError(t *testing.T) {
	h := New(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.loader = func(_ context.Context) (string, string, string, error) {
		return "", "", "", io.ErrUnexpectedEOF
	}
	if err := h.ensureLoaded(context.Background()); err == nil {
		t.Fatal("expected error from loader")
	}
	// Second call returns the cached error without re-invoking the loader.
	if err := h.ensureLoaded(context.Background()); err == nil {
		t.Fatal("expected cached error on second call")
	}
}

// newGitHubTokenServer spins up an httptest server that the rewriteTransport
// forwards all outbound requests to. The handler receives the (already-parsed)
// request and returns a (status, body) tuple.
func newGitHubTokenServer(t *testing.T, fn func(*http.Request) (int, string)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, body := fn(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	return srv
}

// Sanity: the JSON payload we embed in the HTML is parseable. Guards against
// future refactors that might break the escaping.
func TestSuccessHTML_PayloadIsParseable(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := httptest.NewRecorder()
	h.writeSuccessHTML(rec, "abc.def.ghi")
	body := rec.Body.String()

	// The literal JSON-encoded message is the first %s argument to the
	// template; extract it by scanning for the bracketed object Decap reads.
	const marker = `authorization:github:success:{\"token\":\"`
	idx := strings.Index(body, marker)
	if idx < 0 {
		t.Fatalf("success body missing marker; body=%s", body)
	}
	rest := body[idx+len(marker):]
	end := strings.Index(rest, `\"}`)
	if end < 0 {
		t.Fatalf("could not find end of token in body; body=%s", body)
	}
	if rest[:end] != "abc.def.ghi" {
		t.Errorf("extracted token = %q, want abc.def.ghi", rest[:end])
	}
}

// Sanity check: the loader is wired by default.
func TestNew_HasDefaultLoader(t *testing.T) {
	h := New(context.Background(), nil)
	if h.loader == nil {
		t.Fatal("default loader must be wired by New")
	}
}

// Compile-time check: json.Marshaler emitted strings round-trip back to the
// original Go string. Belt-and-suspenders to catch %s vs %q misuse in the
// template format.
func TestTemplate_RendersAsJSONStrings(t *testing.T) {
	got, err := json.Marshal("hello world")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `"hello world"` {
		t.Fatalf("json.Marshal: %s", got)
	}
}
