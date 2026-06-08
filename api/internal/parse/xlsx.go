package parse

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"

	"github.com/xuri/excelize/v2"
)

// parseXLSX reads an XLSX byte stream and returns a ParseResult. If the
// workbook has multiple sheets and tabName is empty, it returns
// AvailableTabs (and zero rows) so the caller can prompt for a tab.
func parseXLSX(r io.Reader, tabName string) (ParseResult, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return ParseResult{}, fmt.Errorf("%w: read xlsx: %v", ErrInvalidArgument, err)
	}
	if len(data) == 0 {
		return ParseResult{}, fmt.Errorf("%w: file is empty", ErrInvalidArgument)
	}

	if err := rejectMacroEnabled(data); err != nil {
		return ParseResult{}, err
	}

	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return ParseResult{}, fmt.Errorf("%w: open xlsx: %v", ErrInvalidArgument, err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return ParseResult{}, fmt.Errorf("%w: workbook has no sheets", ErrInvalidArgument)
	}

	if tabName == "" && len(sheets) > 1 {
		return ParseResult{AvailableTabs: sheets}, nil
	}
	target := tabName
	if target == "" {
		target = sheets[0]
	} else {
		found := false
		for _, s := range sheets {
			if s == target {
				found = true
				break
			}
		}
		if !found {
			return ParseResult{}, fmt.Errorf("%w: tab %q not found in workbook", ErrInvalidArgument, target)
		}
	}

	rows, err := readSheetRows(f, target)
	if err != nil {
		return ParseResult{}, fmt.Errorf("%w: read sheet: %v", ErrInvalidArgument, err)
	}
	return buildResult(rows)
}

// rejectMacroEnabled scans the .xlsx zip directory for xl/vbaProject.bin
// without decompressing the entries. Macro-enabled workbooks are rejected
// outright.
func rejectMacroEnabled(data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("%w: corrupt xlsx: %v", ErrInvalidArgument, err)
	}
	for _, f := range zr.File {
		if f.Name == "xl/vbaProject.bin" {
			return fmt.Errorf("%w: macro-enabled workbooks are not supported", ErrInvalidArgument)
		}
	}
	return nil
}

// readSheetRows streams rows out of the given sheet using excelize's Rows
// iterator. Caps reads at MaxRows+1 (header + data) so excessively large
// sheets short-circuit; buildResult enforces the final cap with a clean
// error message.
func readSheetRows(f *excelize.File, sheet string) ([][]string, error) {
	rows, err := f.Rows(sheet)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	const cap = MaxRows + 1
	var out [][]string
	for rows.Next() {
		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		if isBlankRow(cols) {
			continue
		}
		out = append(out, cols)
		if len(out) > cap {
			// Drain just enough to give buildResult a clear "too many rows"
			// signal; we intentionally do not keep reading the whole sheet.
			break
		}
	}
	if err := rows.Error(); err != nil {
		return nil, err
	}
	return out, nil
}

func isBlankRow(cols []string) bool {
	for _, c := range cols {
		if c != "" {
			return false
		}
	}
	return true
}
