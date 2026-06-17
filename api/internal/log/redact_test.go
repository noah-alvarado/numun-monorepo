package log

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactsTopLevelSensitiveKeys(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewJSON(buf, nil)
	logger.Info("login",
		"userId", "u-1",
		"password", "hunter2",
		"refresh_token", "rt-abc",
		"x-csrf-token", "csrf-xyz",
	)

	m := parseJSON(t, buf.Bytes())
	if m["userId"] != "u-1" {
		t.Errorf("userId clobbered: %v", m["userId"])
	}
	for _, k := range []string{"password", "refresh_token", "x-csrf-token"} {
		if v, _ := m[k].(string); v != redactedValue {
			t.Errorf("expected %q redacted, got %v", k, m[k])
		}
	}
}

func TestRedactsNestedGroup(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewJSON(buf, nil)
	logger.Info("session",
		slog.Group("session",
			slog.String("id", "sess-1"),
			slog.String("refresh_token", "rt-xyz"),
			slog.Group("inner",
				slog.String("authorization", "Bearer leak"),
				slog.String("safe", "ok"),
			),
		),
	)

	m := parseJSON(t, buf.Bytes())
	session, ok := m["session"].(map[string]any)
	if !ok {
		t.Fatalf("session group missing: %#v", m)
	}
	if session["id"] != "sess-1" {
		t.Errorf("id clobbered: %v", session["id"])
	}
	if v, _ := session["refresh_token"].(string); v != redactedValue {
		t.Errorf("nested refresh_token not redacted: %v", session["refresh_token"])
	}
	inner, ok := session["inner"].(map[string]any)
	if !ok {
		t.Fatalf("inner group missing: %#v", session)
	}
	if v, _ := inner["authorization"].(string); v != redactedValue {
		t.Errorf("doubly-nested authorization not redacted: %v", inner["authorization"])
	}
	if inner["safe"] != "ok" {
		t.Errorf("safe attr clobbered: %v", inner["safe"])
	}
}

func TestCaseInsensitive(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewJSON(buf, nil)
	logger.Info("headers",
		"Authorization", "Basic ZXZpbA==",
		"Cookie", "numun_session=abc",
		"Set-Cookie", "csrf_token=xyz",
	)
	m := parseJSON(t, buf.Bytes())
	for _, k := range []string{"Authorization", "Cookie", "Set-Cookie"} {
		if v, _ := m[k].(string); v != redactedValue {
			t.Errorf("expected %q redacted (case-insensitive); got %v", k, m[k])
		}
	}
}

func TestWithAttrsRedactsBoundFields(t *testing.T) {
	buf := &bytes.Buffer{}
	root := NewJSON(buf, nil)
	bound := root.With("refresh_token", "rt-bound", "request_id", "req-1")
	bound.Info("dispatch")

	m := parseJSON(t, buf.Bytes())
	if v, _ := m["refresh_token"].(string); v != redactedValue {
		t.Errorf("bound refresh_token not redacted: %v", m["refresh_token"])
	}
	if m["request_id"] != "req-1" {
		t.Errorf("request_id clobbered: %v", m["request_id"])
	}
}

func TestEnabledRespectsLevel(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(NewRedactingHandler(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	logger.Info("dropped")
	if buf.Len() != 0 {
		t.Fatalf("info should be filtered out: %q", buf.String())
	}
	logger.Warn("kept", "password", "x")
	out := buf.String()
	if !strings.Contains(out, `"kept"`) || !strings.Contains(out, redactedValue) {
		t.Fatalf("warn missing or unredacted: %q", out)
	}
}

func TestRedactedFieldsHasExpectedSet(t *testing.T) {
	// Guard against accidental shrinkage of the redaction set.
	want := []string{
		"password",
		"refresh_token",
		"access_token",
		"id_token",
		"client_secret",
		"csrf_token",
		"authorization",
		"cookie",
		"set-cookie",
	}
	for _, k := range want {
		if _, ok := RedactedFields[k]; !ok {
			t.Errorf("RedactedFields missing %q", k)
		}
	}
}

func parseJSON(t *testing.T, b []byte) map[string]any {
	t.Helper()
	// slog writes one JSON object per record + trailing newline.
	b = bytes.TrimSpace(b)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse: %v\nbody=%s", err, b)
	}
	return m
}

func TestRedactingHandlerInterfaceCompat(t *testing.T) {
	var _ slog.Handler = (*redactingHandler)(nil)
	// Exercise Enabled to silence the unused-import linter on context.
	h := NewRedactingHandler(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	_ = h.Enabled(context.Background(), slog.LevelInfo)
}
