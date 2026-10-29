package contextview

import (
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
)

// Expose only the governed semantic field, never arbitrary link properties.
func visibleRelationCode(link contextgraph.Link, masked map[string]bool) string {
	if link.LinkType != "knowledge_entity_related_to" || masked["link_type"] || masked["status"] || masked["properties"] || masked["properties.*"] || masked["properties.relation_code"] || masked["relation_code"] {
		return ""
	}
	code, _ := link.Properties["relation_code"].(string)
	if !ontology.ValidKnowledgeRelationCode(code) {
		return ""
	}
	return code
}
