package hil

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"orrery_v2/internal/app/actioncontract"
)

const (
	BusinessApprovalToolName   = "request_business_approval"
	businessApprovalAction     = string(actioncontract.ActionBusinessApprovalRequest)
	businessApprovalActionType = string(actioncontract.ActionBusinessApprovalRequest)
)

type BusinessApprovalDecisionCreator interface {
	CreateDecision(context.Context, CreateDecisionInput) (CreateDecisionResult, error)
}

type BusinessApprovalToolRequest struct {
	TenantID    string
	PrincipalID string
	AgentID     string
	Arguments   map[string]any
}

type BusinessApprovalArtifact struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Kind  string `json:"kind,omitempty"`
}

type BusinessApprovalToolResult struct {
	DecisionID         string
	WorkflowInstanceID string
	Status             DecisionStatus
	RouteMode          DecisionMode
	MaterialHash       string
	Revision           int
}

type BusinessApprovalToolExecutor struct {
	creator BusinessApprovalDecisionCreator
	now     func() time.Time
}

func NewBusinessApprovalToolExecutor(creator BusinessApprovalDecisionCreator, now func() time.Time) *BusinessApprovalToolExecutor {
	if now == nil {
		now = time.Now
	}
	return &BusinessApprovalToolExecutor{creator: creator, now: now}
}

func (e *BusinessApprovalToolExecutor) Execute(ctx context.Context, request BusinessApprovalToolRequest) (BusinessApprovalToolResult, error) {
	if e == nil || e.creator == nil {
		return BusinessApprovalToolResult{}, ErrInvalidRequest
	}
	arguments, err := decodeBusinessApprovalArguments(request.Arguments)
	if err != nil {
		return BusinessApprovalToolResult{}, err
	}
	material, err := NormalizeBusinessApprovalMaterial(arguments.BusinessApprovalMaterial)
	if err != nil {
		return BusinessApprovalToolResult{}, err
	}
	tenantID := strings.TrimSpace(request.TenantID)
	requesterID := strings.TrimSpace(request.AgentID)
	if requesterID == "" {
		requesterID = strings.TrimSpace(request.PrincipalID)
	}
	resource := ObjectRef{Kind: strings.TrimSpace(arguments.ResourceKind), ID: strings.TrimSpace(arguments.ResourceID)}
	idempotencyKey := strings.TrimSpace(arguments.IdempotencyKey)
	if tenantID == "" || requesterID == "" || resource.Kind == "" || resource.ID == "" || idempotencyKey == "" {
		return BusinessApprovalToolResult{}, ErrInvalidRequest
	}
	expiresAt := e.now().UTC().Add(7 * 24 * time.Hour)
	if raw := strings.TrimSpace(arguments.ExpiresAt); raw != "" {
		parsed, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil || !parsed.After(e.now().UTC()) {
			return BusinessApprovalToolResult{}, ErrInvalidRequest
		}
		expiresAt = parsed.UTC()
	}
	materialHash := HashBusinessApprovalMaterial(material)
	evidenceRefs := normalizedBusinessApprovalStrings(arguments.EvidenceRefs)
	artifacts := normalizeBusinessApprovalArtifacts(arguments.Artifacts)
	payloadPreview := map[string]any{
		"approval_request_kind":      "business",
		"approval_tool_name":         BusinessApprovalToolName,
		"business_approval_material": businessApprovalMaterialMap(material),
		"material_hash":              materialHash,
		"evidence_refs":              evidenceRefs,
		"artifacts":                  artifacts,
	}
	argsHash := "sha256:" + stableFingerprint(struct {
		Resource     ObjectRef
		MaterialHash string
		EvidenceRefs []string
		Artifacts    []BusinessApprovalArtifact
	}{resource, materialHash, evidenceRefs, artifacts})
	created, err := e.creator.CreateDecision(ctx, CreateDecisionInput{
		TenantID:         tenantID,
		Requester:        ActorRef{Kind: ActorAgent, ID: requesterID},
		Action:           businessApprovalAction,
		ActionTypeID:     businessApprovalActionType,
		Resource:         resource,
		ObjectRefs:       []ObjectRef{resource},
		ArgsHash:         argsHash,
		Purpose:          material.DecisionRequest,
		RiskLevel:        RiskHigh,
		ApprovalRequired: true,
		SubjectRefs:      []ObjectRef{resource},
		PayloadPreview:   payloadPreview,
		IdempotencyKey:   idempotencyKey,
		ExpiresAt:        expiresAt,
	})
	if err != nil {
		return BusinessApprovalToolResult{}, err
	}
	return BusinessApprovalToolResult{
		DecisionID: created.Decision.ID, Status: created.Decision.Status, RouteMode: created.Decision.Route.Mode,
		MaterialHash: materialHash, Revision: 1,
	}, nil
}

type businessApprovalArguments struct {
	BusinessApprovalMaterial
	ResourceKind   string                     `json:"resource_kind"`
	ResourceID     string                     `json:"resource_id"`
	EvidenceRefs   []string                   `json:"evidence_refs"`
	Artifacts      []BusinessApprovalArtifact `json:"artifacts"`
	ExpiresAt      string                     `json:"expires_at"`
	IdempotencyKey string                     `json:"idempotency_key"`
}

func decodeBusinessApprovalArguments(arguments map[string]any) (businessApprovalArguments, error) {
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return businessApprovalArguments{}, &ApprovalMaterialError{Fields: []string{"material"}}
	}
	var decoded businessApprovalArguments
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return businessApprovalArguments{}, &ApprovalMaterialError{Fields: []string{"material"}}
	}
	return decoded, nil
}

func businessApprovalMaterialMap(material BusinessApprovalMaterial) map[string]any {
	encoded, _ := json.Marshal(material)
	var result map[string]any
	_ = json.Unmarshal(encoded, &result)
	return result
}

func normalizedBusinessApprovalStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func normalizeBusinessApprovalArtifacts(values []BusinessApprovalArtifact) []BusinessApprovalArtifact {
	result := make([]BusinessApprovalArtifact, len(values))
	for index, value := range values {
		result[index] = BusinessApprovalArtifact{
			Label: strings.TrimSpace(value.Label), URL: strings.TrimSpace(value.URL), Kind: strings.TrimSpace(value.Kind),
		}
	}
	return result
}
