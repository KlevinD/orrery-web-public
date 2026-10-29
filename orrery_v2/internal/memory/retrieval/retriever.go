package retrieval

import (
	"context"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
)

type Repository interface {
	GetProfile(ctx context.Context, id agentcore.MemoryProfileID) (memcore.MemoryProfile, error)
	GetRecord(ctx context.Context, id string) (memcore.MemoryRecord, error)
	FindRecords(ctx context.Context, query memstore.RecordQuery) ([]memcore.MemoryRecord, error)
}

type Retriever struct {
	repository Repository
	compressor *Compressor
}

type RetrieveRequest struct {
	AgentID     agentcore.AgentID
	MissionID   string
	TaskID      string
	Subject     memcore.AccessSubject
	ProfileRefs []agentcore.MemoryProfileRef
}

func NewRetriever(repository Repository, compressor *Compressor) *Retriever {
	return &Retriever{repository: repository, compressor: compressor}
}

func (r *Retriever) Retrieve(ctx context.Context, req RetrieveRequest) (memcore.MemoryDigest, error) {
	var visibleProfileIDs []agentcore.MemoryProfileID
	var scope memcore.MemoryScope
	query := memstore.RecordQuery{IncludeArchived: false}
	limit := 0
	topicSet := map[string]struct{}{}
	for _, ref := range req.ProfileRefs {
		profile, err := r.repository.GetProfile(ctx, ref.MemoryProfileID)
		if err != nil {
			return memcore.MemoryDigest{}, err
		}
		if !profile.CanRead(req.Subject) {
			continue
		}
		visibleProfileIDs = append(visibleProfileIDs, profile.ID)
		if scope.Kind == "" {
			scope = profile.DefaultScope
		}
		if profile.RetrievalPolicy.MaxRecords > 0 && (limit == 0 || profile.RetrievalPolicy.MaxRecords < limit) {
			limit = profile.RetrievalPolicy.MaxRecords
		}
		for _, tag := range profile.RetrievalPolicy.Tags {
			if tag = strings.TrimSpace(tag); tag != "" {
				topicSet[tag] = struct{}{}
			}
		}
	}
	if len(visibleProfileIDs) == 0 {
		return r.compressor.Compress(CompressRequest{
			AgentID:   req.AgentID,
			MissionID: req.MissionID,
			TaskID:    req.TaskID,
		})
	}
	query.ProfileIDs = visibleProfileIDs
	records, err := r.repository.FindRecords(ctx, query)
	if err != nil {
		return memcore.MemoryDigest{}, err
	}
	eligible := make([]memcore.MemoryRecord, 0, len(records))
	allowed := map[agentcore.MemoryProfileID]bool{}
	for _, id := range visibleProfileIDs {
		allowed[id] = true
	}
	for _, record := range records {
		if allowed[record.ProfileID] && memcore.RetrievalEligible(record, r.compressor.now()) {
			eligible = append(eligible, record)
		}
	}
	records = rankRecords(eligible, topicSet)
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}
	digest, err := r.compressor.Compress(CompressRequest{
		AgentID:   req.AgentID,
		MissionID: req.MissionID,
		TaskID:    req.TaskID,
		Scope:     scope,
		Records:   records,
	})
	if err != nil {
		return memcore.MemoryDigest{}, err
	}
	digest.Evidence, err = ValidateRecords(ctx, r.repository, req.Subject, records, r.compressor.now())
	if err != nil {
		return memcore.MemoryDigest{}, err
	}
	return digest, nil
}

func rankRecords(records []memcore.MemoryRecord, topicSet map[string]struct{}) []memcore.MemoryRecord {
	if len(records) < 2 {
		return records
	}
	out := make([]memcore.MemoryRecord, 0, len(records))
	for _, record := range records {
		out = append(out, record.Normalized().Clone())
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := rankedRecord{record: out[i], topicSet: topicSet}
		right := rankedRecord{record: out[j], topicSet: topicSet}
		return left.less(right)
	})
	return out
}

type rankedRecord struct {
	record   memcore.MemoryRecord
	topicSet map[string]struct{}
}

func (r rankedRecord) less(other rankedRecord) bool {
	if left, right := retrievalScore(r), retrievalScore(other); left != right {
		return left > right
	}
	if left, right := recordRecency(r.record), recordRecency(other.record); !left.Equal(right) {
		return left.After(right)
	}
	return r.record.ID < other.record.ID
}

func retrievalScore(r rankedRecord) float64 {
	return layerScore(r.record.Layer) +
		float64(topicRank(r.record, r.topicSet)) +
		float64(lifecycleRank(r.record.LifecycleStatus)) +
		r.record.Confidence*10 +
		referenceScore(r.record.ReferenceCount)
}

func layerScore(layer memcore.MemoryLayer) float64 {
	switch memcore.NormalizeMemoryLayer(layer) {
	case memcore.MemoryLayerWorld:
		return 8
	case memcore.MemoryLayerExperience:
		return 6
	default:
		return 0
	}
}

func topicRank(record memcore.MemoryRecord, topicSet map[string]struct{}) int {
	if len(topicSet) == 0 {
		return 0
	}
	if _, ok := topicSet[strings.TrimSpace(record.Topic)]; ok {
		return 2
	}
	for _, tag := range record.Tags {
		if _, ok := topicSet[strings.TrimSpace(tag)]; ok {
			return 1
		}
	}
	return 0
}

func lifecycleRank(status memcore.MemoryLifecycleStatus) int {
	switch memcore.NormalizeMemoryLifecycleStatus(status) {
	case memcore.MemoryLifecycleActive:
		return 3
	case memcore.MemoryLifecycleSettled:
		return 2
	case memcore.MemoryLifecycleDistilled:
		return 1
	default:
		return 0
	}
}

func referenceScore(count int) float64 {
	if count <= 0 {
		return 0
	}
	if count > 10 {
		count = 10
	}
	return float64(count) * 0.5
}

func recordRecency(record memcore.MemoryRecord) time.Time {
	for _, value := range []time.Time{record.LastReferencedAt, record.UpdatedAt, record.CreatedAt, record.ValidFrom} {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
