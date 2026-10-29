package authorization

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/identity"
)

func TestRelationProjectorWritesMissionProjectAndResourceTuples(t *testing.T) {
	sink := &recordingRelationSink{}
	projector := NewRelationProjector(sink)
	projector.now = func() time.Time { return time.Unix(10, 0).UTC() }

	if err := projector.WriteMissionCreated(context.Background(), SubjectRef{Kind: "user", ID: "planner"}, "mission-1", "", []SubjectRef{{Kind: "agent", ID: "agent-1"}}); err != nil {
		t.Fatalf("WriteMissionCreated: %v", err)
	}
	if err := projector.WriteProjectCreated(context.Background(), SubjectRef{Kind: "user", ID: "planner"}, "project-1", "org-1", "org-1"); err != nil {
		t.Fatalf("WriteProjectCreated: %v", err)
	}
	if err := projector.WriteResourceBinding(context.Background(), "agent-1", "tool.deploy", ""); err != nil {
		t.Fatalf("WriteResourceBinding: %v", err)
	}
	if len(sink.written) != 5 {
		t.Fatalf("written tuples = %+v", sink.written)
	}
	assertTuple(t, sink.written[0], "user", "planner", "owner", "mission", "mission-1", "global")
	assertTuple(t, sink.written[1], "agent", "agent-1", "participant", "mission", "mission-1", "global")
	assertTuple(t, sink.written[2], "user", "planner", "owner", "project", "project-1", "org-1")
	assertTuple(t, sink.written[3], "organization", "org-1", "org", "project", "project-1", "org-1")
	assertTuple(t, sink.written[4], "agent", "agent-1", "operator", "resource", "tool.deploy", "global")
}

func TestResolveResourceBindingSubject(t *testing.T) {
	tests := []struct {
		name       string
		profileID  string
		instanceID string
		human      bool
		want       SubjectRef
	}{
		{name: "ai profile", profileID: "agent-reviewer", want: SubjectRef{Kind: "agent", ID: "agent-reviewer"}},
		{name: "human profile", profileID: "human-reviewer", human: true, want: SubjectRef{Kind: "user", ID: "human-reviewer"}},
		{name: "instance fallback", instanceID: "agent-instance", want: SubjectRef{Kind: "agent", ID: "agent-instance"}},
		{name: "deferred human profile", profileID: deferredHumanResourceBindingProfileID, human: true, want: SubjectRef{Kind: "agent", ID: deferredHumanResourceBindingProfileID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveResourceBindingSubject(tt.profileID, tt.instanceID, tt.human); got != tt.want {
				t.Fatalf("ResolveResourceBindingSubject() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRelationProjectorWritesMissionAutomationTuple(t *testing.T) {
	sink := &recordingRelationSink{}
	projector := NewRelationProjector(sink)

	if err := projector.WriteMissionAutomation(context.Background(), SubjectRef{Kind: "system", ID: "responsibility-coordinator"}, "mission-1", "tenant-1"); err != nil {
		t.Fatalf("WriteMissionAutomation: %v", err)
	}
	if len(sink.written) != 1 {
		t.Fatalf("written tuples = %+v", sink.written)
	}
	assertTuple(t, sink.written[0], "system", "responsibility-coordinator", "automation", "mission", "mission-1", "tenant-1")
}

func TestRelationProjectorDeletesMissionOwnerTuple(t *testing.T) {
	sink := &recordingRelationSink{}
	projector := NewRelationProjector(sink)

	if err := projector.DeleteMissionOwner(context.Background(), SubjectRef{Kind: "agent", ID: "lead-agent"}, "mission-1", "tenant-1"); err != nil {
		t.Fatalf("DeleteMissionOwner: %v", err)
	}
	if len(sink.deleted) != 1 {
		t.Fatalf("deleted tuples = %+v", sink.deleted)
	}
	assertTuple(t, sink.deleted[0], "agent", "lead-agent", "owner", "mission", "mission-1", "tenant-1")
}

func TestRelationProjectorDeletesResourceBinding(t *testing.T) {
	sink := &recordingRelationSink{}
	projector := NewRelationProjector(sink)

	if err := projector.DeleteResourceBinding(context.Background(), "agent-1", "tool.deploy", ""); err != nil {
		t.Fatalf("DeleteResourceBinding: %v", err)
	}

	if len(sink.deleted) != 1 {
		t.Fatalf("deleted tuples = %+v", sink.deleted)
	}
	assertTuple(t, sink.deleted[0], "agent", "agent-1", "operator", "resource", "tool.deploy", "global")
}

func TestRelationProjectorDeletesMissionRelations(t *testing.T) {
	sink := &recordingRelationSink{}
	projector := NewRelationProjector(sink)

	err := projector.DeleteMissionRelations(context.Background(), SubjectRef{Kind: "user", ID: "planner"}, "mission-1", "", []SubjectRef{
		{Kind: "agent", ID: "agent-1"},
		{Kind: "agent", ID: "agent-1"},
	})
	if err != nil {
		t.Fatalf("DeleteMissionRelations: %v", err)
	}

	if len(sink.deleted) != 2 {
		t.Fatalf("deleted tuples = %+v", sink.deleted)
	}
	assertTuple(t, sink.deleted[0], "user", "planner", "owner", "mission", "mission-1", "global")
	assertTuple(t, sink.deleted[1], "agent", "agent-1", "participant", "mission", "mission-1", "global")
}

func TestRelationProjectorWritesAndDeletesDiscussionRelations(t *testing.T) {
	sink := &recordingRelationSink{}
	projector := NewRelationProjector(sink)

	err := projector.WriteDiscussionCreated(context.Background(), SubjectRef{Kind: "user", ID: "planner"}, "discussion-1", "mission-1", "", []SubjectRef{
		{Kind: "agent", ID: "agent-1"},
		{Kind: "agent", ID: "agent-1"},
	})
	if err != nil {
		t.Fatalf("WriteDiscussionCreated: %v", err)
	}
	if len(sink.written) != 3 {
		t.Fatalf("written tuples = %+v", sink.written)
	}
	assertTuple(t, sink.written[0], "mission", "mission-1", "mission", "discussion", "discussion-1", "global")
	assertTuple(t, sink.written[1], "user", "planner", "owner", "discussion", "discussion-1", "global")
	assertTuple(t, sink.written[2], "agent", "agent-1", "member", "discussion", "discussion-1", "global")

	err = projector.DeleteDiscussionRelations(context.Background(), SubjectRef{Kind: "user", ID: "planner"}, "discussion-1", "mission-1", "", []SubjectRef{{Kind: "agent", ID: "agent-1"}})
	if err != nil {
		t.Fatalf("DeleteDiscussionRelations: %v", err)
	}
	if len(sink.deleted) != 3 {
		t.Fatalf("deleted tuples = %+v", sink.deleted)
	}
	assertTuple(t, sink.deleted[0], "mission", "mission-1", "mission", "discussion", "discussion-1", "global")
	assertTuple(t, sink.deleted[1], "user", "planner", "owner", "discussion", "discussion-1", "global")
	assertTuple(t, sink.deleted[2], "agent", "agent-1", "member", "discussion", "discussion-1", "global")
}

func TestRelationProjectorSupportsStandaloneDiscussion(t *testing.T) {
	sink := &recordingRelationSink{}
	projector := NewRelationProjector(sink)
	owner := SubjectRef{Kind: "user", ID: "human-ding"}
	members := []SubjectRef{
		{Kind: "user", ID: "human-ding"},
		{Kind: "agent", ID: "bianbian"},
	}

	if err := projector.WriteDiscussionCreated(context.Background(), owner, "discussion-direct", "", "", members); err != nil {
		t.Fatalf("WriteDiscussionCreated: %v", err)
	}
	if len(sink.written) != 3 {
		t.Fatalf("standalone discussion tuples = %+v", sink.written)
	}
	for _, tuple := range sink.written {
		if tuple.Relation == "mission" {
			t.Fatalf("standalone discussion contains a Mission tuple: %+v", tuple)
		}
	}
	assertTuple(t, sink.written[0], "user", "human-ding", "owner", "discussion", "discussion-direct", "global")
	assertTuple(t, sink.written[1], "user", "human-ding", "member", "discussion", "discussion-direct", "global")
	assertTuple(t, sink.written[2], "agent", "bianbian", "member", "discussion", "discussion-direct", "global")

	if err := projector.DeleteDiscussionRelations(context.Background(), owner, "discussion-direct", "", "", members); err != nil {
		t.Fatalf("DeleteDiscussionRelations: %v", err)
	}
	if len(sink.deleted) != 3 {
		t.Fatalf("deleted standalone discussion tuples = %+v", sink.deleted)
	}
	for _, tuple := range sink.deleted {
		if tuple.Relation == "mission" {
			t.Fatalf("standalone discussion deletion contains a Mission tuple: %+v", tuple)
		}
	}
}

func TestRelationProjectorDeletesDiscussionMember(t *testing.T) {
	sink := &recordingRelationSink{}
	projector := NewRelationProjector(sink)

	if err := projector.DeleteDiscussionMember(context.Background(), SubjectRef{Kind: "user", ID: "reviewer-old"}, "discussion-1", "tenant-a"); err != nil {
		t.Fatalf("DeleteDiscussionMember: %v", err)
	}
	if len(sink.deleted) != 1 {
		t.Fatalf("deleted tuples = %+v", sink.deleted)
	}
	assertTuple(t, sink.deleted[0], "user", "reviewer-old", "member", "discussion", "discussion-1", "tenant-a")
}

func TestRelationProjectorBackfillSupportsStandaloneDiscussion(t *testing.T) {
	projector := NewRelationProjector(&recordingRelationSink{})
	plan := projector.PlanBackfill(RelationBackfillSnapshot{
		Discussions: []DiscussionRelationSnapshot{{
			DiscussionID: "discussion-direct",
			Owner:        SubjectRef{Kind: "user", ID: "human-ding"},
			Members: []SubjectRef{
				{Kind: "user", ID: "human-ding"},
				{Kind: "agent", ID: "bianbian"},
			},
		}},
	})

	if plan.Discussions != 1 || len(plan.Tuples) != 3 {
		t.Fatalf("standalone discussion backfill plan = %+v", plan)
	}
	for _, tuple := range plan.Tuples {
		if tuple.Relation == "mission" {
			t.Fatalf("standalone discussion backfill contains a Mission tuple: %+v", tuple)
		}
	}
}

func TestRelationProjectorOmitsInvalidSystemDiscussionOwner(t *testing.T) {
	sink := &recordingRelationSink{}
	projector := NewRelationProjector(sink)

	owner := SubjectRef{Kind: "system", ID: "scheduler-agent-task"}
	if err := projector.WriteDiscussionCreated(context.Background(), owner, "discussion-system", "mission-system", "tenant-a", []SubjectRef{{Kind: "agent", ID: "worker"}}); err != nil {
		t.Fatalf("WriteDiscussionCreated: %v", err)
	}
	if len(sink.written) != 2 || relationTupleExists(sink.written, "system", "scheduler-agent-task", "owner", "discussion", "discussion-system") {
		t.Fatalf("system discussion tuples = %+v", sink.written)
	}
	assertTuple(t, sink.written[0], "mission", "mission-system", "mission", "discussion", "discussion-system", "tenant-a")
	assertTuple(t, sink.written[1], "agent", "worker", "member", "discussion", "discussion-system", "tenant-a")

	if err := projector.DeleteDiscussionRelations(context.Background(), owner, "discussion-system", "mission-system", "tenant-a", []SubjectRef{{Kind: "agent", ID: "worker"}}); err != nil {
		t.Fatalf("DeleteDiscussionRelations: %v", err)
	}
	if len(sink.deleted) != 2 || relationTupleExists(sink.deleted, "system", "scheduler-agent-task", "owner", "discussion", "discussion-system") {
		t.Fatalf("deleted system discussion tuples = %+v", sink.deleted)
	}
}

func TestRelationProjectorWritesOrganizationDepartmentAndRoleRelations(t *testing.T) {
	sink := &recordingRelationSink{}
	projector := NewRelationProjector(sink)

	if err := projector.WriteOrganizationCreated(context.Background(), SubjectRef{Kind: "user", ID: "admin"}, "org-1", "org-1"); err != nil {
		t.Fatalf("WriteOrganizationCreated: %v", err)
	}
	if err := projector.WriteDepartmentCreated(context.Background(), "dept-1", "org-1", "org-1"); err != nil {
		t.Fatalf("WriteDepartmentCreated: %v", err)
	}
	if err := projector.WriteDepartmentManager(context.Background(), SubjectRef{Kind: "user", ID: "manager"}, "dept-1", "org-1"); err != nil {
		t.Fatalf("WriteDepartmentManager: %v", err)
	}
	if err := projector.WriteWorkforceMemberPlacement(context.Background(), SubjectRef{Kind: "agent", ID: "agent-1"}, "org-1", "dept-1", "org-1", []string{"org.manager", "org.manager"}); err != nil {
		t.Fatalf("WriteWorkforceMemberPlacement: %v", err)
	}

	if len(sink.written) != 9 {
		t.Fatalf("written tuples = %+v", sink.written)
	}
	assertTuple(t, sink.written[0], "user", "admin", "admin", "organization", "org-1", "org-1")
	assertTuple(t, sink.written[1], "organization", "org-1", "org", "department", "dept-1", "org-1")
	assertTuple(t, sink.written[2], "user", "manager", "manager", "department", "dept-1", "org-1")
	assertTuple(t, sink.written[3], "agent", "agent-1", "member", "platform", "global", "org-1")
	assertTuple(t, sink.written[4], "agent", "agent-1", "member", "organization", "org-1", "org-1")
	assertTuple(t, sink.written[5], "agent", "agent-1", "member", "department", "dept-1", "org-1")
	roleObjectID := "org-1/org.manager/profile-role:agent:agent-1:org.manager:dept-1"
	assertTuple(t, sink.written[6], "department", "dept-1", "scope", "role", roleObjectID, "org-1")
	assertTuple(t, sink.written[7], "agent", "agent-1", "assignee", "role", roleObjectID, "org-1")
	assertTuple(t, sink.written[8], "organization", "org-1", "org", "role", roleObjectID, "org-1")

	if err := projector.DeleteWorkforceMemberPlacement(context.Background(), SubjectRef{Kind: "agent", ID: "agent-1"}, "org-1", "dept-1", "org-1", []string{"org.manager"}); err != nil {
		t.Fatalf("DeleteWorkforceMemberPlacement: %v", err)
	}
	if len(sink.deleted) != 6 {
		t.Fatalf("deleted tuples = %+v", sink.deleted)
	}
	assertTuple(t, sink.deleted[0], "agent", "agent-1", "member", "platform", "global", "org-1")
	assertTuple(t, sink.deleted[1], "agent", "agent-1", "member", "organization", "org-1", "org-1")
	assertTuple(t, sink.deleted[2], "agent", "agent-1", "member", "department", "dept-1", "org-1")
	assertTuple(t, sink.deleted[3], "department", "dept-1", "scope", "role", roleObjectID, "org-1")
	assertTuple(t, sink.deleted[4], "agent", "agent-1", "assignee", "role", roleObjectID, "org-1")
	assertTuple(t, sink.deleted[5], "organization", "org-1", "org", "role", roleObjectID, "org-1")
}

func TestPlatformRoleAssignmentDerivesGlobalPlatformMembership(t *testing.T) {
	sink := &recordingRelationSink{}
	projector := NewRelationProjector(sink)
	assignment := RoleAssignmentRelationSnapshot{
		AssignmentID: "platform-admin-1", Assignee: SubjectRef{Kind: "user", ID: "admin"},
		RoleID: "platform.admin", OrganizationID: "global", ScopeMode: "platform", TenantID: "global",
	}

	if err := projector.WriteRoleAssignment(context.Background(), assignment); err != nil {
		t.Fatalf("WriteRoleAssignment: %v", err)
	}
	if len(sink.written) != 3 {
		t.Fatalf("platform role tuples = %+v", sink.written)
	}
	roleObjectID := "global/platform.admin/platform-admin-1"
	assertTuple(t, sink.written[0], "platform", "global", "scope", "role", roleObjectID, "global")
	assertTuple(t, sink.written[1], "user", "admin", "assignee", "role", roleObjectID, "global")
	assertTuple(t, sink.written[2], "role", roleObjectID, "assigned_role", "platform", "global", "global")

	if err := projector.DeleteRoleAssignment(context.Background(), assignment); err != nil {
		t.Fatalf("DeleteRoleAssignment: %v", err)
	}
	if len(sink.deleted) != 3 {
		t.Fatalf("deleted platform role tuples = %+v", sink.deleted)
	}
}

func TestRelationProjectorBestEffortIgnoresSinkErrors(t *testing.T) {
	errBoom := errors.New("openfga down")
	projector := NewRelationProjector(&recordingRelationSink{writeErr: errBoom}).WithBestEffort()

	err := projector.WriteProjectCreated(context.Background(), SubjectRef{Kind: "user", ID: "planner"}, "project-1", "", "")

	if err != nil {
		t.Fatalf("best effort should ignore write error: %v", err)
	}
}

func TestRelationProjectorBackfillDeduplicatesTuples(t *testing.T) {
	sink := &recordingRelationSink{}
	projector := NewRelationProjector(sink)
	result, err := projector.Backfill(context.Background(), RelationBackfillSnapshot{
		Projects: []ProjectRelationSnapshot{{ProjectID: "project-1", Owner: SubjectRef{Kind: "user", ID: "planner"}}},
		Missions: []MissionRelationSnapshot{{
			MissionID:    "mission-1",
			Owner:        SubjectRef{Kind: "user", ID: "planner"},
			Owners:       []SubjectRef{{Kind: "agent", ID: "lead-agent"}, {Kind: "user", ID: "planner"}},
			Participants: []SubjectRef{{Kind: "agent", ID: "agent-1"}, {Kind: "agent", ID: "agent-1"}},
		}},
		Discussions: []DiscussionRelationSnapshot{{
			DiscussionID: "discussion-1",
			MissionID:    "mission-1",
			Owner:        SubjectRef{Kind: "user", ID: "planner"},
			Members:      []SubjectRef{{Kind: "agent", ID: "agent-1"}},
		}},
		Todos: []TodoRelationSnapshot{{
			TodoID: "todo-1", TenantID: "org-1",
			Proposer: SubjectRef{Kind: "user", ID: "planner"},
			Assignee: SubjectRef{Kind: "agent", ID: "agent-1"},
			Reviewer: SubjectRef{Kind: "user", ID: "reviewer"},
		}},
		Organizations: []OrganizationRelationSnapshot{{OrganizationID: "org-1", TenantID: "org-1", Admin: SubjectRef{Kind: "user", ID: "admin"}}},
		Departments: []DepartmentRelationSnapshot{{
			DepartmentID:   "dept-1",
			OrganizationID: "org-1",
			TenantID:       "org-1",
			Managers:       []SubjectRef{{Kind: "user", ID: "manager"}},
		}},
		WorkforceMembers: []WorkforceMemberRelationSnapshot{{
			Subject:        SubjectRef{Kind: "agent", ID: "agent-1"},
			OrganizationID: "org-1",
			DepartmentID:   "dept-1",
			TenantID:       "org-1",
			RoleIDs:        []string{"org.manager", "org.manager"},
		}},
		RoleAssignments: []RoleAssignmentRelationSnapshot{{
			Assignee:       SubjectRef{Kind: "user", ID: "reviewer"},
			RoleID:         "approval.reviewer",
			OrganizationID: "org-1",
			TenantID:       "org-1",
		}},
		ResourceBindings: []ResourceBindingRelationSnapshot{{ResourceID: "tool.deploy", Subject: SubjectRef{Kind: "agent", ID: "agent-1"}}},
	})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if result.Projects != 1 || result.Missions != 1 || result.Discussions != 1 || result.Todos != 1 || result.Organizations != 1 || result.Departments != 1 || result.WorkforceMembers != 1 || result.RoleAssignments != 1 || result.ResourceBindings != 1 || result.Tuples != 23 {
		t.Fatalf("result = %+v", result)
	}
	if result.Report.ByObjectType["mission"] != 3 || result.Report.ByObjectType["discussion"] != 3 || result.Report.ByObjectType["role"] != 6 || result.Report.ByObjectType["platform"] != 1 || result.Report.ByRelation["owner"] != 4 || result.Report.BySubjectKind["agent"] != 9 {
		t.Fatalf("report = %+v", result.Report)
	}
	if len(sink.written) != 23 {
		t.Fatalf("written = %+v", sink.written)
	}
}

func TestRelationProjectorBackfillDoesNotCreateOwnerForTodoOriginMission(t *testing.T) {
	sink := &recordingRelationSink{}
	projector := NewRelationProjector(sink)
	_, err := projector.Backfill(context.Background(), RelationBackfillSnapshot{Missions: []MissionRelationSnapshot{{
		MissionID: "mission-todo", TenantID: "tenant-a", Owner: SubjectRef{Kind: "system", ID: "todo-automation-agent"},
		Participants: []SubjectRef{{Kind: "agent", ID: "agent-1"}}, Automation: []SubjectRef{{Kind: "system", ID: "responsibility-coordinator"}}, OriginTodoID: "todo-1",
	}}})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	for _, tuple := range sink.written {
		if tuple.Object.Type == "mission" && tuple.Object.ID == "mission-todo" && tuple.Relation == "owner" {
			t.Fatalf("Todo-origin backfill wrote owner: %+v", tuple)
		}
	}
	if !relationTupleExists(sink.written, "agent", "agent-1", "participant", "mission", "mission-todo") ||
		!relationTupleExists(sink.written, "system", "responsibility-coordinator", "automation", "mission", "mission-todo") ||
		!relationTupleExists(sink.written, "todo", "todo-1", "origin_todo", "mission", "mission-todo") {
		t.Fatalf("Todo-origin backfill tuples = %+v", sink.written)
	}
}

func TestRelationProjectorBackfillProjectsSystemCreatorAsAutomation(t *testing.T) {
	sink := &recordingRelationSink{}
	projector := NewRelationProjector(sink)
	_, err := projector.Backfill(context.Background(), RelationBackfillSnapshot{
		Missions: []MissionRelationSnapshot{{
			MissionID: "mission-scheduled", TenantID: "tenant-a", Owner: SubjectRef{Kind: "system", ID: "scheduler-agent-task"},
			Participants: []SubjectRef{{Kind: "agent", ID: "worker"}},
		}},
		Discussions: []DiscussionRelationSnapshot{{
			DiscussionID: "discussion-scheduled", MissionID: "mission-scheduled", TenantID: "tenant-a",
			Owner: SubjectRef{Kind: "system", ID: "scheduler-agent-task"}, Members: []SubjectRef{{Kind: "agent", ID: "worker"}},
		}},
	})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if !relationTupleExists(sink.written, "system", "scheduler-agent-task", "automation", "mission", "mission-scheduled") {
		t.Fatalf("missing scheduler automation relation: %+v", sink.written)
	}
	for _, tuple := range sink.written {
		if tuple.Subject.Kind == "system" && tuple.Relation == "owner" {
			t.Fatalf("backfill emitted invalid system owner: %+v", tuple)
		}
	}
}

func relationTupleExists(tuples []RelationTuple, subjectKind, subjectID, relation, objectType, objectID string) bool {
	for _, tuple := range tuples {
		if tuple.Subject.Kind == subjectKind && tuple.Subject.ID == subjectID && tuple.Relation == relation && tuple.Object.Type == objectType && tuple.Object.ID == objectID {
			return true
		}
	}
	return false
}

func TestRelationProjectorBackfillDryRunDoesNotWrite(t *testing.T) {
	sink := &recordingRelationSink{}
	projector := NewRelationProjector(sink)

	result, err := projector.BackfillWithOptions(context.Background(), RelationBackfillSnapshot{
		Projects: []ProjectRelationSnapshot{{ProjectID: "project-1", Owner: SubjectRef{Kind: "user", ID: "planner"}}},
	}, RelationBackfillOptions{DryRun: true})
	if err != nil {
		t.Fatalf("BackfillWithOptions dry-run: %v", err)
	}
	if !result.DryRun || result.Tuples != 1 || result.Projects != 1 {
		t.Fatalf("result = %+v", result)
	}
	if result.Report.ByObjectType["project"] != 1 || result.Report.ByTenant["global"] != 1 {
		t.Fatalf("dry-run report = %+v", result.Report)
	}
	if len(sink.written) != 0 {
		t.Fatalf("dry-run should not write tuples: %+v", sink.written)
	}
	plan := projector.PlanBackfill(RelationBackfillSnapshot{
		Projects: []ProjectRelationSnapshot{{ProjectID: "project-1", Owner: SubjectRef{Kind: "user", ID: "planner"}}},
	})
	if len(plan.Tuples) != 1 || plan.Result().Tuples != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	if report := plan.Report(); report.ByRelation["owner"] != 1 {
		t.Fatalf("plan report = %+v", report)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = projector.BackfillWithOptions(canceled, RelationBackfillSnapshot{
		Projects: []ProjectRelationSnapshot{{ProjectID: "project-1", Owner: SubjectRef{Kind: "user", ID: "planner"}}},
	}, RelationBackfillOptions{DryRun: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("dry-run canceled error = %v, want context.Canceled", err)
	}
}

func TestRelationProjectorBackfillDryRunDiffComparesDirectTuples(t *testing.T) {
	present := RelationTuple{TenantID: "global", Subject: SubjectRef{Kind: "user", ID: "planner"}, Relation: "owner", Object: identity.ObjectRef{Type: "project", ID: "project-present", TenantID: "global"}}
	sink := &recordingRelationSink{
		existing: []RelationTuple{present},
	}
	projector := NewRelationProjector(sink)

	result, err := projector.BackfillWithOptions(context.Background(), RelationBackfillSnapshot{
		Projects: []ProjectRelationSnapshot{
			{ProjectID: "project-present", Owner: SubjectRef{Kind: "user", ID: "planner"}},
			{ProjectID: "project-missing", Owner: SubjectRef{Kind: "user", ID: "planner"}},
		},
	}, RelationBackfillOptions{DryRun: true, Diff: true})
	if err != nil {
		t.Fatalf("BackfillWithOptions dry-run diff: %v", err)
	}
	if !result.DryRun || result.Tuples != 2 || result.Diff.Checked != 2 || result.Diff.Missing != 1 || result.Diff.Errors != 0 {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Diff.MissingTuples) != 1 || result.Diff.MissingTuples[0].Object.ID != "project-missing" {
		t.Fatalf("missing tuples = %+v", result.Diff.MissingTuples)
	}
	if len(sink.checked) != 0 {
		t.Fatalf("computed relation checks must not be used for tuple diff: %+v", sink.checked)
	}
	if len(sink.written) != 0 {
		t.Fatalf("dry-run diff should not write tuples: %+v", sink.written)
	}
}

func TestRelationProjectorBackfillWritesOnlyMissingTuples(t *testing.T) {
	desired := RelationTuple{TenantID: "global", Subject: SubjectRef{Kind: "user", ID: "planner"}, Relation: "owner", Object: identity.ObjectRef{Type: "project", ID: "project-present", TenantID: "global"}}
	sink := &recordingRelationSink{existing: []RelationTuple{desired}}
	projector := NewRelationProjector(sink)
	result, err := projector.BackfillWithOptions(context.Background(), RelationBackfillSnapshot{
		Projects: []ProjectRelationSnapshot{
			{ProjectID: "project-present", Owner: SubjectRef{Kind: "user", ID: "planner"}},
			{ProjectID: "project-missing", Owner: SubjectRef{Kind: "user", ID: "planner"}},
		},
	}, RelationBackfillOptions{Diff: true})
	if err != nil {
		t.Fatalf("BackfillWithOptions: %v", err)
	}
	if result.Diff.Missing != 0 || len(sink.written) != 1 || sink.written[0].Object.ID != "project-missing" {
		t.Fatalf("result=%+v written=%+v", result, sink.written)
	}
}

func TestRelationProjectorProjectsOnlyCurrentlyEffectiveRoleAssignments(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	sink := &recordingRelationSink{}
	projector := NewRelationProjector(sink)
	projector.now = func() time.Time { return now }

	for _, assignment := range []RoleAssignmentRelationSnapshot{
		{AssignmentID: "future", Assignee: SubjectRef{Kind: "user", ID: "future"}, RoleID: "org.admin", OrganizationID: "org-1", OrganizationNodeID: "dept-1", StartsAt: now.Add(time.Hour)},
		{AssignmentID: "expired", Assignee: SubjectRef{Kind: "user", ID: "expired"}, RoleID: "org.admin", OrganizationID: "org-1", OrganizationNodeID: "dept-1", ExpiresAt: now},
	} {
		if err := projector.WriteRoleAssignment(context.Background(), assignment); err != nil {
			t.Fatalf("WriteRoleAssignment(%s): %v", assignment.AssignmentID, err)
		}
	}
	if len(sink.written) != 0 {
		t.Fatalf("inactive role assignments were projected: %+v", sink.written)
	}
	active := RoleAssignmentRelationSnapshot{AssignmentID: "active", Assignee: SubjectRef{Kind: "user", ID: "active"}, RoleID: "org.admin", OrganizationID: "org-1", OrganizationNodeID: "dept-1", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}
	if err := projector.WriteRoleAssignment(context.Background(), active); err != nil {
		t.Fatalf("WriteRoleAssignment(active): %v", err)
	}
	if len(sink.written) != 3 {
		t.Fatalf("active assignment tuples = %+v", sink.written)
	}
	projector.now = func() time.Time { return now.Add(2 * time.Hour) }
	if err := projector.DeleteRoleAssignment(context.Background(), active); err != nil {
		t.Fatalf("DeleteRoleAssignment(expired): %v", err)
	}
	if len(sink.deleted) != 3 {
		t.Fatalf("expired assignment deletion tuples = %+v", sink.deleted)
	}
}

func TestRelationProjectorBackfillPrunesStaleManagedTuples(t *testing.T) {
	desired := RelationTuple{TenantID: "global", Subject: SubjectRef{Kind: "user", ID: "planner"}, Relation: "owner", Object: identity.ObjectRef{Type: "project", ID: "project-current", TenantID: "global"}}
	stale := RelationTuple{TenantID: "global", Subject: SubjectRef{Kind: "user", ID: "former-owner"}, Relation: "owner", Object: identity.ObjectRef{Type: "project", ID: "project-current", TenantID: "global"}}
	unmanaged := RelationTuple{TenantID: "global", Subject: SubjectRef{Kind: "user", ID: "planner"}, Relation: "custom", Object: identity.ObjectRef{Type: "project", ID: "project-current", TenantID: "global"}}
	sink := &recordingRelationSink{existing: []RelationTuple{desired, stale, unmanaged}}
	projector := NewRelationProjector(sink)
	result, err := projector.BackfillWithOptions(context.Background(), RelationBackfillSnapshot{
		Projects: []ProjectRelationSnapshot{{ProjectID: "project-current", Owner: SubjectRef{Kind: "user", ID: "planner"}}},
	}, RelationBackfillOptions{Diff: true, Prune: true})
	if err != nil {
		t.Fatalf("BackfillWithOptions: %v", err)
	}
	if result.Pruned != 1 || result.Diff.Missing != 0 || result.Diff.Extra != 0 || len(sink.deleted) != 1 {
		t.Fatalf("result=%+v deleted=%+v", result, sink.deleted)
	}
	if relationTupleComparisonKey(sink.deleted[0]) != relationTupleComparisonKey(stale) {
		t.Fatalf("deleted tuple = %+v, want stale tuple", sink.deleted[0])
	}
}

func assertTuple(t *testing.T, tuple RelationTuple, subjectKind string, subjectID string, relation string, objectType string, objectID string, tenantID string) {
	t.Helper()
	if tuple.Subject.Kind != subjectKind || tuple.Subject.ID != subjectID || tuple.Relation != relation || tuple.Object.Type != objectType || tuple.Object.ID != objectID || tuple.TenantID != tenantID {
		t.Fatalf("tuple = %+v", tuple)
	}
}

type recordingRelationSink struct {
	writeErr        error
	deleteErr       error
	relationAllowed map[string]bool
	relationErr     map[string]error
	written         []RelationTuple
	deleted         []RelationTuple
	checked         []RelationTuple
	existing        []RelationTuple
}

func (s *recordingRelationSink) WriteRelations(ctx context.Context, tuples []RelationTuple) error {
	_ = ctx
	if s.writeErr != nil {
		return s.writeErr
	}
	s.written = append(s.written, tuples...)
	for _, tuple := range tuples {
		key := relationTupleComparisonKey(tuple)
		found := false
		for _, existing := range s.existing {
			if relationTupleComparisonKey(existing) == key {
				found = true
				break
			}
		}
		if !found {
			s.existing = append(s.existing, tuple)
		}
	}
	return nil
}

func (s *recordingRelationSink) DeleteRelations(ctx context.Context, tuples []RelationTuple) error {
	_ = ctx
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deleted = append(s.deleted, tuples...)
	deleted := map[string]struct{}{}
	for _, tuple := range tuples {
		deleted[relationTupleComparisonKey(tuple)] = struct{}{}
	}
	remaining := s.existing[:0]
	for _, tuple := range s.existing {
		if _, ok := deleted[relationTupleComparisonKey(tuple)]; !ok {
			remaining = append(remaining, tuple)
		}
	}
	s.existing = remaining
	return nil
}

func (s *recordingRelationSink) CheckRelation(ctx context.Context, tuple RelationTuple) (bool, error) {
	_ = ctx
	s.checked = append(s.checked, tuple)
	key := relationSinkKey(tuple)
	if s.relationErr != nil && s.relationErr[key] != nil {
		return false, s.relationErr[key]
	}
	if s.relationAllowed != nil {
		return s.relationAllowed[key], nil
	}
	for _, existing := range s.existing {
		if relationTupleComparisonKey(existing) == relationTupleComparisonKey(tuple) {
			return true, nil
		}
	}
	return false, nil
}

func (s *recordingRelationSink) ListRelations(ctx context.Context) ([]RelationTuple, error) {
	_ = ctx
	return append([]RelationTuple(nil), s.existing...), nil
}

func relationSinkKey(tuple RelationTuple) string {
	tuple = normalizeRelationTuple(tuple)
	return tuple.Subject.Kind + ":" + tuple.Subject.ID + "#" + tuple.Relation + "@" + tuple.Object.Type + ":" + tuple.Object.ID + "#" + tuple.TenantID
}
