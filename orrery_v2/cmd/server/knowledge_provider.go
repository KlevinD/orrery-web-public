package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/userauth"
	knowledgecore "orrery_v2/internal/knowledge/core"
	knowledgeingest "orrery_v2/internal/knowledge/ingest"
	"orrery_v2/internal/knowledge/provider"
	localprovider "orrery_v2/internal/knowledge/provider/local"
	pageindexprovider "orrery_v2/internal/knowledge/provider/pageindex"
	knowledgestore "orrery_v2/internal/knowledge/store"
	"orrery_v2/internal/knowledge/vector"
	toolstd "orrery_v2/internal/resource/tool/std"
	httptransport "orrery_v2/internal/transport/http"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type dynamicKnowledgeProviderRegistry struct {
	settings      productionSettingsStore
	settingsCache *knowledgeSettingsSnapshot
	secrets       platformSecretResolver
	repo          knowledgestore.Repository
	vector        vector.Store
	embedding     knowledgeingest.EmbeddingProvider
	ids           localprovider.IDGenerator
	clock         localprovider.Clock
	artifacts     localprovider.ArtifactWriter
	execution     localprovider.ExecutionSubmitter
	maxBytes      int64
	mcp           provider.Provider
}

type knowledgeSettingsSnapshot struct {
	mu       sync.RWMutex
	settings httptransport.SettingsDTO
	ready    bool
}

func (s *knowledgeSettingsSnapshot) Remember(settings httptransport.SettingsDTO) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.settings = settings
	s.ready = true
	s.mu.Unlock()
}

func (s *knowledgeSettingsSnapshot) Load() (httptransport.SettingsDTO, bool) {
	if s == nil {
		return httptransport.SettingsDTO{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings, s.ready
}

type localArtifactWriterAdapter struct {
	next platformKnowledgeArtifacts
}

func (w localArtifactWriterAdapter) PutArtifact(ctx context.Context, name, contentType string, reader io.Reader) (localprovider.ArtifactRef, error) {
	ref, err := w.next.PutArtifact(ctx, name, contentType, reader)
	if err != nil {
		return localprovider.ArtifactRef{}, err
	}
	return localprovider.ArtifactRef{
		ID:          ref.ID,
		Name:        ref.Name,
		ContentType: ref.ContentType,
		SizeBytes:   ref.SizeBytes,
		Checksum:    ref.Checksum,
	}, nil
}

func (r dynamicKnowledgeProviderRegistry) Default() provider.Provider {
	settings := r.settingsDTO(context.Background())
	return r.Provider(knowledgecore.KnowledgeProvider(strings.TrimSpace(settings.Knowledge.DefaultProvider)))
}

func (r dynamicKnowledgeProviderRegistry) ForBase(base knowledgecore.KnowledgeBase) provider.Provider {
	return r.Provider(base.EffectiveProvider())
}

func (r dynamicKnowledgeProviderRegistry) Provider(name knowledgecore.KnowledgeProvider) provider.Provider {
	settings := r.settingsDTO(context.Background())
	if name == "" {
		name = knowledgecore.KnowledgeProvider(strings.TrimSpace(settings.Knowledge.DefaultProvider))
	}
	switch name {
	case knowledgecore.ProviderPageIndex:
		apiKey := firstNonBlank(settings.Knowledge.PageIndex.APIKey, resolveOptionalSecret(context.Background(), r.secrets, settings.Knowledge.PageIndex.APIKeyRef))
		return pageindexprovider.New(pageindexprovider.Config{
			Disabled:  !settings.Knowledge.Enabled,
			APIURL:    settings.Knowledge.PageIndex.APIURL,
			APIKey:    apiKey,
			APIKeyRef: settings.Knowledge.PageIndex.APIKeyRef,
			Timeout:   time.Duration(settings.Knowledge.PageIndex.TimeoutSeconds) * time.Second,
		})
	case knowledgecore.ProviderLocal:
		return localprovider.New(localprovider.Options{
			Repository: r.repo,
			Vector:     r.vector,
			Embedding:  r.embedding,
			IDs:        r.ids,
			Clock:      r.clock,
			Artifacts:  r.artifacts,
			Execution:  r.execution,
			MaxBytes:   r.maxBytes,
		})
	case knowledgecore.ProviderMCP:
		return r.mcp
	default:
		return nil
	}
}

func (r dynamicKnowledgeProviderRegistry) settingsDTO(ctx context.Context) httptransport.SettingsDTO {
	settings, err := r.settings.GetSettings(ctx)
	if err == nil {
		r.settingsCache.Remember(settings)
		return settings
	}
	if cached, ok := r.settingsCache.Load(); ok {
		return cached
	}
	return r.settings.productionDefaultSettings()
}

func resolveOptionalSecret(ctx context.Context, resolver platformSecretResolver, ref string) string {
	if strings.TrimSpace(ref) == "" {
		return ""
	}
	value, err := resolver.ResolveSecret(ctx, ref)
	if err != nil {
		return ""
	}
	return value
}

func shouldBootstrapKnowledgeDefaults(ctx context.Context, settingsStore productionSettingsStore) bool {
	settings, err := settingsStore.GetSettings(ctx)
	if err != nil {
		return false
	}
	if !settings.Knowledge.Enabled {
		return false
	}
	switch strings.TrimSpace(settings.Knowledge.DefaultProvider) {
	case string(knowledgecore.ProviderLocal):
		return settings.Knowledge.Local.Enabled
	case string(knowledgecore.ProviderPageIndex):
		return pageIndexKnowledgeConfigured(settings.Knowledge.PageIndex)
	default:
		return false
	}
}

type settingsAwareOperationsKnowledgeLifecycle struct {
	settings productionSettingsStore
	next     interface {
		CreateKnowledgeBase(context.Context, knowledgeadmin.ViewerRef, knowledgeadmin.CreateKnowledgeBaseCommand) (knowledgeadmin.KnowledgeBaseDTO, error)
		DeleteKnowledgeBase(context.Context, knowledgeadmin.ViewerRef, string) error
	}
}

func (l settingsAwareOperationsKnowledgeLifecycle) CreateKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.CreateKnowledgeBaseCommand) (knowledgeadmin.KnowledgeBaseDTO, error) {
	if l.next == nil || !operationsKnowledgeLifecycleEnabled(ctx, l.settings) {
		return knowledgeadmin.KnowledgeBaseDTO{}, nil
	}
	return l.next.CreateKnowledgeBase(ctx, viewer, cmd)
}

func (l settingsAwareOperationsKnowledgeLifecycle) DeleteKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, id string) error {
	if l.next == nil || strings.TrimSpace(id) == "" || !operationsKnowledgeLifecycleEnabled(ctx, l.settings) {
		return nil
	}
	return l.next.DeleteKnowledgeBase(ctx, viewer, id)
}

func operationsKnowledgeLifecycleEnabled(ctx context.Context, settingsStore productionSettingsStore) bool {
	settings, err := settingsStore.GetSettings(ctx)
	if err != nil {
		return false
	}
	if !settings.Knowledge.Enabled {
		return false
	}
	switch strings.TrimSpace(settings.Knowledge.DefaultProvider) {
	case string(knowledgecore.ProviderLocal):
		return settings.Knowledge.Local.Enabled
	case string(knowledgecore.ProviderPageIndex):
		return pageIndexKnowledgeConfigured(settings.Knowledge.PageIndex)
	default:
		return false
	}
}

func pageIndexKnowledgeConfigured(settings httptransport.SettingsPageIndexKnowledgeDTO) bool {
	apiURL := strings.TrimSpace(settings.APIURL)
	if apiURL == "" {
		return false
	}
	return strings.TrimSpace(settings.APIKey) != "" || strings.TrimSpace(settings.APIKeyRef) != ""
}

const (
	pageIndexFastSearchTimeout           = 45 * time.Second
	pageIndexAgenticSearchTimeout        = 60 * time.Second
	pageIndexAgenticPrimarySearchTimeout = 20 * time.Second
)

type knowledgeSettingsLookup interface {
	GetSettings(context.Context) (httptransport.SettingsDTO, error)
}

type knowledgeHumanAccountLookup interface {
	GetAccountByUsername(context.Context, string) (userauth.Account, error)
}

type productionKnowledgeToolExecutor struct {
	profiles      knowledgeAgentProfileLookup
	accounts      knowledgeHumanAccountLookup
	settings      knowledgeSettingsLookup
	searchTimeout time.Duration
	search        interface {
		SearchKnowledge(context.Context, knowledgeadmin.ViewerRef, knowledgeadmin.SearchKnowledgeCommand) (knowledgeadmin.KnowledgeSearchPageDTO, error)
	}
	baseFinder interface {
		ListKnowledgeBases(context.Context, knowledgecore.KnowledgeBaseQuery) (knowledgecore.KnowledgeBasePage, error)
	}
	ensureDefault interface {
		EnsureDefaultPersonalKnowledgeBase(context.Context, knowledgeadmin.ViewerRef, knowledgeadmin.EnsureDefaultCommand) (knowledgeadmin.KnowledgeBaseDTO, error)
	}
	upload interface {
		UploadDocument(context.Context, knowledgeadmin.ViewerRef, knowledgeadmin.UploadDocumentCommand) (knowledgeadmin.KnowledgeDocumentDTO, error)
	}
}

type productionDiscussionKnowledgeProvider struct {
	profiles knowledgeAgentProfileLookup
	search   interface {
		SearchKnowledge(context.Context, knowledgeadmin.ViewerRef, knowledgeadmin.SearchKnowledgeCommand) (knowledgeadmin.KnowledgeSearchPageDTO, error)
	}
}

func newProductionDiscussionKnowledgeProvider(profiles knowledgeAgentProfileLookup, search interface {
	SearchKnowledge(context.Context, knowledgeadmin.ViewerRef, knowledgeadmin.SearchKnowledgeCommand) (knowledgeadmin.KnowledgeSearchPageDTO, error)
}) *productionDiscussionKnowledgeProvider {
	return &productionDiscussionKnowledgeProvider{profiles: profiles, search: search}
}

func (e productionKnowledgeToolExecutor) ExecuteKnowledgeTool(ctx context.Context, toolID string, req mcptransport.KnowledgeToolRequest) (mcptransport.KnowledgeToolResult, error) {
	switch strings.TrimSpace(toolID) {
	case string(toolstd.KnowledgeAdd):
		return e.executeKnowledgeAdd(ctx, req)
	default:
		return e.executeKnowledgeSearch(ctx, req)
	}
}

func (e productionKnowledgeToolExecutor) executeKnowledgeSearch(ctx context.Context, req mcptransport.KnowledgeToolRequest) (mcptransport.KnowledgeToolResult, error) {
	retrievalMode, err := provider.NormalizeRetrievalMode(req.RetrievalMode)
	if err != nil {
		return mcptransport.KnowledgeToolResult{}, fmt.Errorf("%w: %v", knowledgeadmin.ErrInvalidRequest, err)
	}
	timeout := e.knowledgeSearchTimeout(ctx, retrievalMode)
	searchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	limit := req.Limit
	if limit <= 0 || limit > 8 {
		limit = 8
	}
	agentID := firstNonBlank(req.AgentID, req.PrincipalID)
	var agentIDs []string
	if strings.TrimSpace(agentID) != "" {
		agentIDs = []string{strings.TrimSpace(agentID)}
	}
	viewer, err := productionKnowledgeViewer(searchCtx, e.profiles, agentID, knowledgeadmin.ViewerRef{
		ViewerID:        firstNonBlank(req.PrincipalID, req.AgentID),
		AgentIDs:        agentIDs,
		HumanIDs:        productionKnowledgeHumanIDs(searchCtx, e.profiles, e.accounts, req),
		OrganizationIDs: productionKnowledgeOrganizationIDs(req),
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead},
	})
	if err != nil {
		return mcptransport.KnowledgeToolResult{}, err
	}
	searchCommand := knowledgeadmin.SearchKnowledgeCommand{
		Query:            req.Query,
		RetrievalMode:    retrievalMode,
		KnowledgeBaseIDs: req.KnowledgeBaseIDs,
		OwnerFilter: knowledgeadmin.OwnerFilter{
			OwnerType: req.OwnerType,
			OwnerID:   req.OwnerID,
		},
		Limit: limit,
	}
	page, err := e.searchKnowledgeWithAgenticFallback(searchCtx, viewer, searchCommand, timeout)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(searchCtx.Err(), context.DeadlineExceeded) {
			return mcptransport.KnowledgeToolResult{}, fmt.Errorf("%w: %w", knowledgeadmin.ErrServiceDependency, context.DeadlineExceeded)
		}
		return mcptransport.KnowledgeToolResult{}, err
	}
	results := make([]any, 0, len(page.Results))
	for _, result := range page.Results {
		results = append(results, map[string]any{
			"knowledge_base_id": result.KnowledgeBaseID,
			"document_id":       result.DocumentID,
			"chunk_id":          result.ChunkID,
			"score":             result.Score,
			"snippet":           result.Snippet,
			"heading_path":      append([]string(nil), result.HeadingPath...),
			"document_name":     result.DocumentName,
		})
	}
	retrievals := make([]any, 0, len(page.Executions))
	for _, execution := range page.Executions {
		retrievals = append(retrievals, map[string]any{
			"provider": execution.Provider, "knowledge_base_id": execution.KnowledgeBaseID,
			"requested_mode": execution.RequestedMode, "actual_mode": execution.ActualMode,
			"fallback_reason": execution.FallbackReason,
		})
	}
	return mcptransport.KnowledgeToolResult{Output: map[string]any{"results": results, "retrievals": retrievals}}, nil
}

func (e productionKnowledgeToolExecutor) searchKnowledgeWithAgenticFallback(
	ctx context.Context,
	viewer knowledgeadmin.ViewerRef,
	cmd knowledgeadmin.SearchKnowledgeCommand,
	totalTimeout time.Duration,
) (knowledgeadmin.KnowledgeSearchPageDTO, error) {
	if cmd.RetrievalMode != provider.RetrievalModeAgentic {
		return e.search.SearchKnowledge(ctx, viewer, cmd)
	}
	primaryTimeout := pageIndexAgenticPrimarySearchTimeout
	if totalTimeout <= primaryTimeout {
		primaryTimeout = totalTimeout / 2
	}
	if primaryTimeout <= 0 {
		return e.search.SearchKnowledge(ctx, viewer, cmd)
	}
	primaryCtx, cancel := context.WithTimeout(ctx, primaryTimeout)
	page, err := e.search.SearchKnowledge(primaryCtx, viewer, cmd)
	primaryDeadlineExceeded := errors.Is(err, context.DeadlineExceeded) || errors.Is(primaryCtx.Err(), context.DeadlineExceeded)
	cancel()
	if err == nil || !primaryDeadlineExceeded || ctx.Err() != nil {
		return page, err
	}

	fallbackCommand := cmd
	fallbackCommand.RetrievalMode = provider.RetrievalModeFast
	page, err = e.search.SearchKnowledge(ctx, viewer, fallbackCommand)
	if err != nil {
		return page, err
	}
	for index := range page.Executions {
		page.Executions[index].RequestedMode = string(provider.RetrievalModeAgentic)
		if strings.TrimSpace(page.Executions[index].FallbackReason) == "" {
			page.Executions[index].FallbackReason = "agentic_timeout_fast_fallback"
		}
	}
	return page, nil
}

func (e productionKnowledgeToolExecutor) knowledgeSearchTimeout(_ context.Context, mode provider.RetrievalMode) time.Duration {
	if e.searchTimeout > 0 {
		return e.searchTimeout
	}
	if mode == provider.RetrievalModeAgentic {
		return pageIndexAgenticSearchTimeout
	}
	return pageIndexFastSearchTimeout
}

type knowledgeAgentProfileLookup interface {
	GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

func productionKnowledgeViewer(ctx context.Context, profiles knowledgeAgentProfileLookup, agentID string, viewer knowledgeadmin.ViewerRef) (knowledgeadmin.ViewerRef, error) {
	agentID = strings.TrimSpace(agentID)
	if profiles == nil || agentID == "" {
		return viewer, nil
	}
	profile, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(agentID))
	if err != nil {
		return knowledgeadmin.ViewerRef{}, err
	}
	if !profile.Enabled {
		return knowledgeadmin.ViewerRef{}, knowledgeadmin.ErrPermissionDenied
	}
	if profile.IsPersonalAssistant() {
		viewer.AgentIDs = removeKnowledgeIdentity(viewer.AgentIDs, profile.ID.String())
		owner, owns := personalAssistantKnowledgePrincipal(ctx, profiles, profile, viewer)
		if !owns {
			return viewer, nil
		}
		viewer.AgentIDs = appendKnowledgeIdentity(viewer.AgentIDs, profile.ID.String())
		viewer.HumanIDs = appendKnowledgeIdentity(viewer.HumanIDs, viewer.ViewerID)
		viewer = appendKnowledgeProfileScope(viewer, owner)
		return viewer, nil
	}
	viewer.AgentIDs = appendKnowledgeIdentity(viewer.AgentIDs, profile.ID.String())
	return appendKnowledgeProfileScope(viewer, profile), nil
}

func appendKnowledgeProfileScope(viewer knowledgeadmin.ViewerRef, profile agentcore.AgentProfile) knowledgeadmin.ViewerRef {
	viewer.OrganizationIDs = appendKnowledgeIdentity(viewer.OrganizationIDs, string(profile.OrgIdentity.OrganizationRef.ID))
	viewer.PositionIDs = appendKnowledgeIdentity(viewer.PositionIDs, string(profile.OrgIdentity.PositionRef.ID))
	for _, role := range profile.OrgIdentity.RoleRefs {
		viewer.RoleIDs = appendKnowledgeIdentity(viewer.RoleIDs, string(role.ID))
	}
	return viewer
}

func personalAssistantKnowledgePrincipal(ctx context.Context, profiles knowledgeAgentProfileLookup, assistant agentcore.AgentProfile, viewer knowledgeadmin.ViewerRef) (agentcore.AgentProfile, bool) {
	ownerID := strings.TrimSpace(assistant.PersonalAssistantOwnerID)
	principalID := strings.TrimSpace(viewer.ViewerID)
	if profiles == nil || ownerID == "" || principalID == "" || principalID == assistant.ID.String() {
		return agentcore.AgentProfile{}, false
	}
	candidateIDs := append([]string{principalID}, viewer.HumanIDs...)
	for _, candidateID := range candidateIDs {
		candidateID = strings.TrimSpace(candidateID)
		if candidateID == "" {
			continue
		}
		owner, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(candidateID))
		if err != nil || !owner.Enabled || owner.AgentKind != agentcore.AgentKindHuman {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(owner.ID.String()), ownerID) ||
			strings.EqualFold(strings.TrimSpace(owner.UserAccount.Username), ownerID) {
			return owner, true
		}
	}
	if strings.EqualFold(ownerID, principalID) {
		return agentcore.AgentProfile{}, true
	}
	return agentcore.AgentProfile{}, false
}

func appendKnowledgeIdentity(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if strings.TrimSpace(existing) == value {
			return values
		}
	}
	return append(values, value)
}

func removeKnowledgeIdentity(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || len(values) == 0 {
		return values
	}
	out := make([]string, 0, len(values))
	for _, existing := range values {
		if !strings.EqualFold(strings.TrimSpace(existing), value) {
			out = append(out, existing)
		}
	}
	return out
}

func productionKnowledgeHumanIDs(ctx context.Context, profiles knowledgeAgentProfileLookup, accounts knowledgeHumanAccountLookup, req mcptransport.KnowledgeToolRequest) []string {
	if agentcore.ExecutionPrincipalKind(strings.TrimSpace(req.PrincipalKind)) != agentcore.ExecutionPrincipalUser {
		return nil
	}
	principalID := strings.TrimSpace(req.PrincipalID)
	if principalID == "" {
		return nil
	}
	humanIDs := []string{principalID}
	if profiles != nil {
		if profile, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(principalID)); err == nil && profile.Enabled && profile.AgentKind == agentcore.AgentKindHuman {
			return appendKnowledgeIdentity(humanIDs, profile.ID.String())
		}
	}
	if accounts == nil || profiles == nil {
		return humanIDs
	}
	account, err := accounts.GetAccountByUsername(ctx, principalID)
	if err != nil || !account.Enabled || strings.TrimSpace(account.AgentProfileID) == "" {
		return humanIDs
	}
	profile, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(account.AgentProfileID))
	if err != nil || !profile.Enabled || profile.AgentKind != agentcore.AgentKindHuman {
		return humanIDs
	}
	return appendKnowledgeIdentity(humanIDs, profile.ID.String())
}

func productionKnowledgeOrganizationIDs(req mcptransport.KnowledgeToolRequest) []string {
	organizationID := runtimeOrganizationTenantID(req.TenantID)
	if organizationID == "" {
		return nil
	}
	return []string{organizationID}
}

func runtimeOrganizationTenantID(tenantID string) string {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || strings.Contains(tenantID, ":") || strings.EqualFold(tenantID, "global") || strings.EqualFold(tenantID, "system") {
		return ""
	}
	return tenantID
}

func (e productionKnowledgeToolExecutor) executeKnowledgeAdd(ctx context.Context, req mcptransport.KnowledgeToolRequest) (mcptransport.KnowledgeToolResult, error) {
	if e.upload == nil {
		return mcptransport.KnowledgeToolResult{}, knowledgeadmin.ErrServiceDependency
	}
	agentID := strings.TrimSpace(firstNonBlank(req.AgentID, req.PrincipalID))
	if agentID == "" {
		return mcptransport.KnowledgeToolResult{}, fmt.Errorf("%w: agent_id is required", knowledgecore.ErrInvalid)
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return mcptransport.KnowledgeToolResult{}, fmt.Errorf("%w: content is required", knowledgecore.ErrInvalid)
	}
	knowledgeBaseID, err := e.knowledgeAddTargetBaseID(ctx, agentID, req)
	if err != nil {
		return mcptransport.KnowledgeToolResult{}, err
	}
	title := strings.TrimSpace(req.Title)
	filename := knowledgeAddFilename(req)
	contentType := strings.TrimSpace(req.ContentType)
	if contentType == "" {
		contentType = "text/markdown; charset=utf-8"
	}
	doc, err := e.upload.UploadDocument(ctx, knowledgeadmin.ViewerRef{
		ViewerID:        firstNonBlank(req.PrincipalID, req.AgentID),
		AgentIDs:        []string{agentID},
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeWrite},
	}, knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: knowledgeBaseID,
		Filename:        filename,
		ContentType:     contentType,
		Reader:          strings.NewReader(knowledgeAddDocumentContent(title, content)),
	})
	if err != nil {
		return mcptransport.KnowledgeToolResult{}, err
	}
	output := map[string]any{
		"knowledge_base_id":    doc.KnowledgeBaseID,
		"document_id":          doc.ID,
		"provider_document_id": doc.ProviderDocumentID,
		"filename":             doc.Filename,
		"content_type":         doc.ContentType,
		"status":               doc.Status,
	}
	if strings.TrimSpace(doc.JobID) != "" {
		output["job_id"] = doc.JobID
	}
	return mcptransport.KnowledgeToolResult{Output: output}, nil
}

func (e productionKnowledgeToolExecutor) knowledgeAddTargetBaseID(ctx context.Context, agentID string, req mcptransport.KnowledgeToolRequest) (string, error) {
	if explicit := strings.TrimSpace(req.KnowledgeBaseID); explicit != "" {
		return explicit, nil
	}
	if len(req.KnowledgeBaseIDs) > 0 && strings.TrimSpace(req.KnowledgeBaseIDs[0]) != "" {
		return strings.TrimSpace(req.KnowledgeBaseIDs[0]), nil
	}
	profileKnowledgeBaseID := ""
	if e.profiles != nil {
		profile, err := e.profiles.GetProfile(ctx, agentcore.AgentProfileID(agentID))
		if err == nil {
			profileKnowledgeBaseID = strings.TrimSpace(profile.PersonalKnowledgeBaseID)
		}
	}
	if e.baseFinder == nil {
		return "", knowledgeadmin.ErrServiceDependency
	}
	page, err := e.baseFinder.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{
		OwnerType: knowledgecore.OwnerAgent,
		OwnerID:   agentID,
		Status:    knowledgecore.KnowledgeActive,
	})
	if err != nil {
		return "", err
	}
	for _, base := range page.Items {
		if profileKnowledgeBaseID != "" && string(base.ID) == profileKnowledgeBaseID {
			return profileKnowledgeBaseID, nil
		}
	}
	for _, base := range page.Items {
		if base.IsDefault {
			return string(base.ID), nil
		}
	}
	if e.ensureDefault == nil {
		return "", fmt.Errorf("%w: default agent knowledge base not found for %s", knowledgecore.ErrNotFound, agentID)
	}
	base, err := e.ensureDefault.EnsureDefaultPersonalKnowledgeBase(ctx, knowledgeBootstrapViewer(), knowledgeadmin.EnsureDefaultCommand{
		OwnerType: string(knowledgecore.OwnerAgent),
		OwnerID:   agentID,
	})
	if err != nil {
		return "", fmt.Errorf("ensure default agent knowledge base for %s: %w", agentID, err)
	}
	if strings.TrimSpace(base.ID) == "" {
		return "", fmt.Errorf("%w: default agent knowledge base was not created for %s", knowledgecore.ErrNotFound, agentID)
	}
	return base.ID, nil
}

func knowledgeAddDocumentContent(title string, content string) string {
	content = strings.TrimSpace(content)
	title = strings.TrimSpace(title)
	if title == "" {
		return content + "\n"
	}
	if strings.HasPrefix(content, "# ") || strings.HasPrefix(content, "## ") {
		return content + "\n"
	}
	return "# " + title + "\n\n" + content + "\n"
}

func knowledgeAddFilename(req mcptransport.KnowledgeToolRequest) string {
	if filename := strings.TrimSpace(req.Filename); filename != "" {
		return ensureMarkdownFilename(safeKnowledgeFilename(filename))
	}
	seed := firstNonBlank(req.Title, req.Content, "knowledge-note")
	return ensureMarkdownFilename(safeKnowledgeFilename(seed))
}

var unsafeKnowledgeFilename = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func safeKnowledgeFilename(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Trim(unsafeKnowledgeFilename.ReplaceAllString(value, "-"), ".-")
	if value == "" {
		return "knowledge-note"
	}
	if len(value) > 80 {
		value = strings.Trim(value[:80], ".-")
	}
	if value == "" {
		return "knowledge-note"
	}
	return value
}

func ensureMarkdownFilename(value string) string {
	if strings.HasSuffix(strings.ToLower(value), ".md") {
		return value
	}
	return value + ".md"
}
