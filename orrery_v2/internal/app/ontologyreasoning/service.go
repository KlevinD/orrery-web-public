package ontologyreasoning

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
)

type Store interface {
	SaveRun(ctx context.Context, run ReasoningRun) error
	GetRun(ctx context.Context, tenantID string, runID string) (ReasoningRun, error)
	ListRuns(ctx context.Context, tenantID string) ([]ReasoningRun, error)
}

type RegistryReader interface {
	ListObjectTypes(context.Context) ([]ontology.ObjectType, error)
	ListLinkTypes(context.Context) ([]ontology.LinkType, error)
	ListPropertyTypes(context.Context, string) ([]ontology.PropertyType, error)
}

type GraphReader interface {
	Snapshot(ctx context.Context, tenantID string) ([]contextgraph.Object, []contextgraph.Link, []contextgraph.Fact, []contextgraph.Evidence, error)
}

type ContextReader interface {
	BuildContextBundle(ctx context.Context, req contextgraph.BuildContextBundleRequest) (contextgraph.ContextBundle, error)
}

type ActionTypeReader interface {
	ListActionTypes(context.Context) ([]actioncontract.ActionType, error)
}

type MetadataProposalWriter interface {
	SaveMetadataProposal(context.Context, ontology.OntologyMetadataProposal) error
}

type ServiceOptions struct {
	Store    Store
	Registry RegistryReader
	Graph    GraphReader
	Context  ContextReader
	Actions  ActionTypeReader
	Metadata MetadataProposalWriter
	Clock    func() time.Time
}

type Service struct {
	store    Store
	registry RegistryReader
	graph    GraphReader
	context  ContextReader
	actions  ActionTypeReader
	metadata MetadataProposalWriter
	clock    func() time.Time
}

const maxReasoningCandidates = 500

func NewService(opts ServiceOptions) *Service {
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		store:    opts.Store,
		registry: opts.Registry,
		graph:    opts.Graph,
		context:  opts.Context,
		actions:  opts.Actions,
		metadata: opts.Metadata,
		clock:    clock,
	}
}

func (s *Service) CreateRun(ctx context.Context, req CreateRunRequest) (ReasoningRun, error) {
	if err := ctx.Err(); err != nil {
		return ReasoningRun{}, err
	}
	if s == nil || s.store == nil {
		return ReasoningRun{}, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	req.TenantID = strings.TrimSpace(req.TenantID)
	if req.TenantID == "" {
		return ReasoningRun{}, fmt.Errorf("%w: tenant_id is required", ErrInvalidRequest)
	}
	if len(req.Candidates) > maxReasoningCandidates {
		return ReasoningRun{}, fmt.Errorf("%w: candidate count exceeds %d", ErrInvalidRequest, maxReasoningCandidates)
	}
	if req.ContextPack.TenantID != "" && strings.TrimSpace(req.ContextPack.TenantID) != req.TenantID {
		return ReasoningRun{}, fmt.Errorf("%w: context pack tenant does not match run tenant", ErrInvalidRequest)
	}
	if req.ContextPack.Hash != "" {
		actual := hashContextPack(req.ContextPack)
		if req.ContextPackHash != "" && strings.TrimSpace(req.ContextPackHash) != actual {
			return ReasoningRun{}, fmt.Errorf("%w: context pack hash does not match payload", ErrInvalidRequest)
		}
		req.ContextPackHash = actual
	}
	now := s.clock()
	run := ReasoningRun{
		ID:              newRunID(now),
		TenantID:        req.TenantID,
		Engine:          strings.TrimSpace(req.Engine),
		Model:           strings.TrimSpace(req.Model),
		ModelVersion:    strings.TrimSpace(req.ModelVersion),
		IntentHash:      strings.TrimSpace(req.IntentHash),
		InputHash:       strings.TrimSpace(req.InputHash),
		PromptHash:      strings.TrimSpace(req.PromptHash),
		ContextPackHash: strings.TrimSpace(req.ContextPackHash),
		Status:          RunStatusDraft,
		CandidateCount:  len(req.Candidates),
		Request:         cloneCreateRunRequest(req),
		ContextPack:     cloneContextPack(req.ContextPack),
		Candidates:      cloneCandidates(req.Candidates),
		Validation:      []ValidationResult{},
		Proposals:       []ReasoningProposal{},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.store.SaveRun(ctx, run); err != nil {
		return ReasoningRun{}, err
	}
	return cloneReasoningRun(run), nil
}

func (s *Service) GetRun(ctx context.Context, tenantID string, runID string) (ReasoningRun, error) {
	if err := ctx.Err(); err != nil {
		return ReasoningRun{}, err
	}
	if s == nil || s.store == nil {
		return ReasoningRun{}, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	tenantID = strings.TrimSpace(tenantID)
	runID = strings.TrimSpace(runID)
	if tenantID == "" {
		return ReasoningRun{}, fmt.Errorf("%w: tenant_id is required", ErrInvalidRequest)
	}
	if runID == "" {
		return ReasoningRun{}, fmt.Errorf("%w: run_id is required", ErrInvalidRequest)
	}
	return s.store.GetRun(ctx, tenantID, runID)
}

func (s *Service) ListRuns(ctx context.Context, tenantID string) ([]ReasoningRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ErrInvalidRequest)
	}
	runs, err := s.store.ListRuns(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	cloned := make([]ReasoningRun, 0, len(runs))
	for _, run := range runs {
		cloned = append(cloned, cloneReasoningRun(run))
	}
	return cloned, nil
}

func (s *Service) saveUpdatedRun(ctx context.Context, run ReasoningRun) (ReasoningRun, error) {
	run.UpdatedAt = s.clock()
	if err := s.store.SaveRun(ctx, run); err != nil {
		return ReasoningRun{}, err
	}
	return cloneReasoningRun(run), nil
}

func newRunID(now time.Time) string {
	return "ontology_reasoning_run_" + strconv.FormatInt(now.UnixNano(), 36) + "_" + randomRunSuffix()
}
