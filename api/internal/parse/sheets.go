package parse

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// sheetIDRe extracts the sheet id from URL paths of the form
// /spreadsheets/d/<id>/...
var sheetIDRe = regexp.MustCompile(`^/spreadsheets/d/([A-Za-z0-9_\-]+)(/.*)?$`)

// parseSheet fetches a public Google Sheet as CSV via the provided
// *http.Client (which carries any egress allow-list / timeout policy the
// caller imposes) and runs the standard CSV parser on the result.
func parseSheet(rawURL, tabName string, client *http.Client) (ParseResult, error) {
	sheetID, gid, err := extractSheetCoords(rawURL)
	if err != nil {
		return ParseResult{}, err
	}

	if tabName == "" {
		tabs, err := listSheetTabs(sheetID, client)
		if err != nil {
			return ParseResult{}, err
		}
		if len(tabs) > 1 {
			// If a gid is present in the URL we honour it as an implicit
			// selection; otherwise force the caller to pick.
			if gid == "" {
				return ParseResult{AvailableTabs: tabs}, nil
			}
		}
	}

	if gid == "" {
		gid = "0"
	}
	exportURL := fmt.Sprintf("https://docs.google.com/spreadsheets/d/%s/export?format=csv&gid=%s", sheetID, gid)
	body, err := fetchSheet(exportURL, client)
	if err != nil {
		return ParseResult{}, err
	}
	return parseCSV(bytes.NewReader(body))
}

// extractSheetCoords validates the URL is a docs.google.com spreadsheets URL
// and returns its sheet id and (optional) gid.
func extractSheetCoords(rawURL string) (string, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("%w: malformed sheets url", ErrInvalidArgument)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", "", fmt.Errorf("%w: sheets url must use https", ErrInvalidArgument)
	}
	if !strings.EqualFold(u.Host, "docs.google.com") {
		return "", "", fmt.Errorf("%w: sheets url must be on docs.google.com", ErrInvalidArgument)
	}
	m := sheetIDRe.FindStringSubmatch(u.Path)
	if m == nil {
		return "", "", fmt.Errorf("%w: sheets url path must be /spreadsheets/d/<id>/...", ErrInvalidArgument)
	}
	sheetID := m[1]

	gid := u.Query().Get("gid")
	if gid == "" && u.Fragment != "" {
		// Common form: ...#gid=12345
		for _, part := range strings.Split(u.Fragment, "&") {
			if strings.HasPrefix(part, "gid=") {
				gid = strings.TrimPrefix(part, "gid=")
				break
			}
		}
	}
	return sheetID, gid, nil
}

func fetchSheet(exportURL string, client *http.Client) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, exportURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build sheets request: %v", ErrInvalidArgument, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch sheet: %v", ErrInvalidArgument, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: sheet is not publicly viewable — set sharing to 'Anyone with the link can view'", ErrInvalidArgument)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: sheets export returned status %d", ErrInvalidArgument, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read sheets response: %v", ErrInvalidArgument, err)
	}
	return body, nil
}

// sheetTitleRe matches <sheet ... name="My Tab" ...> elements emitted by the
// Google Sheets HTML export. We intentionally don't pull in a full HTML
// parser; the pattern is stable enough for the listing use case.
var sheetTitleRe = regexp.MustCompile(`<sheet[^>]*\bname="([^"]+)"`)

// listSheetTabs fetches the HTML export and extracts the sheet tab names. If
// the request fails or no tabs are found, returns an empty slice with no
// error so the caller can fall back to gid=0 behaviour.
func listSheetTabs(sheetID string, client *http.Client) ([]string, error) {
	htmlURL := fmt.Sprintf("https://docs.google.com/spreadsheets/d/%s/export?format=html", sheetID)
	req, err := http.NewRequest(http.MethodGet, htmlURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build sheets html request: %v", ErrInvalidArgument, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch sheet tabs: %v", ErrInvalidArgument, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: sheet is not publicly viewable — set sharing to 'Anyone with the link can view'", ErrInvalidArgument)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil
	}
	matches := sheetTitleRe.FindAllSubmatch(body, -1)
	tabs := make([]string, 0, len(matches))
	seen := map[string]struct{}{}
	for _, m := range matches {
		name := string(m[1])
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		tabs = append(tabs, name)
	}
	return tabs, nil
}
