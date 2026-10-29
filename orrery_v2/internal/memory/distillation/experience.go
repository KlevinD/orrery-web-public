package distillation

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
)

var (
	ErrInsufficientFacts = errors.New("insufficient fact records for experience extraction")
	ErrInvalidRequest    = errors.New("invalid memory distillation request")
	ErrSensitiveScope    = errors.New("sensitive memory cannot be distilled to broader scope without de-identification")
)

type ClockFunc func() time.Time

type ExperienceExtractorOptions struct {
	Now ClockFunc
}

type ExperienceExtractor struct {
	now ClockFunc
}

type ExperienceRequest struct {
	TargetScope memcore.MemoryScope
	Topic       string
	CreatedBy   agentcore.AgentID
}

type ExperienceResult struct {
	Candidates      []memcore.MemoryCandidate
	SourceMemoryIDs []string
}

func NewExperienceExtractor(opts ExperienceExtractorOptions) *ExperienceExtractor {
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &ExperienceExtractor{now: now}
}

func (e *ExperienceExtractor) Extract(records []memcore.MemoryRecord, req ExperienceRequest) (ExperienceResult, error) {
	if e == nil {
		e = NewExperienceExtractor(ExperienceExtractorOptions{})
	}
	topic := strings.TrimSpace(req.Topic)
	if topic == "" || !isExperienceTargetScope(req.TargetScope.Kind) || strings.TrimSpace(req.TargetScope.ID) == "" || req.CreatedBy == "" {
		return ExperienceResult{}, ErrInvalidRequest
	}
	facts := eligibleFacts(records, topic)
	if len(facts) < 2 {
		return ExperienceResult{}, ErrInsufficientFacts
	}
	if containsSensitiveBroaderScope(facts, req.TargetScope) {
		return ExperienceResult{}, ErrSensitiveScope
	}
	now := e.now()
	sourceIDs := sourceMemoryIDs(facts)
	candidateID := "candidate.experience." + idSegment(topic) + "." + idSegment(strings.Join(sourceIDs, "."))
	candidate := memcore.MemoryCandidate{
		ID:             memcore.BoundedIdentifier(candidateID, "candidate.experience"),
		SourceType:     memcore.SourceSummary,
		SourceRef:      "experience:" + topic,
		ProposedScope:  req.TargetScope,
		ProposedKind:   memoryKindForScope(req.TargetScope.Kind),
		ProposedLayer:  memcore.MemoryLayerExperience,
		Topic:          topic,
		Summary:        experienceSummary(topic, facts),
		Lessons:        []string{experienceLesson(topic, facts)},
		Warnings:       []string{"Derived experience must remain traceable to its source memories."},
		Tags:           appendUnique([]string{"experience", topic}, sharedTags(facts)...),
		LineageRefs:    lineageRefs(sourceIDs),
		EvidenceRefs:   sourceEvidenceRefs(facts),
		Sensitivity:    memcore.SensitivityNormal,
		DeIDLevel:      memcore.DeIDTeamSafe,
		Consent:        memcore.ConsentNotRequired,
		ReviewRequired: false,
		Confidence:     0.7,
		CreatedBy:      req.CreatedBy,
		Status:         memcore.CandidateAccepted,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return ExperienceResult{Candidates: []memcore.MemoryCandidate{candidate}, SourceMemoryIDs: sourceIDs}, nil
}

func isExperienceTargetScope(kind memcore.ScopeKind) bool {
	switch kind {
	case memcore.ScopeProject, memcore.ScopeDepartment, memcore.ScopeOrganization:
		return true
	default:
		return false
	}
}

func isExperienceSourceScope(kind memcore.ScopeKind) bool {
	switch kind {
	case memcore.ScopeMission, memcore.ScopeDiscussion, memcore.ScopeProject, memcore.ScopeDepartment, memcore.ScopeOrganization:
		return true
	default:
		return false
	}
}

func eligibleFacts(records []memcore.MemoryRecord, topic string) []memcore.MemoryRecord {
	topic = strings.TrimSpace(topic)
	var out []memcore.MemoryRecord
	for _, record := range records {
		normalized := record.Normalized()
		if normalized.Layer != memcore.MemoryLayerFact {
			continue
		}
		if !isExperienceSourceScope(normalized.Scope.Kind) {
			continue
		}
		if normalized.LifecycleStatus != memcore.MemoryLifecycleActive && normalized.LifecycleStatus != memcore.MemoryLifecycleSettled {
			continue
		}
		if !matchesTopicOrTag(normalized, topic) {
			continue
		}
		out = append(out, normalized.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func matchesTopicOrTag(record memcore.MemoryRecord, topic string) bool {
	topic = strings.TrimSpace(topic)
	if strings.TrimSpace(record.Topic) == topic {
		return true
	}
	for _, tag := range record.Tags {
		if strings.TrimSpace(tag) == topic {
			return true
		}
	}
	return false
}

func containsSensitiveBroaderScope(records []memcore.MemoryRecord, target memcore.MemoryScope) bool {
	for _, record := range records {
		if memcore.NormalizeSensitivity(record.Sensitivity) == memcore.SensitivityNormal {
			continue
		}
		if record.Scope.Kind != target.Kind || strings.TrimSpace(record.Scope.ID) != strings.TrimSpace(target.ID) {
			return true
		}
		switch target.Kind {
		case memcore.ScopeOrganization, memcore.ScopeDepartment, memcore.ScopeProject:
			return true
		}
	}
	return false
}

func sourceMemoryIDs(records []memcore.MemoryRecord) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		if id := strings.TrimSpace(record.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func lineageRefs(sourceIDs []string) []string {
	refs := make([]string, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		if id = strings.TrimSpace(id); id != "" {
			refs = append(refs, "memory:"+id)
		}
	}
	return refs
}

func sourceEvidenceRefs(records []memcore.MemoryRecord) []string {
	var refs []string
	for _, record := range records {
		refs = appendUnique(refs, record.SourceEvidenceRefs...)
	}
	return refs
}

func sharedTags(records []memcore.MemoryRecord) []string {
	var tags []string
	for _, record := range records {
		tags = appendUnique(tags, record.Tags...)
	}
	sort.Strings(tags)
	return tags
}

func experienceSummary(topic string, records []memcore.MemoryRecord) string {
	return "Repeated " + topic + " pattern across " + strconv.Itoa(len(records)) + " accepted facts."
}

func experienceLesson(topic string, records []memcore.MemoryRecord) string {
	return "Review " + topic + " evidence before repeating work; first source: " + strings.TrimSpace(records[0].Summary)
}

func memoryKindForScope(kind memcore.ScopeKind) agentcore.MemoryKind {
	switch kind {
	case memcore.ScopeAgent:
		return agentcore.MemoryKindPersonal
	case memcore.ScopeDiscussion:
		return agentcore.MemoryKindDiscussion
	case memcore.ScopeMission:
		return agentcore.MemoryKindMission
	case memcore.ScopeProject:
		return agentcore.MemoryKindProject
	case memcore.ScopeOrganization:
		return agentcore.MemoryKindOrganization
	default:
		return agentcore.MemoryKindCollective
	}
}

func appendUnique(base []string, values ...string) []string {
	seen := make(map[string]struct{}, len(base)+len(values))
	out := make([]string, 0, len(base)+len(values))
	for _, value := range append(base, values...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func idSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, value)
	value = strings.Trim(value, "-")
	if value == "" {
		return "unknown"
	}
	return value
}
