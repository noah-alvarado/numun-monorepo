package parse

import (
	"errors"
	"fmt"
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

// htmlview renders tabs the way Google's /htmlview page lists them; args
// are name, gid pairs.
func htmlview(nameGID ...string) string {
	var b strings.Builder
	b.WriteString("<html><script>var items = [];")
	for i := 0; i+1 < len(nameGID); i += 2 {
		fmt.Fprintf(&b, `items.push({name: "%s", pageUrl: "https:\/\/docs.google.com\/spreadsheets\/d\/abc\/htmlview\/sheet?headers\x3dtrue&gid=%s", gid: "%s",initialSheet: false});`, nameGID[i], nameGID[i+1], nameGID[i+1])
	}
	b.WriteString("</script></html>")
	return b.String()
}

// fakeSheet serves /htmlview with the given tabs and records the gid of
// each CSV export.
func fakeSheet(t *testing.T, seenGID *string, nameGID ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/htmlview"):
			_, _ = w.Write([]byte(htmlview(nameGID...)))
		case strings.HasSuffix(r.URL.Path, "/export") && r.URL.Query().Get("format") == "csv":
			if seenGID != nil {
				*seenGID = r.URL.Query().Get("gid")
			}
			w.Header().Set("Content-Type", "text/csv")
			_, _ = w.Write([]byte("firstName,lastName\nJane,Doe\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestParseSheet_SingleTabSuccess(t *testing.T) {
	var seenGID string
	srv := fakeSheet(t, &seenGID, "Roster", "917")

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
	if seenGID != "917" {
		t.Errorf("a single-tab sheet should export that tab's gid, got %q", seenGID)
	}
}

func TestParseSheet_MultiTabReturnsTabs(t *testing.T) {
	srv := fakeSheet(t, nil, "Roster", "0", "Fall \\x26 Spring", "55")

	res, err := Parse(ParseRequest{
		SheetURL:   "https://docs.google.com/spreadsheets/d/abc/edit",
		HTTPClient: newSheetsClient(srv),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.AvailableTabs) != 2 || res.AvailableTabs[1] != "Fall & Spring" {
		t.Errorf("want [Roster, Fall & Spring], got %v", res.AvailableTabs)
	}
	if len(res.Rows) != 0 {
		t.Errorf("multi-tab without gid: rows should be empty, got %d", len(res.Rows))
	}
}

func TestParseSheet_TabNameSelectsThatTab(t *testing.T) {
	var seenGID string
	srv := fakeSheet(t, &seenGID, "Roster", "0", "Advisors", "55")

	res, err := Parse(ParseRequest{
		SheetURL:   "https://docs.google.com/spreadsheets/d/abc/edit",
		TabName:    "Advisors",
		HTTPClient: newSheetsClient(srv),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenGID != "55" || len(res.Rows) != 1 {
		t.Errorf("want export of gid 55 with 1 row, got gid %q and %d rows", seenGID, len(res.Rows))
	}

	_, err = Parse(ParseRequest{
		SheetURL:   "https://docs.google.com/spreadsheets/d/abc/edit",
		TabName:    "Missing",
		HTTPClient: newSheetsClient(srv),
	})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), `no tab named "Missing"`) {
		t.Errorf("want a no-such-tab error, got %v", err)
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
	if !strings.Contains(err.Error(), "Anyone with the link") {
		t.Errorf("want a sharing hint in the error, got %v", err)
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
	srv := fakeSheet(t, &seenGID, "A", "0", "B", "42")

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
