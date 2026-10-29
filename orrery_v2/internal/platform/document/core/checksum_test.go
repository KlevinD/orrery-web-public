package core_test

import (
	"testing"

	"orrery_v2/internal/platform/document/core"
)

func TestChecksumTextStable(t *testing.T) {
	got := core.ChecksumText("hello\nworld")
	again := core.ChecksumText("hello\nworld")
	if got == "" || got != again {
		t.Fatalf("checksum not stable: %q %q", got, again)
	}
}

func TestChecksumTextChangesWithContent(t *testing.T) {
	first := core.ChecksumText("hello")
	second := core.ChecksumText("hello!")
	if first == second {
		t.Fatalf("checksum should change with content: %q", first)
	}
}

func TestExtractedDocumentCloneKeepsSectionsAndMetadataIndependent(t *testing.T) {
	doc := core.ExtractedDocument{
		Title: "Title",
		Text:  "Body",
		Sections: []core.Section{
			{HeadingPath: []string{"A"}, Text: "Body", StartOffset: 0, EndOffset: 4},
		},
		Metadata: map[string]string{"source": "test"},
		Checksum: "sum",
	}

	clone := doc.Clone()
	clone.Sections[0].HeadingPath[0] = "B"
	clone.Metadata["source"] = "changed"

	if doc.Sections[0].HeadingPath[0] != "A" || doc.Metadata["source"] != "test" {
		t.Fatal("clone mutated original")
	}
}

func TestChunkCloneKeepsMetadataAndHeadingsIndependent(t *testing.T) {
	chunk := core.Chunk{
		Ordinal:     1,
		Text:        "Body",
		HeadingPath: []string{"A", "B"},
		StartOffset: 0,
		EndOffset:   4,
		ContentHash: "sum",
		Metadata:    map[string]string{"kind": "paragraph"},
	}

	clone := chunk.Clone()
	clone.HeadingPath[0] = "Changed"
	clone.Metadata["kind"] = "changed"

	if chunk.HeadingPath[0] != "A" || chunk.Metadata["kind"] != "paragraph" {
		t.Fatal("clone mutated original")
	}
}
