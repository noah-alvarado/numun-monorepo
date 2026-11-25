package parse

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	errNotASheetLink  = fmt.Errorf("%w: that isn't a Google Sheets link — copy it from the sheet's address bar (https://docs.google.com/spreadsheets/d/…)", ErrInvalidArgument)
	errSheetNotShared = fmt.Errorf("%w: we can't open that sheet — in Google Sheets, click Share and set General access to \"Anyone with the link\", then try again", ErrInvalidArgument)
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

	tabs, err := listSheetTabs(sheetID, client)
	if err != nil {
		return ParseResult{}, err
	}
	if tabName != "" {
		tabGID, ok := tabGIDByName(tabs, tabName)
		if !ok {
			return ParseResult{}, fmt.Errorf("%w: the sheet has no tab named %q", ErrInvalidArgument, tabName)
		}
		gid = tabGID
	} else if len(tabs) > 1 && gid == "" {
		// A gid in the URL is an implicit selection; otherwise make the
		// caller pick.
		names := make([]string, len(tabs))
		for i, t := range tabs {
			names[i] = t.name
		}
		return ParseResult{AvailableTabs: names}, nil
	}

	if gid == "" {
		gid = "0"
		if len(tabs) == 1 {
			gid = tabs[0].gid
		}
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
		return "", "", errNotASheetLink
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", "", errNotASheetLink
	}
	if !strings.EqualFold(u.Host, "docs.google.com") {
		return "", "", errNotASheetLink
	}
	m := sheetIDRe.FindStringSubmatch(u.Path)
	if m == nil {
		return "", "", errNotASheetLink
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
		return nil, errSheetNotShared
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

type sheetTab struct {
	name string
	gid  string
}

func tabGIDByName(tabs []sheetTab, name string) (string, bool) {
	for _, t := range tabs {
		if t.name == name {
			return t.gid, true
		}
	}
	return "", false
}

// sheetTabRe matches the per-tab entries in the /htmlview page's inline
// script: items.push({name: "Roster", pageUrl: "...", gid: "123"...
// The "export?format=html" endpoint can't be used: it returns 400.
var sheetTabRe = regexp.MustCompile(`items\.push\(\{name: "((?:[^"\\]|\\.)*)", pageUrl: "(?:[^"\\]|\\.)*", gid: "(\d+)"`)

// listSheetTabs reads the sheet's tab names and gids from /htmlview. If the
// page can't be read or has no tabs, it returns no tabs and no error so the
// caller falls back to the first tab (gid=0).
func listSheetTabs(sheetID string, client *http.Client) ([]sheetTab, error) {
	htmlURL := fmt.Sprintf("https://docs.google.com/spreadsheets/d/%s/htmlview", sheetID)
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
		return nil, errSheetNotShared
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil
	}
	var tabs []sheetTab
	seen := map[string]bool{}
	for _, m := range sheetTabRe.FindAllSubmatch(body, -1) {
		name := unescapeJSString(string(m[1]))
		if seen[name] {
			continue
		}
		seen[name] = true
		tabs = append(tabs, sheetTab{name: name, gid: string(m[2])})
	}
	return tabs, nil
}

// unescapeJSString decodes the escapes Google uses in inline script string
// literals (\xNN, \uNNNN, \/, \", \\).
func unescapeJSString(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	if out, err := strconv.Unquote(`"` + strings.ReplaceAll(s, `\/`, "/") + `"`); err == nil {
		return out
	}
	return s
}
