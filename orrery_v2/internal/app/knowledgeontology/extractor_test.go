package knowledgeontology

import "testing"

func TestExtractFrontmatterUsesCanonicalOntologyShape(t *testing.T) {
	source := `---
title: Orrery knowledge architecture
ontology:
  subjects:
    - kind: project
      id: orrery
  assertions:
    - link_type: project_uses_tool
      from:
        kind: project
        id: orrery
      to:
        kind: tool
        id: pageindex
---
# Architecture
`

	extracted, err := ExtractFrontmatter(source)
	if err != nil {
		t.Fatalf("ExtractFrontmatter: %v", err)
	}
	if len(extracted.Subjects) != 1 || extracted.Subjects[0] != (ObjectRef{Kind: "project", ID: "orrery"}) {
		t.Fatalf("subjects = %+v", extracted.Subjects)
	}
	if len(extracted.Assertions) != 1 || extracted.Assertions[0].LinkType != "project_uses_tool" {
		t.Fatalf("assertions = %+v", extracted.Assertions)
	}
	if extracted.Assertions[0].Anchor != "frontmatter:ontology.assertions[0]" {
		t.Fatalf("anchor = %q", extracted.Assertions[0].Anchor)
	}
}

func TestExtractFrontmatterRejectsUnknownOntologyFields(t *testing.T) {
	_, err := ExtractFrontmatter(`---
ontology:
  subjects: []
  create_missing_objects: true
---
`)
	if err == nil {
		t.Fatal("expected unknown ontology field to fail")
	}
}

func TestExtractFrontmatterTreatsMissingOntologyAsEmpty(t *testing.T) {
	extracted, err := ExtractFrontmatter("# Plain document\n")
	if err != nil {
		t.Fatalf("ExtractFrontmatter: %v", err)
	}
	if len(extracted.Subjects) != 0 || len(extracted.Assertions) != 0 {
		t.Fatalf("extracted = %+v", extracted)
	}
}

func TestExtractFrontmatterRejectsIncompleteReferences(t *testing.T) {
	_, err := ExtractFrontmatter(`---
ontology:
  assertions:
    - link_type: project_uses_tool
      from: {kind: project, id: orrery}
      to: {kind: tool}
---
`)
	if err == nil {
		t.Fatal("expected incomplete reference to fail")
	}
}
