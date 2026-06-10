package parse

import (
	"fmt"
	"strings"
	"unicode"
)

// canonical column identifiers. These are not user-facing strings but stable
// keys used to look up which raw column a value came from.
const (
	colFirstName  = "firstName"
	colLastName   = "lastName"
	colFullName   = "fullName"
	colEmail      = "email"
	colExperience = "experienceLevel"
)

// aliasIndex maps a normalized (lowercased, non-alphanumeric stripped) header
// to its canonical column. See BULK_IMPORT.md §3.
var aliasIndex = map[string]string{
	// firstName
	"firstname": colFirstName,
	"givenname": colFirstName,
	"first":     colFirstName,
	// lastName
	"lastname":   colLastName,
	"surname":    colLastName,
	"familyname": colLastName,
	"last":       colLastName,
	// fullName
	"fullname": colFullName,
	"name":     colFullName,
	// email
	"email":        colEmail,
	"emailaddress": colEmail,
	"mail":         colEmail,
	// experienceLevel
	"experiencelevel": colExperience,
	"experience":      colExperience,
	"level":           colExperience,
	"tier":            colExperience,
}

// headerMap records, for each canonical column found in the file, the source
// column index. Canonical columns absent from the file are absent from the
// map.
type headerMap map[string]int

// normalizeHeader lowercases and strips all non-alphanumeric runes. See
// BULK_IMPORT.md §3.
func normalizeHeader(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// mapHeaders walks the header row, builds a headerMap of canonical-column ->
// source-column-index, returns the list of unrecognized column header names
// (for the summary), and enforces the conflicting-columns rule.
func mapHeaders(header []string) (headerMap, []string, error) {
	hdr := headerMap{}
	var ignored []string
	for i, raw := range header {
		key := normalizeHeader(raw)
		if key == "" {
			continue
		}
		canon, ok := aliasIndex[key]
		if !ok {
			ignored = append(ignored, strings.TrimSpace(raw))
			continue
		}
		// First occurrence wins; later duplicate aliases are ignored. This
		// keeps behavior deterministic if a file repeats e.g. "First Name"
		// and "first".
		if _, dup := hdr[canon]; dup {
			continue
		}
		hdr[canon] = i
	}

	_, hasFirst := hdr[colFirstName]
	_, hasLast := hdr[colLastName]
	_, hasFull := hdr[colFullName]
	if hasFull && (hasFirst || hasLast) {
		return nil, nil, fmt.Errorf("%w: conflicting columns: provide either fullName, or firstName+lastName, not both", ErrInvalidArgument)
	}

	return hdr, ignored, nil
}
