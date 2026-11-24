package observability

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const portalDSN = "https://abc123@o1.ingest.us.sentry.io/42"

func envelope(dsn string) string {
	return `{"event_id":"e1","dsn":"` + dsn + `"}` + "\n" + `{"type":"event"}` + "\n" + `{"message":"x"}` + "\n"
}

func TestTunnelRejectsOtherProjects(t *testing.T) {
	tun, err := NewTunnel(portalDSN)
	if err != nil {
		t.Fatalf("NewTunnel: %v", err)
	}
	for name, body := range map[string]string{
		"other project": envelope("https://abc123@o1.ingest.us.sentry.io/99"),
		"other host":    envelope("https://abc123@evil.example.com/42"),
		"http scheme":   envelope("http://abc123@o1.ingest.us.sentry.io/42"),
		"no header":     "not json\n",
		"missing dsn":   `{"event_id":"e1"}` + "\n",
	} {
		rec := httptest.NewRecorder()
		tun.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, TunnelPath, strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", name, rec.Code)
		}
	}
}

func TestTunnelRejectsOversizedEnvelope(t *testing.T) {
	tun, _ := NewTunnel(portalDSN)
	body := envelope(portalDSN) + strings.Repeat("x", maxEnvelopeBytes)
	rec := httptest.NewRecorder()
	tun.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, TunnelPath, strings.NewReader(body)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d", rec.Code)
	}
}

func TestTunnelForwardsToConfiguredProject(t *testing.T) {
	var gotPath, gotType, gotBody string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotType = r.URL.Path, r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	host := strings.TrimPrefix(upstream.URL, "https://")
	dsn := "https://abc123@" + host + "/42"
	tun := &Tunnel{host: host, projectID: "42", client: upstream.Client()}

	body := envelope(dsn)
	rec := httptest.NewRecorder()
	tun.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, TunnelPath, strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if gotPath != "/api/42/envelope/" || gotType != "application/x-sentry-envelope" || gotBody != body {
		t.Fatalf("upstream got path=%q type=%q body-match=%v", gotPath, gotType, gotBody == body)
	}
}
