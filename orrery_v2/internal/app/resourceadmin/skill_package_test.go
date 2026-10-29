package resourceadmin

import (
	"archive/zip"
	"bytes"
	"context"
	"path/filepath"
	"testing"

	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestCommandServiceExportsSkillStandardPackage(t *testing.T) {
	ctx := context.Background()
	skillRegistry := skillres.NewMemoryRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	service := NewCommandService(toolres.NewMemoryRegistry(), skillRegistry)
	service.SetSkillPackageStore(store)
	packageBytes := buildSkillPackage(t, "skill.export", "Export", "# Export\n")
	if _, err := service.ImportSkillPackage(ctx, ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}, bytes.NewReader(packageBytes), int64(len(packageBytes))); err != nil {
		t.Fatalf("ImportSkillPackage: %v", err)
	}

	pkg, err := service.ExportSkillPackage(ctx, ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceRead},
	}, "skill.export")
	if err != nil {
		t.Fatalf("ExportSkillPackage: %v", err)
	}
	if pkg.FileName != "skill.export.skill.zip" || pkg.ContentType != "application/zip" {
		t.Fatalf("package metadata = %+v", pkg)
	}
	if _, err := zip.NewReader(bytes.NewReader(pkg.Bytes), int64(len(pkg.Bytes))); err != nil {
		t.Fatalf("invalid zip package: %v", err)
	}
}

func TestCommandServiceAuditsSkillPackageExportWithCorrelationID(t *testing.T) {
	ctx := context.Background()
	skillRegistry := skillres.NewMemoryRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	audit := &fakeResourceAuditRecorder{}
	service := NewCommandService(toolres.NewMemoryRegistry(), skillRegistry)
	service.SetSkillPackageStore(store)
	service.SetAuditRecorder(audit)
	packageBytes := buildSkillPackage(t, "skill.export", "Export", "# Export\n")
	if _, err := service.ImportSkillPackage(ctx, ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}, bytes.NewReader(packageBytes), int64(len(packageBytes))); err != nil {
		t.Fatalf("ImportSkillPackage: %v", err)
	}

	if _, err := service.ExportSkillPackage(ctx, ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceRead},
		RequestID:       "corr-export-skill",
	}, "skill.export"); err != nil {
		t.Fatalf("ExportSkillPackage: %v", err)
	}
	if len(audit.events) != 2 {
		t.Fatalf("audit events = %+v", audit.events)
	}
	event := audit.events[len(audit.events)-1]
	if event.Action != "resource.skill.export" || event.EntityID != "skill.export" || event.CorrelationID != "corr-export-skill" {
		t.Fatalf("export audit = %+v", event)
	}
}
