package extractor

import (
	"context"
	"fmt"

	"orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/platform/document/extractor/docx"
	"orrery_v2/internal/platform/document/extractor/html"
	"orrery_v2/internal/platform/document/extractor/markdown"
	"orrery_v2/internal/platform/document/extractor/pdf"
	"orrery_v2/internal/platform/document/extractor/plaintext"
)

type Registry struct {
	extractors []Extractor
}

func NewRegistry(extractors ...Extractor) *Registry {
	copied := make([]Extractor, 0, len(extractors))
	for _, extractor := range extractors {
		if extractor != nil {
			copied = append(copied, extractor)
		}
	}
	return &Registry{extractors: copied}
}

func NewDefaultRegistry() *Registry {
	return NewRegistry(
		plaintext.Extractor{},
		markdown.Extractor{},
		html.Extractor{},
		docx.Extractor{},
		pdf.Extractor{},
	)
}

func (r *Registry) Extract(ctx context.Context, source core.Source) (core.ExtractedDocument, error) {
	if err := ctx.Err(); err != nil {
		return core.ExtractedDocument{}, err
	}
	if r == nil {
		return core.ExtractedDocument{}, fmt.Errorf("%w: extractor registry is nil", core.ErrUnsupported)
	}
	for _, extractor := range r.extractors {
		if extractor.Supports(source.ContentType, source.Name) {
			return extractor.Extract(ctx, source)
		}
	}
	return core.ExtractedDocument{}, fmt.Errorf("%w: %s %s", core.ErrUnsupported, source.ContentType, source.Name)
}
