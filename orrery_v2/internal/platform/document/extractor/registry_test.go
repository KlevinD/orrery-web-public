package extractor_test

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/platform/document/extractor"
)

type stubExtractor struct {
	name        string
	contentType string
}

func (e stubExtractor) Supports(contentType, filename string) bool {
	return contentType == e.contentType || filename == e.name
}

func (e stubExtractor) Extract(context.Context, core.Source) (core.ExtractedDocument, error) {
	return core.ExtractedDocument{Title: e.name, Text: e.name, Checksum: core.ChecksumText(e.name)}, nil
}

func TestRegistryChoosesExtractorByContentType(t *testing.T) {
	registry := extractor.NewRegistry(stubExtractor{name: "plain", contentType: "text/plain"})

	doc, err := registry.Extract(context.Background(), core.Source{
		Name:        "note.txt",
		ContentType: "text/plain",
		Reader:      strings.NewReader("ignored"),
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if doc.Title != "plain" {
		t.Fatalf("expected plain extractor, got %q", doc.Title)
	}
}

func TestRegistryReturnsUnsupportedWhenNoExtractorMatches(t *testing.T) {
	registry := extractor.NewRegistry()

	_, err := registry.Extract(context.Background(), core.Source{
		Name:        "file.bin",
		ContentType: "application/octet-stream",
		Reader:      strings.NewReader("x"),
	})
	if err == nil {
		t.Fatal("expected unsupported error")
	}
}

func TestDefaultRegistryIncludesPhase2Formats(t *testing.T) {
	registry := extractor.NewDefaultRegistry()

	if _, err := registry.Extract(context.Background(), core.Source{
		Name:        "notes.txt",
		ContentType: "text/plain",
		Reader:      strings.NewReader("hello"),
	}); err != nil {
		t.Fatalf("default registry text extract: %v", err)
	}
	pdfDoc, err := registry.Extract(context.Background(), core.Source{
		Name:        "brief.pdf",
		ContentType: "application/pdf",
		Reader:      strings.NewReader("%PDF-1.7\n1 0 obj\n<< /Length 1 >>\nstream\nBT (registry pdf text) Tj ET\nendstream\nendobj\n%%EOF\n"),
	})
	if err != nil {
		t.Fatalf("default registry pdf extract: %v", err)
	}
	if !strings.Contains(pdfDoc.Text, "registry pdf text") {
		t.Fatalf("default registry pdf text = %q", pdfDoc.Text)
	}
}
