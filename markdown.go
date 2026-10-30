package main

import (
	"bytes"
	"html/template"
	"log"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// md renders list descriptions. It is goldmark's safe mode, which is its
// default: raw HTML in the source is dropped rather than passed through, and
// links to javascript:, vbscript:, file: and data: URLs are not linked. That
// is what makes it fine to show one account's writing to anyone.
var md = goldmark.New(goldmark.WithExtensions(extension.Strikethrough, extension.Linkify))

// renderMarkdown turns Markdown into HTML for a template. A description that
// fails to render shows as nothing rather than breaking the page.
func renderMarkdown(src string) template.HTML {
	if src == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		log.Printf("render markdown: %v", err)
		return ""
	}
	return template.HTML(buf.String())
}
