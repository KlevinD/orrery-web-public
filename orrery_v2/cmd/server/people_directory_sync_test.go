package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/peopledirectory"
)

func TestPeopleDirectoryCLIRejectsSyncAndApplyTogether(t *testing.T) {
	err := run(context.Background(), []string{
		"-people-directory-sync-dry-run",
		"-people-directory-apply-snapshot=snapshot-1",
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("run error = %v", err)
	}
}

func TestFeishuDirectoryApplyUsesGovernedWorkforceCommands(t *testing.T) {
	ctx := context.Background()
	store := &fakePeopleDirectoryApplyStore{
		snapshot: peopledirectory.Snapshot{ID: "snapshot-1", Source: peopledirectory.SourceFeishu, TenantID: "tenant-1", Complete: true, Status: peopledirectory.SnapshotStatusComplete},
		users: []peopledirectory.DirectoryUser{
			{SourceRecordID: "ou_new", EmployeeNumber: "E-100", Name: "高闯", EnterpriseEmail: "gaochuang@example.com", Active: true},
			{SourceRecordID: "ou_existing", EmployeeNumber: "E-001", Name: "屈正", EnterpriseEmail: "quzheng@example.com", Active: true},
		},
		matches: []peopledirectory.MatchResult{
			{SourceRecordID: "ou_new", Action: peopledirectory.MatchActionCreate, MatchKind: peopledirectory.MatchByNewPerson, ApplyStatus: peopledirectory.ApplyStatusPending},
			{SourceRecordID: "ou_existing", Action: peopledirectory.MatchActionUpdate, MatchKind: peopledirectory.MatchByEmployeeNumber, ProposedPersonID: "human-quzheng", ApplyStatus: peopledirectory.ApplyStatusPending},
			{SourceRecordID: "ou_conflict", Action: peopledirectory.MatchActionConflict, MatchKind: peopledirectory.MatchByConflict, ConflictReason: "contradictory identifiers", ApplyStatus: peopledirectory.ApplyStatusPendingReview},
			{SourceRecordID: "ou_missing", Action: peopledirectory.MatchActionDisable, MatchKind: peopledirectory.MatchByMissingFromSnapshot, ProposedPersonID: "human-lipi", ApplyStatus: peopledirectory.ApplyStatusPending},
		},
	}
	workforce := &fakePeopleDirectoryWorkforce{nextID: "human-generated"}
	applier := newPeopleDirectorySnapshotApplier(store, workforce, peopleDirectoryPlacement{
		OrganizationID: "org-1", DepartmentID: "dept-1", PositionID: "position-1",
	})

	report, err := applier.ApplySnapshot(ctx, "snapshot-1")
	if err != nil {
		t.Fatalf("ApplySnapshot: %v", err)
	}
	if report.Created != 1 || report.Updated != 1 || report.Disabled != 1 || report.Conflicts != 1 {
		t.Fatalf("apply report = %+v", report)
	}
	if report.SnapshotID != "snapshot-1" || len(report.InitialCredentials) != 1 || report.InitialCredentials[0].Password != "initial-password-once" {
		t.Fatalf("apply credentials = %+v", report)
	}
	if len(workforce.upserts) != 2 {
		t.Fatalf("upsert calls = %+v", workforce.upserts)
	}
	created := workforce.upserts[0]
	if created.command.AgentProfileID != "E-100" || created.command.PersonSource.Source != peopledirectory.SourceFeishu || created.command.PersonSource.SourceRecordID != "ou_new" || created.command.PersonSource.SourceTenantID != "tenant-1" {
		t.Fatalf("create command = %+v", created.command)
	}
	if !reflect.DeepEqual(created.command.RoleIDs, []string{operations.RoleIDBasicUser, "member"}) {
		t.Fatalf("baseline roles = %+v", created.command.RoleIDs)
	}
	if created.command.OrganizationID != "org-1" || created.command.DepartmentID != "dept-1" || created.command.PositionID != "position-1" || !created.command.UserAccount.AccountEnabled || created.command.UserAccount.Password != "" {
		t.Fatalf("create placement/account = %+v", created.command)
	}
	updated := workforce.upserts[1]
	if updated.command.AgentProfileID != "human-quzheng" || len(updated.command.RoleIDs) != 0 || !reflect.DeepEqual(updated.command.UpdateMask, []string{"display_name", "real_name", "email", "person_source", "enabled", "organization_id", "department_id", "position_id"}) {
		t.Fatalf("update command = %+v", updated.command)
	}
	if updated.command.OrganizationID != "org-1" || updated.command.DepartmentID != "dept-1" || updated.command.PositionID != "position-1" {
		t.Fatalf("update placement = %+v", updated.command)
	}
	if len(workforce.enabled) != 1 || workforce.enabled[0].command.AgentProfileID != "human-lipi" || workforce.enabled[0].command.Enabled {
		t.Fatalf("enabled calls = %+v", workforce.enabled)
	}
	wantScopes := []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}
	for _, call := range append(workforce.upserts, workforce.enabled...) {
		if call.viewer.ViewerKind != "system" || !reflect.DeepEqual(call.viewer.PermissionScope, wantScopes) {
			t.Fatalf("system viewer = %+v", call.viewer)
		}
	}
	if !reflect.DeepEqual(store.reserved, []reservedDirectoryPerson{{snapshotID: "snapshot-1", sourceRecordID: "ou_new", personID: "E-100"}}) {
		t.Fatalf("reserved ids = %+v", store.reserved)
	}
	if len(store.applied) != 3 {
		t.Fatalf("applied results = %+v", store.applied)
	}
}

func TestFeishuDirectoryApplyRejectsPartialSnapshotBeforeDisabling(t *testing.T) {
	store := &fakePeopleDirectoryApplyStore{
		snapshot: peopledirectory.Snapshot{ID: "snapshot-partial", Source: peopledirectory.SourceFeishu, TenantID: "tenant-1", Status: peopledirectory.SnapshotStatusScanning},
		matches:  []peopledirectory.MatchResult{{SourceRecordID: "ou_missing", Action: peopledirectory.MatchActionDisable, ProposedPersonID: "human-lipi"}},
	}
	workforce := &fakePeopleDirectoryWorkforce{}

	_, err := newPeopleDirectorySnapshotApplier(store, workforce, peopleDirectoryPlacement{}).ApplySnapshot(context.Background(), "snapshot-partial")
	if !errors.Is(err, peopledirectory.ErrSnapshotIncomplete) {
		t.Fatalf("ApplySnapshot error = %v, want ErrSnapshotIncomplete", err)
	}
	if len(workforce.upserts) != 0 || len(workforce.enabled) != 0 {
		t.Fatalf("partial snapshot mutated workforce: upserts=%+v enabled=%+v", workforce.upserts, workforce.enabled)
	}
}

func TestPeopleDirectoryApplyReportPreservesCredentialsOnLaterFailure(t *testing.T) {
	report := peopleDirectoryApplyReport{
		SnapshotID: "snapshot-1",
		Created:    1,
		InitialCredentials: []peopleAccountReconciliationInitialCredential{
			{PersonID: "human-quzheng", Username: "quzheng", Password: "initial-password-once"},
		},
	}
	applyErr := errors.New("later apply failed")
	var out strings.Builder
	if err := writePeopleDirectoryApplyReport(&out, report, applyErr); !errors.Is(err, applyErr) {
		t.Fatalf("write report error = %v", err)
	}
	var decoded peopleDirectoryApplyReport
	if err := json.NewDecoder(strings.NewReader(out.String())).Decode(&decoded); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if len(decoded.InitialCredentials) != 1 || decoded.InitialCredentials[0].Password != "initial-password-once" {
		t.Fatalf("decoded report = %+v", decoded)
	}
}

type fakePeopleDirectoryApplyStore struct {
	snapshot peopledirectory.Snapshot
	users    []peopledirectory.DirectoryUser
	matches  []peopledirectory.MatchResult
	reserved []reservedDirectoryPerson
	applied  []reservedDirectoryPerson
}

func (s *fakePeopleDirectoryApplyStore) Snapshot(context.Context, string) (peopledirectory.Snapshot, error) {
	return s.snapshot, nil
}

func (s *fakePeopleDirectoryApplyStore) StagedUsers(context.Context, string) ([]peopledirectory.DirectoryUser, error) {
	return append([]peopledirectory.DirectoryUser(nil), s.users...), nil
}

func (s *fakePeopleDirectoryApplyStore) MatchResults(context.Context, string) ([]peopledirectory.MatchResult, error) {
	return append([]peopledirectory.MatchResult(nil), s.matches...), nil
}

func (s *fakePeopleDirectoryApplyStore) ReservePersonID(_ context.Context, snapshotID, sourceRecordID, personID string) error {
	s.reserved = append(s.reserved, reservedDirectoryPerson{snapshotID: snapshotID, sourceRecordID: sourceRecordID, personID: personID})
	for i := range s.matches {
		if s.matches[i].SourceRecordID == sourceRecordID {
			s.matches[i].ProposedPersonID = personID
		}
	}
	return nil
}

func (s *fakePeopleDirectoryApplyStore) MarkApplied(_ context.Context, snapshotID, sourceRecordID, personID string) error {
	s.applied = append(s.applied, reservedDirectoryPerson{snapshotID: snapshotID, sourceRecordID: sourceRecordID, personID: personID})
	return nil
}

type fakePeopleDirectoryWorkforce struct {
	nextID  string
	upserts []peopleDirectoryWorkforceCall
	enabled []peopleDirectoryWorkforceCall
}

func (f *fakePeopleDirectoryWorkforce) NewWorkforceMemberID() (string, error) {
	return f.nextID, nil
}

func (f *fakePeopleDirectoryWorkforce) UpsertWorkforceMember(_ context.Context, viewer operations.ViewerRef, command operations.UpsertWorkforceMemberCommand) (operations.WorkforceMemberDTO, error) {
	f.upserts = append(f.upserts, peopleDirectoryWorkforceCall{viewer: viewer, command: command})
	member := operations.WorkforceMemberDTO{AgentProfileID: command.AgentProfileID, UserAccount: command.UserAccount}
	if len(command.UpdateMask) == 0 {
		member.AccountProjection.InitialPassword = "initial-password-once"
	}
	return member, nil
}

func (f *fakePeopleDirectoryWorkforce) SetWorkforceMemberEnabled(_ context.Context, viewer operations.ViewerRef, command operations.SetWorkforceMemberEnabledCommand) error {
	f.enabled = append(f.enabled, peopleDirectoryWorkforceCall{viewer: viewer, command: operations.UpsertWorkforceMemberCommand{AgentProfileID: command.AgentProfileID, Enabled: command.Enabled}})
	return nil
}

type peopleDirectoryWorkforceCall struct {
	viewer  operations.ViewerRef
	command operations.UpsertWorkforceMemberCommand
}

type reservedDirectoryPerson struct {
	snapshotID     string
	sourceRecordID string
	personID       string
}
