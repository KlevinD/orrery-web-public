package observability

import (
	"context"

	"orrery_v2/internal/knowledge/core"
)

type StatusDeps struct {
	KnowledgeBases func(context.Context) (map[core.KnowledgeStatus]int64, error)
	Documents      func(context.Context) (map[core.DocumentStatus]int64, error)
	Jobs           func(context.Context) (map[core.JobStatus]int64, error)
}

type StatusProvider struct {
	deps StatusDeps
}

func NewStatusProvider(deps StatusDeps) *StatusProvider {
	return &StatusProvider{deps: deps}
}

func (p *StatusProvider) KnowledgeBaseStatusCounts(ctx context.Context) (map[string]int64, error) {
	if p == nil || p.deps.KnowledgeBases == nil {
		return map[string]int64{}, nil
	}
	counts, err := p.deps.KnowledgeBases(ctx)
	if err != nil {
		return nil, err
	}
	return knowledgeStatusLabels(counts), nil
}

func (p *StatusProvider) DocumentStatusCounts(ctx context.Context) (map[string]int64, error) {
	if p == nil || p.deps.Documents == nil {
		return map[string]int64{}, nil
	}
	counts, err := p.deps.Documents(ctx)
	if err != nil {
		return nil, err
	}
	return documentStatusLabels(counts), nil
}

func (p *StatusProvider) JobStatusCounts(ctx context.Context) (map[string]int64, error) {
	if p == nil || p.deps.Jobs == nil {
		return map[string]int64{}, nil
	}
	counts, err := p.deps.Jobs(ctx)
	if err != nil {
		return nil, err
	}
	return jobStatusLabels(counts), nil
}

type statusValue interface {
	~string
}

func mapStatusLabels[T statusValue](counts map[T]int64) map[string]int64 {
	out := make(map[string]int64, len(counts))
	for status, count := range counts {
		out[string(status)] = count
	}
	return out
}

func knowledgeStatusLabels(counts map[core.KnowledgeStatus]int64) map[string]int64 {
	return mapStatusLabels(counts)
}

func documentStatusLabels(counts map[core.DocumentStatus]int64) map[string]int64 {
	return mapStatusLabels(counts)
}

func jobStatusLabels(counts map[core.JobStatus]int64) map[string]int64 {
	return mapStatusLabels(counts)
}
