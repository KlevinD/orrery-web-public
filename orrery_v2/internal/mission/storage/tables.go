package storage

import (
	"context"
	"fmt"

	"orrery_v2/internal/platform"
)

const (
	RoleCore = platform.DatabaseRoleCore

	TeamRecords                  = "mission_team_records"
	TeamAssignments              = "mission_team_assignments"
	TeamStaffingRequests         = "mission_team_staffing_requests"
	TeamStaffingDecisions        = "mission_team_staffing_decisions"
	TeamStaffingAudits           = "mission_team_staffing_audits"
	TaskRecords                  = "mission_task_records"
	TaskSubmissions              = "mission_task_submissions"
	TaskSubmissionIdempotency    = "mission_task_submission_idempotency"
	TaskDeliverableSubmissions   = "mission_task_deliverable_submissions"
	TaskEvidenceSubmissions      = "mission_task_evidence_submissions"
	TaskReworkRequests           = "mission_task_rework_requests"
	TaskQAResults                = "mission_task_qa_results"
	TaskQAIdempotency            = "mission_task_qa_idempotency"
	TaskAcceptanceReports        = "mission_task_acceptance_reports"
	DiscussionGroups             = "mission_discussion_groups"
	DiscussionMembers            = "mission_discussion_members"
	DiscussionMessages           = "mission_discussion_messages"
	DiscussionMessageIdempotency = "mission_discussion_message_idempotency"
	DiscussionInboxItems         = "mission_discussion_inbox_items"
)

var (
	TableTeamRecords                  = MustTable(TeamRecords)
	TableTeamAssignments              = MustTable(TeamAssignments)
	TableTeamStaffingRequests         = MustTable(TeamStaffingRequests)
	TableTeamStaffingDecisions        = MustTable(TeamStaffingDecisions)
	TableTeamStaffingAudits           = MustTable(TeamStaffingAudits)
	TableTaskRecords                  = MustTable(TaskRecords)
	TableTaskSubmissions              = MustTable(TaskSubmissions)
	TableTaskSubmissionIdempotency    = MustTable(TaskSubmissionIdempotency)
	TableTaskDeliverableSubmissions   = MustTable(TaskDeliverableSubmissions)
	TableTaskEvidenceSubmissions      = MustTable(TaskEvidenceSubmissions)
	TableTaskReworkRequests           = MustTable(TaskReworkRequests)
	TableTaskQAResults                = MustTable(TaskQAResults)
	TableTaskQAIdempotency            = MustTable(TaskQAIdempotency)
	TableTaskAcceptanceReports        = MustTable(TaskAcceptanceReports)
	TableDiscussionGroups             = MustTable(DiscussionGroups)
	TableDiscussionMembers            = MustTable(DiscussionMembers)
	TableDiscussionMessages           = MustTable(DiscussionMessages)
	TableDiscussionMessageIdempotency = MustTable(DiscussionMessageIdempotency)
	TableDiscussionInboxItems         = MustTable(DiscussionInboxItems)
)

type TableSpec struct {
	LogicalName string
	Role        string
	KeyFields   []string
	IndexFields []string
}

func CoreTables() []TableSpec {
	return []TableSpec{
		{LogicalName: TeamRecords, Role: RoleCore, KeyFields: []string{"mission_id"}, IndexFields: []string{"team_id", "version"}},
		{LogicalName: TeamAssignments, Role: RoleCore, KeyFields: []string{"assignment_id"}, IndexFields: []string{"mission_id", "agent_profile_id", "status", "idempotency_key"}},
		{LogicalName: TeamStaffingRequests, Role: RoleCore, KeyFields: []string{"request_id"}, IndexFields: []string{"mission_id", "status", "idempotency_key"}},
		{LogicalName: TeamStaffingDecisions, Role: RoleCore, KeyFields: []string{"decision_id"}, IndexFields: []string{"request_id", "audit_id", "idempotency_key"}},
		{LogicalName: TeamStaffingAudits, Role: RoleCore, KeyFields: []string{"audit_id"}, IndexFields: []string{"mission_id", "request_id", "decision_id", "created_at"}},
		{LogicalName: TaskRecords, Role: RoleCore, KeyFields: []string{"task_id"}, IndexFields: []string{"mission_id", "status", "version"}},
		{LogicalName: TaskSubmissions, Role: RoleCore, KeyFields: []string{"submission_id"}, IndexFields: []string{"task_id", "submitted_at", "idempotency_key"}},
		{LogicalName: TaskSubmissionIdempotency, Role: RoleCore, KeyFields: []string{"task_id", "idempotency_key"}, IndexFields: []string{"submission_id"}},
		{LogicalName: TaskDeliverableSubmissions, Role: RoleCore, KeyFields: []string{"deliverable_submission_id"}, IndexFields: []string{"task_id", "submission_id"}},
		{LogicalName: TaskEvidenceSubmissions, Role: RoleCore, KeyFields: []string{"evidence_submission_id"}, IndexFields: []string{"task_id", "submission_id"}},
		{LogicalName: TaskReworkRequests, Role: RoleCore, KeyFields: []string{"rework_id"}, IndexFields: []string{"task_id", "status", "created_at"}},
		{LogicalName: TaskQAResults, Role: RoleCore, KeyFields: []string{"qa_result_id"}, IndexFields: []string{"task_id", "status", "idempotency_key", "checked_at"}},
		{LogicalName: TaskQAIdempotency, Role: RoleCore, KeyFields: []string{"task_id", "idempotency_key"}, IndexFields: []string{"qa_result_id"}},
		{LogicalName: TaskAcceptanceReports, Role: RoleCore, KeyFields: []string{"report_id"}, IndexFields: []string{"task_id", "submission_id", "qa_result_id", "created_at"}},
		{LogicalName: DiscussionGroups, Role: RoleCore, KeyFields: []string{"group_id"}, IndexFields: []string{"mission_id", "status"}},
		{LogicalName: DiscussionMembers, Role: RoleCore, KeyFields: []string{"group_id", "agent_instance_id"}, IndexFields: []string{"agent_instance_id"}},
		{LogicalName: DiscussionMessages, Role: RoleCore, KeyFields: []string{"message_id"}, IndexFields: []string{"group_id", "thread_id", "created_at"}},
		{LogicalName: DiscussionMessageIdempotency, Role: RoleCore, KeyFields: []string{"tenant_id", "idempotency_key"}, IndexFields: []string{"message_id"}},
		{LogicalName: DiscussionInboxItems, Role: RoleCore, KeyFields: []string{"inbox_item_id"}, IndexFields: []string{"mission_id", "group_id", "message_id", "recipient", "status", "created_at"}},
	}
}

func RegisterMissionStorage(ctx context.Context, registrar *platform.ModuleRegistrar) error {
	if registrar == nil {
		return fmt.Errorf("mission storage registrar is required")
	}
	if err := registrar.RegisterInitializer("mission.storage.core_tables", func(ctx context.Context, init platform.InitializerContext) error {
		router := init.DBRouter()
		if router == nil {
			return fmt.Errorf("mission storage initializer requires db router")
		}
		for _, table := range CoreTables() {
			store, _, err := router.StoreForTable(table.Role + "." + table.LogicalName)
			if err != nil {
				return err
			}
			if err := store.Ping(ctx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}
