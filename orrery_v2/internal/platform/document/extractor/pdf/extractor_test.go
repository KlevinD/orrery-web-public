package pdf_test

import (
	"bytes"
	"compress/zlib"
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/platform/document/extractor/pdf"
)

func TestExtractorSupportsPDFContentTypeAndExtension(t *testing.T) {
	extractor := pdf.Extractor{}
	if !extractor.Supports("application/pdf", "x") {
		t.Fatal("expected content type support")
	}
	if !extractor.Supports("application/octet-stream", "brief.pdf") {
		t.Fatal("expected extension support")
	}
	if extractor.Supports("text/plain", "brief.txt") {
		t.Fatal("unexpected plain text support")
	}
}

func TestExtractorExtractsLiteralTextFromUncompressedStream(t *testing.T) {
	doc, err := pdf.Extractor{}.Extract(context.Background(), core.Source{
		Name:        "brief.pdf",
		ContentType: "application/pdf",
		Reader: strings.NewReader(minimalPDF(`BT
/F1 12 Tf
72 720 Td
(Launch \(Alpha\) checklist) Tj
T*
(Second line) Tj
ET`)),
	})
	if err != nil {
		t.Fatalf("extract pdf: %v", err)
	}
	if !strings.Contains(doc.Text, "Launch (Alpha) checklist") || !strings.Contains(doc.Text, "Second line") {
		t.Fatalf("pdf text = %q", doc.Text)
	}
	if doc.Checksum == "" || doc.Metadata["filename"] != "brief.pdf" {
		t.Fatalf("metadata/checksum mismatch: %#v", doc)
	}
}

func TestExtractorExtractsLiteralTextFromFlateStream(t *testing.T) {
	stream := flatePDFStream(t, `BT [(Incident ) 35 (response)] TJ ET`)

	doc, err := pdf.Extractor{}.Extract(context.Background(), core.Source{
		Name:        "compressed.pdf",
		ContentType: "application/pdf",
		Reader:      strings.NewReader(minimalPDFWithObject("<< /Length 42 /Filter /FlateDecode >>", stream)),
	})
	if err != nil {
		t.Fatalf("extract flate pdf: %v", err)
	}
	if !strings.Contains(doc.Text, "Incident response") {
		t.Fatalf("pdf text = %q", doc.Text)
	}
}

func TestExtractorRejectsPDFWithoutText(t *testing.T) {
	_, err := pdf.Extractor{}.Extract(context.Background(), core.Source{
		Name:        "blank.pdf",
		ContentType: "application/pdf",
		Reader:      strings.NewReader(minimalPDF(`q 0 0 100 100 re S Q`)),
	})
	if err == nil {
		t.Fatal("expected blank pdf error")
	}
	if !strings.Contains(err.Error(), "pdf contains no extractable text") {
		t.Fatalf("error = %v", err)
	}
}

func minimalPDF(stream string) string {
	return minimalPDFWithObject("<< /Length 1 >>", stream)
}

func minimalPDFWithObject(dictionary string, stream string) string {
	return "%PDF-1.7\n1 0 obj\n" + dictionary + "\nstream\n" + stream + "\nendstream\nendobj\n%%EOF\n"
}

func flatePDFStream(t *testing.T, text string) string {
	t.Helper()
	var buf bytes.Buffer
	writer := zlib.NewWriter(&buf)
	if _, err := writer.Write([]byte(text)); err != nil {
		t.Fatalf("write zlib: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zlib: %v", err)
	}
	return buf.String()
}
