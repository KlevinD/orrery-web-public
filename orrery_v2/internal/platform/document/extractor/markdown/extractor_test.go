package markdown_test

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/platform/document/extractor/markdown"
)

func TestExtractorUsesFirstHeadingAsTitleAndSections(t *testing.T) {
	input := "# Product\n\nIntro\n\n## Process\n\nStep one\n\n```go\n# not heading\n```\n\n### Detail\n\nMore"

	doc, err := markdown.Extractor{}.Extract(context.Background(), core.Source{
		Name:        "plan.md",
		ContentType: "text/markdown",
		Reader:      strings.NewReader(input),
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if doc.Title != "Product" {
		t.Fatalf("title mismatch: %q", doc.Title)
	}
	if doc.Text != input {
		t.Fatalf("text mismatch")
	}
	if len(doc.Sections) != 3 {
		t.Fatalf("expected 3 sections, got %d: %#v", len(doc.Sections), doc.Sections)
	}
	if got := strings.Join(doc.Sections[1].HeadingPath, " > "); got != "Product > Process" {
		t.Fatalf("section heading path mismatch: %q", got)
	}
	if got := strings.Join(doc.Sections[2].HeadingPath, " > "); got != "Product > Process > Detail" {
		t.Fatalf("nested heading path mismatch: %q", got)
	}
}

func TestExtractorSupportsMarkdownContentTypesAndExtensions(t *testing.T) {
	extractor := markdown.Extractor{}
	if !extractor.Supports("text/markdown", "notes.txt") {
		t.Fatal("expected text/markdown support")
	}
	if !extractor.Supports("", "notes.md") || !extractor.Supports("", "notes.markdown") {
		t.Fatal("expected markdown extension support")
	}
	if extractor.Supports("text/plain", "notes.txt") {
		t.Fatal("did not expect plain text support")
	}
}
