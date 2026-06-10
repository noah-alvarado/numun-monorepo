package cms

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	cfg := Config{
		AppID:          "123",
		InstallationID: "456",
		PrivateKeyPEM:  keyPEM,
		Repo:           "numun/numun-monorepo",
		Branch:         "main",
		APIBaseURL:     srv.URL,
	}
	c, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

func writeTokenResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"token":      "ghs_testtoken",
		"expires_at": time.Now().Add(50 * time.Minute).Format(time.RFC3339),
	})
}

func TestUpsertFileCreatesWhenAbsent(t *testing.T) {
	var putBody []byte
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/456/access_tokens", func(w http.ResponseWriter, _ *http.Request) {
		writeTokenResponse(w)
	})
	mux.HandleFunc("/repos/numun/numun-monorepo/contents/content/awards-archive/foo.md", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			http.Error(w, "not found", http.StatusNotFound)
		case http.MethodPut:
			putBody, _ = io.ReadAll(r.Body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"commit": map[string]any{"sha": "deadbeef"},
			})
		default:
			t.Fatalf("unexpected %s", r.Method)
		}
	})
	c := newTestClient(t, mux)

	sha, err := c.UpsertFile(context.Background(), "content/awards-archive/foo.md", []byte("hello"), "msg")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if sha != "deadbeef" {
		t.Fatalf("sha: got %q", sha)
	}
	if !strings.Contains(string(putBody), "aGVsbG8=") { // "hello" base64
		t.Fatalf("body missing base64-encoded content: %s", putBody)
	}
}

func TestUpsertFileUpdatesWhenPresent(t *testing.T) {
	var seenSHA string
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/456/access_tokens", func(w http.ResponseWriter, _ *http.Request) {
		writeTokenResponse(w)
	})
	mux.HandleFunc("/repos/numun/numun-monorepo/contents/content/awards-archive/foo.md", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": "oldsha"})
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			var parsed map[string]any
			_ = json.Unmarshal(body, &parsed)
			if s, ok := parsed["sha"].(string); ok {
				seenSHA = s
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"commit": map[string]any{"sha": "newsha"},
			})
		default:
			t.Fatalf("unexpected %s", r.Method)
		}
	})
	c := newTestClient(t, mux)
	if _, err := c.UpsertFile(context.Background(), "content/awards-archive/foo.md", []byte("hi"), "msg"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if seenSHA != "oldsha" {
		t.Fatalf("expected sha=oldsha in PUT body, got %q", seenSHA)
	}
}

func TestDeleteFileMissingIsSuccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/456/access_tokens", func(w http.ResponseWriter, _ *http.Request) {
		writeTokenResponse(w)
	})
	mux.HandleFunc("/repos/numun/numun-monorepo/contents/content/awards-archive/foo.md", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		t.Fatalf("delete should not be issued when file is absent (saw %s)", r.Method)
	})
	c := newTestClient(t, mux)
	if err := c.DeleteFile(context.Background(), "content/awards-archive/foo.md", "msg"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestDeleteFilePresent(t *testing.T) {
	calls := atomic.Int32{}
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/456/access_tokens", func(w http.ResponseWriter, _ *http.Request) {
		writeTokenResponse(w)
	})
	mux.HandleFunc("/repos/numun/numun-monorepo/contents/content/awards-archive/foo.md", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": "abc"})
		case http.MethodDelete:
			calls.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"commit":{"sha":"deletedsha"}}`))
		default:
			t.Fatalf("unexpected %s", r.Method)
		}
	})
	c := newTestClient(t, mux)
	if err := c.DeleteFile(context.Background(), "content/awards-archive/foo.md", "msg"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected exactly 1 DELETE, saw %d", calls.Load())
	}
}

func TestWithRetrySucceedsAfterTransient(t *testing.T) {
	attempts := 0
	status := (&Client{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}).WithRetry(context.Background(), func(_ context.Context) (string, error) {
		attempts++
		if attempts < 3 {
			return "", &transientErr{msg: "boom"}
		}
		return "ok-sha", nil
	})
	if !status.OK || status.Attempts != 3 || status.CommitSHA != "ok-sha" {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestWithRetryExhausts(t *testing.T) {
	status := (&Client{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}).WithRetry(context.Background(), func(_ context.Context) (string, error) {
		return "", &transientErr{msg: "still down"}
	})
	if status.OK || status.Attempts != 3 || status.FinalError == "" {
		t.Fatalf("expected exhausted status: %+v", status)
	}
}

func TestStubClientSkipsNetwork(t *testing.T) {
	c := NewStub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !c.IsStub() {
		t.Fatalf("stub IsStub: false")
	}
	if _, err := c.UpsertFile(context.Background(), "any", []byte("x"), "m"); err != nil {
		t.Fatalf("stub upsert: %v", err)
	}
	if err := c.DeleteFile(context.Background(), "any", "m"); err != nil {
		t.Fatalf("stub delete: %v", err)
	}
}

type transientErr struct{ msg string }

func (e *transientErr) Error() string { return e.msg }
