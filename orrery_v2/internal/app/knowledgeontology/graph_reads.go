package knowledgeontology

import (
	"context"
	"orrery_v2/internal/app/contextgraph"
)

func graphObjects(ctx context.Context, graph GraphSnapshot, tenantID string) ([]contextgraph.Object, error) {
	if reader, ok := graph.(interface {
		Objects(context.Context, string) ([]contextgraph.Object, error)
	}); ok {
		return reader.Objects(ctx, tenantID)
	}
	objects, _, _, _, err := graph.Snapshot(ctx, tenantID)
	return objects, err
}

func graphObjectsByID(ctx context.Context, graph GraphSnapshot, tenantID string, ids []string) ([]contextgraph.Object, error) {
	if reader, ok := graph.(interface {
		ObjectsByID(context.Context, string, []string) ([]contextgraph.Object, error)
	}); ok {
		return reader.ObjectsByID(ctx, tenantID, ids)
	}
	return graphObjects(ctx, graph, tenantID)
}

func graphDocumentSnapshot(ctx context.Context, graph GraphSnapshot, tenantID, documentID string) ([]contextgraph.Object, []contextgraph.Link, []contextgraph.Fact, []contextgraph.Evidence, error) {
	if reader, ok := graph.(interface {
		SnapshotKnowledgeDocument(context.Context, string, string) ([]contextgraph.Object, []contextgraph.Link, []contextgraph.Fact, []contextgraph.Evidence, error)
	}); ok {
		return reader.SnapshotKnowledgeDocument(ctx, tenantID, documentID)
	}
	return graph.Snapshot(ctx, tenantID)
}

func graphDocumentRelations(ctx context.Context, graph GraphSnapshot, tenantID, documentID string) ([]contextgraph.Link, []contextgraph.Evidence, error) {
	if reader, ok := graph.(interface {
		KnowledgeDocumentRelations(context.Context, string, string) ([]contextgraph.Link, []contextgraph.Evidence, error)
	}); ok {
		return reader.KnowledgeDocumentRelations(ctx, tenantID, documentID)
	}
	_, links, _, evidence, err := graph.Snapshot(ctx, tenantID)
	return links, evidence, err
}
