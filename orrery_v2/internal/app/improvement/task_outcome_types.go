package improvement

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

const (
	taskOutcomeTable        = "ontology_improvement_task_outcomes"
	businessBindingTable    = "ontology_improvement_business_bindings"
	businessPendingTable    = "ontology_improvement_business_pending"
	businessAssessmentTable = "ontology_improvement_business_assessments"
	businessOutboxTable     = "ontology_improvement_business_outbox"
	businessCursorTable     = "ontology_improvement_business_cursor"
)

// BusinessBinding is captured by the host before dispatch. Task text is hashed,
// not stored. It supplements, and never changes, historical request bytes.
type BusinessBinding struct {
	ID              string `json:"id"`
	TenantID        string `json:"tenant_id"`
	AttemptID       string `json:"attempt_id"`
	RequestID       string `json:"request_id"`
	MissionID       string `json:"mission_id"`
	TaskID          string `json:"task_id"`
	InputTaskSHA256 string `json:"input_task_sha256"`
	ContractSHA256  string `json:"contract_sha256,omitempty"`
	ContractVersion int64  `json:"contract_version,omitempty"`
	SourceKey       string `json:"source_key"`
}

// TaskOutcome is a committed domain receipt, not a quality or causal oracle.
// SourceSHA256 covers the original submission/check; raw content stays in its
// owning domain. Artifact bindings are references, not verified artifact bytes.
type TaskOutcome struct {
	ID                       string    `json:"id"`
	SourceKey                string    `json:"source_key"`
	ExecutionTenantID        string    `json:"execution_tenant_id,omitempty"`
	MissionID                string    `json:"mission_id"`
	TaskID                   string    `json:"task_id"`
	RunID                    string    `json:"run_id"`
	AgentID                  string    `json:"agent_id"`
	TraceRef                 string    `json:"trace_ref"`
	RuntimeAttemptID         string    `json:"runtime_attempt_id"`
	SubmissionID             string    `json:"submission_id"`
	ResultID                 string    `json:"result_id"`
	TaskVersion              int64     `json:"task_version"`
	ContractSHA256           string    `json:"contract_sha256"`
	SourceSHA256             string    `json:"source_sha256"`
	Stage                    string    `json:"stage"`
	Status                   string    `json:"task_status"`
	QARequired               bool      `json:"qa_required"`
	Blocked                  bool      `json:"blocked"`
	QAResultID               string    `json:"qa_result_id,omitempty"`
	QAStatus                 string    `json:"qa_status,omitempty"`
	AcceptanceReportID       string    `json:"acceptance_report_id,omitempty"`
	Deliverables             int       `json:"deliverables"`
	Evidence                 int       `json:"evidence"`
	SubmissionBindingsSHA256 string    `json:"submission_bindings_sha256"`
	OccurredAt               time.Time `json:"occurred_at"`
}

type BusinessAssessment struct {
	ID                string      `json:"id"`
	Protocol          string      `json:"protocol"`
	TenantID          string      `json:"tenant_id"`
	AttemptID         string      `json:"attempt_id"`
	CaseID            string      `json:"case_id"`
	RuntimeReviewID   string      `json:"runtime_review_id"`
	BindingID         string      `json:"binding_id"`
	Outcome           TaskOutcome `json:"outcome"`
	BusinessStatus    string      `json:"business_status"`
	CausalAttribution string      `json:"causal_attribution"`
	Blockers          []string    `json:"blockers"`
	AssessedAt        time.Time   `json:"assessed_at"`
}

func businessSourceKey(mission, task, run, trace, attempt string) string {
	return Digest([]string{mission, task, run, trace, attempt})
}

func buildBusinessBinding(request RuntimeRequestEvidence, task agentcore.TaskInput) (BusinessBinding, error) {
	b := BusinessBinding{TenantID: request.Scope.TenantID, AttemptID: request.AttemptID, RequestID: request.ID,
		MissionID: task.MissionID, TaskID: task.ID, InputTaskSHA256: Digest(task), SourceKey: businessSourceKey(task.MissionID, task.ID, request.RunID, request.TraceRef, request.AttemptID)}
	b.ID = "runtime-business-binding:" + Digest(b)
	if validateRuntimeEvidence(request) != nil || !textOK(task.ID, 256) || !textOK(task.MissionID, 256) {
		return b, ErrInvalid
	}
	return b, nil
}

func validateBusinessBinding(request RuntimeRequestEvidence, b BusinessBinding) error {
	copy := b
	copy.ID = ""
	if b.ID != "runtime-business-binding:"+Digest(copy) || b.RequestID != request.ID || b.TenantID != request.Scope.TenantID || b.AttemptID != request.AttemptID ||
		!textOK(b.TaskID, 256) || !textOK(b.MissionID, 256) || !hashOK(b.InputTaskSHA256) || (b.ContractSHA256 != "" && (!hashOK(b.ContractSHA256) || b.ContractVersion < 1)) || (b.ContractSHA256 == "" && b.ContractVersion != 0) || b.SourceKey != businessSourceKey(b.MissionID, b.TaskID, request.RunID, request.TraceRef, request.AttemptID) {
		return ErrInvalid
	}
	return nil
}
