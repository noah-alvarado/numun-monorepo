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

type commitInput struct {
	Branch struct {
		RepositoryNameWithOwner string `json:"repositoryNameWithOwner"`
		BranchName              string `json:"branchName"`
	} `json:"branch"`
	Message struct {
		Headline string `json:"headline"`
	} `json:"message"`
	ExpectedHeadOid string      `json:"expectedHeadOid"`
	FileChanges     fileChanges `json:"fileChanges"`
}

// githubMux serves the token, branch-head, and GraphQL endpoints, recording
// each createCommitOnBranch input; a non-empty gqlErr is returned as a GraphQL error.
func githubMux(t *testing.T, inputs *[]commitInput, gqlErr string) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/456/access_tokens", func(w http.ResponseWriter, _ *http.Request) {
		writeTokenResponse(w)
	})
	mux.HandleFunc("GET /repos/numun/numun-monorepo/git/ref/heads/main", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"sha": "head1"}})
	})
	mux.HandleFunc("POST /graphql", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string `json:"query"`
			Variables struct {
				Input commitInput `json:"input"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode graphql: %v", err)
		}
		if !strings.Contains(req.Query, "createCommitOnBranch") {
			t.Fatalf("unexpected query: %s", req.Query)
		}
		*inputs = append(*inputs, req.Variables.Input)
		if gqlErr != "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]any{{"message": gqlErr}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"createCommitOnBranch": map[string]any{"commit": map[string]any{"oid": "newsha"}}},
		})
	})
	return mux
}

func TestUpsertFileCommitsViaGraphQL(t *testing.T) {
	var inputs []commitInput
	c := newTestClient(t, githubMux(t, &inputs, ""))

	sha, err := c.UpsertFile(context.Background(), "content/awards-archive/foo.md", []byte("hello"), "award: upsert foo")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if sha != "newsha" {
		t.Fatalf("sha: got %q", sha)
	}
	if len(inputs) != 1 {
		t.Fatalf("want 1 commit, got %d", len(inputs))
	}
	in := inputs[0]
	if in.Branch.RepositoryNameWithOwner != "numun/numun-monorepo" || in.Branch.BranchName != "main" ||
		in.ExpectedHeadOid != "head1" || in.Message.Headline != "award: upsert foo" {
		t.Fatalf("unexpected input: %+v", in)
	}
	adds := in.FileChanges.Additions
	if len(adds) != 1 || adds[0].Path != "content/awards-archive/foo.md" || adds[0].Contents != "aGVsbG8=" { // "hello"
		t.Fatalf("unexpected additions: %+v", adds)
	}
	if len(in.FileChanges.Deletions) != 0 {
		t.Fatalf("unexpected deletions: %+v", in.FileChanges.Deletions)
	}
}

func TestUpsertFileSurfacesGraphQLErrors(t *testing.T) {
	var inputs []commitInput
	c := newTestClient(t, githubMux(t, &inputs, "Expected branch to point to head1"))
	_, err := c.UpsertFile(context.Background(), "content/awards-archive/foo.md", []byte("x"), "msg")
	if err == nil || !strings.Contains(err.Error(), "Expected branch to point to") {
		t.Fatalf("want GraphQL error, got %v", err)
	}
}

func TestDeleteFileMissingIsSuccess(t *testing.T) {
	var inputs []commitInput
	mux := githubMux(t, &inputs, "")
	mux.HandleFunc("GET /repos/numun/numun-monorepo/contents/content/awards-archive/foo.md", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	c := newTestClient(t, mux)
	if err := c.DeleteFile(context.Background(), "content/awards-archive/foo.md", "msg"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	if len(inputs) != 0 {
		t.Fatalf("no commit should be made when the file is absent, got %d", len(inputs))
	}
}

func TestDeleteFilePresent(t *testing.T) {
	var inputs []commitInput
	mux := githubMux(t, &inputs, "")
	mux.HandleFunc("GET /repos/numun/numun-monorepo/contents/content/awards-archive/foo.md", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "abc"})
	})
	c := newTestClient(t, mux)
	if err := c.DeleteFile(context.Background(), "content/awards-archive/foo.md", "award: delete foo"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(inputs) != 1 {
		t.Fatalf("want 1 commit, got %d", len(inputs))
	}
	dels := inputs[0].FileChanges.Deletions
	if len(dels) != 1 || dels[0].Path != "content/awards-archive/foo.md" || len(inputs[0].FileChanges.Additions) != 0 {
		t.Fatalf("unexpected file changes: %+v", inputs[0].FileChanges)
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
