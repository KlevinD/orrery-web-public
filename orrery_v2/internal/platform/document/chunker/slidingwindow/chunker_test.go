package slidingwindow_test

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/platform/document/chunker"
	"orrery_v2/internal/platform/document/chunker/slidingwindow"
	"orrery_v2/internal/platform/document/core"
)

func TestChunkerValidatesPolicy(t *testing.T) {
	_, err := slidingwindow.New().Chunk(context.Background(), core.ExtractedDocument{Text: "hello"}, core.ChunkPolicy{
		MaxRunes:     10,
		OverlapRunes: 10,
		MinRunes:     1,
	})
	if err == nil {
		t.Fatal("expected invalid policy error")
	}
}

func TestChunkerProducesDeterministicChunks(t *testing.T) {
	doc := core.ExtractedDocument{
		Text:     "alpha beta gamma\n\ndelta epsilon zeta\n\neta theta iota",
		Checksum: "docsum",
	}
	policy := core.ChunkPolicy{MaxRunes: 24, OverlapRunes: 6, MinRunes: 5}

	first, err := slidingwindow.New().Chunk(context.Background(), doc, policy)
	if err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	second, err := slidingwindow.New().Chunk(context.Background(), doc, policy)
	if err != nil {
		t.Fatalf("second chunk: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("chunk count mismatch: %d != %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Text != second[i].Text || first[i].ContentHash != second[i].ContentHash {
			t.Fatalf("chunk %d not deterministic: %#v %#v", i, first[i], second[i])
		}
	}
}

func TestChunkerPreservesHeadingPathFromSections(t *testing.T) {
	doc := core.ExtractedDocument{
		Text: "Heading body paragraph one.\n\nParagraph two with more content.",
		Sections: []core.Section{{
			HeadingPath: []string{"Product", "Process"},
			Text:        "Heading body paragraph one.\n\nParagraph two with more content.",
			StartOffset: 0,
			EndOffset:   60,
		}},
	}

	chunks, err := slidingwindow.New().Chunk(context.Background(), doc, core.ChunkPolicy{
		MaxRunes:      32,
		OverlapRunes:  4,
		MinRunes:      8,
		PreserveHeads: true,
	})
	if err != nil {
		t.Fatalf("chunk: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}
	for _, chunk := range chunks {
		if strings.Join(chunk.HeadingPath, " > ") != "Product > Process" {
			t.Fatalf("heading path not preserved: %#v", chunk.HeadingPath)
		}
	}
}

func TestChunkerAppliesOverlapWithoutEmptyChunks(t *testing.T) {
	text := "0123456789 abcdefghij 0123456789 abcdefghij"
	chunks, err := slidingwindow.New().Chunk(context.Background(), core.ExtractedDocument{Text: text}, core.ChunkPolicy{
		MaxRunes:     18,
		OverlapRunes: 5,
		MinRunes:     4,
	})
	if err != nil {
		t.Fatalf("chunk: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if chunk.Text == "" {
			t.Fatalf("empty chunk: %#v", chunk)
		}
	}
	if chunks[1].StartOffset >= chunks[0].EndOffset {
		t.Fatalf("expected overlap between first chunks: %#v %#v", chunks[0], chunks[1])
	}
}

func TestValidatePolicyAcceptsUsefulDefaults(t *testing.T) {
	policy, err := chunker.NormalizePolicy(core.ChunkPolicy{MaxRunes: 20, OverlapRunes: 4})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if policy.MinRunes == 0 {
		t.Fatal("expected min runes default")
	}
}
