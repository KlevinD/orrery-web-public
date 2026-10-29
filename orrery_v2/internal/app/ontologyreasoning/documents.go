package ontologyreasoning

import (
	"fmt"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/knowledgeontology"
)

// DocumentInput is explicitly requester-submitted material, never an attested
// knowledge-store revision. An external extractor can supply quoted claims;
// frontmatter assertions have a built-in deterministic extraction path.
type DocumentInput struct {
	Text             string          `json:"text"`
	ExtractorVersion string          `json:"extractor_version"`
	Claims           []DocumentClaim `json:"claims,omitempty"`
}

type DocumentClaim struct {
	Quote     string    `json:"quote"`
	Candidate Candidate `json:"candidate"`
}

type DocumentQuote struct {
	EvidenceRef contextgraph.Ref `json:"evidence_ref"`
	Quote       string           `json:"quote,omitempty"`
	Anchor      string           `json:"anchor,omitempty"`
}

type DocumentReceipt struct {
	ContentHash      string          `json:"content_hash"`
	ExtractorVersion string          `json:"extractor_version"`
	SourceBytes      int             `json:"source_bytes"`
	Quotes           []DocumentQuote `json:"quotes"`
}

func ExtractDocumentCandidates(pack ContextPack, documents []DocumentInput) ([]Candidate, []DocumentReceipt, error) {
	if len(documents) > 8 {
		return nil, nil, fmt.Errorf("%w: document budget exceeded", ErrInvalidRequest)
	}
	var candidates []Candidate
	var receipts []DocumentReceipt
	seen := map[string]bool{}
	seenCandidates := map[string]bool{}
	for _, doc := range documents {
		if len(doc.Text) == 0 || len(doc.Text) > 128*1024 || doc.ExtractorVersion == "" || len(doc.ExtractorVersion) > 128 || len(doc.Claims) > 100 {
			return nil, nil, fmt.Errorf("%w: invalid submitted document", ErrInvalidRequest)
		}
		hash := hashString(doc.Text)
		if seen[hash] {
			return nil, nil, fmt.Errorf("%w: duplicate submitted document", ErrInvalidRequest)
		}
		seen[hash] = true
		receipt := DocumentReceipt{ContentHash: hash, ExtractorVersion: doc.ExtractorVersion, SourceBytes: len(doc.Text)}
		add := func(c Candidate, quote, anchor string) error {
			if c.Kind != CandidateEntity && c.Kind != CandidateLink && c.Kind != CandidateFact {
				return fmt.Errorf("%w: document claim kind is not supported", ErrInvalidRequest)
			}
			c.Operation = OperationSuggest
			c.ID, c.Reason, c.EvidenceRefs = "", "", nil
			if c.Kind == CandidateEntity {
				if c.CanonicalKey == "" {
					return fmt.Errorf("%w: entity claim requires a canonical key", ErrInvalidRequest)
				}
				c.ObjectID = stableID("proposed_object:", []string{pack.TenantID, c.ObjectType, c.CanonicalKey})
			}
			c.ID = stableID("document_candidate:", struct {
				Tenant, Hash, Quote, Anchor string
				Candidate                   Candidate
			}{pack.TenantID, hash, quote, anchor, c})
			if seenCandidates[c.ID] {
				return nil
			}
			seenCandidates[c.ID] = true
			ref := contextgraph.Ref{Kind: "submitted_document", ID: c.ID}
			c.EvidenceRefs = []contextgraph.Ref{ref}
			c.Reason = "claim extracted from requester-submitted material; review required"
			candidates = append(candidates, c)
			receipt.Quotes = append(receipt.Quotes, DocumentQuote{EvidenceRef: ref, Quote: quote, Anchor: anchor})
			return nil
		}
		for _, claim := range doc.Claims {
			if len(claim.Quote) == 0 || len(claim.Quote) > 4000 || !strings.Contains(doc.Text, claim.Quote) {
				return nil, nil, fmt.Errorf("%w: claim quote does not occur in submitted material", ErrValidationFailed)
			}
			if err := add(claim.Candidate, claim.Quote, ""); err != nil {
				return nil, nil, err
			}
		}
		extracted, err := knowledgeontology.ExtractFrontmatter(doc.Text)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrValidationFailed, err)
		}
		for _, assertion := range extracted.Assertions {
			c := Candidate{Kind: CandidateLink, LinkType: assertion.LinkType, From: &ObjectRef{Type: assertion.From.Kind, ID: assertion.From.ID}, To: &ObjectRef{Type: assertion.To.Kind, ID: assertion.To.ID}, Confidence: 1}
			if err := add(c, "", assertion.Anchor); err != nil {
				return nil, nil, err
			}
		}
		receipts = append(receipts, receipt)
	}
	if len(candidates) > 200 {
		return nil, nil, fmt.Errorf("%w: document candidate budget exceeded", ErrValidationFailed)
	}
	return candidates, receipts, nil
}
