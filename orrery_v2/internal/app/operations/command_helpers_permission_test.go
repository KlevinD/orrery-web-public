package operations

import "testing"

func TestOperationsScopeDoesNotAuthorizeSpecificDomains(t *testing.T) {
	for _, test := range []struct {
		granted  string
		required string
	}{
		{granted: PermissionOperationsRead, required: PermissionWorkforceRead},
		{granted: PermissionOperationsRead, required: PermissionOrgRead},
		{granted: PermissionOperationsManage, required: PermissionWorkforceManage},
		{granted: PermissionOperationsManage, required: PermissionRolesManage},
		{granted: PermissionOperationsManage, required: PermissionPermissionsManage},
	} {
		if operationsPermissionScopeMatches(test.granted, test.required) {
			t.Fatalf("scope %s must not authorize %s", test.granted, test.required)
		}
	}
}

func TestDomainManageScopeStillAuthorizesDomainRead(t *testing.T) {
	if !operationsPermissionScopeMatches(PermissionWorkforceManage, PermissionWorkforceRead) {
		t.Fatal("workforce.manage should authorize workforce.read")
	}
}
