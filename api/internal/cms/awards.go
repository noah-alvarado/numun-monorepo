package cms

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/numun/numun/api/internal/domain"
)

// AwardMarkdownPath returns the stable CMS path for a given award. The
// filename is the immutable award ID so renames don't lose history.
func AwardMarkdownPath(awardID string) string {
	return "content/awards-archive/" + awardID + ".md"
}

// RenderAwardMarkdown produces the YAML-frontmatter markdown body the Astro
// content collection ingests. Body is intentionally empty; the public archive
// renders from frontmatter alone. Year is derived from the supplied conference
// year (caller passes the right value — usually conference.EndsAt year, fall
// back to conference.Year). DisplayName on each recipient should already be
// populated by the handler.
func RenderAwardMarkdown(a domain.Award, conferenceYear int) []byte {
	var buf bytes.Buffer
	buf.WriteString("---\n")
	fmt.Fprintf(&buf, "awardId: %s\n", yamlString(a.ID))
	fmt.Fprintf(&buf, "conferenceId: %s\n", yamlString(a.ConferenceID))
	if conferenceYear > 0 {
		fmt.Fprintf(&buf, "year: %d\n", conferenceYear)
	}
	fmt.Fprintf(&buf, "awardName: %s\n", yamlString(a.Name))
	if a.Category != "" {
		fmt.Fprintf(&buf, "category: %s\n", yamlString(a.Category))
	}
	if !a.AwardedAt.IsZero() {
		fmt.Fprintf(&buf, "awardedAt: %s\n", a.AwardedAt.UTC().Format(time.RFC3339))
	}
	if a.AwardedBy != "" {
		fmt.Fprintf(&buf, "awardedBy: %s\n", yamlString(a.AwardedBy))
	}
	buf.WriteString("recipients:\n")
	for _, r := range a.Recipients {
		buf.WriteString("  - kind: ")
		buf.WriteString(string(r.Kind))
		buf.WriteByte('\n')
		fmt.Fprintf(&buf, "    id: %s\n", yamlString(r.ID))
		if r.DisplayName != "" {
			fmt.Fprintf(&buf, "    displayName: %s\n", yamlString(r.DisplayName))
		}
	}
	buf.WriteString("---\n")
	return buf.Bytes()
}

// yamlString wraps any value that might need quoting in YAML. We always quote
// to keep the generator dumb-simple — Astro reads it the same either way.
func yamlString(s string) string {
	// Escape backslashes and double-quotes; everything else is fine inside a
	// double-quoted YAML scalar.
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}
