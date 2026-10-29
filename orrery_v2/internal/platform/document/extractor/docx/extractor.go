package docx

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"orrery_v2/internal/platform/document/core"
)

const docxContentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

type Extractor struct{}

func (Extractor) Supports(contentType, filename string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if contentType == docxContentType {
		return true
	}
	return strings.EqualFold(filepath.Ext(filename), ".docx")
}

func (Extractor) Extract(ctx context.Context, source core.Source) (core.ExtractedDocument, error) {
	if err := ctx.Err(); err != nil {
		return core.ExtractedDocument{}, err
	}
	if source.Reader == nil {
		return core.ExtractedDocument{}, fmt.Errorf("%w: source reader is nil", core.ErrInvalid)
	}
	data, err := io.ReadAll(source.Reader)
	if err != nil {
		return core.ExtractedDocument{}, fmt.Errorf("read docx document: %w", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return core.ExtractedDocument{}, fmt.Errorf("%w: invalid docx zip: %v", core.ErrInvalid, err)
	}
	documentXML, err := openDocumentXML(reader)
	if err != nil {
		return core.ExtractedDocument{}, err
	}
	paragraphs, err := parseParagraphs(documentXML)
	if err != nil {
		return core.ExtractedDocument{}, err
	}
	text := strings.Join(paragraphs, "\n")
	sections := make([]core.Section, 0, len(paragraphs))
	offset := 0
	for _, paragraph := range paragraphs {
		start := offset
		end := start + len([]rune(paragraph))
		sections = append(sections, core.Section{Text: paragraph, StartOffset: start, EndOffset: end})
		offset = end + 1
	}
	return core.ExtractedDocument{
		Text:     text,
		Sections: sections,
		Metadata: map[string]string{"filename": source.Name},
		Checksum: core.ChecksumText(text),
	}, nil
}

func openDocumentXML(reader *zip.Reader) (io.ReadCloser, error) {
	for _, file := range reader.File {
		if file.Name == "word/document.xml" {
			return file.Open()
		}
	}
	return nil, fmt.Errorf("%w: docx word/document.xml missing", core.ErrInvalid)
}

func parseParagraphs(document io.ReadCloser) ([]string, error) {
	defer document.Close()
	decoder := xml.NewDecoder(document)
	var paragraphs []string
	var builder strings.Builder
	inParagraph := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: parse docx xml: %v", core.ErrInvalid, err)
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if typed.Name.Local == "p" {
				inParagraph = true
				builder.Reset()
			}
			if typed.Name.Local == "t" && inParagraph {
				var text string
				if err := decoder.DecodeElement(&text, &typed); err != nil {
					return nil, fmt.Errorf("%w: parse docx text: %v", core.ErrInvalid, err)
				}
				builder.WriteString(text)
			}
		case xml.EndElement:
			if typed.Name.Local == "p" && inParagraph {
				paragraph := strings.TrimSpace(builder.String())
				if paragraph != "" {
					paragraphs = append(paragraphs, paragraph)
				}
				inParagraph = false
				builder.Reset()
			}
		}
	}
	if len(paragraphs) == 0 {
		return nil, fmt.Errorf("%w: docx contains no text", core.ErrInvalid)
	}
	return paragraphs, nil
}
