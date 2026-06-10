// Package emailtmpl embeds the EMAIL.md §3 template files so the email package
// can render them without filesystem access. Keeping the templates here, in
// /api/templates/email/, matches the path in EMAIL.md §3.1; the embed FS keeps
// them in the Lambda zip.
package emailtmpl

import "embed"

// FS exposes every *.tmpl file in this directory. The email package walks
// the FS at startup to build its html/template + text/template trees.
//
//go:embed *.tmpl
var FS embed.FS
