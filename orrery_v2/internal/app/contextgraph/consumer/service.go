package consumer

import (
	"context"
	"fmt"
	"sort"
	"strings"

	contextgraph "orrery_v2/internal/app/contextgraph"
)

type ContextBundleBuilder interface {
	BuildContextBundle(ctx context.Context, req contextgraph.BuildContextBundleRequest) (contextgraph.ContextBundle, error)
}

type Service struct {
	contextGraph ContextBundleBuilder
}

func NewService(contextGraph ContextBundleBuilder) *Service {
	return &Service{contextGraph: contextGraph}
}

const (
	MinContextBundleTokenBudget = 512
	MaxContextBundleTokenBudget = 16000
	MaxContextBundleSubjects    = 32
	MaxContextBundleDepth       = 2
	MaxContextBundleKinds       = 8
	MaxGraphKindLength          = 256
	defaultContextBundleBudget  = 8000
)

type BuildContextBundleInput struct {
	TenantID         string             `json:"tenant_id"`
	Consumer         string             `json:"consumer"`
	Purpose          string             `json:"purpose"`
	ActorRef         contextgraph.Ref   `json:"actor_ref"`
	SubjectRefs      []contextgraph.Ref `json:"subject_refs"`
	Query            string             `json:"query,omitempty"`
	IncludeKinds     []string           `json:"include_kinds,omitempty"`
	MaxDepth         int                `json:"max_depth,omitempty"`
	TokenBudget      int                `json:"token_budget,omitempty"`
	PermissionScopes []string           `json:"-"`
}

type RequestWritebackInput struct {
	TenantID     string             `json:"tenant_id"`
	ActorRef     contextgraph.Ref   `json:"actor_ref"`
	ActionTypeID string             `json:"action_type_id"`
	ObjectRefs   []contextgraph.Ref `json:"object_refs"`
	Args         map[string]any     `json:"args"`
}

type RequestWritebackResult struct {
	ActionRunID string `json:"action_run_id,omitempty"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
}

func (s *Service) BuildContextBundle(ctx context.Context, input BuildContextBundleInput) (contextgraph.ContextBundle, error) {
	if s == nil || s.contextGraph == nil {
		return contextgraph.ContextBundle{}, fmt.Errorf("context graph service is required")
	}
	input, err := NormalizeBuildContextBundleInput(input)
	if err != nil {
		return contextgraph.ContextBundle{}, err
	}
	return s.contextGraph.BuildContextBundle(ctx, contextgraph.BuildContextBundleRequest{
		TenantID:         input.TenantID,
		Consumer:         input.Consumer,
		Purpose:          input.Purpose,
		ActorRef:         input.ActorRef,
		SubjectRefs:      append([]contextgraph.Ref(nil), input.SubjectRefs...),
		Query:            input.Query,
		IncludeKinds:     append([]string(nil), input.IncludeKinds...),
		MaxDepth:         input.MaxDepth,
		TokenBudget:      input.TokenBudget,
		PermissionScopes: append([]string(nil), input.PermissionScopes...),
	})
}

func NormalizeBuildContextBundleInput(input BuildContextBundleInput) (BuildContextBundleInput, error) {
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.Consumer = strings.TrimSpace(input.Consumer)
	input.Purpose = strings.TrimSpace(input.Purpose)
	input.ActorRef.Kind = strings.ToLower(strings.TrimSpace(input.ActorRef.Kind))
	input.ActorRef.ID = strings.TrimSpace(input.ActorRef.ID)
	if input.TenantID == "" || input.Consumer == "" || input.Purpose == "" || input.ActorRef.Kind == "" || input.ActorRef.ID == "" {
		return BuildContextBundleInput{}, fmt.Errorf("%w: tenant, consumer, purpose, and actor are required", contextgraph.ErrQueryInvalid)
	}
	if strings.TrimSpace(input.Query) != "" {
		return BuildContextBundleInput{}, fmt.Errorf("%w: ContextBundle does not accept query", contextgraph.ErrQueryInvalid)
	}
	if input.MaxDepth < 0 || input.MaxDepth > MaxContextBundleDepth {
		return BuildContextBundleInput{}, fmt.Errorf("%w: ContextBundle depth must be between 0 and %d", contextgraph.ErrQueryInvalid, MaxContextBundleDepth)
	}
	if input.TokenBudget == 0 {
		input.TokenBudget = defaultContextBundleBudget
	}
	if input.TokenBudget < MinContextBundleTokenBudget || input.TokenBudget > MaxContextBundleTokenBudget {
		return BuildContextBundleInput{}, fmt.Errorf("%w: ContextBundle token budget must be between %d and %d", contextgraph.ErrQueryInvalid, MinContextBundleTokenBudget, MaxContextBundleTokenBudget)
	}
	seen := make(map[string]bool, len(input.SubjectRefs))
	subjects := make([]contextgraph.Ref, 0, len(input.SubjectRefs))
	for _, ref := range input.SubjectRefs {
		ref.Kind = strings.ToLower(strings.TrimSpace(ref.Kind))
		ref.ID = strings.TrimSpace(ref.ID)
		if ref.Kind == "" || ref.ID == "" {
			return BuildContextBundleInput{}, fmt.Errorf("%w: ContextBundle subject kind and id are required", contextgraph.ErrQueryInvalid)
		}
		key := ref.Kind + "\x00" + ref.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		subjects = append(subjects, ref)
	}
	if len(subjects) == 0 || len(subjects) > MaxContextBundleSubjects {
		return BuildContextBundleInput{}, fmt.Errorf("%w: ContextBundle requires 1 to %d unique subjects", contextgraph.ErrQueryInvalid, MaxContextBundleSubjects)
	}
	input.SubjectRefs = subjects
	if len(input.IncludeKinds) > MaxContextBundleKinds {
		return BuildContextBundleInput{}, fmt.Errorf("%w: ContextBundle accepts at most %d include kinds", contextgraph.ErrQueryInvalid, MaxContextBundleKinds)
	}
	seenKinds := make(map[string]bool, len(input.IncludeKinds))
	kinds := make([]string, 0, len(input.IncludeKinds))
	for _, kind := range input.IncludeKinds {
		kind = strings.ToLower(strings.TrimSpace(kind))
		if kind == "" || seenKinds[kind] {
			continue
		}
		if len(kind) > MaxGraphKindLength {
			return BuildContextBundleInput{}, fmt.Errorf("%w: ContextBundle include kind exceeds %d bytes", contextgraph.ErrQueryInvalid, MaxGraphKindLength)
		}
		seenKinds[kind] = true
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	input.IncludeKinds = kinds
	input.Query = ""
	return input, nil
}

func (s *Service) RequestWriteback(ctx context.Context, input RequestWritebackInput) (RequestWritebackResult, error) {
	if err := ctx.Err(); err != nil {
		return RequestWritebackResult{}, err
	}
	if strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.ActionTypeID) == "" || strings.TrimSpace(input.ActorRef.ID) == "" || len(input.ObjectRefs) == 0 {
		return RequestWritebackResult{}, fmt.Errorf("tenant_id, actor_ref, action_type_id, and object_refs are required")
	}
	return RequestWritebackResult{
		Status: "needs_decision",
		Reason: "writeback requests must pass through Action Contract before commit",
	}, nil
}
