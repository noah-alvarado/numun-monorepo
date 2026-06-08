package parse

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractSheetCoords(t *testing.T) {
	cases := []struct {
		name, in, id, gid string
		wantErr           bool
	}{
		{"basic", "https://docs.google.com/spreadsheets/d/abc123/edit", "abc123", "", false},
		{"with-gid-query", "https://docs.google.com/spreadsheets/d/abc123/edit?gid=42", "abc123", "42", false},
		{"with-gid-fragment", "https://docs.google.com/spreadsheets/d/abc123/edit#gid=99", "abc123", "99", false},
		{"wrong-host", "https://evil.com/spreadsheets/d/abc/edit", "", "", true},
		{"wrong-scheme", "ftp://docs.google.com/spreadsheets/d/abc/edit", "", "", true},
		{"wrong-path", "https://docs.google.com/foo/bar", "", "", true},
		{"malformed", "::::not a url::::", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, gid, err := extractSheetCoords(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err: want %v, got %v", c.wantErr, err)
			}
			if !c.wantErr && (id != c.id || gid != c.gid) {
				t.Errorf("want (%q,%q), got (%q,%q)", c.id, c.gid, id, gid)
			}
		})
	}
}

// sheetsRoundTripper intercepts calls to docs.google.com and serves them
// from a local test server. Used to drive the sheets parser deterministically.
type sheetsRoundTripper struct {
	srv *httptest.Server
}

func (rt *sheetsRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme = "http"
	r.URL.Host = rt.srv.Listener.Addr().String()
	r.Host = r.URL.Host
	return http.DefaultTransport.RoundTrip(r)
}

func newSheetsClient(srv *httptest.Server) *http.Client {
	return &http.Client{Transport: &sheetsRoundTripper{srv: srv}}
}

func TestParseSheet_SingleTabSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("format")
		switch q {
		case "html":
			// Single sheet — HTML export lists one <sheet name="...">
			_, _ = w.Write([]byte(`<workbook><sheet name="Roster"/></workbook>`))
		case "csv":
			w.Header().Set("Content-Type", "text/csv")
			_, _ = w.Write([]byte("firstName,lastName\nJane,Doe\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	res, err := Parse(ParseRequest{
		SheetURL:   "https://docs.google.com/spreadsheets/d/abc/edit",
		HTTPClient: newSheetsClient(srv),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0].Input.GetFirstName() != "Jane" {
		t.Errorf("unexpected rows: %+v", res.Rows)
	}
}

func TestParseSheet_MultiTabReturnsTabs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("format")
		if q == "html" {
			_, _ = w.Write([]byte(`<workbook><sheet name="Roster"/><sheet name="Advisors"/></workbook>`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	res, err := Parse(ParseRequest{
		SheetURL:   "https://docs.google.com/spreadsheets/d/abc/edit",
		HTTPClient: newSheetsClient(srv),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.AvailableTabs) != 2 {
		t.Errorf("want 2 tabs, got %v", res.AvailableTabs)
	}
	if len(res.Rows) != 0 {
		t.Errorf("multi-tab without gid: rows should be empty, got %d", len(res.Rows))
	}
}

func TestParseSheet_NotPubliclyViewable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := Parse(ParseRequest{
		SheetURL:   "https://docs.google.com/spreadsheets/d/abc/edit",
		HTTPClient: newSheetsClient(srv),
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("want ErrInvalidArgument, got %v", err)
	}
	if !strings.Contains(err.Error(), "publicly viewable") {
		t.Errorf("want 'publicly viewable' hint in error, got %v", err)
	}
}

func TestParseSheet_RequiresClient(t *testing.T) {
	_, err := Parse(ParseRequest{SheetURL: "https://docs.google.com/spreadsheets/d/abc/edit"})
	if err == nil || !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("want ErrInvalidArgument for missing client, got %v", err)
	}
}

func TestParseSheet_GidFragmentInURL(t *testing.T) {
	var seenGID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("format") {
		case "html":
			_, _ = w.Write([]byte(`<workbook><sheet name="A"/><sheet name="B"/></workbook>`))
		case "csv":
			seenGID = r.URL.Query().Get("gid")
			_, _ = w.Write([]byte("firstName,lastName\nJ,D\n"))
		}
	}))
	defer srv.Close()

	// gid in URL fragment should pick that tab implicitly even though >1 tab exists.
	_, err := Parse(ParseRequest{
		SheetURL:   "https://docs.google.com/spreadsheets/d/abc/edit#gid=42",
		HTTPClient: newSheetsClient(srv),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenGID != "42" {
		t.Errorf("export should have received gid=42, got %q", seenGID)
	}
}

