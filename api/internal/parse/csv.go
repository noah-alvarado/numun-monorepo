package parse

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/text/encoding/charmap"
)

// utf8BOM is the leading byte-order mark stripped by §2.3.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// candidateDelimiters lists the CSV delimiters auto-detection considers, in
// the order ties are broken (comma first). See BULK_IMPORT.md §2.3.
var candidateDelimiters = []rune{',', ';', '\t'}

func parseCSV(r io.Reader) (ParseResult, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return ParseResult{}, fmt.Errorf("%w: read csv: %v", ErrInvalidArgument, err)
	}
	if len(data) == 0 {
		return ParseResult{}, fmt.Errorf("%w: file is empty", ErrInvalidArgument)
	}

	data = bytes.TrimPrefix(data, utf8BOM)

	if !isValidUTF8(data) {
		decoded, err := charmap.Windows1252.NewDecoder().Bytes(data)
		if err != nil {
			return ParseResult{}, fmt.Errorf("%w: file is not valid UTF-8 or Windows-1252", ErrInvalidArgument)
		}
		data = decoded
	}

	// Normalize bare \r line endings to \n so encoding/csv's record splitter
	// (which only recognizes \n and \r\n) treats them as row terminators.
	data = normalizeNewlines(data)

	delim := detectDelimiter(data)

	rows, err := readCSVRecords(data, delim)
	if err != nil {
		return ParseResult{}, fmt.Errorf("%w: parse csv: %v", ErrInvalidArgument, err)
	}
	return buildResult(rows)
}

// isValidUTF8 reports whether b is valid UTF-8. We use a hand-rolled check to
// avoid the unicode/utf8.Valid import dance for tiny payloads.
func isValidUTF8(b []byte) bool {
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c < 0x80:
			i++
		case c < 0xC2:
			return false
		case c < 0xE0:
			if i+1 >= len(b) || b[i+1]&0xC0 != 0x80 {
				return false
			}
			i += 2
		case c < 0xF0:
			if i+2 >= len(b) || b[i+1]&0xC0 != 0x80 || b[i+2]&0xC0 != 0x80 {
				return false
			}
			i += 3
		case c < 0xF5:
			if i+3 >= len(b) || b[i+1]&0xC0 != 0x80 || b[i+2]&0xC0 != 0x80 || b[i+3]&0xC0 != 0x80 {
				return false
			}
			i += 4
		default:
			return false
		}
	}
	return true
}

// normalizeNewlines turns bare \r (no following \n) into \n. \r\n and \n are
// left untouched.
func normalizeNewlines(b []byte) []byte {
	if !bytes.ContainsRune(b, '\r') {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == '\r' {
			if i+1 < len(b) && b[i+1] == '\n' {
				out = append(out, '\r', '\n')
				i++
				continue
			}
			out = append(out, '\n')
			continue
		}
		out = append(out, b[i])
	}
	return out
}

// detectDelimiter inspects the first 4 KB and picks the delimiter that
// maximizes consistent column counts across the first ten lines. Ties are
// broken in candidateDelimiters order (comma wins).
func detectDelimiter(data []byte) rune {
	sample := data
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	lines := splitLinesUpTo(sample, 10)
	if len(lines) == 0 {
		return ','
	}

	bestDelim := ','
	bestScore := -1
	for _, d := range candidateDelimiters {
		score := scoreDelimiter(lines, d)
		if score > bestScore {
			bestScore = score
			bestDelim = d
		}
	}
	return bestDelim
}

func splitLinesUpTo(b []byte, n int) []string {
	s := string(b)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	all := strings.Split(s, "\n")
	out := make([]string, 0, n)
	for _, line := range all {
		if line == "" {
			continue
		}
		out = append(out, line)
		if len(out) >= n {
			break
		}
	}
	return out
}

// scoreDelimiter returns a higher score when more lines split into the same
// (>=2) number of fields. Lines that produce a single field don't count.
func scoreDelimiter(lines []string, delim rune) int {
	counts := map[int]int{}
	for _, line := range lines {
		// Cheap field-count estimate ignoring quoting; good enough for the
		// detection heuristic. The real parse uses encoding/csv with proper
		// quote handling.
		n := strings.Count(line, string(delim)) + 1
		if n < 2 {
			continue
		}
		counts[n]++
	}
	best := 0
	for _, c := range counts {
		if c > best {
			best = c
		}
	}
	return best
}

func readCSVRecords(data []byte, delim rune) ([][]string, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.TrimLeadingSpace = false

	var rows [][]string
	for {
		rec, err := r.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		rows = append(rows, rec)
	}
	return rows, nil
}
