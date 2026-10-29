// Package retrievalevidence describes what the host checked at retrieval time.
// A source version or an access check is not a proof that its text is true.
package retrievalevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const Protocol = "orrery.retrieval-evidence.v1"

var ErrInputChanged = errors.New("retrieval source or access changed; retrieve again")

const (
	PurposeMaterial   = "material"
	PurposeMemory     = "memory_record"
	PurposeHistorical = "historical_record"
	TruthUnknown      = "unknown"
	TruthDisputed     = "disputed"
)

// Source keeps provider identities separate from Orrery identities. Version is
// an opaque host fingerprint; it must not be described as a provider revision.
type Source struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	ContainerID string `json:"container_id,omitempty"`
	ProviderID  string `json:"provider_id,omitempty"`
	Version     string `json:"version,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
}

type Assertion struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	TextHash   string `json:"text_hash"`
	TruthState string `json:"truth_state"`
}

type Item struct {
	Protocol   string `json:"protocol"`
	Purpose    string `json:"purpose"`
	TruthState string `json:"truth_state"`
	Source     Source `json:"source"`
	// record_verified, catalog_verified, or unversioned. Catalog verification
	// does not attest that a remote snippet came from that catalog version.
	SourceState    string      `json:"source_state"`
	AccessHash     string      `json:"access_hash,omitempty"`
	CandidateHash  string      `json:"candidate_hash,omitempty"`
	ValidFrom      *time.Time  `json:"valid_from,omitempty"`
	RetentionUntil *time.Time  `json:"retention_until,omitempty"`
	Assertions     []Assertion `json:"assertions,omitempty"`
	CheckedAt      time.Time   `json:"checked_at"`
	InputHash      string      `json:"input_hash"`
}

func Fingerprint(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Seal always generates a new host stamp. CheckedAt is not part of the input
// version, so checking identical input twice does not invent a new version.
func Seal(item Item, now time.Time) Item {
	item.Protocol, item.InputHash, item.CheckedAt = Protocol, "", time.Time{}
	item.InputHash = Fingerprint(item)
	item.CheckedAt = now.UTC()
	return item
}

func Clone(items []Item) []Item {
	if items == nil {
		return nil
	}
	out := append([]Item(nil), items...)
	for i := range out {
		out[i].Assertions = append([]Assertion(nil), items[i].Assertions...)
		if items[i].ValidFrom != nil {
			value := *items[i].ValidFrom
			out[i].ValidFrom = &value
		}
		if items[i].RetentionUntil != nil {
			value := *items[i].RetentionUntil
			out[i].RetentionUntil = &value
		}
	}
	return out
}

func CloneItem(item *Item) *Item {
	if item == nil {
		return nil
	}
	value := Clone([]Item{*item})[0]
	return &value
}

// Prompt renders only metadata; source text already has its own context budget.
func Prompt(items []Item) string {
	if len(items) == 0 {
		return ""
	}
	type entry struct {
		Source      Source    `json:"source"`
		Purpose     string    `json:"purpose"`
		TruthState  string    `json:"truth_state"`
		SourceState string    `json:"source_state"`
		CheckedAt   time.Time `json:"checked_at"`
		InputHash   string    `json:"input_hash"`
	}
	out := make([]entry, 0, len(items))
	for _, item := range items {
		out = append(out, entry{item.Source, item.Purpose, item.TruthState, item.SourceState, item.CheckedAt, item.InputHash})
	}
	body, _ := json.Marshal(out)
	return Guidance + "\nRetrieval evidence: " + string(body)
}

const Guidance = "Retrieval evidence records host source/access checks, not truth. Material and memory assertions with truth_state unknown are source statements, not verified current facts. Disputed assertions need resolution. Historical records describe past experience; retention_until is a storage deadline, not a business validity end. References and lineage are provenance, not logical support dependencies. Use current ontology evidence for claims about the present; when ontology_grounded_context is available, recheck explicit claim or exact support dependencies there before reusing derived context."
