package contextgraph

import (
	"reflect"
	"testing"
)

func TestEvidenceReferenceMatchingSeparatesRecordIDsFromSourceReferences(t *testing.T) {
	refs := []Ref{
		{Kind: "knowledge_document", ID: "doc-1"},
		{Kind: "event", ID: "event-1"},
		{Kind: "evidence", ID: "evidence-1"},
		{Kind: " EVENT ", ID: " event-1 "},
	}
	if got, want := EvidenceRecordIDsForRefs(refs), []string{"event-1", "evidence-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("EvidenceRecordIDsForRefs() = %v, want %v", got, want)
	}

	collision := Evidence{ID: "doc-1", SourceRef: Ref{Kind: "unrelated", ID: "other"}}
	if EvidenceMatchesRef(collision, Ref{Kind: "knowledge_document", ID: "doc-1"}) {
		t.Fatal("source reference matched an unrelated evidence record ID")
	}
	sourced := Evidence{ID: "evidence-row", SourceRef: Ref{Kind: "Knowledge_Document", ID: "doc-1"}}
	if !EvidenceMatchesRef(sourced, Ref{Kind: "knowledge_document", ID: "doc-1"}) {
		t.Fatal("matching evidence source reference was not resolved")
	}
	recorded := Evidence{ID: "event-1", SourceRef: Ref{Kind: "unrelated", ID: "other"}}
	if !EvidenceMatchesRef(recorded, Ref{Kind: "event", ID: "event-1"}) {
		t.Fatal("event reference did not resolve by evidence record ID")
	}
}
