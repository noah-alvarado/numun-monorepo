package parse

import (
	"fmt"
	"strings"

	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
)

// buildRow assembles one PreviewRow from a raw data row. rowNumber is
// 1-indexed and excludes the header. The Match oneof is left unset; the
// handler fills it in once it has the existing-roster context, except for
// same-upload conflicts which detectSameUploadConflicts handles after all
// rows are built.
func buildRow(rowNumber int32, hdr headerMap, raw []string) *v1.PreviewRow {
	input := &v1.DelegateInput{}
	var violations []*v1.FieldViolation

	if idx, ok := hdr[colFullName]; ok {
		first, last := splitFullName(cellAt(raw, idx))
		input.FirstName = first
		input.LastName = last
	} else {
		if idx, ok := hdr[colFirstName]; ok {
			input.FirstName = cellAt(raw, idx)
		}
		if idx, ok := hdr[colLastName]; ok {
			input.LastName = cellAt(raw, idx)
		}
	}

	if input.FirstName == "" {
		violations = append(violations, &v1.FieldViolation{
			Field:   "firstName",
			Message: "firstName must not be empty",
		})
	}
	if input.LastName == "" {
		violations = append(violations, &v1.FieldViolation{
			Field:   "lastName",
			Message: "lastName must not be empty",
		})
	}

	if idx, ok := hdr[colEmail]; ok {
		em := normalizeEmail(cellAt(raw, idx))
		input.Email = em
		if em != "" && !emailRe.MatchString(em) {
			violations = append(violations, &v1.FieldViolation{
				Field:   "email",
				Message: "invalid format",
			})
		}
	}

	if idx, ok := hdr[colExperience]; ok {
		raw := strings.ToLower(cellAt(raw, idx))
		if raw == "" {
			input.ExperienceLevel = v1.ExperienceLevel_EXPERIENCE_LEVEL_INTERMEDIATE
		} else if lvl, ok := experienceAliases[raw]; ok {
			input.ExperienceLevel = lvl
		} else {
			input.ExperienceLevel = v1.ExperienceLevel_EXPERIENCE_LEVEL_UNSPECIFIED
			violations = append(violations, &v1.FieldViolation{
				Field:   "experienceLevel",
				Message: fmt.Sprintf("unknown experience level %q", raw),
			})
		}
	} else {
		input.ExperienceLevel = v1.ExperienceLevel_EXPERIENCE_LEVEL_INTERMEDIATE
	}

	return &v1.PreviewRow{
		RowNumber: rowNumber,
		Input:     input,
		Errors:    violations,
	}
}

// DedupeKey returns the cross-row matching key per BULK_IMPORT.md §6.1:
// lowercased trimmed email if present, else normalized "first last".
// Empty key means the row has no matchable identity (e.g. all blank) and is
// excluded from conflict detection. Exported so the bulk-import handler can
// reuse the same logic when matching uploaded rows to the live roster.
func DedupeKey(in *v1.DelegateInput) string { return dedupeKey(in) }

func dedupeKey(in *v1.DelegateInput) string {
	if in == nil {
		return ""
	}
	if em := strings.TrimSpace(strings.ToLower(in.Email)); em != "" {
		return "email:" + em
	}
	name := strings.TrimSpace(strings.ToLower(in.FirstName + " " + in.LastName))
	name = collapseSpaces(name)
	if name == "" {
		return ""
	}
	return "name:" + name
}

func collapseSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				b.WriteRune(' ')
			}
			prevSpace = true
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// detectSameUploadConflicts annotates each row whose dedupe key collides with
// an earlier row's via the Match oneof's conflict variant. Both rows are
// marked, pointing at each other's earliest conflict partner. See
// BULK_IMPORT.md §6.3.
func detectSameUploadConflicts(rows []*v1.PreviewRow) {
	first := map[string]int{}
	for i, r := range rows {
		key := dedupeKey(r.Input)
		if key == "" {
			continue
		}
		if j, seen := first[key]; seen {
			reason := fmt.Sprintf("duplicate of row %d", rows[j].RowNumber)
			r.Match = &v1.PreviewRow_Conflict{
				Conflict: &v1.PreviewRow_ConflictMatch{
					WithRowNumber: rows[j].RowNumber,
					Reason:        reason,
				},
			}
			// Mark the original too if not already a conflict; later
			// duplicates still point at the first occurrence.
			if _, isConflict := rows[j].Match.(*v1.PreviewRow_Conflict); !isConflict {
				rows[j].Match = &v1.PreviewRow_Conflict{
					Conflict: &v1.PreviewRow_ConflictMatch{
						WithRowNumber: r.RowNumber,
						Reason:        fmt.Sprintf("duplicate of row %d", r.RowNumber),
					},
				}
			}
			continue
		}
		first[key] = i
	}
}
