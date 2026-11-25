package handlers_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/email"
	"github.com/numun/numun/api/internal/handlers"
	"github.com/numun/numun/api/internal/store"
)

func TestUnsubscribeConfirmsBeforeOptingOut(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	cfg := email.Config{UnsubscribeSecret: "test-secret", PortalBaseURL: "https://portal.example"}
	mux := http.NewServeMux()
	(&handlers.UnsubscribeRoutes{Store: st, Cfg: cfg}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	u := seedUser(t, st, domain.RoleAdvisor, uniqueEmail(t, "unsub"))
	optIn := true
	if _, err := st.UpdateUser(ctx, u.ID, u.Version, store.UpdateUserPatch{AnnouncementsOptIn: &optIn}); err != nil {
		t.Fatalf("opt in: %v", err)
	}
	link, err := email.SignedUnsubscribeURL(srv.URL+"/v1/email/unsubscribe", cfg.UnsubscribeSecret, u.ID)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	optedIn := func() bool {
		got, err := st.GetUser(ctx, u.ID)
		if err != nil {
			t.Fatalf("get user: %v", err)
		}
		return got.AnnouncementsOptIn
	}

	resp, body := fetch(t, http.MethodGet, link, nil)
	if resp.StatusCode != 200 || !strings.Contains(body, "<form method=\"post\"") || !optedIn() {
		t.Fatalf("GET must only confirm: status %d, opted in %v", resp.StatusCode, optedIn())
	}

	resp, body = fetch(t, http.MethodPost, link, url.Values{})
	if resp.StatusCode != 200 || !strings.Contains(body, "been unsubscribed") || optedIn() {
		t.Fatalf("form POST: status %d, opted in %v", resp.StatusCode, optedIn())
	}

	again := true
	cur, _ := st.GetUser(ctx, u.ID)
	if _, err := st.UpdateUser(ctx, u.ID, cur.Version, store.UpdateUserPatch{AnnouncementsOptIn: &again}); err != nil {
		t.Fatalf("opt back in: %v", err)
	}
	resp, _ = fetch(t, http.MethodPost, link, url.Values{"List-Unsubscribe": {"One-Click"}})
	if resp.StatusCode != http.StatusNoContent || optedIn() {
		t.Fatalf("one-click POST: status %d, opted in %v", resp.StatusCode, optedIn())
	}

	resp, body = fetch(t, http.MethodGet, srv.URL+"/v1/email/unsubscribe?token=forged", nil)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "valid") {
		t.Fatalf("forged token: status %d body %q", resp.StatusCode, body)
	}
}

func fetch(t *testing.T, method, target string, form url.Values) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}
