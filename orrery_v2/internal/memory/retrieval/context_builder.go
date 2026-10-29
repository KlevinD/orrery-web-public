package retrieval

import (
	"context"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
)

type MemoryBinder interface {
	Bind(ctx context.Context, req MemoryBindRequest) (MemoryBinding, error)
}

type MemoryBindRequest struct {
	AgentID               agentcore.AgentID
	MissionID             string
	TaskID                string
	Subject               memcore.AccessSubject
	PersonalProfileRef    agentcore.MemoryProfileRef
	CollectiveProfileRefs []agentcore.MemoryProfileRef
}

type MemoryBinding struct {
	PersonalDigest    memcore.MemoryDigest
	CollectiveDigests []memcore.MemoryDigest
	Warnings          []BindingWarning
}

type BindingWarning struct {
	Code    string
	Message string
}

type Binder struct {
	retriever *Retriever
}

func NewBinder(retriever *Retriever) *Binder {
	return &Binder{retriever: retriever}
}

func (b *Binder) Bind(ctx context.Context, req MemoryBindRequest) (MemoryBinding, error) {
	personal := memcore.MemoryDigest{}
	if !req.PersonalProfileRef.IsZero() {
		var err error
		personal, err = b.retriever.Retrieve(ctx, RetrieveRequest{
			AgentID:     req.AgentID,
			MissionID:   req.MissionID,
			TaskID:      req.TaskID,
			Subject:     req.Subject,
			ProfileRefs: []agentcore.MemoryProfileRef{req.PersonalProfileRef},
		})
		if err != nil {
			return MemoryBinding{}, err
		}
	}
	collectiveDigests := []memcore.MemoryDigest{}
	collectiveRefs := make([]agentcore.MemoryProfileRef, 0, len(req.CollectiveProfileRefs))
	for _, ref := range req.CollectiveProfileRefs {
		if !ref.IsZero() {
			collectiveRefs = append(collectiveRefs, ref)
		}
	}
	if len(collectiveRefs) > 0 {
		collective, err := b.retriever.Retrieve(ctx, RetrieveRequest{
			AgentID:     req.AgentID,
			MissionID:   req.MissionID,
			TaskID:      req.TaskID,
			Subject:     req.Subject,
			ProfileRefs: collectiveRefs,
		})
		if err != nil {
			return MemoryBinding{}, err
		}
		if len(collective.SourceMemoryRefs) > 0 || collective.Summary != "" {
			collectiveDigests = append(collectiveDigests, collective)
		}
	}
	return MemoryBinding{
		PersonalDigest:    personal,
		CollectiveDigests: collectiveDigests,
	}, nil
}
