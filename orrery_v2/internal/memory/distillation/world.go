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

var ErrInsufficientWorldSources = errors.New("insufficient source records for world consolidation")

type WorldConsolidatorOptions struct {
	Now ClockFunc
}

type WorldConsolidator struct {
	now ClockFunc
}

type WorldRequest struct {
	TargetScope memcore.MemoryScope
	Topic       string
	CreatedBy   agentcore.AgentID
}

type WorldResult struct {
	Candidates      []memcore.MemoryCandidate
	SourceMemoryIDs []string
}

func NewWorldConsolidator(opts WorldConsolidatorOptions) *WorldConsolidator {
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &WorldConsolidator{now: now}
}

func (w *WorldConsolidator) Consolidate(records []memcore.MemoryRecord, req WorldRequest) (WorldResult, error) {
	if w == nil {
		w = NewWorldConsolidator(WorldConsolidatorOptions{})
	}
	topic := strings.TrimSpace(req.Topic)
	if topic == "" || !isWorldTargetScope(req.TargetScope.Kind) || strings.TrimSpace(req.TargetScope.ID) == "" || req.CreatedBy == "" {
		return WorldResult{}, ErrInvalidRequest
	}
	sources := eligibleWorldSources(records, topic)
	if len(sources) == 0 {
		return WorldResult{}, ErrInsufficientWorldSources
	}
	if containsSensitiveWorldSource(sources, req.TargetScope) {
		return WorldResult{}, ErrSensitiveScope
	}

	existingWorld := eligibleExistingWorld(records, topic, req.TargetScope)
	supersedes, contradicts := worldRelations(sources, existingWorld)
	now := w.now()
	sourceIDs := sourceMemoryIDs(sources)
	candidateID := "candidate.world." + idSegment(string(req.TargetScope.Kind)) + "." + idSegment(req.TargetScope.ID) + "." + idSegment(topic) + "." + idSegment(strings.Join(sourceIDs, "."))
	candidate := memcore.MemoryCandidate{
		ID:              memcore.BoundedIdentifier(candidateID, "candidate.world"),
		SourceType:      memcore.SourceSummary,
		SourceRef:       "world:" + string(req.TargetScope.Kind) + ":" + strings.TrimSpace(req.TargetScope.ID) + ":" + topic,
		ProposedScope:   req.TargetScope,
		ProposedKind:    memoryKindForScope(req.TargetScope.Kind),
		ProposedLayer:   memcore.MemoryLayerWorld,
		Topic:           topic,
		Summary:         worldSummary(topic, sources),
		Narrative:       worldNarrative(topic, sources),
		Lessons:         worldLessons(topic, sources),
		Warnings:        worldWarnings(sources, contradicts),
		Tags:            []string{"world", topic},
		LineageRefs:     lineageRefs(sourceIDs),
		SupersedesRefs:  memoryRefs(supersedes),
		ContradictsRefs: memoryRefs(contradicts),
		EvidenceRefs:    sourceEvidenceRefs(sources),
		Sensitivity:     memcore.SensitivityNormal,
		DeIDLevel:       memcore.DeIDTeamSafe,
		Consent:         memcore.ConsentNotRequired,
		Confidence:      worldConfidence(sources, contradicts, now),
		ReviewRequired:  false,
		CreatedBy:       req.CreatedBy,
		Status:          memcore.CandidateAccepted,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	return WorldResult{Candidates: []memcore.MemoryCandidate{candidate}, SourceMemoryIDs: sourceIDs}, nil
}

func isWorldTargetScope(kind memcore.ScopeKind) bool {
	return kind == memcore.ScopeOrganization
}

func isWorldSourceScope(kind memcore.ScopeKind) bool {
	switch kind {
	case memcore.ScopeOrganization, memcore.ScopeDepartment, memcore.ScopeProject:
		return true
	default:
		return false
	}
}

func eligibleWorldSources(records []memcore.MemoryRecord, topic string) []memcore.MemoryRecord {
	var out []memcore.MemoryRecord
	for _, record := range records {
		normalized := record.Normalized()
		if normalized.Layer != memcore.MemoryLayerFact && normalized.Layer != memcore.MemoryLayerExperience {
			continue
		}
		if !isWorldSourceScope(normalized.Scope.Kind) {
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

func eligibleExistingWorld(records []memcore.MemoryRecord, topic string, target memcore.MemoryScope) []memcore.MemoryRecord {
	var out []memcore.MemoryRecord
	for _, record := range records {
		normalized := record.Normalized()
		if normalized.Layer != memcore.MemoryLayerWorld {
			continue
		}
		if normalized.Scope.Kind != target.Kind || strings.TrimSpace(normalized.Scope.ID) != strings.TrimSpace(target.ID) {
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

func containsSensitiveWorldSource(records []memcore.MemoryRecord, target memcore.MemoryScope) bool {
	if target.Kind != memcore.ScopeOrganization {
		return false
	}
	for _, record := range records {
		if memcore.NormalizeSensitivity(record.Sensitivity) != memcore.SensitivityNormal {
			return true
		}
	}
	return false
}

func worldRelations(sources, existingWorld []memcore.MemoryRecord) ([]string, []string) {
	evidenceText := joinedWorldEvidence(sources)
	latestSource := latestUpdatedAt(sources)
	var supersedes []string
	var contradicts []string
	for _, world := range existingWorld {
		if worldContradicts(evidenceText, worldText(world)) {
			contradicts = append(contradicts, world.ID)
			continue
		}
		if recordUpdatedAt(world).Before(latestSource) && !worldTextSimilar(evidenceText, worldText(world)) {
			supersedes = append(supersedes, world.ID)
		}
	}
	return supersedes, contradicts
}

func joinedWorldEvidence(records []memcore.MemoryRecord) string {
	parts := make([]string, 0, len(records)*3)
	for _, record := range records {
		parts = append(parts, record.Summary, record.Narrative)
		parts = append(parts, record.Lessons...)
		parts = append(parts, record.Facts...)
	}
	return strings.Join(parts, "\n")
}

func worldText(record memcore.MemoryRecord) string {
	parts := []string{record.Summary, record.Narrative}
	parts = append(parts, record.Lessons...)
	parts = append(parts, record.Facts...)
	return strings.Join(parts, "\n")
}

func latestUpdatedAt(records []memcore.MemoryRecord) time.Time {
	var latest time.Time
	for _, record := range records {
		updated := recordUpdatedAt(record)
		if updated.After(latest) {
			latest = updated
		}
	}
	return latest
}

func recordUpdatedAt(record memcore.MemoryRecord) time.Time {
	if !record.UpdatedAt.IsZero() {
		return record.UpdatedAt
	}
	return record.CreatedAt
}

func memoryRefs(ids []string) []string {
	refs := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			refs = append(refs, "memory:"+id)
		}
	}
	return refs
}

func worldSummary(topic string, records []memcore.MemoryRecord) string {
	return "Organization world memory for " + topic + " consolidated from " + strconv.Itoa(len(records)) + " accepted memory records."
}

func worldNarrative(topic string, records []memcore.MemoryRecord) string {
	if len(records) == 0 {
		return "No accepted source memory was available for " + topic + "."
	}
	return "The current " + topic + " belief is based on " + strconv.Itoa(len(records)) + " accepted " + primaryWorldSourceLayer(records) + " memory records."
}

func worldLessons(topic string, records []memcore.MemoryRecord) []string {
	if len(records) == 0 {
		return nil
	}
	return []string{"Use accepted " + topic + " source memory before updating organization guidance."}
}

func worldWarnings(records []memcore.MemoryRecord, contradicts []string) []string {
	warnings := []string{"Organization guidance must remain traceable to its accepted source memories."}
	if len(records) == 1 {
		warnings = append(warnings, "World memory is based on a sparse source set and should be treated with lower confidence.")
	}
	if len(contradicts) > 0 {
		warnings = append(warnings, "Conflicting accepted world memory exists; do not silently overwrite prior belief.")
	}
	return warnings
}

func primaryWorldSourceLayer(records []memcore.MemoryRecord) string {
	for _, record := range records {
		if record.Layer == memcore.MemoryLayerExperience {
			return "experience"
		}
	}
	return "fact"
}

func worldConfidence(records []memcore.MemoryRecord, contradicts []string, now time.Time) float64 {
	confidence := 0.55
	switch {
	case len(records) >= 4:
		confidence = 0.86
	case len(records) >= 2:
		confidence = 0.78
	}
	if len(records) > 0 {
		total := 0.0
		for _, record := range records {
			value := record.Confidence
			if value <= 0 {
				value = 0.5
			}
			total += value
		}
		avg := total / float64(len(records))
		confidence = (confidence + avg) / 2
	}
	if !now.IsZero() && latestUpdatedAt(records).Before(now.Add(-90*24*time.Hour)) {
		confidence -= 0.15
	}
	if len(contradicts) > 0 {
		confidence -= 0.1
	}
	if confidence < 0.1 {
		return 0.1
	}
	if confidence > 1 {
		return 1
	}
	return confidence
}

func worldContradicts(newText, oldText string) bool {
	if chineseRequirementsContradict(newText, oldText) {
		return true
	}
	newNorm := normalizeWorldText(newText)
	oldNorm := normalizeWorldText(oldText)
	if newNorm == "" || oldNorm == "" {
		return false
	}
	if hasNegativeRequirement(newNorm) && hasPositiveRequirement(oldNorm) && keywordOverlap(newNorm, oldNorm) >= 2 {
		return true
	}
	if hasPositiveRequirement(newNorm) && hasNegativeRequirement(oldNorm) && keywordOverlap(newNorm, oldNorm) >= 2 {
		return true
	}
	return false
}

func worldTextSimilar(newText, oldText string) bool {
	if normalized := normalizeWorldText(newText); normalized != "" && normalized == normalizeWorldText(oldText) {
		return true
	}
	return keywordOverlap(normalizeWorldText(newText), normalizeWorldText(oldText)) >= 6
}

func normalizeWorldText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Join(strings.Fields(value), " ")
}

func hasNegativeRequirement(value string) bool {
	negativePhrases := []string{"must not", "should not", "do not", "does not", "cannot", "can't", "without"}
	for _, phrase := range negativePhrases {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

func hasPositiveRequirement(value string) bool {
	positivePhrases := []string{"must require", "must use", "should require", "requires", "required", "require ", "needs", "need "}
	for _, phrase := range positivePhrases {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

func keywordOverlap(left, right string) int {
	leftSet := keywords(left)
	rightSet := keywords(right)
	count := 0
	for value := range leftSet {
		if _, ok := rightSet[value]; ok {
			count++
		}
	}
	return count
}

func keywords(value string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, token := range strings.FieldsFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		token = strings.TrimSpace(token)
		if len(token) < 3 || isWorldStopword(token) {
			continue
		}
		out[token] = struct{}{}
	}
	addHanKeywords(out, value)
	return out
}

func isWorldStopword(value string) bool {
	switch value {
	case "the", "and", "for", "with", "from", "that", "this", "are", "was", "were", "when", "before", "after", "memory", "accepted", "world":
		return true
	default:
		return false
	}
}
