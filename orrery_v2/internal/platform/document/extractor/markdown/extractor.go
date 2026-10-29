package markdown

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"orrery_v2/internal/platform/document/core"
)

type Extractor struct{}

func (Extractor) Supports(contentType, filename string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if contentType == "text/markdown" || contentType == "text/x-markdown" {
		return true
	}
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".md" || ext == ".markdown"
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
		return core.ExtractedDocument{}, fmt.Errorf("read markdown document: %w", err)
	}
	text := string(data)
	headings := parseHeadings(text)
	sections := make([]core.Section, 0)
	for _, draft := range buildSections(text, headings) {
		sections = append(sections, core.Section{
			HeadingPath: draft.headingPath,
			Text:        runeSlice(text, draft.start, draft.end),
			StartOffset: draft.start,
			EndOffset:   draft.end,
		})
	}
	title := ""
	if len(headings) > 0 {
		title = headings[0].title
	}
	return core.ExtractedDocument{
		Title:    title,
		Text:     text,
		Sections: sections,
		Metadata: map[string]string{
			"filename": source.Name,
		},
		Checksum: core.ChecksumText(text),
	}, nil
}
