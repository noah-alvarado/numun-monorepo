package email

import (
	"bytes"
	"fmt"
	htmltmpl "html/template"
	"io/fs"
	"strings"
	texttmpl "text/template"

	emailtmpl "github.com/numun/numun/api/templates/email"
)

// TemplateData mirrors EMAIL.md §3.3 — the struct every template receives.
type TemplateData struct {
	RecipientName  string
	Subject        string
	NowFormatted   string
	BrandColor     string
	AssetsBaseURL  string
	PortalBaseURL  string
	UnsubscribeURL string
	Kind           string
	Vars           map[string]any
}

// Rendered carries the per-send HTML, plaintext, and subject. The subject is
// derived from the `{{define "subject"}}` block in the per-kind template; the
// per-call SendRequest.Subject can override it.
type Rendered struct {
	Subject string
	HTML    string
	Text    string
}

// Templates is the parsed bundle. One *html/template per kind for HTML output,
// one *text/template per kind for plaintext. Each per-kind tree has the
// layout cloned in so `subject`/`body` block names don't collide across kinds.
type Templates struct {
	html map[string]*htmltmpl.Template
	text map[string]*texttmpl.Template
}

// LoadTemplates parses _layout.{html,txt}.tmpl once and clones the layout into
// every per-kind tree. A per-kind file must define `subject` and `body`
// blocks; it has no top-level content (the layout is the root).
//
// Missing-key access in any template raises an error → broken mail fails fast
// instead of silently sending blanks.
func LoadTemplates() (*Templates, error) {
	layoutHTML, err := fs.ReadFile(emailtmpl.FS, "_layout.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("read _layout.html.tmpl: %w", err)
	}
	layoutText, err := fs.ReadFile(emailtmpl.FS, "_layout.txt.tmpl")
	if err != nil {
		return nil, fmt.Errorf("read _layout.txt.tmpl: %w", err)
	}

	t := &Templates{
		html: map[string]*htmltmpl.Template{},
		text: map[string]*texttmpl.Template{},
	}

	entries, err := fs.ReadDir(emailtmpl.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("read templates dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "_") {
			continue
		}
		raw, err := fs.ReadFile(emailtmpl.FS, name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		switch {
		case strings.HasSuffix(name, ".html.tmpl"):
			kind := strings.TrimSuffix(name, ".html.tmpl")
			tmpl := htmltmpl.New(kind).Option("missingkey=error")
			if _, err := tmpl.Parse(string(layoutHTML)); err != nil {
				return nil, fmt.Errorf("parse layout for %s: %w", kind, err)
			}
			if _, err := tmpl.Parse(string(raw)); err != nil {
				return nil, fmt.Errorf("parse %s: %w", name, err)
			}
			t.html[kind] = tmpl
		case strings.HasSuffix(name, ".txt.tmpl"):
			kind := strings.TrimSuffix(name, ".txt.tmpl")
			tmpl := texttmpl.New(kind).Option("missingkey=error")
			if _, err := tmpl.Parse(string(layoutText)); err != nil {
				return nil, fmt.Errorf("parse layout for %s: %w", kind, err)
			}
			if _, err := tmpl.Parse(string(raw)); err != nil {
				return nil, fmt.Errorf("parse %s: %w", name, err)
			}
			t.text[kind] = tmpl
		}
	}
	return t, nil
}

// Render produces a Rendered triple for the given kind.
func (t *Templates) Render(kind string, data TemplateData) (Rendered, error) {
	htmlT, ok := t.html[kind]
	if !ok {
		return Rendered{}, fmt.Errorf("no html template for kind %q", kind)
	}
	textT, ok := t.text[kind]
	if !ok {
		return Rendered{}, fmt.Errorf("no text template for kind %q", kind)
	}

	var subjBuf bytes.Buffer
	if sub := htmlT.Lookup("subject"); sub != nil {
		if err := sub.Execute(&subjBuf, data); err != nil {
			return Rendered{}, fmt.Errorf("subject render: %w", err)
		}
	}

	var htmlBuf bytes.Buffer
	if err := htmlT.ExecuteTemplate(&htmlBuf, "_layout", data); err != nil {
		return Rendered{}, fmt.Errorf("html render: %w", err)
	}
	var textBuf bytes.Buffer
	if err := textT.ExecuteTemplate(&textBuf, "_layout", data); err != nil {
		return Rendered{}, fmt.Errorf("text render: %w", err)
	}

	return Rendered{
		Subject: strings.TrimSpace(subjBuf.String()),
		HTML:    htmlBuf.String(),
		Text:    textBuf.String(),
	}, nil
}

// Kinds returns the set of kinds with both HTML and text templates loaded.
// Used by the unit test that asserts every catalog entry has both formats.
func (t *Templates) Kinds() []string {
	out := make([]string, 0, len(t.html))
	for k := range t.html {
		if _, ok := t.text[k]; ok {
			out = append(out, k)
		}
	}
	return out
}
