package knowledgeontology

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

func ExtractFrontmatter(source string) (Extraction, error) {
	body, ok := frontmatterBody(source)
	if !ok {
		return Extraction{}, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(body), &root); err != nil {
		return Extraction{}, fmt.Errorf("decode frontmatter: %w", err)
	}
	ontologyNode := mappingValue(&root, "ontology")
	if ontologyNode == nil {
		return Extraction{}, nil
	}
	encoded, err := yaml.Marshal(ontologyNode)
	if err != nil {
		return Extraction{}, fmt.Errorf("encode ontology frontmatter: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(encoded))
	decoder.KnownFields(true)
	var extracted Extraction
	if err := decoder.Decode(&extracted); err != nil && err != io.EOF {
		return Extraction{}, fmt.Errorf("decode ontology frontmatter: %w", err)
	}
	for index := range extracted.Subjects {
		ref, err := normalizeRef(extracted.Subjects[index])
		if err != nil {
			return Extraction{}, fmt.Errorf("ontology subject %d: %w", index, err)
		}
		extracted.Subjects[index] = ref
	}
	for index := range extracted.Assertions {
		assertion := &extracted.Assertions[index]
		assertion.LinkType = strings.ToLower(strings.TrimSpace(assertion.LinkType))
		if assertion.LinkType == "" {
			return Extraction{}, fmt.Errorf("ontology assertion %d: link_type is required", index)
		}
		from, err := normalizeRef(assertion.From)
		if err != nil {
			return Extraction{}, fmt.Errorf("ontology assertion %d from: %w", index, err)
		}
		to, err := normalizeRef(assertion.To)
		if err != nil {
			return Extraction{}, fmt.Errorf("ontology assertion %d to: %w", index, err)
		}
		assertion.From = from
		assertion.To = to
		assertion.Anchor = fmt.Sprintf("frontmatter:ontology.assertions[%d]", index)
	}
	return extracted, nil
}

func frontmatterBody(source string) (string, bool) {
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return "", false
	}
	remainder := normalized[len("---\n"):]
	end := strings.Index(remainder, "\n---")
	if end < 0 {
		return "", false
	}
	return remainder[:end], true
}

func mappingValue(root *yaml.Node, key string) *yaml.Node {
	if root == nil || len(root.Content) == 0 {
		return nil
	}
	node := root.Content[0]
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func normalizeRef(ref ObjectRef) (ObjectRef, error) {
	ref.Kind = strings.ToLower(strings.TrimSpace(ref.Kind))
	ref.ID = strings.TrimSpace(ref.ID)
	if ref.Kind == "" || ref.ID == "" {
		return ObjectRef{}, fmt.Errorf("kind and id are required")
	}
	return ref, nil
}
