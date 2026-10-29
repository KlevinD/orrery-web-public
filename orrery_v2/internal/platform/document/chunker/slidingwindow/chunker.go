package slidingwindow

import (
	"context"
	"strings"

	basechunker "orrery_v2/internal/platform/document/chunker"
	"orrery_v2/internal/platform/document/core"
)

type Chunker struct{}

func New() Chunker {
	return Chunker{}
}

func (Chunker) Chunk(ctx context.Context, doc core.ExtractedDocument, policy core.ChunkPolicy) ([]core.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	policy, err := basechunker.NormalizePolicy(policy)
	if err != nil {
		return nil, err
	}
	sections := doc.Sections
	if len(sections) == 0 {
		sections = []core.Section{{
			Text:        doc.Text,
			StartOffset: 0,
			EndOffset:   len([]rune(doc.Text)),
		}}
	}
	chunks := make([]core.Chunk, 0)
	for _, section := range sections {
		for _, chunk := range chunkSection(section, policy, len(chunks)) {
			if strings.TrimSpace(chunk.Text) == "" {
				continue
			}
			chunks = append(chunks, chunk)
		}
	}
	for i := range chunks {
		chunks[i].Ordinal = i
	}
	return chunks, nil
}

func chunkSection(section core.Section, policy core.ChunkPolicy, ordinalBase int) []core.Chunk {
	runes := []rune(section.Text)
	if len(runes) == 0 {
		return nil
	}
	chunks := make([]core.Chunk, 0, (len(runes)/policy.MaxRunes)+1)
	start := 0
	for start < len(runes) {
		maxEnd := start + policy.MaxRunes
		if maxEnd > len(runes) {
			maxEnd = len(runes)
		}
		minEnd := start + policy.MinRunes
		if minEnd > maxEnd {
			minEnd = maxEnd
		}
		end := chooseEnd(runes, start, maxEnd, minEnd)
		text := strings.TrimSpace(string(runes[start:end]))
		if text != "" {
			absoluteStart := section.StartOffset + start
			absoluteEnd := section.StartOffset + end
			chunks = append(chunks, core.Chunk{
				Ordinal:     ordinalBase + len(chunks),
				Text:        text,
				HeadingPath: cloneStrings(section.HeadingPath),
				StartOffset: absoluteStart,
				EndOffset:   absoluteEnd,
				ContentHash: core.ChecksumText(text),
			})
		}
		if end >= len(runes) {
			break
		}
		start = nextStart(start, end, policy.OverlapRunes)
	}
	return chunks
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
