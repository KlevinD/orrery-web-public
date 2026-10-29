package operations

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/authorization"
)

func TestOwnerRelationWriteFailureRestoresPreviousOwner(t *testing.T) {
	writeErr := errors.New("authorization relation write failed")
	writer := &ownerRelationSecurityWriter{writeErr: writeErr}
	service := &CommandService{authorizationRelations: writer}

	err := service.syncOrganizationOwnerRelation(context.Background(), OrganizationNodeDTO{
		ID:             "org-1",
		NodeType:       organizationNodeTypeCompany,
		OwnerProfileID: "human-new-owner",
	}, "human-previous-owner", "org-1")
	if !errors.Is(err, writeErr) {
		t.Fatalf("syncOrganizationOwnerRelation error = %v, want write error", err)
	}
	if len(writer.organizationAdminWrites) != 2 || writer.organizationAdminWrites[0] != "human-new-owner" || writer.organizationAdminWrites[1] != "human-previous-owner" {
		t.Fatalf("organization admin writes = %+v", writer.organizationAdminWrites)
	}
}

type ownerRelationSecurityWriter struct {
	writeErr                error
	organizationAdminWrites []string
}

func (*ownerRelationSecurityWriter) WriteOrganizationCreated(context.Context, authorization.SubjectRef, string, string) error {
	return nil
}

func (w *ownerRelationSecurityWriter) WriteOrganizationAdmin(_ context.Context, admin authorization.SubjectRef, _, _ string) error {
	w.organizationAdminWrites = append(w.organizationAdminWrites, admin.ID)
	if len(w.organizationAdminWrites) == 1 {
		return w.writeErr
	}
	return nil
}

func (*ownerRelationSecurityWriter) DeleteOrganizationAdmin(context.Context, authorization.SubjectRef, string, string) error {
	return nil
}

func (*ownerRelationSecurityWriter) WriteDepartmentCreated(context.Context, string, string, string) error {
	return nil
}

func (*ownerRelationSecurityWriter) DeleteDepartmentCreated(context.Context, string, string, string) error {
	return nil
}

func (*ownerRelationSecurityWriter) WriteDepartmentManager(context.Context, authorization.SubjectRef, string, string) error {
	return nil
}

func (*ownerRelationSecurityWriter) DeleteDepartmentManager(context.Context, authorization.SubjectRef, string, string) error {
	return nil
}

func (*ownerRelationSecurityWriter) WriteWorkforceMemberPlacement(context.Context, authorization.SubjectRef, string, string, string, []string) error {
	return nil
}

func (*ownerRelationSecurityWriter) DeleteWorkforceMemberPlacement(context.Context, authorization.SubjectRef, string, string, string, []string) error {
	return nil
}

func (*ownerRelationSecurityWriter) WriteRoleAssignment(context.Context, authorization.RoleAssignmentRelationSnapshot) error {
	return nil
}

func (*ownerRelationSecurityWriter) DeleteRoleAssignment(context.Context, authorization.RoleAssignmentRelationSnapshot) error {
	return nil
}
