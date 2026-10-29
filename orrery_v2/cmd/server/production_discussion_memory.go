package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	memcapture "orrery_v2/internal/memory/capture"
	memcore "orrery_v2/internal/memory/core"
	mempageindex "orrery_v2/internal/memory/pageindex"
	memretrieval "orrery_v2/internal/memory/retrieval"
)

type productionDiscussionMemoryRepository interface {
	memretrieval.Repository
	memcapture.Repository
}

type productionDiscussionMemoryProviderOptions struct {
	PreferenceQueue productionPreferenceQueue
	Audit           memcapture.AuditRecorder
	Metadata        memcapture.RecordMetadataSink
	Now             memcapture.ClockFunc
	Enabled         func(context.Context) (bool, error)
	Recall          productionSemanticMemoryRecall
}

type productionSemanticMemoryRecall interface {
	Recall(context.Context, mempageindex.RecallRequest) ([]memcore.MemoryRecord, error)
}

type productionDiscussionMemoryProvider struct {
	preferenceQueue productionPreferenceQueue
	now             memcapture.ClockFunc
	profiles        *agentcore.PlatformProfileRepository
	memory          productionDiscussionMemoryRepository
	writer          *memcapture.EpisodicWriter
	enabled         func(context.Context) (bool, error)
	recall          productionSemanticMemoryRecall
}

func newProductionDiscussionMemoryProvider(
	profiles *agentcore.PlatformProfileRepository,
	memory productionDiscussionMemoryRepository,
	options ...productionDiscussionMemoryProviderOptions,
) *productionDiscussionMemoryProvider {
	var opts productionDiscussionMemoryProviderOptions
	if len(options) > 0 {
		opts = options[0]
	}
	provider := &productionDiscussionMemoryProvider{
		preferenceQueue: opts.PreferenceQueue,
		now:             opts.Now,
		profiles:        profiles,
		memory:          memory,
		enabled:         opts.Enabled,
		recall:          opts.Recall,
	}
	if memory != nil {
		provider.writer = memcapture.NewEpisodicWriter(memcapture.EpisodicWriterOptions{
			Repository: memory,
			Audit:      opts.Audit,
			Metadata:   opts.Metadata,
			Now:        opts.Now,
		})
	}
	if provider.now == nil {
		provider.now = func() time.Time { return time.Now().UTC() }
	}
	return provider
}

func (p *productionDiscussionMemoryProvider) DiscussionAgentMemoryContext(ctx context.Context, agentID string) (discussionsession.DiscussionAgentMemoryContext, error) {
	return p.DiscussionAgentMemoryContextForQuery(ctx, agentID, "")
}

func (p *productionDiscussionMemoryProvider) DiscussionAgentMemoryContextForQuery(ctx context.Context, agentID, query string) (discussionsession.DiscussionAgentMemoryContext, error) {
	return p.DiscussionAgentMemoryContextForAuthorizedQuery(ctx, discussionsession.DiscussionAgentMemoryAccessContext{}, agentID, query)
}

func (p *productionDiscussionMemoryProvider) DiscussionAgentMemoryContextForAuthorizedQuery(ctx context.Context, access discussionsession.DiscussionAgentMemoryAccessContext, agentID, query string) (discussionsession.DiscussionAgentMemoryContext, error) {
	if p == nil || p.memory == nil {
		return discussionsession.DiscussionAgentMemoryContext{}, nil
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID = defaultLeadAgentID
	}
	if discussionsession.IsPersonalAssistantAgentID(agentID) {
		enabled, err := p.personalAssistantMemoryEnabled(ctx)
		if err != nil || !enabled {
			return discussionsession.DiscussionAgentMemoryContext{}, err
		}
		profile, err := p.ensurePersonalAssistantMemoryProfile(ctx, agentID)
		if err != nil {
			return discussionsession.DiscussionAgentMemoryContext{}, err
		}
		if !profile.Enabled {
			return discussionsession.DiscussionAgentMemoryContext{}, nil
		}
		ref := memcore.MemoryProfileRefForScope(profile)
		// Personal-assistant recall is invoked only through the explicit
		// memory_search tool, so a non-empty query may use semantic recall.
		digest, err := p.retrieveMemoryDigest(
			ctx,
			agentcore.AgentID(agentID),
			memcore.AccessSubject{AgentID: agentcore.AgentID(agentID)},
			ref,
			query,
			strings.TrimSpace(query) != "",
		)
		if err != nil {
			return discussionsession.DiscussionAgentMemoryContext{}, err
		}
		return discussionsession.DiscussionAgentMemoryContext{
			PersonalDigest: mapDiscussionMemoryDigest(digest, profile.MemoryKind),
		}, nil
	}
	if p.profiles == nil {
		return discussionsession.DiscussionAgentMemoryContext{}, nil
	}
	profile, err := p.profiles.GetProfile(ctx, agentcore.AgentProfileID(agentID))
	if err != nil {
		return discussionsession.DiscussionAgentMemoryContext{}, err
	}
	subject := productionMemoryAccessSubject(profile, agentID)
	appendUniqueString(&subject.OrganizationIDs, runtimeOrganizationTenantID(access.TenantID))
	out := discussionsession.DiscussionAgentMemoryContext{}
	if !profile.PersonalMemoryProfileRef.IsZero() {
		digest, err := p.retrieveMemoryDigest(ctx, agentcore.AgentID(agentID), subject, profile.PersonalMemoryProfileRef, query, true)
		if err != nil {
			return discussionsession.DiscussionAgentMemoryContext{}, err
		}
		out.PersonalDigest = mapDiscussionMemoryDigest(digest, profile.PersonalMemoryProfileRef.MemoryKind)
	}
	for _, ref := range profile.CollectiveMemoryScopeRefs {
		if ref.IsZero() {
			continue
		}
		digest, err := p.retrieveMemoryDigest(ctx, agentcore.AgentID(agentID), subject, ref, query, true)
		if err != nil {
			return discussionsession.DiscussionAgentMemoryContext{}, err
		}
		mapped := mapDiscussionMemoryDigest(digest, ref.MemoryKind)
		if strings.TrimSpace(mapped.Summary) != "" {
			out.CollectiveDigests = append(out.CollectiveDigests, mapped)
		}
	}
	return out, nil
}

func (p *productionDiscussionMemoryProvider) retrieveMemoryDigest(
	ctx context.Context,
	agentID agentcore.AgentID,
	subject memcore.AccessSubject,
	ref agentcore.MemoryProfileRef,
	query string,
	semanticRecall bool,
) (memcore.MemoryDigest, error) {
	profile, err := p.memory.GetProfile(ctx, ref.MemoryProfileID)
	if err != nil {
		return memcore.MemoryDigest{}, err
	}
	compressor := memretrieval.NewCompressor(memretrieval.CompressionOptions{MaxSummaryBytes: profile.CompressionPolicy.MaxSummaryBytes, Now: p.now})
	if semanticRecall && strings.TrimSpace(query) != "" {
		if p.recall == nil {
			return memcore.MemoryDigest{RetrievalMode: "none", FallbackReason: "semantic_unavailable"}, nil
		}
		records, recallErr := p.recall.Recall(ctx, mempageindex.RecallRequest{
			Query:       query,
			Subject:     subject,
			ProfileRefs: []agentcore.MemoryProfileRef{ref},
			Limit:       profile.RetrievalPolicy.MaxRecords,
		})
		if recallErr != nil {
			return memcore.MemoryDigest{}, recallErr
		}
		digest, err := compressor.Compress(memretrieval.CompressRequest{
			AgentID: agentID,
			Scope:   profile.DefaultScope,
			Records: records,
		})
		if err != nil {
			return memcore.MemoryDigest{}, err
		}
		digest.Evidence, err = memretrieval.ValidateRecords(ctx, p.memory, subject, records, p.now())
		if err != nil {
			return memcore.MemoryDigest{}, err
		}
		digest.RetrievalMode = "semantic"
		if strings.TrimSpace(digest.Summary) == "" {
			digest.RetrievalMode = "none"
			digest.FallbackReason = "no_semantic_match"
		}
		return digest, err
	}
	digest, err := memretrieval.NewRetriever(p.memory, compressor).Retrieve(ctx, memretrieval.RetrieveRequest{
		AgentID:     agentID,
		Subject:     subject,
		ProfileRefs: []agentcore.MemoryProfileRef{ref},
	})
	digest.RetrievalMode = "profile_digest"
	if strings.TrimSpace(digest.Summary) == "" {
		digest.RetrievalMode = "none"
	}
	return digest, err
}

func productionMemoryAccessSubject(profile agentcore.AgentProfile, agentID string) memcore.AccessSubject {
	subject := memcore.AccessSubject{AgentID: agentcore.AgentID(strings.TrimSpace(agentID))}
	appendUniqueString(&subject.OrganizationIDs, string(profile.OrgIdentity.OrganizationRef.ID))
	appendUniqueString(&subject.DepartmentIDs, string(profile.OrgIdentity.DepartmentRef.ID))
	return subject
}

func appendUniqueString(values *[]string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	for _, existing := range *values {
		if existing == value {
			return
		}
	}
	*values = append(*values, value)
}

func (p *productionDiscussionMemoryProvider) CapturePersonalAssistantMemoryExchange(ctx context.Context, exchange discussionsession.PersonalAssistantMemoryExchange) error {
	if p == nil || p.memory == nil || p.writer == nil {
		return fmt.Errorf("personal assistant memory provider is not configured")
	}
	enabled, err := p.personalAssistantMemoryEnabled(ctx)
	if err != nil || !enabled {
		return err
	}
	expectedAgentID := discussionsession.PersonalAssistantAgentID(exchange.TenantID, exchange.OwnerID)
	agentID := strings.TrimSpace(exchange.AgentID)
	if expectedAgentID == "" || agentID != expectedAgentID || !discussionsession.IsPersonalAssistantAgentID(agentID) {
		return fmt.Errorf("personal assistant memory owner is invalid")
	}
	if strings.TrimSpace(exchange.RunID) == "" {
		return fmt.Errorf("personal assistant memory exchange requires run_id")
	}
	userContent := boundedPersonalAssistantMemoryText(exchange.UserContent, 2000)
	assistantContent := boundedPersonalAssistantMemoryText(exchange.AssistantContent, 4000)
	if userContent == "" && assistantContent == "" {
		return nil
	}
	profile, err := p.ensurePersonalAssistantMemoryProfile(ctx, agentID)
	if err != nil {
		return err
	}
	if !profile.Enabled {
		return nil
	}
	narrative := personalAssistantMemoryExchangeNarrative(userContent, assistantContent)
	sourceRef := firstNonEmptyText(exchange.AssistantMessageID, exchange.RunID)
	lineageRefs := compactPersonalAssistantMemoryRefs(
		"conversation:"+strings.TrimSpace(exchange.ConversationID),
		"run:"+strings.TrimSpace(exchange.RunID),
		"user_message:"+strings.TrimSpace(exchange.UserMessageID),
		"assistant_message:"+strings.TrimSpace(exchange.AssistantMessageID),
	)
	candidate, err := p.writer.CaptureCandidate(ctx, memcapture.CandidateDraft{
		ID:                personalAssistantMemoryCandidateID(agentID, exchange.RunID),
		DiscussionGroupID: strings.TrimSpace(exchange.ConversationID),
		SourceType:        memcore.SourcePersonalAssistantExchange,
		SourceRef:         sourceRef,
		ProposedScope:     memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: agentID},
		ProposedKind:      agentcore.MemoryKindPersonal,
		Layer:             memcore.MemoryLayerFact,
		Topic:             "personal_assistant_exchange",
		Summary:           narrative,
		Narrative:         narrative,
		Tags:              compactPersonalAssistantMemoryRefs("personal_assistant", "conversation:"+strings.TrimSpace(exchange.ConversationID)),
		LineageRefs:       lineageRefs,
		CreatedBy:         agentcore.AgentID(agentID),
		CreatedAt:         exchange.OccurredAt,
		UpdatedAt:         exchange.OccurredAt,
	})
	if err != nil {
		return err
	}
	return p.enqueuePersonalAssistantPreferences(ctx, exchange, candidate)
}

func (p *productionDiscussionMemoryProvider) personalAssistantMemoryEnabled(ctx context.Context) (bool, error) {
	if p == nil || p.enabled == nil {
		return true, nil
	}
	return p.enabled(ctx)
}

func (p *productionDiscussionMemoryProvider) ensurePersonalAssistantMemoryProfile(ctx context.Context, agentID string) (memcore.MemoryProfile, error) {
	agentID = strings.TrimSpace(agentID)
	if p == nil || p.memory == nil || !discussionsession.IsPersonalAssistantAgentID(agentID) {
		return memcore.MemoryProfile{}, fmt.Errorf("personal assistant memory profile owner is invalid")
	}
	profileID := agentcore.MemoryProfileID(discussionsession.PersonalAssistantMemoryProfileID(agentID))
	profile, err := p.memory.GetProfile(ctx, profileID)
	if err == nil {
		if profile.ID != profileID ||
			profile.OwnerRef.AgentID != agentcore.AgentID(agentID) ||
			profile.MemoryKind != agentcore.MemoryKindPersonal ||
			profile.DefaultScope.Kind != memcore.ScopeAgent ||
			strings.TrimSpace(profile.DefaultScope.ID) != agentID ||
			profile.VisibilityPolicy.Mode != memcore.VisibilityOwnerOnly {
			return memcore.MemoryProfile{}, fmt.Errorf("personal assistant memory profile %q has incompatible ownership", profileID)
		}
		changed := false
		if profile.VisibilityPolicy.ScopeKind == "" {
			profile.VisibilityPolicy.ScopeKind = memcore.ScopeAgent
			changed = true
		} else if profile.VisibilityPolicy.ScopeKind != memcore.ScopeAgent {
			return memcore.MemoryProfile{}, fmt.Errorf("personal assistant memory profile %q has incompatible ownership", profileID)
		}
		if strings.TrimSpace(profile.VisibilityPolicy.ScopeID) == "" {
			profile.VisibilityPolicy.ScopeID = agentID
			changed = true
		} else if strings.TrimSpace(profile.VisibilityPolicy.ScopeID) != agentID {
			return memcore.MemoryProfile{}, fmt.Errorf("personal assistant memory profile %q has incompatible ownership", profileID)
		}
		if changed {
			if err := p.memory.SaveProfile(ctx, profile); err != nil {
				return memcore.MemoryProfile{}, fmt.Errorf("repair personal assistant memory profile visibility: %w", err)
			}
		}
		return profile, nil
	}
	if !errors.Is(err, memcore.ErrNotFound) {
		return memcore.MemoryProfile{}, err
	}
	profile = memcore.NewScopeMemoryProfile(
		memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: agentID},
		memcore.OwnerRef{AgentID: agentcore.AgentID(agentID)},
	)
	if profile.ID != profileID || !profile.Enabled {
		return memcore.MemoryProfile{}, fmt.Errorf("personal assistant memory profile is invalid")
	}
	if err := p.memory.SaveProfile(ctx, profile); err != nil {
		return memcore.MemoryProfile{}, err
	}
	return profile, nil
}

func boundedPersonalAssistantMemoryText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "..."
}

func personalAssistantMemoryExchangeNarrative(userContent, assistantContent string) string {
	parts := make([]string, 0, 2)
	if userContent = strings.TrimSpace(userContent); userContent != "" {
		parts = append(parts, "User: "+userContent)
	}
	if assistantContent = strings.TrimSpace(assistantContent); assistantContent != "" {
		parts = append(parts, "Assistant: "+assistantContent)
	}
	return strings.Join(parts, "\n")
}

func compactPersonalAssistantMemoryRefs(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || strings.HasSuffix(value, ":") {
			continue
		}
		out = append(out, value)
	}
	return out
}

func personalAssistantMemoryCandidateID(agentID, runID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runID)))
	return fmt.Sprintf("candidate.personal_assistant_exchange.%x", digest)
}

func mapDiscussionMemoryDigest(digest memcore.MemoryDigest, fallbackKind agentcore.MemoryKind) discussionsession.DiscussionAgentMemoryDigest {
	kind := strings.TrimSpace(string(fallbackKind))
	if kind == "" {
		kind = string(memoryKindFromScope(digest.Scope.Kind))
	}
	return discussionsession.DiscussionAgentMemoryDigest{
		Evidence:       digest.Evidence,
		RetrievalMode:  digest.RetrievalMode,
		FallbackReason: digest.FallbackReason,
		DigestID:       string(digest.ID),
		MemoryKind:     kind,
		Summary:        strings.TrimSpace(digest.Summary),
		SourceMemory:   append([]string(nil), digest.SourceMemoryRefs...),
		SourceEvidence: append([]string(nil), digest.SourceEvidenceRefs...),
		Confidence:     digest.Confidence,
		Sensitivity:    string(digest.Sensitivity),
	}
}

func memoryKindFromScope(kind memcore.ScopeKind) agentcore.MemoryKind {
	switch kind {
	case memcore.ScopeAgent:
		return agentcore.MemoryKindPersonal
	case memcore.ScopeOrganization:
		return agentcore.MemoryKindOrganization
	case memcore.ScopeProject:
		return agentcore.MemoryKindProject
	case memcore.ScopeMission:
		return agentcore.MemoryKindMission
	case memcore.ScopeDiscussion:
		return agentcore.MemoryKindDiscussion
	default:
		return agentcore.MemoryKindCollective
	}
}
