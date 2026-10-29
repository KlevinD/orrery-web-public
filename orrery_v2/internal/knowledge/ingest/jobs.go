package ingest

import (
	"fmt"

	"orrery_v2/internal/knowledge/core"
)

const (
	TopicIngestDocument = "knowledge.ingest_document"
	TopicReindexBase    = "knowledge.reindex_base"
	TopicDeleteBase     = "knowledge.delete_base"
	TopicDeleteDocument = "knowledge.delete_document"
)

type DocumentIngestPayload struct {
	JobID           core.KnowledgeJobID
	KnowledgeBaseID core.KnowledgeBaseID
	DocumentID      core.KnowledgeDocumentID
}

type ReindexBasePayload struct {
	JobID           core.KnowledgeJobID
	KnowledgeBaseID core.KnowledgeBaseID
}

func LockName(knowledgeBaseID core.KnowledgeBaseID, operation string) string {
	return fmt.Sprintf("knowledge:%s:%s", knowledgeBaseID, operation)
}
