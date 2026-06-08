package parse

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"

	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/xuri/excelize/v2"
)

func buildXLSX(t *testing.T, sheets map[string][][]string, order []string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()

	defaultSheet := "Sheet1"
	first := true
	for _, name := range order {
		rows := sheets[name]
		if first {
			if name != defaultSheet {
				if err := f.SetSheetName(defaultSheet, name); err != nil {
					t.Fatalf("rename sheet: %v", err)
				}
			}
			first = false
		} else {
			if _, err := f.NewSheet(name); err != nil {
				t.Fatalf("new sheet: %v", err)
			}
		}
		for ri, row := range rows {
			for ci, cell := range row {
				cellRef, err := excelize.CoordinatesToCellName(ci+1, ri+1)
				if err != nil {
					t.Fatalf("cell ref: %v", err)
				}
				if err := f.SetCellValue(name, cellRef, cell); err != nil {
					t.Fatalf("set cell: %v", err)
				}
			}
		}
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write xlsx: %v", err)
	}
	return buf.Bytes()
}

func TestParseXLSX_SingleSheet(t *testing.T) {
	data := buildXLSX(t, map[string][][]string{
		"Roster": {
			{"firstName", "lastName", "email"},
			{"Jane", "Doe", "jane@x.com"},
		},
	}, []string{"Roster"})

	res, err := Parse(ParseRequest{
		Reader: bytes.NewReader(data),
		Format: v1.SourceFormat_SOURCE_FORMAT_XLSX,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(res.Rows))
	}
	if res.Rows[0].Input.GetFirstName() != "Jane" || res.Rows[0].Input.GetEmail() != "jane@x.com" {
		t.Errorf("xlsx row parsed wrong: %+v", res.Rows[0].Input)
	}
}

func TestParseXLSX_MultiTabReturnsTabs(t *testing.T) {
	data := buildXLSX(t, map[string][][]string{
		"Roster":   {{"firstName", "lastName"}, {"Jane", "Doe"}},
		"Advisors": {{"firstName", "lastName"}, {"Adv", "Isor"}},
	}, []string{"Roster", "Advisors"})

	res, err := Parse(ParseRequest{
		Reader: bytes.NewReader(data),
		Format: v1.SourceFormat_SOURCE_FORMAT_XLSX,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Errorf("multi-tab without tab_name: rows should be empty, got %d", len(res.Rows))
	}
	if len(res.AvailableTabs) != 2 {
		t.Fatalf("want 2 tabs, got %v", res.AvailableTabs)
	}
}

func TestParseXLSX_MultiTabWithSelection(t *testing.T) {
	data := buildXLSX(t, map[string][][]string{
		"Roster":   {{"firstName", "lastName"}, {"Jane", "Doe"}},
		"Advisors": {{"firstName", "lastName"}, {"Adv", "Isor"}},
	}, []string{"Roster", "Advisors"})

	res, err := Parse(ParseRequest{
		Reader:  bytes.NewReader(data),
		Format:  v1.SourceFormat_SOURCE_FORMAT_XLSX,
		TabName: "Advisors",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("want 1 row from Advisors, got %d", len(res.Rows))
	}
	if res.Rows[0].Input.GetFirstName() != "Adv" {
		t.Errorf("wrong tab parsed: %+v", res.Rows[0].Input)
	}
}

func TestParseXLSX_MacroRejected(t *testing.T) {
	// Synthesize a minimal zip containing xl/vbaProject.bin to trip the
	// macro check without needing a real .xlsm fixture.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if w, err := zw.Create("xl/vbaProject.bin"); err != nil {
		t.Fatalf("zip create: %v", err)
	} else if _, err := w.Write([]byte("VBA")); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if w, err := zw.Create("[Content_Types].xml"); err != nil {
		t.Fatalf("zip create: %v", err)
	} else if _, err := w.Write([]byte("<Types/>")); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	_, err := Parse(ParseRequest{
		Reader: bytes.NewReader(buf.Bytes()),
		Format: v1.SourceFormat_SOURCE_FORMAT_XLSX,
	})
	if err == nil {
		t.Fatal("expected error for macro-enabled workbook")
	}
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("want ErrInvalidArgument, got %v", err)
	}
	if !strings.Contains(err.Error(), "macro") {
		t.Errorf("want macro mention in error, got: %v", err)
	}
}

func TestParseXLSX_EmptyReader(t *testing.T) {
	_, err := Parse(ParseRequest{
		Reader: bytes.NewReader(nil),
		Format: v1.SourceFormat_SOURCE_FORMAT_XLSX,
	})
	if err == nil || !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("want ErrInvalidArgument for empty xlsx, got %v", err)
	}
}
