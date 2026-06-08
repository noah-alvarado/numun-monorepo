package parse

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
)

// emailRe is the BULK_IMPORT.md §3.3 RFC-5322-lite check.
var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// experienceAliases maps lowercased input values to the canonical enum. See
// BULK_IMPORT.md §3.2.
var experienceAliases = map[string]v1.ExperienceLevel{
	"novice":       v1.ExperienceLevel_EXPERIENCE_LEVEL_NOVICE,
	"beginner":     v1.ExperienceLevel_EXPERIENCE_LEVEL_NOVICE,
	"n":            v1.ExperienceLevel_EXPERIENCE_LEVEL_NOVICE,
	"intermediate": v1.ExperienceLevel_EXPERIENCE_LEVEL_INTERMEDIATE,
	"mid":          v1.ExperienceLevel_EXPERIENCE_LEVEL_INTERMEDIATE,
	"medium":       v1.ExperienceLevel_EXPERIENCE_LEVEL_INTERMEDIATE,
	"i":            v1.ExperienceLevel_EXPERIENCE_LEVEL_INTERMEDIATE,
	"advanced":     v1.ExperienceLevel_EXPERIENCE_LEVEL_ADVANCED,
	"experienced":  v1.ExperienceLevel_EXPERIENCE_LEVEL_ADVANCED,
	"senior":       v1.ExperienceLevel_EXPERIENCE_LEVEL_ADVANCED,
	"a":            v1.ExperienceLevel_EXPERIENCE_LEVEL_ADVANCED,
}

// cellAt returns the trimmed value at column index, or "" if out of range.
func cellAt(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

// splitFullName splits on the LAST whitespace run. "Mary Jane Smith" yields
// ("Mary Jane", "Smith"); "Madonna" yields ("", "Madonna"). See
// BULK_IMPORT.md §3.1.
func splitFullName(full string) (first, last string) {
	full = strings.TrimSpace(full)
	if full == "" {
		return "", ""
	}
	lastIdx := -1
	lastWidth := 0
	for i, r := range full {
		if unicode.IsSpace(r) {
			lastIdx = i
			lastWidth = utf8.RuneLen(r)
		}
	}
	if lastIdx < 0 {
		return "", full
	}
	return strings.TrimSpace(full[:lastIdx]), strings.TrimSpace(full[lastIdx+lastWidth:])
}

// EmailValid reports whether s matches the RFC-5322-lite regex used by the
// parser. Exported so the bulk-import handler can re-validate inline edits
// at commit time.
func EmailValid(s string) bool { return emailRe.MatchString(s) }

// normalizeEmail trims, lowercases, and returns "" for empty inputs.
func normalizeEmail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.ToLower(s)
}
