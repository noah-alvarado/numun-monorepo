// Package observability wires Sentry into the API. The init helpers no-op
// when SENTRY_DSN is empty so `make dev` runs without external errors.
//
// Configuration (env vars, all optional):
//
//   - SENTRY_DSN          — Sentry project DSN. If empty, Sentry is disabled.
//   - SENTRY_ENVIRONMENT  — environment tag (test|prod|...). Defaults to ENV_NAME.
//   - SENTRY_RELEASE      — release tag (commit SHA). Defaults to COMMIT_SHA.
//   - SENTRY_TRACES_SAMPLE_RATE — float, default 0.0 (errors only).
//
// All events flow through a BeforeSend hook that strips the same field set
// the slog redaction wrapper covers (api/internal/log). Logs and Sentry
// breadcrumbs stay aligned: a field that never appears in logs never appears
// in Sentry either.
package observability

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"

	numunlog "github.com/numun/numun/api/internal/log"
)

// FlushTimeout is the default time we wait for the Sentry transport to drain
// before a Lambda invocation exits.
const FlushTimeout = 2 * time.Second

// InitFromEnv configures the global Sentry hub from environment variables.
// Returns true when Sentry is active (DSN was set) so callers can decide
// whether to defer Flush. Errors during init are logged, not returned —
// telemetry must never crash the process.
func InitFromEnv(component string, logger *slog.Logger) bool {
	dsn := os.Getenv("SENTRY_DSN")
	if dsn == "" {
		return false
	}
	if logger == nil {
		logger = slog.Default()
	}

	env := firstNonEmpty(os.Getenv("SENTRY_ENVIRONMENT"), os.Getenv("ENV_NAME"), "unknown")
	release := firstNonEmpty(os.Getenv("SENTRY_RELEASE"), os.Getenv("COMMIT_SHA"))

	tracesRate := 0.0
	if raw := os.Getenv("SENTRY_TRACES_SAMPLE_RATE"); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			tracesRate = v
		}
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      env,
		Release:          release,
		ServerName:       component,
		AttachStacktrace: true,
		TracesSampleRate: tracesRate,
		BeforeSend:       scrub,
	})
	if err != nil {
		logger.Warn("sentry init failed", "err", err)
		return false
	}
	sentry.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetTag("component", component)
	})
	return true
}

// Flush drains the queued events. Safe to call on a process where Sentry was
// not initialized — no-op.
func Flush() {
	sentry.Flush(FlushTimeout)
}

// HTTPMiddleware wraps an http.Handler so panics and request context flow to
// Sentry. Safe to call before InitFromEnv (returns the handler unchanged when
// Sentry is disabled).
func HTTPMiddleware(next http.Handler) http.Handler {
	if sentry.CurrentHub().Client() == nil {
		return next
	}
	return sentryhttp.New(sentryhttp.Options{
		Repanic:         true,
		WaitForDelivery: false,
	}).Handle(next)
}

// CaptureWithUser sends an error with a userId tag attached. Use from Lambda
// handlers (cognito-post-confirmation, email-worker, email-feedback) where
// there's no HTTP-derived hub.
func CaptureWithUser(ctx context.Context, userID string, err error) {
	if err == nil || sentry.CurrentHub().Client() == nil {
		return
	}
	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		hub = sentry.CurrentHub().Clone()
	}
	if userID != "" {
		hub.Scope().SetUser(sentry.User{ID: userID})
	}
	hub.CaptureException(err)
}

// scrub is the BeforeSend hook. It walks the event in place and replaces
// values whose key matches the redaction set with "[REDACTED]". Mirrors the
// slog redaction wrapper (api/internal/log).
func scrub(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if event == nil {
		return nil
	}
	scrubStrMap(event.Tags)
	for k, ctx := range event.Contexts {
		// Contexts is map[string]Context where Context = map[string]any. Walk
		// each context map recursively so nested keys (e.g., "request.headers")
		// also get redacted.
		scrubAnyMap(map[string]any(ctx))
		event.Contexts[k] = ctx
	}
	for i := range event.Breadcrumbs {
		if event.Breadcrumbs[i] == nil {
			continue
		}
		scrubAnyMap(event.Breadcrumbs[i].Data)
	}
	if event.Request != nil {
		scrubStrMap(event.Request.Headers)
		scrubStrMap(event.Request.Env)
		// Cookies is a single semicolon-delimited string; nuke it entirely
		// if present — every cookie name we set is sensitive.
		if event.Request.Cookies != "" {
			event.Request.Cookies = "[REDACTED]"
		}
	}
	return event
}

func scrubStrMap(m map[string]string) {
	for k := range m {
		if isSensitive(k) {
			m[k] = "[REDACTED]"
		}
	}
}

func scrubAnyMap(m map[string]any) {
	for k, v := range m {
		if isSensitive(k) {
			m[k] = "[REDACTED]"
			continue
		}
		if sub, ok := v.(map[string]any); ok {
			scrubAnyMap(sub)
		}
	}
}

func isSensitive(k string) bool {
	lk := strings.ToLower(k)
	if _, ok := numunlog.RedactedFields[lk]; ok {
		return true
	}
	stripped := strings.ReplaceAll(strings.ReplaceAll(lk, "-", ""), "_", "")
	_, ok := numunlog.RedactedFields[stripped]
	return ok
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
