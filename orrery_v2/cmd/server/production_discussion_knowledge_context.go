package main

import (
	"strings"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/retrievalevidence"
)

func mapDiscussionKnowledgeContext(results []knowledgeadmin.KnowledgeSearchResultDTO) discussionsession.DiscussionAgentKnowledgeContext {
	out := discussionsession.DiscussionAgentKnowledgeContext{Results: make([]discussionsession.DiscussionAgentKnowledgeResult, 0, len(results))}
	for _, result := range results {
		if strings.TrimSpace(result.Snippet) == "" {
			continue
		}
		out.Results = append(out.Results, discussionsession.DiscussionAgentKnowledgeResult{
			Evidence:        retrievalevidence.CloneItem(result.Evidence),
			KnowledgeBaseID: result.KnowledgeBaseID,
			DocumentID:      result.DocumentID,
			ChunkID:         result.ChunkID,
			DocumentName:    result.DocumentName,
			Score:           result.Score,
			Snippet:         strings.TrimSpace(result.Snippet),
			HeadingPath:     append([]string(nil), result.HeadingPath...),
		})
	}
	return out
}
