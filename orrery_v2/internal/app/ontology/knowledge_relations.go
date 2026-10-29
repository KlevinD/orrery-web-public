package ontology

import "slices"

// KnowledgeRelationCodes are directed meanings offered to the document model.
// They do not create LinkTypes or grant authority over business domains.
func KnowledgeRelationCodes() []string {
	return []string{"subsidiary_of", "owns", "supplies", "customer_of", "works_for", "party_to", "produces", "located_in", "depends_on", "related_to"}
}

func ValidKnowledgeRelationCode(code string) bool {
	return slices.Contains(KnowledgeRelationCodes(), code)
}
