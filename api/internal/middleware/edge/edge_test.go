package edge

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyOrigin(t *testing.T) {
	var sawHeader string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeader = r.Header.Get(OriginVerifyHeader)
		w.WriteHeader(http.StatusTeapot)
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cases := []struct {
		name    string
		secret  string
		enforce bool
		header  string
		want    int
	}{
		{"matching secret passes", "s3cret", true, "s3cret", http.StatusTeapot},
		{"missing header rejected", "s3cret", true, "", http.StatusForbidden},
		{"wrong secret rejected", "s3cret", true, "nope", http.StatusForbidden},
		{"prefix of secret rejected", "s3cret", true, "s3c", http.StatusForbidden},
		{"report-only lets mismatch through", "s3cret", false, "nope", http.StatusTeapot},
		{"empty secret disables check", "", true, "", http.StatusTeapot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sawHeader = "unset"
			req := httptest.NewRequest(http.MethodPost, "/numun.v1.UserService/GetMe", nil)
			if tc.header != "" {
				req.Header.Set(OriginVerifyHeader, tc.header)
			}
			rec := httptest.NewRecorder()
			VerifyOrigin(tc.secret, tc.enforce, logger)(next).ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if tc.want == http.StatusTeapot && sawHeader != "" {
				t.Errorf("origin-verify header leaked to handler: %q", sawHeader)
			}
		})
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{
			"viewer header wins over spoofed XFF",
			map[string]string{ViewerIPHeader: "203.0.113.7", "X-Forwarded-For": "1.2.3.4, 203.0.113.7, 130.176.0.1"},
			"203.0.113.7",
		},
		{"XFF first hop fallback", map[string]string{"X-Forwarded-For": "198.51.100.1, 10.0.0.1"}, "198.51.100.1"},
		{"X-Real-Ip fallback", map[string]string{"X-Real-Ip": "198.51.100.2"}, "198.51.100.2"},
		{"unknown", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range tc.headers {
				h.Set(k, v)
			}
			if got := ClientIP(h); got != tc.want {
				t.Errorf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}
