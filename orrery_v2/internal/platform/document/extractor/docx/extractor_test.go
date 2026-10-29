package docx_test

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/platform/document/extractor/docx"
)

func TestExtractorExtractsParagraphTextFromDOCX(t *testing.T) {
	data := minimalDOCX(t, `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>Mission</w:t></w:r><w:r><w:t> bootstrap</w:t></w:r></w:p>
    <w:p><w:r><w:t>Second paragraph</w:t></w:r></w:p>
  </w:body>
</w:document>`)
	extractor := docx.Extractor{}

	doc, err := extractor.Extract(context.Background(), core.Source{
		Name:        "brief.docx",
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		Reader:      bytes.NewReader(data),
	})
	if err != nil {
		t.Fatalf("extract docx: %v", err)
	}
	if doc.Text != "Mission bootstrap\nSecond paragraph" {
		t.Fatalf("text = %q", doc.Text)
	}
	if len(doc.Sections) != 2 || doc.Sections[0].Text != "Mission bootstrap" || doc.Sections[1].Text != "Second paragraph" {
		t.Fatalf("sections = %#v", doc.Sections)
	}
	if doc.Metadata["filename"] != "brief.docx" || doc.Checksum == "" {
		t.Fatalf("metadata/checksum mismatch: %#v", doc)
	}
}

func TestExtractorSupportsDOCXContentTypeAndExtension(t *testing.T) {
	extractor := docx.Extractor{}
	if !extractor.Supports("application/vnd.openxmlformats-officedocument.wordprocessingml.document", "x") {
		t.Fatal("expected content type support")
	}
	if !extractor.Supports("application/octet-stream", "brief.docx") {
		t.Fatal("expected extension support")
	}
	if extractor.Supports("text/plain", "brief.txt") {
		t.Fatal("unexpected plain text support")
	}
}

func TestExtractorRejectsInvalidDOCX(t *testing.T) {
	_, err := docx.Extractor{}.Extract(context.Background(), core.Source{
		Name:   "bad.docx",
		Reader: strings.NewReader("not a zip"),
	})
	if err == nil {
		t.Fatal("expected invalid docx error")
	}
}

func minimalDOCX(t *testing.T, documentXML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	file, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatalf("create document xml: %v", err)
	}
	if _, err := file.Write([]byte(documentXML)); err != nil {
		t.Fatalf("write document xml: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}
