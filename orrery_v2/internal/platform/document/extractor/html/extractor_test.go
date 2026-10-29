package html_test

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/platform/document/extractor/html"
)

func TestExtractorStripsTagsAndScriptStyle(t *testing.T) {
	input := `<html><head><style>.x{}</style><script>alert(1)</script><title>Doc</title></head><body><h1>Main</h1><p>Hello <b>world</b></p></body></html>`

	doc, err := html.Extractor{}.Extract(context.Background(), core.Source{
		Name:        "doc.html",
		ContentType: "text/html",
		Reader:      strings.NewReader(input),
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if strings.Contains(doc.Text, "alert") || strings.Contains(doc.Text, ".x") || strings.Contains(doc.Text, "<") {
		t.Fatalf("html was not cleaned: %q", doc.Text)
	}
	if !strings.Contains(doc.Text, "Main") || !strings.Contains(doc.Text, "Hello world") {
		t.Fatalf("expected visible text, got %q", doc.Text)
	}
	if doc.Title != "Doc" {
		t.Fatalf("title mismatch: %q", doc.Title)
	}
}

func TestExtractorSupportsHTMLContentTypesAndExtensions(t *testing.T) {
	extractor := html.Extractor{}
	if !extractor.Supports("text/html", "doc.txt") {
		t.Fatal("expected text/html support")
	}
	if !extractor.Supports("", "doc.html") || !extractor.Supports("", "doc.htm") {
		t.Fatal("expected html extension support")
	}
	if extractor.Supports("text/plain", "doc.txt") {
		t.Fatal("did not expect plain text support")
	}
}
