package memorypreference

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	agentcore "orrery_v2/internal/agent/core"
	memaudit "orrery_v2/internal/memory/audit"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
)

func NewService(options Options) *Service {
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{options: options}
}

func (s *Service) Extract(ctx context.Context, request Request) error {
	if s == nil || s.options.Repository == nil || s.options.Extractor == nil {
		return fmt.Errorf("personal preference service is not configured")
	}
	if request.ProfileID == "" || request.AgentID == "" || request.SourceRecordID == "" || request.UserMessageID == "" ||
		strings.TrimSpace(request.UserText) == "" || utf8.RuneCountInString(request.UserText) > 2000 {
		return fmt.Errorf("personal preference source is incomplete")
	}
	if enabled, err := s.enabled(ctx); err != nil || !enabled {
		return err
	}
	if !HasPreferenceSignal(request.UserText) {
		return nil
	}
	profile, err := s.options.Repository.GetProfile(ctx, request.ProfileID)
	if err != nil {
		return err
	}
	if !profile.Enabled {
		return nil
	}
	if !ownedProfile(profile, request.AgentID) {
		return fmt.Errorf("personal preference profile owner mismatch")
	}
	source, err := s.options.Repository.GetRecord(ctx, request.SourceRecordID)
	if errors.Is(err, memcore.ErrNotFound) {
		return nil // A deleted source must never be reconstructed from a queued job.
	}
	if err != nil {
		return err
	}
	ok, err := s.acceptedSource(ctx, s.options.Repository, request, source)
	if err != nil || !ok {
		return err
	}
	if err := s.republishSourcePreferences(ctx, request, source); err != nil {
		return err
	}
	digest, err := Context(ctx, s.options.Repository, profile, request.AgentID, s.options.Now())
	if err != nil {
		return err
	}
	existing := map[string]string{}
	for _, id := range digest.SourceMemoryRefs {
		record, err := s.options.Repository.GetRecord(ctx, id)
		if err != nil {
			return err
		}
		existing[strings.TrimPrefix(record.Topic, TopicPrefix)] = record.Preferences[0]
	}
	facts, err := s.options.Extractor.Extract(ctx, ExtractionInput{UserText: request.UserText, Existing: existing})
	if err != nil {
		return err
	}
	if err := validateFacts(facts, request.UserText); err != nil {
		return err
	}
	if enabled, err := s.enabled(ctx); err != nil || !enabled {
		return err
	}
	currentProfile, err := s.options.Repository.GetProfile(ctx, request.ProfileID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(profile, currentProfile) {
		return nil // Ownership/retention changes while the model ran invalidate its work.
	}
	var materialized []memcore.MemoryRecord
	for _, fact := range facts {
		records, err := s.saveFact(ctx, request, profile, source, fact)
		if err != nil {
			return err
		}
		materialized = append(materialized, records...)
	}
	for _, record := range materialized {
		if err := s.publish(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

// Delivery retry is independent of model output: a retry may produce no new
// facts because the first attempt already saved the preference successfully.
func (s *Service) republishSourcePreferences(ctx context.Context, request Request, source memcore.MemoryRecord) error {
	if s.options.Metadata == nil && s.options.Audit == nil {
		return nil
	}
	for _, key := range preferenceKeys {
		id := preferenceID(request.ProfileID, key)
		record, err := s.options.Repository.GetRecord(ctx, id)
		if errors.Is(err, memcore.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if record.CandidateID != identifier("candidate.preference", id, source.ID) || !active(record, s.options.Now()) {
			continue
		}
		valid, err := preferenceSourceActive(ctx, s.options.Repository, record, s.options.Now())
		if err != nil {
			return err
		}
		if !valid {
			continue
		}
		if err := s.publish(ctx, record); err != nil {
			return err
		}
		for _, ref := range record.SupersedesRefs {
			revision, err := s.options.Repository.GetRecord(ctx, strings.TrimPrefix(ref, "memory:"))
			if err != nil {
				return err
			}
			if err := s.publish(ctx, revision); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) acceptedSource(ctx context.Context, repository memstore.RecordWriteTx, request Request, source memcore.MemoryRecord) (bool, error) {
	if !active(source, s.options.Now()) || source.Sensitivity != memcore.SensitivityNormal {
		return false, nil
	}
	if source.ProfileID != request.ProfileID || source.Scope != (memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: string(request.AgentID)}) ||
		source.CreatedBy != request.AgentID || source.Topic != "personal_assistant_exchange" || source.Layer != memcore.MemoryLayerFact ||
		!slices.Contains(source.LineageRefs, "user_message:"+request.UserMessageID) ||
		!strings.HasPrefix(source.Narrative, "User: "+request.UserText+"\nAssistant: ") {
		return false, fmt.Errorf("personal preference source does not match the accepted user exchange")
	}
	candidate, err := repository.GetCandidate(ctx, source.CandidateID)
	if errors.Is(err, memcore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return candidate.Status == memcore.CandidateAccepted && !candidate.ReviewRequired &&
		candidate.SourceType == memcore.SourcePersonalAssistantExchange && candidate.Sensitivity == memcore.SensitivityNormal &&
		candidate.ProposedScope == source.Scope, nil
}

func (s *Service) saveFact(ctx context.Context, request Request, profile memcore.MemoryProfile, snapshot memcore.MemoryRecord, fact Fact) ([]memcore.MemoryRecord, error) {
	id := preferenceID(profile.ID, fact.Key)
	candidateID := identifier("candidate.preference", id, snapshot.ID)
	var published []memcore.MemoryRecord
	err := s.options.Repository.WithRecordWriteTx(ctx, id, func(tx memstore.RecordWriteTx) error {
		source, err := tx.GetRecord(ctx, snapshot.ID)
		if errors.Is(err, memcore.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(source, snapshot) {
			return nil
		}
		if ok, err := s.acceptedSource(ctx, tx, request, source); err != nil || !ok {
			return err
		}
		previous, err := tx.GetRecord(ctx, id)
		if err != nil && !errors.Is(err, memcore.ErrNotFound) {
			return err
		}
		if previous.ID != "" {
			if previous.ProfileID != profile.ID || previous.Scope != profile.DefaultScope {
				return fmt.Errorf("preference record owner mismatch")
			}
			if memcore.NormalizeMemoryLifecycleStatus(previous.LifecycleStatus) != memcore.MemoryLifecycleActive || previous.DisabledAt != nil {
				return nil // A disabled, archived or deleted preference is not revived by extraction.
			}
			if !previous.ExpiresAt.IsZero() && !s.options.Now().Before(previous.ExpiresAt) && source.CreatedAt.Before(previous.ExpiresAt) {
				return nil // Only a fresh user statement can renew a time-expired preference.
			}
		}
		if _, err := tx.GetCandidate(ctx, candidateID); err == nil {
			return nil
		} else if !errors.Is(err, memcore.ErrNotFound) {
			return err
		}
		if previous.ID != "" && !previous.ValidFrom.Before(source.CreatedAt) {
			return nil // Late/replayed exchanges cannot overwrite a newer preference.
		}
		now := s.options.Now()
		candidate := memcore.MemoryCandidate{
			ID: candidateID, SourceType: memcore.SourceDiscussionMessage, SourceRef: request.UserMessageID,
			ProposedScope: profile.DefaultScope, ProposedKind: agentcore.MemoryKindPersonal, ProposedLayer: memcore.MemoryLayerFact,
			Topic: TopicPrefix + fact.Key, Summary: fact.Value, Narrative: fact.SourceQuote,
			Tags: []string{Tag}, LineageRefs: append([]string{"memory:" + source.ID}, source.LineageRefs...),
			EvidenceRefs: []string{"user_message:" + request.UserMessageID}, Sensitivity: memcore.SensitivityNormal,
			DeIDLevel: memcore.DeIDRaw, Confidence: 0.9, Status: memcore.CandidateAccepted, CreatedBy: request.AgentID, CreatedAt: now, UpdatedAt: now,
		}
		record := memcore.MemoryRecord{
			ID: id, ProfileID: profile.ID, CandidateID: candidateID, Scope: profile.DefaultScope, Layer: memcore.MemoryLayerFact,
			Topic: candidate.Topic, Summary: fact.Value, Content: fact.Value, Narrative: fact.SourceQuote,
			Preferences: []string{fact.Value}, Facts: []string{fact.SourceQuote}, Tags: []string{Tag},
			LineageRefs: candidate.LineageRefs, SourceRefs: []string{request.UserMessageID}, SourceEvidenceRefs: candidate.EvidenceRefs,
			Sensitivity: memcore.SensitivityNormal, DeIDLevel: memcore.DeIDRaw, Confidence: candidate.Confidence,
			LifecycleStatus: memcore.MemoryLifecycleActive,
			Visibility:      memcore.Visibility{Mode: memcore.VisibilityOwnerOnly, ScopeKind: memcore.ScopeAgent, ScopeID: string(request.AgentID)},
			ValidFrom:       source.CreatedAt, CreatedBy: request.AgentID, CreatedAt: now, UpdatedAt: now, ExpiresAt: source.ExpiresAt,
		}
		if profile.RetentionPolicy.ExpiresAfter > 0 {
			expires := source.CreatedAt.Add(profile.RetentionPolicy.ExpiresAfter)
			if record.ExpiresAt.IsZero() || expires.Before(record.ExpiresAt) {
				record.ExpiresAt = expires
			}
		}
		if previous.ID != "" {
			revision := previous.Clone()
			revision.ID = identifier("memory.preference.revision", id, previous.CandidateID)
			revision.LifecycleStatus = memcore.MemoryLifecycleArchived
			revision.UpdatedAt = now
			if err := tx.SaveRecord(ctx, revision); err != nil {
				return err
			}
			record.CreatedAt = previous.CreatedAt
			record.SupersedesRefs = []string{"memory:" + revision.ID}
			candidate.SupersedesRefs = record.SupersedesRefs
			published = append(published, revision)
		}
		if err := tx.SaveCandidate(ctx, candidate); err != nil {
			return err
		}
		if err := tx.SaveRecord(ctx, record); err != nil {
			return err
		}
		published = append(published, record)
		return nil
	})
	return published, err
}

func (s *Service) enabled(ctx context.Context) (bool, error) {
	if s.options.Enabled == nil {
		return true, ctx.Err()
	}
	return s.options.Enabled(ctx)
}

func (s *Service) publish(ctx context.Context, record memcore.MemoryRecord) error {
	if s.options.Metadata != nil {
		if err := s.options.Metadata.PublishMaterializedMemoryRecord(ctx, record); err != nil {
			return err
		}
	}
	if s.options.Audit != nil {
		event := memaudit.EventMemoryRecordCreated
		if record.LifecycleStatus == memcore.MemoryLifecycleArchived {
			event = memaudit.EventMemoryRecordArchived
		} else if len(record.SupersedesRefs) > 0 {
			event = memaudit.EventMemoryRecordUpdated
		}
		return s.options.Audit.RecordMemoryAudit(ctx, memaudit.Event{
			Type: event, SubjectID: record.ID, IdempotencyKey: record.ID + ":" + record.CandidateID + ":" + string(event),
			ActorID: string(record.CreatedBy), SourceRefs: record.SourceEvidenceRefs, OccurredAt: record.UpdatedAt,
			Summary: "Explicit personal interaction preference recorded",
		})
	}
	return nil
}

func preferenceID(profileID agentcore.MemoryProfileID, key string) string {
	return identifier("memory.preference", string(profileID), key)
}

func identifier(prefix string, parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + "." + hex.EncodeToString(hash[:])
}
