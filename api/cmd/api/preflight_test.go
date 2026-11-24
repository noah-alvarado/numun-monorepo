package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSPreflight(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := corsPreflight(next)

	cases := []struct {
		name   string
		method string
		acrm   string
		want   int
	}{
		{"preflight short-circuits", http.MethodOptions, "POST", http.StatusNoContent},
		{"bare OPTIONS passes through", http.MethodOptions, "", http.StatusTeapot},
		{"POST passes through", http.MethodPost, "", http.StatusTeapot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/numun.v1.AuthService/Exchange", nil)
			if tc.acrm != "" {
				req.Header.Set("Access-Control-Request-Method", tc.acrm)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("got %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
