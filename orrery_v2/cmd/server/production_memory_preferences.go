package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"orrery_v2/internal/agent/llm"
	"strings"
	"time"
	"unicode/utf8"

	agentcore "orrery_v2/internal/agent/core"
	profilemodelruntime "orrery_v2/internal/agent/runtime/providers/profilemodel"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/memorypreference"
	memcore "orrery_v2/internal/memory/core"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/execution"
)

const productionPreferenceTopic = "memory.personal_preference.extract"

type productionPreferenceQueue interface {
	Enqueue(context.Context, execution.Job) error
}

type productionPreferenceJob struct {
	Owner   discussionsession.PersonalAssistantPreferenceOwner `json:"owner"`
	Request memorypreference.Request                           `json:"request"`
}

func (p *productionDiscussionMemoryProvider) PersonalAssistantPreferences(ctx context.Context, owner discussionsession.PersonalAssistantPreferenceOwner) (discussionsession.DiscussionAgentMemoryDigest, error) {
	if p == nil || p.memory == nil {
		return discussionsession.DiscussionAgentMemoryDigest{}, fmt.Errorf("personal preference memory is not configured")
	}
	if owner.OwnerID == "" || owner.AgentID != discussionsession.PersonalAssistantAgentID(owner.TenantID, owner.OwnerID) {
		return discussionsession.DiscussionAgentMemoryDigest{}, fmt.Errorf("personal preference owner mismatch")
	}
	if enabled, err := p.personalAssistantMemoryEnabled(ctx); err != nil || !enabled {
		return discussionsession.DiscussionAgentMemoryDigest{}, err
	}
	profile, err := p.ensurePersonalAssistantMemoryProfile(ctx, owner.AgentID)
	if err != nil || !profile.Enabled {
		return discussionsession.DiscussionAgentMemoryDigest{}, err
	}
	digest, err := memorypreference.Context(ctx, p.memory, profile, agentcore.AgentID(owner.AgentID), p.now())
	digest.RetrievalMode = "stable_preferences"
	return mapDiscussionMemoryDigest(digest, agentcore.MemoryKindPersonal), err
}

func (p *productionDiscussionMemoryProvider) enqueuePersonalAssistantPreferences(ctx context.Context, exchange discussionsession.PersonalAssistantMemoryExchange, candidate memcore.MemoryCandidate) error {
	userText := strings.TrimSpace(exchange.UserContent)
	if p.preferenceQueue == nil || !exchange.PreferenceExtractionAllowed || candidate.Status != memcore.CandidateAccepted ||
		candidate.ReviewRequired || candidate.Sensitivity != memcore.SensitivityNormal || exchange.UserMessageID == "" ||
		utf8.RuneCountInString(userText) > 2000 || !memorypreference.HasPreferenceSignal(userText) {
		return nil
	}
	source, _, err := p.writer.MaterializeAcceptedCandidate(ctx, candidate)
	if err != nil || source.ID == "" {
		return err
	}
	payload, err := json.Marshal(productionPreferenceJob{
		Owner: discussionsession.PersonalAssistantPreferenceOwner{TenantID: exchange.TenantID, OwnerID: exchange.OwnerID, AgentID: exchange.AgentID},
		Request: memorypreference.Request{ProfileID: source.ProfileID, AgentID: agentcore.AgentID(exchange.AgentID),
			SourceRecordID: source.ID, UserMessageID: exchange.UserMessageID, UserText: userText},
	})
	if err != nil {
		return err
	}
	return p.preferenceQueue.Enqueue(ctx, execution.Job{
		ID: productionPreferenceTopic + ":" + candidate.ID, Topic: productionPreferenceTopic, Payload: payload,
		QueueKey: productionPreferenceTopic + ":" + exchange.AgentID, MaxAttempts: 3,
	})
}

func newProductionPreferenceExtractor(settings productionSettingsStore, secrets platformSecretResolver) memorypreference.Extractor {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	models := profilemodelruntime.NewProfileModelFactory(profilemodelruntime.ProfileModelFactoryConfig{
		Profiles: settings, Secrets: secrets, Client: &http.Client{Transport: transport, Timeout: 20 * time.Second},
	})
	return memorypreference.ChatExtractor{Models: func(ctx context.Context) (llm.ChatModel, error) {
		return models.ModelForRequest(ctx, profilemodelruntime.ModelRequest{
			ReasoningEffort: "low",
		})
	}}
}

func registerProductionPreferenceWorker(rt *platform.Runtime, service *memorypreference.Service) error {
	return rt.RegisterExecutionHandler(productionPreferenceTopic, productionPreferenceHandler(service))
}

func productionPreferenceHandler(service *memorypreference.Service) execution.Handler {
	return func(ctx context.Context, job execution.Job) error {
		var payload productionPreferenceJob
		if len(job.Payload) > 20000 || json.Unmarshal(job.Payload, &payload) != nil {
			return execution.Permanent(fmt.Errorf("invalid personal preference job"))
		}
		owner := payload.Owner
		if owner.OwnerID == "" || owner.AgentID != discussionsession.PersonalAssistantAgentID(owner.TenantID, owner.OwnerID) ||
			string(payload.Request.AgentID) != owner.AgentID || string(payload.Request.ProfileID) != discussionsession.PersonalAssistantMemoryProfileID(owner.AgentID) {
			return execution.Permanent(fmt.Errorf("personal preference job owner mismatch"))
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return service.Extract(ctx, payload.Request)
	}
}
