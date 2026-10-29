package html

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode"

	"orrery_v2/internal/platform/document/core"
)

type Extractor struct{}

func (Extractor) Supports(contentType, filename string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if contentType == "text/html" || contentType == "application/xhtml+xml" {
		return true
	}
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".html" || ext == ".htm"
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
		return core.ExtractedDocument{}, fmt.Errorf("read html document: %w", err)
	}
	raw := string(data)
	text := visibleText(raw)
	return core.ExtractedDocument{
		Title: titleText(raw),
		Text:  text,
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

func titleText(raw string) string {
	lower := strings.ToLower(raw)
	start := strings.Index(lower, "<title")
	if start < 0 {
		return ""
	}
	closeStart := strings.Index(lower[start:], ">")
	if closeStart < 0 {
		return ""
	}
	contentStart := start + closeStart + 1
	end := strings.Index(lower[contentStart:], "</title>")
	if end < 0 {
		return ""
	}
	return normalizeWhitespace(stripTags(raw[contentStart : contentStart+end]))
}

func visibleText(raw string) string {
	withoutBlocks := removeElement(raw, "script")
	withoutBlocks = removeElement(withoutBlocks, "style")
	return normalizeWhitespace(stripTags(withoutBlocks))
}

func removeElement(raw, tag string) string {
	lower := strings.ToLower(raw)
	open := "<" + tag
	close := "</" + tag + ">"
	var builder strings.Builder
	position := 0
	for {
		start := strings.Index(lower[position:], open)
		if start < 0 {
			builder.WriteString(raw[position:])
			break
		}
		start += position
		builder.WriteString(raw[position:start])
		openEnd := strings.Index(lower[start:], ">")
		if openEnd < 0 {
			break
		}
		contentStart := start + openEnd + 1
		end := strings.Index(lower[contentStart:], close)
		if end < 0 {
			position = len(raw)
			break
		}
		position = contentStart + end + len(close)
	}
	return builder.String()
}

func stripTags(raw string) string {
	var builder strings.Builder
	inTag := false
	for _, r := range raw {
		switch r {
		case '<':
			if builder.Len() > 0 {
				builder.WriteRune(' ')
			}
			inTag = true
		case '>':
			inTag = false
			builder.WriteRune(' ')
		default:
			if !inTag {
				builder.WriteRune(r)
			}
		}
	}
	return builder.String()
}

func normalizeWhitespace(raw string) string {
	var builder strings.Builder
	pendingSpace := false
	for _, r := range raw {
		if unicode.IsSpace(r) {
			pendingSpace = builder.Len() > 0
			continue
		}
		if pendingSpace {
			builder.WriteRune(' ')
			pendingSpace = false
		}
		builder.WriteRune(r)
	}
	return strings.TrimSpace(builder.String())
}
