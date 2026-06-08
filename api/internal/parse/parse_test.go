package parse

import (
	"errors"
	"strings"
	"testing"

	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
)

func parseString(t *testing.T, s string) (ParseResult, error) {
	t.Helper()
	return Parse(ParseRequest{
		Reader: strings.NewReader(s),
		Format: v1.SourceFormat_SOURCE_FORMAT_CSV,
	})
}

func TestParseCSV_BasicAndBOM(t *testing.T) {
	src := "\xEF\xBB\xBFfirstName,lastName,email,experienceLevel\nJane,Doe,JANE@X.COM,advanced\nMary Jane,Smith,,\n"
	res, err := parseString(t, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(res.Rows))
	}
	if got := res.Rows[0].Input.GetEmail(); got != "jane@x.com" {
		t.Errorf("email lowercased+trimmed: got %q", got)
	}
	if res.Rows[0].Input.GetExperienceLevel() != v1.ExperienceLevel_EXPERIENCE_LEVEL_ADVANCED {
		t.Errorf("experience level mapping wrong: got %v", res.Rows[0].Input.GetExperienceLevel())
	}
	// Empty experience defaults to intermediate (§3.2 "missing -> intermediate").
	if res.Rows[1].Input.GetExperienceLevel() != v1.ExperienceLevel_EXPERIENCE_LEVEL_INTERMEDIATE {
		t.Errorf("missing experience: want intermediate, got %v", res.Rows[1].Input.GetExperienceLevel())
	}
	if res.Summary.ParsedCount != 2 || res.Summary.ValidCount != 2 || res.Summary.ErrorCount != 0 {
		t.Errorf("summary counts wrong: %+v", res.Summary)
	}
}

func TestParseCSV_DelimiterAutoDetect(t *testing.T) {
	cases := map[string]string{
		"semicolon": "firstName;lastName\nJane;Doe\n",
		"tab":       "firstName\tlastName\nJane\tDoe\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := parseString(t, src)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(res.Rows) != 1 {
				t.Fatalf("want 1 row, got %d", len(res.Rows))
			}
			if res.Rows[0].Input.GetFirstName() != "Jane" || res.Rows[0].Input.GetLastName() != "Doe" {
				t.Errorf("delim detect failed: %+v", res.Rows[0].Input)
			}
		})
	}
}

func TestParseCSV_FullNameSplit(t *testing.T) {
	src := "fullName,email\nMary Jane Smith,m@x.com\nMadonna,\n"
	res, err := parseString(t, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := res.Rows[0].Input.GetFirstName(); got != "Mary Jane" {
		t.Errorf("first split on LAST whitespace: got %q", got)
	}
	if got := res.Rows[0].Input.GetLastName(); got != "Smith" {
		t.Errorf("last split on LAST whitespace: got %q", got)
	}
	// Madonna case: first empty, last filled, expect a firstName violation.
	if res.Rows[1].Input.GetFirstName() != "" || res.Rows[1].Input.GetLastName() != "Madonna" {
		t.Errorf("single-word fullName: %+v", res.Rows[1].Input)
	}
	if len(res.Rows[1].Errors) == 0 {
		t.Errorf("expected row error on Madonna case")
	}
	hasFirstNameErr := false
	for _, v := range res.Rows[1].Errors {
		if v.Field == "firstName" {
			hasFirstNameErr = true
		}
	}
	if !hasFirstNameErr {
		t.Errorf("expected firstName violation on Madonna case, got %v", res.Rows[1].Errors)
	}
}

func TestParseCSV_ConflictingColumns(t *testing.T) {
	src := "firstName,lastName,fullName\nA,B,C\n"
	_, err := parseString(t, src)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("want ErrInvalidArgument, got %v", err)
	}
	if !strings.Contains(err.Error(), "conflicting columns") {
		t.Errorf("want conflicting-columns message, got: %v", err)
	}
}

func TestParseCSV_ExperienceAliasesAndInvalid(t *testing.T) {
	src := "firstName,lastName,experience\nA,B,beginner\nC,D,senior\nE,F,medium\nG,H,bogus\n"
	res, err := parseString(t, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []v1.ExperienceLevel{
		v1.ExperienceLevel_EXPERIENCE_LEVEL_NOVICE,
		v1.ExperienceLevel_EXPERIENCE_LEVEL_ADVANCED,
		v1.ExperienceLevel_EXPERIENCE_LEVEL_INTERMEDIATE,
		v1.ExperienceLevel_EXPERIENCE_LEVEL_UNSPECIFIED,
	}
	for i, w := range want {
		if got := res.Rows[i].Input.GetExperienceLevel(); got != w {
			t.Errorf("row %d: want %v, got %v", i, w, got)
		}
	}
	if len(res.Rows[3].Errors) == 0 {
		t.Errorf("row 4 should have a violation for bogus experience level")
	}
}

func TestParseCSV_InvalidEmail(t *testing.T) {
	src := "firstName,lastName,email\nJane,Doe,not-an-email\n"
	res, err := parseString(t, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rows[0].Errors) == 0 {
		t.Fatalf("expected email violation, got none")
	}
	if res.Rows[0].Errors[0].Field != "email" {
		t.Errorf("want email field violation, got %+v", res.Rows[0].Errors[0])
	}
}

func TestParseCSV_EmptyFile(t *testing.T) {
	_, err := parseString(t, "")
	if err == nil || !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("want ErrInvalidArgument for empty, got %v", err)
	}
	if !strings.Contains(err.Error(), "file is empty") {
		t.Errorf("want 'file is empty' in error, got %v", err)
	}
}

func TestParseCSV_HeaderOnly(t *testing.T) {
	_, err := parseString(t, "firstName,lastName\n")
	if err == nil || !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("want ErrInvalidArgument for header-only, got %v", err)
	}
	if !strings.Contains(err.Error(), "no data rows") {
		t.Errorf("want 'no data rows' in error, got %v", err)
	}
}

func TestParseCSV_UnrecognizedColumns(t *testing.T) {
	src := "firstName,lastName,grade,dietary\nJane,Doe,12,Vegan\n"
	res, err := parseString(t, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Summary.IgnoredColumns) != 2 {
		t.Errorf("want 2 ignored cols, got %v", res.Summary.IgnoredColumns)
	}
	if res.Summary.IgnoredColumns[0] != "grade" || res.Summary.IgnoredColumns[1] != "dietary" {
		t.Errorf("ignored columns in unexpected order: %v", res.Summary.IgnoredColumns)
	}
}

func TestParseCSV_SameUploadConflict(t *testing.T) {
	src := "firstName,lastName,email\nJane,Doe,jane@x.com\nJanet,Doh,jane@x.com\n"
	res, err := parseString(t, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c0, ok0 := res.Rows[0].Match.(*v1.PreviewRow_Conflict)
	c1, ok1 := res.Rows[1].Match.(*v1.PreviewRow_Conflict)
	if !ok0 || !ok1 {
		t.Fatalf("expected both rows flagged as conflicts; row0=%T row1=%T", res.Rows[0].Match, res.Rows[1].Match)
	}
	if c0.Conflict.WithRowNumber != 2 || c1.Conflict.WithRowNumber != 1 {
		t.Errorf("conflict cross-references wrong: %d / %d", c0.Conflict.WithRowNumber, c1.Conflict.WithRowNumber)
	}
}

func TestParseCSV_LineEndings(t *testing.T) {
	// Bare \r line endings (legacy Mac).
	src := "firstName,lastName\rJane,Doe\rJohn,Roe\r"
	res, err := parseString(t, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("want 2 rows for \\r endings, got %d", len(res.Rows))
	}
}

func TestParseCSV_TooManyRows(t *testing.T) {
	var b strings.Builder
	b.WriteString("firstName,lastName\n")
	for i := 0; i < MaxRows+1; i++ {
		b.WriteString("A,B\n")
	}
	_, err := parseString(t, b.String())
	if err == nil || !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected row-cap error, got %v", err)
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Errorf("want 'exceeds maximum' in error, got %v", err)
	}
}

func TestNormalizeHeader(t *testing.T) {
	cases := map[string]string{
		"First Name":  "firstname",
		"first_name":  "firstname",
		"FIRST NAME!": "firstname",
		"  Email  ":   "email",
		"":            "",
	}
	for in, want := range cases {
		if got := normalizeHeader(in); got != want {
			t.Errorf("normalizeHeader(%q): want %q, got %q", in, want, got)
		}
	}
}

func TestSplitFullName(t *testing.T) {
	cases := []struct {
		in, first, last string
	}{
		{"Mary Jane Smith", "Mary Jane", "Smith"},
		{"Madonna", "", "Madonna"},
		{"  spaced  ", "", "spaced"},
		{"", "", ""},
		{"Jane Doe", "Jane", "Doe"},
	}
	for _, c := range cases {
		f, l := splitFullName(c.in)
		if f != c.first || l != c.last {
			t.Errorf("splitFullName(%q): want (%q,%q), got (%q,%q)", c.in, c.first, c.last, f, l)
		}
	}
}

func TestParseRequest_RejectsNoSource(t *testing.T) {
	_, err := Parse(ParseRequest{})
	if err == nil || !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected invalid-argument for empty request, got %v", err)
	}
}
