// Package parse implements the bulk-delegate import parser layer for M6.
//
// It consumes CSV/XLSX byte streams or Google Sheets URLs and produces
// PreviewRows + a PreviewSummary suitable for the bulk-import handler. It is
// framework-free: callers convert returned errors into Connect codes.
//
// See docs/subsystems/BULK_IMPORT.md for the authoritative spec.
package parse

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
)

// MaxRows is the per-import parsed-row cap. See BULK_IMPORT.md §2.2.
const MaxRows = 2000

// ErrInvalidArgument is the sentinel wrapped by every top-level parse error
// that the handler should convert to connect.CodeInvalidArgument.
var ErrInvalidArgument = errors.New("invalid argument")

// ParseRequest selects an input source. Exactly one of Reader / SheetURL must
// be set.
type ParseRequest struct {
	// Reader is the raw file bytes (CSV or XLSX). Format must be set when
	// Reader is non-nil.
	Reader io.Reader
	Format v1.SourceFormat

	// SheetURL is a Google Sheets shareable URL. HTTPClient must be non-nil
	// when SheetURL is set; the parser does not construct its own client so
	// that the safe-HTTP layer (egress allow-list, timeouts) can be injected.
	SheetURL   string
	HTTPClient *http.Client

	// TabName selects a workbook tab for XLSX or Google Sheets. Empty means
	// "use the only tab"; if multiple tabs exist the result lists them in
	// AvailableTabs and Rows is empty.
	TabName string
}

// ParseResult is the parser's output. The handler is responsible for filling
// in the per-row Match oneof (Create/Update/Conflict against existing roster
// state); the parser leaves Match unset except for same-upload conflicts,
// which it can resolve purely from the parsed rows.
type ParseResult struct {
	Rows          []*v1.PreviewRow
	Summary       *v1.PreviewSummary
	AvailableTabs []string
}

// Parse runs the appropriate sub-parser based on the request shape.
func Parse(req ParseRequest) (ParseResult, error) {
	switch {
	case req.SheetURL != "":
		if req.HTTPClient == nil {
			return ParseResult{}, fmt.Errorf("%w: http client required for google sheets source", ErrInvalidArgument)
		}
		return parseSheet(req.SheetURL, req.TabName, req.HTTPClient)
	case req.Reader != nil:
		switch req.Format {
		case v1.SourceFormat_SOURCE_FORMAT_CSV:
			return parseCSV(req.Reader)
		case v1.SourceFormat_SOURCE_FORMAT_XLSX:
			return parseXLSX(req.Reader, req.TabName)
		default:
			return ParseResult{}, fmt.Errorf("%w: unsupported source format", ErrInvalidArgument)
		}
	default:
		return ParseResult{}, fmt.Errorf("%w: parse request has no source", ErrInvalidArgument)
	}
}

// buildResult applies header-alias normalization, per-row validation, and
// same-upload conflict detection across the supplied raw rows. The first
// element of rawRows is the header row.
func buildResult(rawRows [][]string) (ParseResult, error) {
	if len(rawRows) == 0 {
		return ParseResult{}, fmt.Errorf("%w: file is empty", ErrInvalidArgument)
	}
	if len(rawRows) == 1 {
		return ParseResult{}, fmt.Errorf("%w: file has no data rows", ErrInvalidArgument)
	}
	dataRows := rawRows[1:]
	if len(dataRows) > MaxRows {
		return ParseResult{}, fmt.Errorf("%w: row count %d exceeds maximum of %d", ErrInvalidArgument, len(dataRows), MaxRows)
	}

	hdr, ignored, err := mapHeaders(rawRows[0])
	if err != nil {
		return ParseResult{}, err
	}

	rows := make([]*v1.PreviewRow, 0, len(dataRows))
	for i, raw := range dataRows {
		rows = append(rows, buildRow(int32(i+1), hdr, raw))
	}

	detectSameUploadConflicts(rows)

	summary := summarize(rows, ignored)
	return ParseResult{Rows: rows, Summary: summary}, nil
}

func summarize(rows []*v1.PreviewRow, ignoredColumns []string) *v1.PreviewSummary {
	s := &v1.PreviewSummary{
		ParsedCount:    int32(len(rows)),
		IgnoredColumns: ignoredColumns,
	}
	for _, r := range rows {
		if len(r.Errors) == 0 {
			s.ValidCount++
		} else {
			s.ErrorCount++
		}
	}
	return s
}

// errMsg returns the human-readable portion of a wrapped ErrInvalidArgument
// (everything after the ": " separator). Used by tests.
func errMsg(err error) string {
	if err == nil {
		return ""
	}
	const sep = ": "
	s := err.Error()
	if i := strings.Index(s, sep); i >= 0 {
		return s[i+len(sep):]
	}
	return s
}
