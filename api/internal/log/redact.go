// Package log provides the slog.Handler wrapper that redacts known-sensitive
// fields by name before they hit any sink. See SECURITY.md §5.4.
//
// Use New (or NewJSON) at every binary's entrypoint instead of constructing a
// slog.Handler directly. The wrapper:
//
//   - Walks every record attribute, including those inside slog.Groups.
//   - Replaces the value of any attribute whose key matches the redaction
//     set (case-insensitive) with the literal string "[REDACTED]".
//   - Recurses into LogValuer-resolved values and slog.GroupValue.
//   - Leaves non-sensitive attributes untouched.
//
// The redaction set is intentionally small and stable — the same set fed to
// the Sentry BeforeSend hook so logs and Sentry breadcrumbs stay aligned.
package log

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

// RedactedFields is the case-insensitive set of attribute keys whose values
// are replaced before being emitted. Mirrors the Sentry BeforeSend allowlist.
var RedactedFields = map[string]struct{}{
	"password":       {},
	"refresh_token":  {},
	"refreshtoken":   {},
	"access_token":   {},
	"accesstoken":    {},
	"id_token":       {},
	"idtoken":        {},
	"client_secret":  {},
	"clientsecret":   {},
	"csrf_token":     {},
	"csrftoken":      {},
	"x-csrf-token":   {},
	"authorization":  {},
	"cookie":         {},
	"set-cookie":     {},
	"session_cookie": {},
	"sessioncookie":  {},
}

const redactedValue = "[REDACTED]"

// NewJSON returns a slog.Logger whose root handler is a redacting JSON handler
// writing to w. Drop-in replacement for `slog.New(slog.NewJSONHandler(w, opts))`.
func NewJSON(w io.Writer, opts *slog.HandlerOptions) *slog.Logger {
	return slog.New(NewRedactingHandler(slog.NewJSONHandler(w, opts)))
}

// NewRedactingHandler wraps the given handler with the redaction filter.
// Exposed for callers that want to redact a non-JSON handler (text, tests).
func NewRedactingHandler(next slog.Handler) slog.Handler {
	return &redactingHandler{next: next}
}

type redactingHandler struct {
	next slog.Handler
}

func (h *redactingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	// Build a new record with redacted attrs. slog records are immutable in
	// spirit; we walk attrs and copy into a fresh record.
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = redactAttr(a)
	}
	return &redactingHandler{next: h.next.WithAttrs(redacted)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name)}
}

// redactAttr returns a copy of a with its value replaced by "[REDACTED]" when
// the key matches; otherwise recurses into group values.
func redactAttr(a slog.Attr) slog.Attr {
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, redactedValue)
	}
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		out := make([]any, 0, len(group)*2)
		for _, sub := range group {
			redacted := redactAttr(sub)
			out = append(out, redacted.Key, anyOfValue(redacted.Value))
		}
		return slog.Group(a.Key, out...)
	}
	if a.Value.Kind() == slog.KindLogValuer {
		// Resolve once and re-check; downstream handler would resolve again
		// otherwise, bypassing our filter.
		resolved := a.Value.Resolve()
		if resolved.Kind() == slog.KindGroup {
			return redactAttr(slog.Attr{Key: a.Key, Value: resolved})
		}
		return slog.Attr{Key: a.Key, Value: resolved}
	}
	return a
}

func isSensitiveKey(k string) bool {
	lk := strings.ToLower(k)
	if _, ok := RedactedFields[lk]; ok {
		return true
	}
	// Dash-style variants (e.g., "X-CSRF-Token") collapse to the underscore
	// form. Strip dashes and underscores to make matching robust.
	stripped := strings.ReplaceAll(strings.ReplaceAll(lk, "-", ""), "_", "")
	_, ok := RedactedFields[stripped]
	return ok
}

func anyOfValue(v slog.Value) any {
	// slog.Group's variadic API expects (key, value, key, value, ...). Round-
	// tripping through Value.Any preserves the kind without forcing a string.
	return v.Any()
}
