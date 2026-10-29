package main

import (
	"context"
	"time"

	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/knowledge/observability"
)

type knowledgeSearchMetricsService struct {
	next interface {
		SearchKnowledge(context.Context, knowledgeadmin.ViewerRef, knowledgeadmin.SearchKnowledgeCommand) (knowledgeadmin.KnowledgeSearchPageDTO, error)
	}
	metrics *observability.Metrics
	now     func() time.Time
}

func (s knowledgeSearchMetricsService) SearchKnowledge(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.SearchKnowledgeCommand) (knowledgeadmin.KnowledgeSearchPageDTO, error) {
	started := s.nowTime()
	page, err := s.next.SearchKnowledge(ctx, viewer, cmd)
	if err == nil && s.metrics != nil {
		s.metrics.RecordSearch(s.nowTime().Sub(started), len(page.Results))
	}
	return page, err
}

func (s knowledgeSearchMetricsService) nowTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}
