// Package cms implements the inline write-back from AwardService mutations
// to the static CMS content tree living in git. The Lambdalith authenticates
// to GitHub as a GitHub App with `contents: write` on the monorepo, generates
// an installation access token on demand, and PUT/DELETEs files via the
// Contents API. A site rebuild kicks off automatically when the resulting
// commit touches /content/**.
//
// scope-check: skip — this package has no Connect handlers; authorization
// happens upstream in /api/internal/handlers/awards.go.
package cms

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	mrand "math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// installationTokenLifetime is the GitHub-imposed maximum; refresh ~10
	// minutes before the hard ceiling.
	installationTokenLifetime = 60 * time.Minute
	installationTokenRenewBy  = 10 * time.Minute

	defaultBranch           = "main"
	defaultRequestTimeout   = 10 * time.Second
	defaultRetryBaseBackoff = 200 * time.Millisecond
)

// Config carries the values resolved at boot (typically from SSM).
type Config struct {
	AppID          string
	InstallationID string
	// PrivateKeyPEM is the RSA PEM string downloaded when the GitHub App was
	// created. Stored as SSM SecureString in prod.
	PrivateKeyPEM string
	// Repo is "owner/name", e.g. "numun/numun-monorepo".
	Repo string
	// Branch the commits should land on. Defaults to "main".
	Branch string
	// APIBaseURL allows tests to point at an httptest server. Defaults to
	// https://api.github.com.
	APIBaseURL string
}

// Client wraps the GitHub App + Contents API interactions. Safe for concurrent
// use after New(). The installation access token is cached + refreshed lazily.
type Client struct {
	cfg        Config
	httpClient *http.Client
	logger     *slog.Logger
	privateKey *rsa.PrivateKey

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
	// stub mode (set in DEV_MODE / when SSM creds aren't present) skips all
	// network I/O and returns ok results so make dev keeps working.
	stub bool
}

// Status reports the final outcome of a CMS sync attempt. Mirrors the
// CmsSyncStatus proto shape.
type Status struct {
	OK         bool
	Attempts   int
	FinalError string
	CommitSHA  string
}

// NewStub returns a Client that swallows every call and reports ok. Use when
// SSM credentials aren't available locally (DEV_MODE).
func NewStub(logger *slog.Logger) *Client {
	return &Client{stub: true, logger: logger}
}

// New constructs a Client from Config. The private key PEM is parsed once
// here; misformatted PEM fails fast at boot rather than on the first sync.
func New(cfg Config, logger *slog.Logger) (*Client, error) {
	if cfg.AppID == "" || cfg.InstallationID == "" || cfg.PrivateKeyPEM == "" || cfg.Repo == "" {
		return nil, errors.New("cms: AppID, InstallationID, PrivateKeyPEM, and Repo are required")
	}
	if cfg.Branch == "" {
		cfg.Branch = defaultBranch
	}
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = "https://api.github.com"
	}
	pk, err := parseRSAPrivateKey(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("cms: parse private key: %w", err)
	}
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: defaultRequestTimeout},
		logger:     logger,
		privateKey: pk,
	}, nil
}

func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	// GitHub-issued keys are PKCS#1 ("RSA PRIVATE KEY"); newer keys may be
	// PKCS#8 ("PRIVATE KEY"). Try PKCS#1 first.
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an RSA key")
	}
	return rk, nil
}

// IsStub reports whether the client is in stub mode.
func (c *Client) IsStub() bool { return c != nil && c.stub }

// UpsertFile creates or updates the file at path with the given content. The
// commit lands on c.cfg.Branch with the supplied message. Returns the commit
// SHA on success.
func (c *Client) UpsertFile(ctx context.Context, path string, content []byte, commitMessage string) (string, error) {
	if c.stub {
		return "", nil
	}
	existingSHA, err := c.fileSHA(ctx, path)
	if err != nil {
		return "", err
	}
	body := contentsWriteBody{
		Message: commitMessage,
		Branch:  c.cfg.Branch,
		Content: base64.StdEncoding.EncodeToString(content),
		SHA:     existingSHA,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	resp, err := c.do(ctx, http.MethodPut, contentsURL(c.cfg, path), raw)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github upsert %s: %s", path, readErr(resp))
	}
	var parsed contentsWriteResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode upsert response: %w", err)
	}
	return parsed.Commit.SHA, nil
}

// DeleteFile removes the file at path. A missing file is treated as success
// (idempotent delete) per IMPLEMENTATION_PLAN.md M11.
func (c *Client) DeleteFile(ctx context.Context, path, commitMessage string) error {
	if c.stub {
		return nil
	}
	existingSHA, err := c.fileSHA(ctx, path)
	if err != nil {
		return err
	}
	if existingSHA == "" {
		return nil // already gone
	}
	body, err := json.Marshal(contentsWriteBody{
		Message: commitMessage,
		Branch:  c.cfg.Branch,
		SHA:     existingSHA,
	})
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, http.MethodDelete, contentsURL(c.cfg, path), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github delete %s: %s", path, readErr(resp))
	}
	return nil
}

// WithRetry runs fn up to 3 times with exponential backoff (200ms, 800ms,
// 3.2s + 0–200ms jitter). The returned Status captures the final outcome.
// In stub mode, fn still runs once but the result is ignored — the stub
// methods return nil error so attempts=1, ok=true.
func (c *Client) WithRetry(ctx context.Context, fn func(context.Context) (string, error)) Status {
	if c == nil {
		// Defensive: a nil client should still behave like a stub.
		return Status{OK: true, Attempts: 0}
	}
	var lastErr error
	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		sha, err := fn(ctx)
		if err == nil {
			return Status{OK: true, Attempts: attempt, CommitSHA: sha}
		}
		lastErr = err
		if attempt == maxAttempts {
			break
		}
		// 200ms * 4^(attempt-1) + jitter.
		base := defaultRetryBaseBackoff * time.Duration(1<<(2*(attempt-1)))
		jitter := time.Duration(mrand.Int63n(int64(200 * time.Millisecond)))
		wait := base + jitter
		if c.logger != nil {
			c.logger.Warn("cms: retrying after error", "attempt", attempt, "wait_ms", wait.Milliseconds(), "err", err)
		}
		select {
		case <-ctx.Done():
			return Status{OK: false, Attempts: attempt, FinalError: ctx.Err().Error()}
		case <-time.After(wait):
		}
	}
	return Status{OK: false, Attempts: maxAttempts, FinalError: lastErr.Error()}
}

// fileSHA returns the blob SHA for the given path on the configured branch,
// or "" when the file does not exist.
func (c *Client) fileSHA(ctx context.Context, path string) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, contentsURL(c.cfg, path)+"?ref="+c.cfg.Branch, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github get %s: %s", path, readErr(resp))
	}
	var parsed struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode get response: %w", err)
	}
	return parsed.SHA, nil
}

func contentsURL(cfg Config, path string) string {
	return cfg.APIBaseURL + "/repos/" + cfg.Repo + "/contents/" + path
}

func readErr(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return strconv.Itoa(resp.StatusCode) + " " + string(b)
}

type contentsWriteBody struct {
	Message string `json:"message"`
	Branch  string `json:"branch,omitempty"`
	Content string `json:"content,omitempty"`
	SHA     string `json:"sha,omitempty"`
}

type contentsWriteResponse struct {
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

func (c *Client) do(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	tok, err := c.installationToken(ctx)
	if err != nil {
		return nil, err
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.httpClient.Do(req)
}

// installationToken returns a cached installation access token, refreshing it
// when within installationTokenRenewBy of expiry.
func (c *Client) installationToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Until(c.tokenExpiry) > installationTokenRenewBy {
		return c.token, nil
	}
	appJWT, err := c.signAppJWT()
	if err != nil {
		return "", err
	}
	url := c.cfg.APIBaseURL + "/app/installations/" + c.cfg.InstallationID + "/access_tokens"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("installation token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("installation token: %s", readErr(resp))
	}
	var parsed struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode installation token: %w", err)
	}
	c.token = parsed.Token
	c.tokenExpiry = parsed.ExpiresAt
	if c.tokenExpiry.IsZero() {
		c.tokenExpiry = time.Now().Add(installationTokenLifetime)
	}
	return c.token, nil
}

func (c *Client) signAppJWT() (string, error) {
	now := time.Now().Add(-30 * time.Second) // small clock-skew tolerance
	claims := jwt.RegisteredClaims{
		Issuer:    c.cfg.AppID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(8 * time.Minute)),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return tok.SignedString(c.privateKey)
}

// nopCloser is used by tests that want to call do() against a static response.
type nopCloser struct{ io.Reader }

func (nopCloser) Close() error { return nil }

// randomBytes is exposed so tests can deterministically reseed.
var randomBytes = func(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}
