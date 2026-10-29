package main

import (
	"context"
	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/knowledgeontology"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/platform/db"
)

func productionSourceWithdrawal(core *db.Store) knowledgeontology.SourceWithdrawalHandler {
	return func(ctx context.Context, tx db.Tx, event knowledgeontology.SourceWithdrawal) error {
		h := event.Source
		input := knowledgeinstances.SourceWithdrawal{EventID: event.ID, EventType: event.EventType, Sequence: event.Sequence, Source: knowledgeinstances.Source{TenantID: h.TenantID, KnowledgeBaseID: h.KnowledgeBaseID, FolderID: h.FolderID, DocumentID: h.DocumentID, RootID: h.RootID, Revision: h.Revision, ContentHash: h.ContentHash, Generation: h.Generation, Access: h.Access}}
		if _, err := (&knowledgeinstances.Store{Core: core}).WithdrawSourceTx(ctx, tx, input, event.Fence); err != nil {
			return err
		}
		return ontologyreasoning.QueueSourceChangeTx(ctx, tx, h.TenantID, event.ID)
	}
}

func productionSourceCatalog(ctx context.Context, tx db.Tx, b knowledgeontology.Binding) ([]knowledgeontology.SourceHead, error) {
	sources, err := knowledgeinstances.SourceCatalogTx(ctx, tx, b.TenantID, b.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}
	var result []knowledgeontology.SourceHead
	for _, s := range sources {
		result = append(result, knowledgeontology.SourceHead{TenantID: s.TenantID, KnowledgeBaseID: s.KnowledgeBaseID, FolderID: s.FolderID, DocumentID: s.DocumentID, RootID: s.RootID, Revision: s.Revision, ContentHash: s.ContentHash, Generation: s.Generation, Access: s.Access})
	}
	return result, nil
}
