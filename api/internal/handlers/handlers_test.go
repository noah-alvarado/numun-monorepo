package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	authv1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/gen/numun/v1/numunv1connect"
	"github.com/numun/numun/api/internal/handlers"
	"github.com/numun/numun/api/internal/store"
)

func testStore(t *testing.T) *store.Client {
	t.Helper()
	if os.Getenv("AWS_ENDPOINT_URL_DYNAMODB") == "" {
		t.Skip("set AWS_ENDPOINT_URL_DYNAMODB to run DDB integration tests")
	}
	t.Setenv("AWS_REGION", "us-east-2")
	t.Setenv("AWS_ACCESS_KEY_ID", "local")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "local")
	c, err := store.New(context.Background())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return c
}

func newUUID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return id.String()
}

// startServer wires the same middleware + services the Lambdalith does, and
// returns a base URL for httptest.
func startServer(t *testing.T, st *store.Client) string {
	t.Helper()
	mux := http.NewServeMux()
	authSvc := &handlers.AuthService{Store: st}
	apath, ahandler := numunv1connect.NewAuthServiceHandler(authSvc)
	mux.Handle(apath, ahandler)
	userSvc := &handlers.UserService{Store: st}
	upath, uhandler := numunv1connect.NewUserServiceHandler(userSvc)
	mux.Handle(upath, uhandler)

	mw := auth.New(auth.MiddlewareConfig{
		Store:     st,
		DevMode:   true,
		DevBypass: true,
	})
	srv := httptest.NewServer(mw(mux))
	t.Cleanup(srv.Close)
	return srv.URL
}

// devTransport injects the X-Dev-User-Id header on every Connect call.
func devTransport(t *testing.T, baseURL, uid string) connect.HTTPClient {
	return &headerInjector{base: http.DefaultClient, headers: map[string]string{auth.DevUserIDHeader: uid}}
}

type headerInjector struct {
	base    *http.Client
	headers map[string]string
}

func (h *headerInjector) Do(req *http.Request) (*http.Response, error) {
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}
	return h.base.Do(req)
}

func TestUserServiceGetMeDevBypass(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	baseURL := startServer(t, st)

	uid := newUUID(t)
	if _, err := st.CreateUser(ctx, domain.User{
		ID:    uid,
		Role:  domain.RoleAdvisor,
		Email: "getme-" + uid + "@test",
		Name:  "GetMe User",
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	client := numunv1connect.NewUserServiceClient(devTransport(t, baseURL, uid), baseURL)

	resp, err := client.GetMe(ctx, connect.NewRequest(&authv1.GetMeRequest{}))
	if err != nil {
		t.Fatalf("GetMe: %v", err)
	}
	if resp.Msg.GetUser().GetId() != uid {
		t.Fatalf("GetMe wrong id: got %q want %q", resp.Msg.GetUser().GetId(), uid)
	}
	if resp.Msg.GetUser().GetRole() != authv1.User_ROLE_ADVISOR {
		t.Fatalf("GetMe wrong role: %v", resp.Msg.GetUser().GetRole())
	}
}

func TestUserServiceUpdateUserOptimisticLock(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	baseURL := startServer(t, st)

	uid := newUUID(t)
	if _, err := st.CreateUser(ctx, domain.User{
		ID:    uid,
		Role:  domain.RoleAdvisor,
		Email: "upd-" + uid + "@test",
		Name:  "Original",
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	client := numunv1connect.NewUserServiceClient(devTransport(t, baseURL, uid), baseURL)

	newName := "Renamed"
	resp, err := client.UpdateUser(ctx, connect.NewRequest(&authv1.UpdateUserRequest{
		UserId:          uid,
		Name:            &newName,
		ExpectedVersion: 1,
	}))
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if resp.Msg.GetUser().GetName() != newName || resp.Msg.GetUser().GetVersion() != 2 {
		t.Fatalf("after update: %+v", resp.Msg.GetUser())
	}

	// Stale version → CodeAborted.
	_, err = client.UpdateUser(ctx, connect.NewRequest(&authv1.UpdateUserRequest{
		UserId:          uid,
		Name:            &newName,
		ExpectedVersion: 1,
	}))
	if err == nil {
		t.Fatalf("stale update unexpectedly succeeded")
	}
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeAborted {
		t.Fatalf("stale update: want CodeAborted, got %v", err)
	}
}

func TestAuthServiceExchangeAndLogoutDevBypass(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)

	// Bring up a server with the same mw chain but use raw HTTP because
	// Exchange / Logout set cookies on the response headers, which the Connect
	// client interceptor would discard; we want to inspect Set-Cookie.
	mux := http.NewServeMux()
	authSvc := &handlers.AuthService{Store: st}
	apath, ahandler := numunv1connect.NewAuthServiceHandler(authSvc)
	mux.Handle(apath, ahandler)
	userSvc := &handlers.UserService{Store: st}
	upath, uhandler := numunv1connect.NewUserServiceHandler(userSvc)
	mux.Handle(upath, uhandler)
	mw := auth.New(auth.MiddlewareConfig{
		Store:     st,
		DevMode:   true,
		DevBypass: true,
	})
	srv := httptest.NewServer(mw(mux))
	defer srv.Close()

	// Activate dev-bypass for the AuthService.Exchange path so the synthetic
	// id_token shape is honored. The handler reads DEV_MODE / DEV_BYPASS_AUTH
	// through os.Getenv at request time, so set them on the test process.
	t.Setenv("DEV_MODE", "true")
	t.Setenv("DEV_BYPASS_AUTH", "true")

	uid := newUUID(t)
	if _, err := st.CreateUser(ctx, domain.User{
		ID:    uid,
		Role:  domain.RoleAdvisor,
		Email: "ex-" + uid + "@test",
		Name:  "Exchange User",
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Exchange — dev shim accepts id_token="dev-<anything>" and uses the
	// access_token as "dev-<uid>".
	body := strings.NewReader(`{"idToken":"dev-tok","accessToken":"dev-` + uid + `","refreshToken":"dev-rt","expiresIn":3600,"rememberMe":true}`)
	exchangeURL, _ := url.JoinPath(srv.URL, "/numun.v1.AuthService/Exchange")
	req, _ := http.NewRequest("POST", exchangeURL, body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("Exchange status %d", resp.StatusCode)
	}
	cookies := resp.Cookies()
	resp.Body.Close()
	var sessCookie, csrfCookie *http.Cookie
	for _, c := range cookies {
		switch c.Name {
		case auth.SessionCookieName:
			sessCookie = c
		case auth.CSRFCookieName:
			csrfCookie = c
		}
	}
	if sessCookie == nil || csrfCookie == nil {
		t.Fatalf("missing session/csrf cookies: %+v", cookies)
	}
	if !sessCookie.HttpOnly {
		t.Fatalf("session cookie must be HttpOnly")
	}
	if csrfCookie.HttpOnly {
		t.Fatalf("csrf cookie must NOT be HttpOnly")
	}

	// Use the session+csrf cookies to call UserService.GetMe through middleware.
	getMeURL, _ := url.JoinPath(srv.URL, "/numun.v1.UserService/GetMe")
	req, _ = http.NewRequest("POST", getMeURL, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.AddCookie(sessCookie)
	req.AddCookie(csrfCookie)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GetMe: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GetMe status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Logout — should set Max-Age=0 cookies and delete the session row.
	logoutURL, _ := url.JoinPath(srv.URL, "/numun.v1.AuthService/Logout")
	req, _ = http.NewRequest("POST", logoutURL, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.AddCookie(sessCookie)
	req.AddCookie(csrfCookie)
	req.Header.Set(auth.CSRFHeader, csrfCookie.Value)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("Logout status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// The session row should now be gone.
	if _, err := st.GetSession(ctx, sessCookie.Value); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("session not deleted: %v", err)
	}

	_ = time.Now() // silence unused import in some build paths
}
