package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/knowledgeontology"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/operations"
	knowledgecore "orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
	knowledgestore "orrery_v2/internal/knowledge/store"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
)

const (
	productionKnowledgeOntologyConcurrency              = 2
	productionKnowledgeOntologyBatchSize                = 2
	productionKnowledgeOntologySweepInterval            = productionIncrementalMaintenanceInterval
	productionKnowledgeOntologyBindingReconcileInterval = productionIncrementalMaintenanceInterval
)

var productionKnowledgeOntologyBackoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute}

type knowledgeOntologyBindingStore interface {
	ListEnabledBindings(context.Context) ([]knowledgeontology.Binding, error)
}

type knowledgeOntologyOwnedBindingStore interface {
	ReconcileOwnedBinding(context.Context, knowledgeontology.Binding) error
}

type knowledgeOntologyOrganizationCatalog interface {
	ListOrganizations(context.Context, operations.OrgGraphQuery) (operations.OrgGraphDTO, error)
}

type knowledgeOntologyRunner interface {
	Run(context.Context, knowledgeontology.Binding, int) (knowledgeontology.RunReport, error)
}

type knowledgeOntologyRunnerFactory func() (knowledgeOntologyRunner, error)

type knowledgeOntologyRetry struct {
	attempt int
	next    time.Time
}

type productionKnowledgeOntologySweeper struct {
	bindings             knowledgeOntologyBindingStore
	repo                 knowledgestore.Repository
	organizations        knowledgeOntologyOrganizationCatalog
	catalog              knowledgeOntologyCatalogProjector
	newRunner            knowledgeOntologyRunnerFactory
	logger               logging.Logger
	now                  func() time.Time
	mu                   sync.Mutex
	retries              map[string]knowledgeOntologyRetry
	lastBindingReconcile time.Time
	afterBinding         string
}

func (s *productionKnowledgeOntologySweeper) Sweep(ctx context.Context) error {
	if s == nil || s.bindings == nil || s.newRunner == nil {
		return fmt.Errorf("knowledge ontology sweeper dependencies are required")
	}
	var sweepErr error
	if s.repo != nil {
		ownedBindings, ok := s.bindings.(knowledgeOntologyOwnedBindingStore)
		if !ok {
			return fmt.Errorf("knowledge ontology owner binding store is required")
		}
		if s.shouldReconcileOwnedBindings() {
			sweepErr = errors.Join(sweepErr, reconcileProductionKnowledgeOntologyBindings(ctx, s.repo, ownedBindings, s.organizations))
		}
	}
	bindings, err := s.bindings.ListEnabledBindings(ctx)
	if err != nil {
		return errors.Join(sweepErr, err)
	}
	if len(bindings) == 0 {
		return sweepErr
	}
	bindings = s.selectReadyBindings(bindings)
	if len(bindings) == 0 {
		return sweepErr
	}
	runner, err := s.newRunner()
	if err != nil {
		return errors.Join(sweepErr, err)
	}
	sem := make(chan struct{}, productionKnowledgeOntologyConcurrency)
	var wg sync.WaitGroup
	var resultMu sync.Mutex
	var resultErr error
	for _, binding := range bindings {
		binding := binding
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			var report knowledgeontology.RunReport
			var runErr error
			if s.catalog != nil {
				runErr = s.catalog.EnsureKnowledgeBaseCatalog(ctx, binding)
			}
			if runErr == nil {
				report, runErr = runner.Run(ctx, binding, 200)
			}
			s.record(binding, runErr)
			if runErr == nil && s.logger != nil && (report.Events > 0 || report.Projected > 0 || report.Tombstoned > 0 || report.Reconciled) {
				s.logger.Info(context.WithoutCancel(ctx), "knowledge ontology incremental sync completed",
					logging.String("tenant_id", binding.TenantID),
					logging.String("knowledge_base_id", binding.KnowledgeBaseID),
					logging.Int("events", report.Events),
					logging.Int("projected", report.Projected),
					logging.Int("tombstoned", report.Tombstoned),
					logging.Any("reconciled", report.Reconciled),
				)
			}
			if runErr != nil {
				resultMu.Lock()
				resultErr = errors.Join(resultErr, fmt.Errorf("sync knowledge base %s: %w", binding.KnowledgeBaseID, runErr))
				resultMu.Unlock()
			}
		}()
	}
	wg.Wait()
	return errors.Join(sweepErr, resultErr)
}

func reconcileProductionKnowledgeOntologyBindings(
	ctx context.Context,
	repo knowledgestore.Repository,
	bindings knowledgeOntologyOwnedBindingStore,
	organizations knowledgeOntologyOrganizationCatalog,
) error {
	if repo == nil || bindings == nil {
		return fmt.Errorf("knowledge ontology binding reconciliation dependencies are required")
	}
	page, err := repo.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{Status: knowledgecore.KnowledgeActive})
	if err != nil {
		return err
	}
	resolver := productionKnowledgeOntologyOwnerResolver{organizations: organizations}
	var resultErr error
	for _, base := range page.Items {
		folderID := strings.TrimSpace(base.ProviderResourceID)
		if base.Status != knowledgecore.KnowledgeActive || !isProductionPageIndexKnowledgeBase(base) || folderID == "" {
			continue
		}
		tenantID, sourceAccessRef, supported, err := resolver.Resolve(ctx, base)
		if err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("resolve knowledge base %s ontology owner: %w", base.ID, err))
			continue
		}
		if !supported {
			continue
		}
		if err := bindings.ReconcileOwnedBinding(ctx, knowledgeontology.Binding{
			TenantID: tenantID, KnowledgeBaseID: string(base.ID), FolderID: folderID, Enabled: true,
			SourceAccessRef: sourceAccessRef,
		}); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("reconcile knowledge base %s ontology owner: %w", base.ID, err))
		}
	}
	return resultErr
}

type productionKnowledgeOntologyOwnerResolver struct {
	organizations knowledgeOntologyOrganizationCatalog
	graph         operations.OrgGraphDTO
	loaded        bool
	loadErr       error
}

func (r *productionKnowledgeOntologyOwnerResolver) Resolve(ctx context.Context, base knowledgecore.KnowledgeBase) (string, contextgraph.AccessRef, bool, error) {
	ownerID := strings.TrimSpace(base.OwnerID)
	switch base.OwnerType {
	case knowledgecore.OwnerOrganization:
		if ownerID == "" {
			return "", contextgraph.AccessRef{}, true, fmt.Errorf("organization owner id is required")
		}
		return ownerID, organizationKnowledgeOntologyAccessRef(ownerID), true, nil
	case knowledgecore.OwnerPosition:
		if ownerID == "" {
			return "", contextgraph.AccessRef{}, true, fmt.Errorf("position owner id is required")
		}
		graph, err := r.organizationGraph(ctx)
		if err != nil {
			return "", contextgraph.AccessRef{}, true, err
		}
		departmentID := ""
		for _, position := range graph.Positions {
			if strings.TrimSpace(position.PositionID) == ownerID {
				departmentID = strings.TrimSpace(position.DepartmentID)
				break
			}
		}
		if departmentID == "" {
			return "", contextgraph.AccessRef{}, true, fmt.Errorf("position %s is not present in the organization catalog", ownerID)
		}
		tenantID := ""
		for _, department := range graph.Departments {
			if strings.TrimSpace(department.DepartmentID) == departmentID {
				tenantID = strings.TrimSpace(department.OrganizationID)
				break
			}
		}
		if tenantID == "" {
			return "", contextgraph.AccessRef{}, true, fmt.Errorf("department %s for position %s has no organization", departmentID, ownerID)
		}
		for _, organization := range graph.Organizations {
			if strings.TrimSpace(organization.OrganizationID) == tenantID {
				return tenantID, positionKnowledgeOntologyAccessRef(base, ownerID), true, nil
			}
		}
		return "", contextgraph.AccessRef{}, true, fmt.Errorf("organization %s for position %s is not present in the organization catalog", tenantID, ownerID)
	default:
		return "", contextgraph.AccessRef{}, false, nil
	}
}

func (r *productionKnowledgeOntologyOwnerResolver) organizationGraph(ctx context.Context) (operations.OrgGraphDTO, error) {
	if !r.loaded {
		r.loaded = true
		if r.organizations == nil {
			r.loadErr = fmt.Errorf("organization catalog is required for position-owned knowledge bases")
		} else {
			r.graph, r.loadErr = r.organizations.ListOrganizations(ctx, operations.OrgGraphQuery{})
		}
	}
	return r.graph, r.loadErr
}

func organizationKnowledgeOntologyAccessRef(tenantID string) contextgraph.AccessRef {
	tenantID = strings.TrimSpace(tenantID)
	return contextgraph.AccessRef{Kind: string(knowledgecore.OwnerOrganization), ID: tenantID, TenantID: tenantID}
}

func positionKnowledgeOntologyAccessRef(base knowledgecore.KnowledgeBase, positionID string) contextgraph.AccessRef {
	// Projections live in the organization tenant, while source visibility stays governed by the position-owned knowledge base ACL.
	return contextgraph.AccessRef{Kind: "knowledge_base", ID: strings.TrimSpace(string(base.ID)), TenantID: strings.TrimSpace(positionID)}
}

func isProductionPageIndexKnowledgeBase(base knowledgecore.KnowledgeBase) bool {
	if base.EffectiveProvider() == knowledgecore.ProviderPageIndex {
		return true
	}
	if base.EffectiveProvider() != knowledgecore.ProviderMCP {
		return false
	}
	bindingID := strings.ToLower(strings.TrimSpace(base.ProviderMetadata["binding_id"]))
	return strings.Contains(bindingID, "pageindex")
}

func (s *productionKnowledgeOntologySweeper) shouldReconcileOwnedBindings() bool {
	now := s.currentTime()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.lastBindingReconcile.IsZero() && now.Sub(s.lastBindingReconcile) < productionKnowledgeOntologyBindingReconcileInterval {
		return false
	}
	s.lastBindingReconcile = now
	return true
}

func (s *productionKnowledgeOntologySweeper) selectReadyBindings(bindings []knowledgeontology.Binding) []knowledgeontology.Binding {
	ready := make([]knowledgeontology.Binding, 0, len(bindings))
	for _, binding := range bindings {
		if s.ready(binding) {
			ready = append(ready, binding)
		}
	}
	if len(ready) == 0 {
		return nil
	}
	sort.Slice(ready, func(i, j int) bool {
		return bindingKey(ready[i]) < bindingKey(ready[j])
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	start := sort.Search(len(ready), func(i int) bool {
		return bindingKey(ready[i]) > s.afterBinding
	})
	if start == len(ready) {
		start = 0
	}
	limit := productionKnowledgeOntologyBatchSize
	if len(ready) < limit {
		limit = len(ready)
	}
	selected := make([]knowledgeontology.Binding, 0, limit)
	for offset := 0; offset < limit; offset++ {
		selected = append(selected, ready[(start+offset)%len(ready)])
	}
	s.afterBinding = bindingKey(selected[len(selected)-1])
	return selected
}

func (s *productionKnowledgeOntologySweeper) ready(binding knowledgeontology.Binding) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	retry, ok := s.retries[bindingKey(binding)]
	return !ok || !s.currentTime().Before(retry.next)
}

func (s *productionKnowledgeOntologySweeper) record(binding knowledgeontology.Binding, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retries == nil {
		s.retries = map[string]knowledgeOntologyRetry{}
	}
	key := bindingKey(binding)
	if err == nil {
		delete(s.retries, key)
		return
	}
	retry := s.retries[key]
	delayIndex := retry.attempt
	if delayIndex >= len(productionKnowledgeOntologyBackoff) {
		delayIndex = len(productionKnowledgeOntologyBackoff) - 1
	}
	retry.next = s.currentTime().Add(productionKnowledgeOntologyBackoff[delayIndex])
	retry.attempt++
	s.retries[key] = retry
}

func (s *productionKnowledgeOntologySweeper) currentTime() time.Time {
	if s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func bindingKey(binding knowledgeontology.Binding) string {
	return binding.TenantID + "\x1f" + binding.KnowledgeBaseID
}

func registerProductionKnowledgeOntologySweeper(rt *platform.Runtime, coreStore *db.Store, repo knowledgestore.Repository, registry dynamicKnowledgeProviderRegistry, projectionRunner *ontology.MappingRuleProjectionRunner) error {
	if rt == nil || coreStore == nil || repo == nil || projectionRunner == nil {
		return fmt.Errorf("knowledge ontology production dependencies are required")
	}
	bridgeStore := knowledgeontology.NewPlatformStore(coreStore).WithSourceLifecycle(productionSourceWithdrawal(coreStore), productionSourceCatalog)
	graph := contextgraph.NewPlatformStore(coreStore)
	metadataSink, err := newProductionOntologyDocumentMetadataSinkWithRunner(coreStore, projectionRunner)
	if err != nil {
		return err
	}
	factory := func() (knowledgeOntologyRunner, error) {
		source, ok := registry.Provider(knowledgecore.ProviderPageIndex).(provider.RevisionProvider)
		if !ok || source == nil {
			return nil, fmt.Errorf("PageIndex revision provider is not configured")
		}
		validator := knowledgeontology.NewOntologyLinkValidator(ontology.NewPlatformRegistry(coreStore), graph)
		applier := knowledgeontology.NewGraphApplier(graph, validator)
		return knowledgeontology.NewSynchronizedRunner(source, graph, bridgeStore, repo, applier), nil
	}
	sweeper := &productionKnowledgeOntologySweeper{
		bindings: bridgeStore, repo: repo, organizations: operations.NewOrganizationCatalogStore(rt.DBRouter),
		catalog:   productionKnowledgeOntologyCatalogProjector{repo: repo, graph: graph, sink: metadataSink},
		newRunner: factory, logger: rt.Logger, now: rt.Clock.Now, retries: map[string]knowledgeOntologyRetry{},
	}
	return rt.RegisterSweeper("knowledge.ontology_bridge", newProductionCadencedSweeper(
		sweeper, rt.Clock.Now, productionKnowledgeOntologySweepInterval,
	))
}
