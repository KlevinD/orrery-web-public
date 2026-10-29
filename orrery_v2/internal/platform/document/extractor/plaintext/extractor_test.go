package plaintext_test

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/platform/document/extractor/plaintext"
)

func TestExtractorReturnsFullTextAndChecksum(t *testing.T) {
	doc, err := plaintext.Extractor{}.Extract(context.Background(), core.Source{
		Name:        "notes.txt",
		ContentType: "text/plain",
		Reader:      strings.NewReader("hello\nworld"),
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if doc.Text != "hello\nworld" {
		t.Fatalf("text mismatch: %q", doc.Text)
	}
	if doc.Checksum != core.ChecksumText("hello\nworld") {
		t.Fatalf("checksum mismatch: %q", doc.Checksum)
	}
	if len(doc.Sections) != 1 || doc.Sections[0].Text != "hello\nworld" {
		t.Fatalf("expected one full-text section, got %#v", doc.Sections)
	}
}

func TestExtractorSupportsTextPlainAndTxtFiles(t *testing.T) {
	extractor := plaintext.Extractor{}
	if !extractor.Supports("text/plain", "notes.bin") {
		t.Fatal("expected text/plain support")
	}
	if !extractor.Supports("", "notes.txt") {
		t.Fatal("expected .txt support")
	}
	if extractor.Supports("application/pdf", "notes.pdf") {
		t.Fatal("did not expect pdf support")
	}
}
