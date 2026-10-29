package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
)

const (
	defaultOntologyStewardPeopleWorkforceChangeSetID      = "change-set-orrery-ontology-steward-workforce-v1"
	defaultOntologyStewardPeopleResponsibilityChangeSetID = "change-set-orrery-ontology-steward-responsibility-v1"
	defaultOntologyStewardPeopleOrgUnitID                 = "org-openclaw-digital"
	defaultOntologyStewardPeopleManagerSubjectID          = "member-dingpi"
	defaultOntologyStewardPeopleJobCode                   = "SYS-ONTOLOGY-STEWARD"
	defaultOntologyStewardPeopleResponsibilityCode        = "ROLE.ONTOLOGY_STEWARD"
)

var defaultOntologyStewardPeopleEffectiveAt = time.Date(2026, time.August, 28, 0, 0, 0, 0, time.UTC)

type ontologyStewardPeopleProjection struct {
	SubjectID        string
	PositionID       string
	ResponsibilityID string
}

type ontologyStewardPeopleChangeSetOperation struct {
	Index         int             `json:"index"`
	OperationType string          `json:"operation_type"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	Payload       json.RawMessage `json:"payload"`
}

type ontologyStewardPeopleChangeSet struct {
	ID          string                                    `json:"id"`
	Reason      string                                    `json:"reason"`
	EffectiveAt string                                    `json:"effective_at"`
	Status      string                                    `json:"status"`
	Revision    int64                                     `json:"revision"`
	Operations  []ontologyStewardPeopleChangeSetOperation `json:"operations"`
	EvidenceRef string                                    `json:"evidence_ref"`
	Impact      ontologyStewardPeopleChangeSetValidation  `json:"impact"`
}

type ontologyStewardPeopleChangeSetValidation struct {
	Blockers []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"blockers"`
}

type ontologyStewardPeopleChangeSetSeed struct {
	ID             string
	Reason         string
	OperationCount int
	Operation      func(int, []ontologyStewardPeopleChangeSetOperation) (string, map[string]any, error)
}

type ontologyStewardPeopleClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func ensureDefaultOntologyStewardPeopleProjection(ctx context.Context, rt *platform.Runtime) error {
	if rt == nil {
		return fmt.Errorf("ensure ontology steward People projection: runtime is required")
	}
	cfg := rt.Config.Integrations.People
	if !cfg.Enabled || skipDefaultProfileBootstrap() {
		return nil
	}
	token, err := (platformSecretResolver{provider: rt.Secrets}).ResolveSecret(ctx, cfg.TokenRef)
	if err != nil {
		return fmt.Errorf("resolve ontology steward People token: %w", err)
	}
	client, err := newOntologyStewardPeopleClient(cfg.BaseURL, token, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	projection, err := client.ensure(ctx)
	if err != nil {
		return fmt.Errorf("ensure ontology steward People projection: %w", err)
	}
	if err := bindDefaultOntologyStewardPeopleSubject(&rt.Config, projection.SubjectID); err != nil {
		return fmt.Errorf("bind ontology steward People subject: %w", err)
	}
	return nil
}

func newOntologyStewardPeopleClient(baseURL, token string, client *http.Client) (*ontologyStewardPeopleClient, error) {
	baseURL, token = strings.TrimRight(strings.TrimSpace(baseURL), "/"), strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return nil, fmt.Errorf("configure ontology steward People projection: base URL and token are required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("configure ontology steward People projection: invalid base URL")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &ontologyStewardPeopleClient{baseURL: baseURL, token: token, http: client}, nil
}

func (c *ontologyStewardPeopleClient) ensure(ctx context.Context) (ontologyStewardPeopleProjection, error) {
	workforce, err := c.ensureChangeSet(ctx, ontologyStewardPeopleWorkforceSeed())
	if err != nil {
		return ontologyStewardPeopleProjection{}, err
	}
	subjectID, err := ontologyStewardPeopleOperationID(workforce.Operations, 0, "subject.create")
	if err != nil {
		return ontologyStewardPeopleProjection{}, err
	}
	positionID, err := ontologyStewardPeopleOperationID(workforce.Operations, 2, "position.create")
	if err != nil {
		return ontologyStewardPeopleProjection{}, err
	}
	responsibilities, err := c.ensureChangeSet(ctx, ontologyStewardPeopleResponsibilitySeed(positionID))
	if err != nil {
		return ontologyStewardPeopleProjection{}, err
	}
	responsibilityID, err := ontologyStewardPeopleOperationID(responsibilities.Operations, 0, "responsibility.create")
	if err != nil {
		return ontologyStewardPeopleProjection{}, err
	}
	if err := c.verifyResponsibility(ctx, subjectID, positionID, responsibilityID); err != nil {
		return ontologyStewardPeopleProjection{}, err
	}
	return ontologyStewardPeopleProjection{
		SubjectID: subjectID, PositionID: positionID, ResponsibilityID: responsibilityID,
	}, nil
}

func ontologyStewardPeopleWorkforceSeed() ontologyStewardPeopleChangeSetSeed {
	return ontologyStewardPeopleChangeSetSeed{
		ID:             defaultOntologyStewardPeopleWorkforceChangeSetID,
		Reason:         "将 Orrery 本体管理员大头纳入 People 权威组织与汇报体系",
		OperationCount: 5,
		Operation: func(index int, existing []ontologyStewardPeopleChangeSetOperation) (string, map[string]any, error) {
			switch index {
			case 0:
				return "subject.create", map[string]any{
					"subject_type": "digital", "display_name": "大头",
				}, nil
			case 1:
				return "job_profile.create", map[string]any{
					"code": defaultOntologyStewardPeopleJobCode, "display_name": "本体管理员",
					"job_family": "系统治理", "job_level": "智能体",
				}, nil
			case 2:
				jobProfileID, err := ontologyStewardPeopleOperationID(existing, 1, "job_profile.create")
				return "position.create", map[string]any{
					"code": defaultOntologyStewardPeopleJobCode, "display_name": "本体管理员",
					"org_unit_id": defaultOntologyStewardPeopleOrgUnitID, "job_profile_id": jobProfileID,
					"approved_headcount": 1, "frozen": false,
				}, err
			case 3:
				subjectID, err := ontologyStewardPeopleOperationID(existing, 0, "subject.create")
				if err != nil {
					return "", nil, err
				}
				positionID, err := ontologyStewardPeopleOperationID(existing, 2, "position.create")
				return "assignment.start", map[string]any{
					"subject_id": subjectID, "org_unit_id": defaultOntologyStewardPeopleOrgUnitID,
					"position_id": positionID, "assignment_kind": "primary", "allocation_bps": 10000,
					"allow_over_allocation": false, "valid_to": nil,
				}, err
			case 4:
				subjectID, err := ontologyStewardPeopleOperationID(existing, 0, "subject.create")
				return "reporting.set", map[string]any{
					"subject_id": subjectID, "manager_id": defaultOntologyStewardPeopleManagerSubjectID,
					"reporting_type": "solid", "valid_to": nil,
				}, err
			default:
				return "", nil, fmt.Errorf("unsupported ontology steward workforce operation %d", index)
			}
		},
	}
}

func ontologyStewardPeopleResponsibilitySeed(positionID string) ontologyStewardPeopleChangeSetSeed {
	return ontologyStewardPeopleChangeSetSeed{
		ID:             defaultOntologyStewardPeopleResponsibilityChangeSetID,
		Reason:         "登记并绑定 Orrery 本体管理员的业务职责",
		OperationCount: 2,
		Operation: func(index int, existing []ontologyStewardPeopleChangeSetOperation) (string, map[string]any, error) {
			switch index {
			case 0:
				return "responsibility.create", map[string]any{
					"code":        defaultOntologyStewardPeopleResponsibilityCode,
					"title":       "本体治理与自动修复",
					"description": "理解实际业务及权威数据源，维护授权本体、映射与投影；主动修复有证据且可回滚的问题，重大变更提交 ding 审批。",
					"valid_to":    nil,
				}, nil
			case 1:
				responsibilityID, err := ontologyStewardPeopleOperationID(existing, 0, "responsibility.create")
				return "responsibility.bind", map[string]any{
					"responsibility_id": responsibilityID, "target_type": "position",
					"target_id": positionID, "valid_to": nil,
				}, err
			default:
				return "", nil, fmt.Errorf("unsupported ontology steward responsibility operation %d", index)
			}
		},
	}
}

func (c *ontologyStewardPeopleClient) ensureChangeSet(ctx context.Context, seed ontologyStewardPeopleChangeSetSeed) (ontologyStewardPeopleChangeSet, error) {
	current, found, err := c.getChangeSet(ctx, seed.ID)
	if err != nil {
		return ontologyStewardPeopleChangeSet{}, err
	}
	if !found {
		current, err = c.mutateChangeSet(ctx, http.MethodPost, "/api/v2/change-sets", 0, seed.ID+"-create", map[string]any{
			"id": seed.ID, "reason": seed.Reason, "effective_at": defaultOntologyStewardPeopleEffectiveAt,
			"evidence_ref": "orrery:bootstrap:ontology-steward-v1",
		})
		if err != nil {
			return ontologyStewardPeopleChangeSet{}, err
		}
	}
	if err := validateOntologyStewardPeopleChangeSet(current, seed); err != nil {
		return ontologyStewardPeopleChangeSet{}, err
	}
	for index := 0; index < seed.OperationCount; index++ {
		operationType, input, err := seed.Operation(index, current.Operations)
		if err != nil {
			return ontologyStewardPeopleChangeSet{}, err
		}
		if index < len(current.Operations) {
			if err := validateOntologyStewardPeopleOperation(current.Operations[index], index, operationType, input); err != nil {
				return ontologyStewardPeopleChangeSet{}, err
			}
			continue
		}
		if current.Status != "draft" && current.Status != "invalid" {
			return ontologyStewardPeopleChangeSet{}, fmt.Errorf("People ChangeSet %q is %q with missing operations", seed.ID, current.Status)
		}
		current, err = c.mutateChangeSet(
			ctx, http.MethodPost, "/api/v2/change-sets/"+url.PathEscape(seed.ID)+"/operations",
			current.Revision, fmt.Sprintf("%s-operation-%d", seed.ID, index),
			map[string]any{"operation_type": operationType, "payload": input},
		)
		if err != nil {
			return ontologyStewardPeopleChangeSet{}, err
		}
	}
	if len(current.Operations) != seed.OperationCount {
		return ontologyStewardPeopleChangeSet{}, fmt.Errorf("People ChangeSet %q has %d operations, want %d", seed.ID, len(current.Operations), seed.OperationCount)
	}
	if current.Status == "draft" {
		current, err = c.mutateChangeSet(ctx, http.MethodPost, "/api/v2/change-sets/"+url.PathEscape(seed.ID)+"/validate", current.Revision, seed.ID+"-validate", struct{}{})
		if err != nil {
			return ontologyStewardPeopleChangeSet{}, err
		}
	}
	if current.Status == "invalid" {
		return ontologyStewardPeopleChangeSet{}, ontologyStewardPeopleValidationError(current)
	}
	if current.Status == "validated" {
		current, err = c.mutateChangeSet(ctx, http.MethodPost, "/api/v2/change-sets/"+url.PathEscape(seed.ID)+"/submit", current.Revision, seed.ID+"-submit", struct{}{})
		if err != nil {
			return ontologyStewardPeopleChangeSet{}, err
		}
	}
	if current.Status == "submitted" || current.Status == "approved" {
		current, err = c.mutateChangeSet(ctx, http.MethodPost, "/api/v2/change-sets/"+url.PathEscape(seed.ID)+"/apply", current.Revision, seed.ID+"-apply", map[string]any{"approval_assertion": ""})
		if err != nil {
			return ontologyStewardPeopleChangeSet{}, err
		}
	}
	if current.Status != "applied" {
		return ontologyStewardPeopleChangeSet{}, fmt.Errorf("People ChangeSet %q stopped in status %q", seed.ID, current.Status)
	}
	return current, nil
}

func (c *ontologyStewardPeopleClient) getChangeSet(ctx context.Context, id string) (ontologyStewardPeopleChangeSet, bool, error) {
	var result ontologyStewardPeopleChangeSet
	status, err := c.request(ctx, http.MethodGet, "/api/v2/change-sets/"+url.PathEscape(id), 0, "", nil, &result)
	if status == http.StatusNotFound {
		return ontologyStewardPeopleChangeSet{}, false, nil
	}
	if err != nil {
		return ontologyStewardPeopleChangeSet{}, false, err
	}
	return result, true, nil
}

func (c *ontologyStewardPeopleClient) mutateChangeSet(ctx context.Context, method, path string, revision int64, idempotencyKey string, body any) (ontologyStewardPeopleChangeSet, error) {
	var result ontologyStewardPeopleChangeSet
	_, err := c.request(ctx, method, path, revision, idempotencyKey, body, &result)
	return result, err
}

func (c *ontologyStewardPeopleClient) request(ctx context.Context, method, path string, revision int64, idempotencyKey string, body, result any) (int, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
		request.Header.Set("X-Correlation-ID", idempotencyKey)
	}
	if revision > 0 {
		request.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(revision, 10)))
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return response.StatusCode, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var problem struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(payload, &problem)
		return response.StatusCode, fmt.Errorf("People API %s %s failed with status %d (%s): %s", method, path, response.StatusCode, strings.TrimSpace(problem.Code), strings.TrimSpace(problem.Detail))
	}
	if result != nil {
		if err := json.Unmarshal(payload, result); err != nil {
			return response.StatusCode, fmt.Errorf("decode People API %s %s response: %w", method, path, err)
		}
	}
	return response.StatusCode, nil
}

func validateOntologyStewardPeopleChangeSet(current ontologyStewardPeopleChangeSet, seed ontologyStewardPeopleChangeSetSeed) error {
	effectiveAt, err := time.Parse(time.RFC3339Nano, current.EffectiveAt)
	if err != nil || current.ID != seed.ID || current.Reason != seed.Reason ||
		!effectiveAt.Equal(defaultOntologyStewardPeopleEffectiveAt) || current.EvidenceRef != "orrery:bootstrap:ontology-steward-v1" {
		return fmt.Errorf("People ChangeSet %q does not match the Orrery ontology steward seed", seed.ID)
	}
	if len(current.Operations) > seed.OperationCount {
		return fmt.Errorf("People ChangeSet %q has unexpected operations", seed.ID)
	}
	return nil
}

func validateOntologyStewardPeopleOperation(current ontologyStewardPeopleChangeSetOperation, index int, operationType string, input map[string]any) error {
	if current.OperationType != operationType || current.Index != index {
		return fmt.Errorf("People ChangeSet operation %d does not match %q", current.Index, operationType)
	}
	serverIDField := ontologyStewardPeopleServerIDField(operationType)
	var actual map[string]any
	if err := json.Unmarshal(current.Payload, &actual); err != nil {
		return fmt.Errorf("decode People ChangeSet operation %d: %w", current.Index, err)
	}
	if serverIDField == "" || strings.TrimSpace(current.AggregateID) == "" || actual[serverIDField] != current.AggregateID {
		return fmt.Errorf("People ChangeSet operation %d has an invalid server-owned id", current.Index)
	}
	delete(actual, serverIDField)
	expectedPayload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	var expected map[string]any
	if err := json.Unmarshal(expectedPayload, &expected); err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("People ChangeSet operation %d payload does not match the Orrery ontology steward seed", current.Index)
	}
	return nil
}

func ontologyStewardPeopleServerIDField(operationType string) string {
	switch operationType {
	case "subject.create":
		return "subject_id"
	case "job_profile.create":
		return "job_profile_id"
	case "position.create":
		return "position_id"
	case "assignment.start":
		return "assignment_id"
	case "reporting.set":
		return "reporting_relationship_id"
	case "responsibility.create":
		return "responsibility_id"
	case "responsibility.bind":
		return "responsibility_binding_id"
	default:
		return ""
	}
}

func ontologyStewardPeopleOperationID(operations []ontologyStewardPeopleChangeSetOperation, index int, operationType string) (string, error) {
	if index < 0 || index >= len(operations) || operations[index].Index != index || operations[index].OperationType != operationType || strings.TrimSpace(operations[index].AggregateID) == "" {
		return "", fmt.Errorf("People ChangeSet is missing operation %d (%s)", index, operationType)
	}
	return strings.TrimSpace(operations[index].AggregateID), nil
}

func ontologyStewardPeopleValidationError(current ontologyStewardPeopleChangeSet) error {
	if len(current.Impact.Blockers) == 0 {
		return fmt.Errorf("People ChangeSet %q failed validation", current.ID)
	}
	blocker := current.Impact.Blockers[0]
	return fmt.Errorf("People ChangeSet %q failed validation (%s): %s", current.ID, blocker.Code, blocker.Message)
}

func (c *ontologyStewardPeopleClient) verifyResponsibility(ctx context.Context, subjectID, positionID, responsibilityID string) error {
	var result struct {
		SubjectID string `json:"subject_id"`
		Items     []struct {
			ID      string `json:"id"`
			Sources []struct {
				ResponsibilityID string `json:"responsibility_id"`
				TargetType       string `json:"target_type"`
				TargetID         string `json:"target_id"`
			} `json:"sources"`
		} `json:"items"`
	}
	_, err := c.request(ctx, http.MethodGet, "/api/v2/subjects/"+url.PathEscape(subjectID)+"/responsibilities", 0, "", nil, &result)
	if err != nil {
		return err
	}
	if result.SubjectID != subjectID {
		return fmt.Errorf("People responsibility resolution returned subject %q, want %q", result.SubjectID, subjectID)
	}
	for _, item := range result.Items {
		if item.ID != responsibilityID {
			continue
		}
		for _, source := range item.Sources {
			if source.ResponsibilityID == responsibilityID && source.TargetType == "position" && source.TargetID == positionID {
				return nil
			}
		}
	}
	return fmt.Errorf("People subject %q is missing ontology steward responsibility %q", subjectID, responsibilityID)
}

func bindDefaultOntologyStewardPeopleSubject(cfg *config.Config, subjectID string) error {
	if cfg == nil || strings.TrimSpace(subjectID) == "" {
		return fmt.Errorf("People subject id is required")
	}
	bindings := cfg.Integrations.People.AgentBindings
	if bindings == nil {
		bindings = make(map[string]string)
		cfg.Integrations.People.AgentBindings = bindings
	}
	subjectID = strings.TrimSpace(subjectID)
	for existingSubjectID, profileID := range bindings {
		existingSubjectID, profileID = strings.TrimSpace(existingSubjectID), strings.TrimSpace(profileID)
		if existingSubjectID == subjectID && profileID != "" && profileID != defaultOntologyStewardAgentID {
			return fmt.Errorf("People subject %q is already bound to Agent %q", subjectID, profileID)
		}
		if profileID == defaultOntologyStewardAgentID && existingSubjectID != subjectID {
			return fmt.Errorf("ontology steward Agent is already bound to People subject %q", existingSubjectID)
		}
	}
	bindings[subjectID] = defaultOntologyStewardAgentID
	return nil
}
