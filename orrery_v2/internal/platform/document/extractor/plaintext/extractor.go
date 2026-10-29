package plaintext

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
	if contentType == "text/plain" {
		return true
	}
	return strings.EqualFold(filepath.Ext(filename), ".txt")
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
		return core.ExtractedDocument{}, fmt.Errorf("read plain text document: %w", err)
	}
	text := string(data)
	return core.ExtractedDocument{
		Text: text,
		Sections: []core.Section{{
			Text:        text,
			StartOffset: 0,
			EndOffset:   len([]rune(text)),
		}},
		Metadata: map[string]string{
			"filename": source.Name,
		},
		Checksum: core.ChecksumText(text),
	}, nil
}
