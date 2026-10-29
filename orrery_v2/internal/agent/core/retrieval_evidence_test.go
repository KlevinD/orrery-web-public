package core

import (
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/retrievalevidence"
)

func TestMemoryPromptCarriesSourceVersionsWithoutDuplicatingSourceText(t *testing.T) {
	item := retrievalevidence.Seal(retrievalevidence.Item{Source: retrievalevidence.Source{Kind: "memory_record", ID: "memory", Version: "version-one"}, Purpose: retrievalevidence.PurposeHistorical, TruthState: retrievalevidence.TruthUnknown, SourceState: "record_verified",
		Assertions: []retrievalevidence.Assertion{{ID: "fact", Kind: "fact", TextHash: "PRIVATE_TEXT_HASH", TruthState: retrievalevidence.TruthUnknown}}}, time.Now())
	pack := ContextPack{PersonalMemoryDigestRef: MemoryDigestRef{ID: "personal", SummaryRef: "authorized summary", Evidence: []retrievalevidence.Item{item}}}
	text := BuildMemoryContextPrompt(pack)
	for _, expected := range []string{"authorized summary", "version-one", "historical_record", "not verified current facts", item.InputHash} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q: %s", expected, text)
		}
	}
	if strings.Contains(text, "PRIVATE_TEXT_HASH") {
		t.Fatal("prompt duplicated the per-assertion manifest outside the summary budget")
	}
	pack.PersonalMemoryDigestRef = MemoryDigestRef{}
	pack.CollectiveMemoryDigestRefs = []MemoryDigestRef{{ID: "collective", SummaryRef: "shared summary", Evidence: []retrievalevidence.Item{item}}}
	if text = BuildMemoryContextPrompt(pack); !strings.Contains(text, item.InputHash) || !strings.Contains(text, "shared summary") {
		t.Fatalf("collective evidence lost: %s", text)
	}
}
