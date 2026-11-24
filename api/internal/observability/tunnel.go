package observability

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/numun/numun/api/internal/httpclient"
)

// TunnelPath receives the portal's Sentry envelopes. Named neutrally so ad
// blockers' "sentry" rules don't match it.
const TunnelPath = "/v1/diag"

const maxEnvelopeBytes = 512 << 10

// Tunnel relays browser envelopes to one Sentry project, so ad blockers that
// block *.sentry.io don't drop portal errors.
type Tunnel struct {
	host      string
	projectID string
	client    *http.Client
}

// NewTunnel returns a Tunnel that only accepts envelopes addressed to dsn's
// project. The upstream URL is built from dsn, never from the request.
func NewTunnel(dsn string) (*Tunnel, error) {
	host, pid, err := parseDSN(dsn)
	if err != nil {
		return nil, err
	}
	return &Tunnel{
		host:      host,
		projectID: pid,
		client:    httpclient.NewWithOptions(httpclient.Options{ExtraExactHosts: []string{host}}),
	}, nil
}

// TunnelFromEnv builds the portal tunnel from /numun/${ENV}/sentry/dsn_portal.
// Returns nil when the DSN is unavailable (local dev, Sentry not set up).
func TunnelFromEnv(logger *slog.Logger) *Tunnel {
	if logger == nil {
		logger = slog.Default()
	}
	dsn := fetchSSMParam(logger, "sentry/dsn_portal")
	if dsn == "" {
		return nil
	}
	t, err := NewTunnel(dsn)
	if err != nil {
		logger.Warn("sentry tunnel: bad portal DSN", "err", err)
		return nil
	}
	return t
}

func (t *Tunnel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEnvelopeBytes))
	if err != nil {
		http.Error(w, "envelope too large", http.StatusRequestEntityTooLarge)
		return
	}
	header := body
	if i := bytes.IndexByte(body, '\n'); i >= 0 {
		header = body[:i]
	}
	var h struct {
		DSN string `json:"dsn"`
	}
	if err := json.Unmarshal(header, &h); err != nil {
		http.Error(w, "bad envelope", http.StatusBadRequest)
		return
	}
	if host, pid, err := parseDSN(h.DSN); err != nil || host != t.host || pid != t.projectID {
		http.Error(w, "bad envelope", http.StatusBadRequest)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"https://"+t.host+"/api/"+t.projectID+"/envelope/", bytes.NewReader(body))
	if err != nil {
		http.Error(w, "upstream", http.StatusBadGateway)
		return
	}
	req.Header.Set("Content-Type", "application/x-sentry-envelope")
	resp, err := t.client.Do(req)
	if err != nil {
		http.Error(w, "upstream", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	w.WriteHeader(resp.StatusCode)
}

func parseDSN(dsn string) (host, projectID string, err error) {
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", "", errors.New("invalid Sentry DSN")
	}
	pid := strings.Trim(u.Path, "/")
	if pid == "" || strings.Contains(pid, "/") {
		return "", "", errors.New("invalid Sentry DSN project")
	}
	return strings.ToLower(u.Host), pid, nil
}
