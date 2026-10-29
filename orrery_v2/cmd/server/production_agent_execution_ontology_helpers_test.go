package main

import (
	"orrery_v2/internal/app/contextgraph"
)

func contextGraphLinksContain(links []contextgraph.Link, linkType, fromID, toID string) bool {
	for _, link := range links {
		if link.LinkType == linkType && link.FromObjectID == fromID && link.ToObjectID == toID {
			return true
		}
	}
	return false
}
