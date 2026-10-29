package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/app/knowledgeadmin"
	knowledgecore "orrery_v2/internal/knowledge/core"
)

const (
	productionKnowledgeCatalogSweepInterval = 30 * time.Second
	productionKnowledgeCatalogBatchSize     = 2
)

type productionKnowledgeCatalogRepository interface {
	ListKnowledgeBases(context.Context, knowledgecore.KnowledgeBaseQuery) (knowledgecore.KnowledgeBasePage, error)
}

type productionKnowledgeCatalogReconciler interface {
	Reconcile(context.Context, knowledgecore.KnowledgeBase) (knowledgeadmin.CatalogReconcileResult, error)
}

type productionKnowledgeCatalogSweeper struct {
	repository  productionKnowledgeCatalogRepository
	reconciler  productionKnowledgeCatalogReconciler
	now         func() time.Time
	interval    time.Duration
	mu          sync.Mutex
	lastRun     time.Time
	afterBaseID string
}

func newProductionKnowledgeCatalogSweeper(repository productionKnowledgeCatalogRepository, reconciler productionKnowledgeCatalogReconciler, now func() time.Time, interval time.Duration) *productionKnowledgeCatalogSweeper {
	if repository == nil || reconciler == nil || now == nil {
		return nil
	}
	if interval <= 0 {
		interval = productionKnowledgeCatalogSweepInterval
	}
	return &productionKnowledgeCatalogSweeper{repository: repository, reconciler: reconciler, now: now, interval: interval}
}

func (s *productionKnowledgeCatalogSweeper) Sweep(ctx context.Context) error {
	if s == nil || !s.mu.TryLock() {
		return nil
	}
	defer s.mu.Unlock()
	now := s.now().UTC()
	if !s.lastRun.IsZero() && now.Sub(s.lastRun) < s.interval {
		return nil
	}
	s.lastRun = now
	page, err := s.repository.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{Status: knowledgecore.KnowledgeActive})
	if err != nil {
		return fmt.Errorf("list PageIndex knowledge catalogs: %w", err)
	}
	bases := make([]knowledgecore.KnowledgeBase, 0, len(page.Items))
	for _, base := range page.Items {
		if base.Status == knowledgecore.KnowledgeActive && base.EffectiveProvider() == knowledgecore.ProviderPageIndex && strings.TrimSpace(base.ProviderResourceID) != "" {
			bases = append(bases, base)
		}
	}
	if len(bases) == 0 {
		return nil
	}
	sort.Slice(bases, func(i, j int) bool { return bases[i].ID < bases[j].ID })
	start := sort.Search(len(bases), func(i int) bool { return string(bases[i].ID) > s.afterBaseID })
	if start == len(bases) {
		start = 0
	}
	limit := productionKnowledgeCatalogBatchSize
	if len(bases) < limit {
		limit = len(bases)
	}
	selected := make([]knowledgecore.KnowledgeBase, 0, limit)
	for offset := 0; offset < limit; offset++ {
		selected = append(selected, bases[(start+offset)%len(bases)])
	}
	s.afterBaseID = string(selected[len(selected)-1].ID)

	var failures []error
	for _, base := range selected {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		baseCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := s.reconciler.Reconcile(baseCtx, base)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("reconcile knowledge catalog %s: %w", base.ID, err))
		}
	}
	return errors.Join(failures...)
}
