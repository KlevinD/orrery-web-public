package httptransport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	projectadmin "orrery_v2/internal/app/projectadmin"
	missioncore "orrery_v2/internal/mission/core"
	discussion "orrery_v2/internal/mission/discussion"
)

const defaultListLimit = 20

func correlationID(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Request-ID")); value != "" {
		return value
	}
	if value := strings.TrimSpace(r.Header.Get("X-Correlation-ID")); value != "" {
		return value
	}
	return "http-" + strconv.FormatInt(r.ContentLength+int64(len(r.URL.Path)), 36)
}

func listPage(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = defaultListLimit
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func nonNegativeQueryInt(req *http.Request, name string, fallback int) int {
	raw := strings.TrimSpace(req.URL.Query().Get(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func idempotencyKey(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("Idempotency-Key"))
}

func requiredIdempotencyKey(r *http.Request) (string, error) {
	key := idempotencyKey(r)
	if key == "" || len(key) > 256 || strings.IndexFunc(key, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("idempotency key is required and must be at most 256 characters")
	}
	return key, nil
}

func expectedVersion(r *http.Request) (int, error) {
	value := strings.TrimSpace(r.Header.Get("If-Match"))
	if value == "" {
		return 0, nil
	}
	value = strings.Trim(value, `"`)
	version, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid If-Match version")
	}
	return version, nil
}

func decodeSubmitResult(r *http.Request, missionID missioncore.MissionID, taskID missioncore.TaskID) (missioncontrol.SubmitResultCommand, error) {
	var body submitResultRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return missioncontrol.SubmitResultCommand{}, err
	}
	if body.InternalTrace != "" {
		return missioncontrol.SubmitResultCommand{}, forbiddenRequestFieldError{field: "internal_trace_ref"}
	}
	version, err := expectedVersion(r)
	if err != nil {
		return missioncontrol.SubmitResultCommand{}, err
	}
	return missioncontrol.SubmitResultCommand{
		RequestID:       correlationID(r),
		IdempotencyKey:  idempotencyKey(r),
		ExpectedVersion: version,
		MissionID:       missionID,
		TaskID:          taskID,
		Result: agentcore.AgentResult{
			ID:           agentcore.AgentResultID(body.AgentResultID),
			MissionID:    missionID,
			TaskID:       taskID,
			AgentID:      agentcore.AgentID(body.AgentID),
			RunID:        agentcore.AgentRunID(body.RunID),
			Summary:      agentcore.Summary{Text: body.Summary},
			CreatedAt:    body.CreatedAt,
			Deliverables: mapDeliverables(body.Deliverables),
			Evidence:     mapEvidence(body.Evidence),
		},
	}, nil
}

func decodeQADecision(r *http.Request, missionID missioncore.MissionID, qaRef string) (missioncontrol.QADecisionCommand, error) {
	var body qaDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return missioncontrol.QADecisionCommand{}, err
	}
	return missioncontrol.QADecisionCommand{
		RequestID:        correlationID(r),
		IdempotencyKey:   idempotencyKey(r),
		MissionID:        missionID,
		TaskID:           missioncore.TaskID(body.TaskID),
		QARef:            qaRef,
		SubmissionID:     body.SubmissionID,
		Decision:         body.Decision,
		Message:          body.Message,
		CheckedCriteria:  body.CheckedCriteria,
		EvidenceResults:  body.EvidenceResults,
		MissingEvidence:  append([]string(nil), body.MissingEvidence...),
		RejectionReasons: append([]string(nil), body.RejectionReasons...),
	}, nil
}

func decodePostDiscussion(r *http.Request, missionID missioncore.MissionID) (missioncontrol.PostDiscussionMessageCommand, error) {
	var body discussionMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return missioncontrol.PostDiscussionMessageCommand{}, err
	}
	return missioncontrol.PostDiscussionMessageCommand{
		RequestID:         correlationID(r),
		IdempotencyKey:    idempotencyKey(r),
		MissionID:         missionID,
		DiscussionGroupID: discussion.DiscussionGroupID(body.DiscussionGroupID),
		Content:           body.Content,
		Summary:           body.Summary,
		MessageKind:       body.MessageKind,
		Visibility:        body.Visibility,
		RelatedTaskID:     missioncore.TaskID(body.RelatedTaskID),
		RelatedEvidenceID: body.RelatedEvidenceID,
		SourceRefs:        append([]string(nil), body.SourceRefs...),
		RequiredResponse:  body.RequiredResponse,
		DueAt:             body.DueAt,
	}, nil
}

func decodeReplyDiscussion(r *http.Request, missionID missioncore.MissionID, messageID string) (missioncontrol.ReplyDiscussionMessageCommand, error) {
	var body discussionReplyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return missioncontrol.ReplyDiscussionMessageCommand{}, err
	}
	return missioncontrol.ReplyDiscussionMessageCommand{
		RequestID:         correlationID(r),
		IdempotencyKey:    idempotencyKey(r),
		MissionID:         missionID,
		DiscussionGroupID: discussion.DiscussionGroupID(body.DiscussionGroupID),
		MessageID:         discussion.MessageID(messageID),
		Content:           body.Content,
		Summary:           body.Summary,
		InboxItemID:       discussion.InboxItemID(body.InboxItemID),
	}, nil
}

func decodeCloseInbox(r *http.Request, inboxID string) (missioncontrol.InboxItemCommand, error) {
	var body closeInboxRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return missioncontrol.InboxItemCommand{}, err
		}
	}
	return missioncontrol.InboxItemCommand{RequestID: correlationID(r), IdempotencyKey: idempotencyKey(r), InboxItemID: discussion.InboxItemID(inboxID), ReplyMessageID: discussion.MessageID(body.ReplyMessageID)}, nil
}

func decodeApprovalDecision(r *http.Request, approvalID string) (missioncontrol.ApprovalDecisionCommand, error) {
	var body approvalDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return missioncontrol.ApprovalDecisionCommand{}, err
	}
	metadata := cloneStringMap(body.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	if value := strings.TrimSpace(body.PlanDigest); value != "" {
		metadata["plan_digest"] = value
	}
	return missioncontrol.ApprovalDecisionCommand{
		RequestID:      correlationID(r),
		IdempotencyKey: idempotencyKey(r),
		ApprovalID:     approvalID,
		ActionID:       body.ActionID,
		Message:        body.Message,
		ModifiedInput:  body.ModifiedInput,
		ChoiceID:       body.ChoiceID,
		Remember:       body.Remember,
		Metadata:       metadata,
		TaskID:         missioncore.TaskID(body.TaskID),
		ReplyMessageID: body.ReplyMessageID,
	}, nil
}

func decodeCreateMission(r *http.Request) (missioncontrol.CreateMissionCommand, error) {
	var body createMissionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return missioncontrol.CreateMissionCommand{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return missioncontrol.CreateMissionCommand{}, missioncontrol.ErrInvalidRequest
	}
	return missioncontrol.CreateMissionCommand{
		RequestID:                             correlationID(r),
		IdempotencyKey:                        idempotencyKey(r),
		Title:                                 body.Title,
		FinalGoal:                             body.FinalGoal,
		BusinessDirection:                     body.BusinessDirection,
		SourcePersonalAssistantConversationID: strings.TrimSpace(body.SourcePersonalAssistantConversationID),
		InitialHumanDisplayName:               body.InitialHumanDisplayName,
		InitialHumanAgentID: firstNonEmpty(
			body.InitialHumanAgentID,
			body.HumanAgentID,
			body.UserID,
		),
	}, nil
}

func decodeCreateProject(r *http.Request) (projectadmin.CreateProjectCommand, error) {
	var body createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return projectadmin.CreateProjectCommand{}, err
	}
	return projectadmin.CreateProjectCommand{
		ProjectID:      body.ProjectID,
		OrganizationID: body.OrganizationID,
		Title:          body.Title,
	}, nil
}

func decodeUpdateProject(r *http.Request) (projectadmin.UpdateProjectCommand, error) {
	var body updateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return projectadmin.UpdateProjectCommand{}, err
	}
	return projectadmin.UpdateProjectCommand{
		ProjectID: body.ProjectID,
		Title:     body.Title,
	}, nil
}

func decodeCreateTask(r *http.Request, missionID missioncore.MissionID) (missioncontrol.CreateTaskCommand, error) {
	var body createTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return missioncontrol.CreateTaskCommand{}, err
	}
	return missioncontrol.CreateTaskCommand{
		RequestID:            correlationID(r),
		IdempotencyKey:       idempotencyKey(r),
		MissionID:            missionID,
		AssigneeID:           body.AssigneeID,
		Goal:                 body.Goal,
		WorkerPersonalGoal:   body.WorkerPersonalGoal,
		Scope:                body.Scope,
		Priority:             body.Priority,
		DueAt:                body.DueAt,
		ExpectedDeliverables: mapExpectedDeliverableRequests(body.ExpectedDeliverables),
		EvidenceRequirements: mapEvidenceRequirementRequests(body.EvidenceRequirements),
	}, nil
}

func decodeAssignTask(r *http.Request, missionID missioncore.MissionID, taskID missioncore.TaskID) (missioncontrol.AssignTaskCommand, error) {
	var body assignTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return missioncontrol.AssignTaskCommand{}, err
	}
	return missioncontrol.AssignTaskCommand{RequestID: correlationID(r), IdempotencyKey: idempotencyKey(r), MissionID: missionID, TaskID: taskID, AssigneeID: body.AssigneeID, Reason: body.Reason}, nil
}

func decodeStartAgentRun(r *http.Request, missionID missioncore.MissionID, taskID missioncore.TaskID) (missioncontrol.StartAgentRunCommand, error) {
	var body startAgentRunRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return missioncontrol.StartAgentRunCommand{}, err
	}
	return missioncontrol.StartAgentRunCommand{
		RequestID:      correlationID(r),
		IdempotencyKey: idempotencyKey(r),
		MissionID:      missionID,
		TaskID:         taskID,
		AgentID:        body.AgentID,
		RuntimeKind:    body.RuntimeKind,
		Prompt:         body.Prompt,
		QueueID:        body.QueueID,
	}, nil
}

func decodeCancelAgentRun(r *http.Request, missionID missioncore.MissionID, runID string) (missioncontrol.CancelAgentRunCommand, error) {
	var body cancelAgentRunRequest
	if r.Body != nil && r.Body != http.NoBody {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return missioncontrol.CancelAgentRunCommand{}, err
		}
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				return missioncontrol.CancelAgentRunCommand{}, err
			}
		}
	}
	return missioncontrol.CancelAgentRunCommand{
		RequestID:      correlationID(r),
		IdempotencyKey: idempotencyKey(r),
		MissionID:      missionID,
		RunID:          agentcore.AgentRunID(runID),
		Reason:         body.Reason,
	}, nil
}

func decodeCancelMission(r *http.Request, missionID missioncore.MissionID) (missioncontrol.CancelMissionCommand, error) {
	var body cancelMissionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return missioncontrol.CancelMissionCommand{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return missioncontrol.CancelMissionCommand{}, missioncontrol.ErrInvalidRequest
	}
	return missioncontrol.CancelMissionCommand{
		RequestID:      correlationID(r),
		IdempotencyKey: idempotencyKey(r),
		MissionID:      missionID,
		Reason:         body.Reason,
	}, nil
}

func decodeRetryAgentRun(r *http.Request, missionID missioncore.MissionID, runID string) (missioncontrol.RetryAgentRunCommand, error) {
	if r.Body != nil && r.Body != http.NoBody {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return missioncontrol.RetryAgentRunCommand{}, err
		}
		if len(bytes.TrimSpace(raw)) > 0 {
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				return missioncontrol.RetryAgentRunCommand{}, err
			}
		}
	}
	return missioncontrol.RetryAgentRunCommand{
		RequestID:      correlationID(r),
		IdempotencyKey: idempotencyKey(r),
		MissionID:      missionID,
		RunID:          agentcore.AgentRunID(runID),
	}, nil
}

type createMissionRequest struct {
	Title                                 string `json:"title"`
	FinalGoal                             string `json:"final_goal"`
	BusinessDirection                     string `json:"business_direction"`
	InitialHumanAgentID                   string `json:"initial_human_agent_id"`
	InitialHumanDisplayName               string `json:"initial_human_display_name"`
	HumanAgentID                          string `json:"human_agent_id"`
	UserID                                string `json:"user_id"`
	SourcePersonalAssistantConversationID string `json:"source_personal_assistant_conversation_id"`
}

type createProjectRequest struct {
	ProjectID      string `json:"project_id"`
	OrganizationID string `json:"organization_id"`
	Title          string `json:"title"`
}

type updateProjectRequest struct {
	ProjectID string `json:"project_id"`
	Title     string `json:"title"`
}

type createTaskRequest struct {
	AssigneeID           string                       `json:"assignee_id"`
	Goal                 string                       `json:"goal"`
	WorkerPersonalGoal   string                       `json:"worker_personal_goal"`
	Scope                string                       `json:"scope"`
	Priority             string                       `json:"priority"`
	DueAt                time.Time                    `json:"due_at"`
	ExpectedDeliverables []expectedDeliverableRequest `json:"expected_deliverables"`
	EvidenceRequirements []evidenceRequirementRequest `json:"evidence_requirements"`
}

type expectedDeliverableRequest struct {
	Title string `json:"title"`
}

func mapExpectedDeliverableRequests(items []expectedDeliverableRequest) []missioncontrol.ExpectedDeliverableCommand {
	out := make([]missioncontrol.ExpectedDeliverableCommand, 0, len(items))
	for _, item := range items {
		out = append(out, missioncontrol.ExpectedDeliverableCommand{Title: item.Title})
	}
	return out
}

type evidenceRequirementRequest struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Required    bool   `json:"required"`
}

func mapEvidenceRequirementRequests(items []evidenceRequirementRequest) []missioncontrol.EvidenceRequirementCommand {
	out := make([]missioncontrol.EvidenceRequirementCommand, 0, len(items))
	for _, item := range items {
		out = append(out, missioncontrol.EvidenceRequirementCommand{
			ID: item.ID, Description: item.Description, Kind: item.Kind, Required: item.Required,
		})
	}
	return out
}

type assignTaskRequest struct {
	AssigneeID string `json:"assignee_id"`
	Reason     string `json:"reason"`
}

type startAgentRunRequest struct {
	AgentID     string `json:"agent_id"`
	RuntimeKind string `json:"runtime_kind"`
	Prompt      string `json:"prompt"`
	QueueID     string `json:"queue_id"`
}

type cancelAgentRunRequest struct {
	Reason string `json:"reason"`
}

type cancelMissionRequest struct {
	Reason string `json:"reason"`
}

type qaDecisionRequest struct {
	TaskID           string                               `json:"task_id"`
	SubmissionID     string                               `json:"submission_id"`
	Decision         string                               `json:"decision"`
	Message          string                               `json:"message"`
	CheckedCriteria  []missioncontrol.CheckedCriterionDTO `json:"checked_criteria"`
	EvidenceResults  []missioncontrol.EvidenceCheckDTO    `json:"evidence_results"`
	MissingEvidence  []string                             `json:"missing_evidence"`
	RejectionReasons []string                             `json:"rejection_reasons"`
}

type discussionMessageRequest struct {
	DiscussionGroupID string    `json:"discussion_group_id"`
	Content           string    `json:"content"`
	Summary           string    `json:"summary"`
	MessageKind       string    `json:"message_kind"`
	Visibility        string    `json:"visibility"`
	RelatedTaskID     string    `json:"related_task_id"`
	RelatedEvidenceID string    `json:"related_evidence_id"`
	SourceRefs        []string  `json:"source_refs"`
	RequiredResponse  bool      `json:"required_response"`
	DueAt             time.Time `json:"due_at"`
}

type discussionReplyRequest struct {
	DiscussionGroupID string `json:"discussion_group_id"`
	Content           string `json:"content"`
	Summary           string `json:"summary"`
	InboxItemID       string `json:"inbox_item_id"`
}

type closeInboxRequest struct {
	ReplyMessageID string `json:"reply_message_id"`
}

type approvalDecisionRequest struct {
	ActionID       string            `json:"action_id"`
	Message        string            `json:"message"`
	ModifiedInput  string            `json:"modified_input"`
	ChoiceID       string            `json:"choice_id"`
	Remember       bool              `json:"remember"`
	Metadata       map[string]string `json:"metadata"`
	TaskID         string            `json:"task_id"`
	ReplyMessageID string            `json:"reply_message_id"`
	PlanDigest     string            `json:"plan_digest"`
}

type forbiddenRequestFieldError struct {
	field string
}

func (e forbiddenRequestFieldError) Error() string {
	return "forbidden request field: " + e.field
}

type submitResultRequest struct {
	AgentResultID string                      `json:"agent_result_id"`
	AgentID       string                      `json:"agent_id"`
	RunID         string                      `json:"run_id"`
	Summary       string                      `json:"summary"`
	CreatedAt     time.Time                   `json:"created_at"`
	Deliverables  []deliverableRequest        `json:"deliverables"`
	Evidence      []evidenceSubmissionRequest `json:"evidence"`
	InternalTrace string                      `json:"internal_trace_ref"`
}

type deliverableRequest struct {
	ID                    string          `json:"id"`
	ExpectedDeliverableID string          `json:"expected_deliverable_id"`
	Title                 string          `json:"title"`
	Artifact              artifactRequest `json:"artifact"`
}

type evidenceSubmissionRequest struct {
	ID                    string          `json:"id"`
	EvidenceRequirementID string          `json:"evidence_requirement_id"`
	AcceptanceCriterionID string          `json:"acceptance_criterion_id"`
	Kind                  string          `json:"kind"`
	Artifact              artifactRequest `json:"artifact"`
	ToolLogID             string          `json:"tool_log_id"`
	ExternalURL           string          `json:"external_url"`
}

type artifactRequest struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	URI  string `json:"uri"`
}

func mapDeliverables(items []deliverableRequest) []agentcore.Deliverable {
	out := make([]agentcore.Deliverable, 0, len(items))
	for _, item := range items {
		out = append(out, agentcore.Deliverable{
			ID:                    agentcore.DeliverableID(item.ID),
			ExpectedDeliverableID: item.ExpectedDeliverableID,
			Title:                 item.Title,
			ArtifactRef:           mapArtifact(item.Artifact),
		})
	}
	return out
}

func mapEvidence(items []evidenceSubmissionRequest) []agentcore.Evidence {
	out := make([]agentcore.Evidence, 0, len(items))
	for _, item := range items {
		out = append(out, agentcore.Evidence{
			ID:                    agentcore.EvidenceID(item.ID),
			EvidenceRequirementID: item.EvidenceRequirementID,
			AcceptanceCriterionID: item.AcceptanceCriterionID,
			Kind:                  agentcore.EvidenceKind(item.Kind),
			ArtifactRef:           mapArtifact(item.Artifact),
			ToolLogRef:            agentcore.ToolLogRef{ID: agentcore.ToolLogID(item.ToolLogID)},
			ExternalURL:           item.ExternalURL,
		})
	}
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func mapArtifact(item artifactRequest) agentcore.ArtifactRef {
	return agentcore.ArtifactRef{ID: item.ID, Kind: agentcore.ArtifactKind(item.Kind), URI: item.URI}
}
