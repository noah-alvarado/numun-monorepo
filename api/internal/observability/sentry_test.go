package observability

import (
	"testing"

	"github.com/getsentry/sentry-go"
)

func TestScrubRedactsTagsAndContextsAndRequest(t *testing.T) {
	event := &sentry.Event{
		Tags: map[string]string{
			"request_id":    "req-1",
			"refresh_token": "rt-leak",
		},
		Contexts: map[string]sentry.Context{
			"auth": sentry.Context{
				"userId":       "u-1",
				"access_token": "leak-1",
				"x-csrf-token": "leak-2",
				"safe_nested": map[string]any{
					"id_token": "leak-3",
					"ok":       "kept",
				},
			},
		},
		Breadcrumbs: []*sentry.Breadcrumb{
			{
				Data: map[string]any{
					"authorization": "Bearer leak",
					"path":          "/numun.v1.X/Y",
				},
			},
			nil, // tolerate nils
		},
		Request: &sentry.Request{
			Headers: map[string]string{
				"X-CSRF-Token": "csrf-leak",
				"X-Request-Id": "req-1",
			},
			Cookies: "numun_session=abc; csrf_token=xyz",
		},
	}

	out := scrub(event, nil)
	if out == nil {
		t.Fatal("scrub returned nil for non-nil event")
	}

	if out.Tags["refresh_token"] != "[REDACTED]" {
		t.Errorf("tags.refresh_token not redacted: %q", out.Tags["refresh_token"])
	}
	if out.Tags["request_id"] != "req-1" {
		t.Errorf("tags.request_id clobbered: %q", out.Tags["request_id"])
	}

	auth := out.Contexts["auth"]
	if auth["access_token"] != "[REDACTED]" {
		t.Errorf("contexts.auth.access_token not redacted: %v", auth["access_token"])
	}
	if auth["x-csrf-token"] != "[REDACTED]" {
		t.Errorf("contexts.auth.x-csrf-token not redacted: %v", auth["x-csrf-token"])
	}
	if auth["userId"] != "u-1" {
		t.Errorf("contexts.auth.userId clobbered: %v", auth["userId"])
	}
	nested, _ := auth["safe_nested"].(map[string]any)
	if nested == nil {
		t.Fatalf("nested map missing: %#v", auth["safe_nested"])
	}
	if nested["id_token"] != "[REDACTED]" {
		t.Errorf("nested id_token not redacted: %v", nested["id_token"])
	}
	if nested["ok"] != "kept" {
		t.Errorf("nested.ok clobbered: %v", nested["ok"])
	}

	bc := out.Breadcrumbs[0]
	if bc.Data["authorization"] != "[REDACTED]" {
		t.Errorf("breadcrumb authorization not redacted: %v", bc.Data["authorization"])
	}
	if bc.Data["path"] != "/numun.v1.X/Y" {
		t.Errorf("breadcrumb.path clobbered: %v", bc.Data["path"])
	}

	if out.Request.Headers["X-CSRF-Token"] != "[REDACTED]" {
		t.Errorf("header X-CSRF-Token not redacted")
	}
	if out.Request.Headers["X-Request-Id"] != "req-1" {
		t.Errorf("header X-Request-Id clobbered: %v", out.Request.Headers["X-Request-Id"])
	}
	if out.Request.Cookies != "[REDACTED]" {
		t.Errorf("cookies not redacted: %q", out.Request.Cookies)
	}
}

func TestScrubNilEvent(t *testing.T) {
	if scrub(nil, nil) != nil {
		t.Fatal("scrub(nil) should return nil")
	}
}

func TestInitFromEnvDisabledWithoutDSN(t *testing.T) {
	t.Setenv("SENTRY_DSN", "")
	if InitFromEnv("test", nil) {
		t.Fatal("InitFromEnv should return false when SENTRY_DSN is empty")
	}
}

func TestIsSensitive(t *testing.T) {
	for _, k := range []string{
		"authorization", "Authorization",
		"x-csrf-token", "X-CSRF-Token",
		"refresh_token", "refreshToken",
		"set-cookie", "Cookie",
	} {
		if !isSensitive(k) {
			t.Errorf("isSensitive(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"request_id", "userId", "path", "duration_ms"} {
		if isSensitive(k) {
			t.Errorf("isSensitive(%q) = true, want false", k)
		}
	}
}
