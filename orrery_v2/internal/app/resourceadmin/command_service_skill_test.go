package resourceadmin

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestCommandServiceImportsDirectorySkillPackageIntoStoreAndRegistry(t *testing.T) {
	ctx := context.Background()
	skillRegistry := skillres.NewMemoryRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	service := NewCommandService(toolres.NewMemoryRegistry(), skillRegistry)
	service.SetSkillPackageStore(store)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	packageBytes := buildSkillPackage(t, "skill.imported", "Imported skill", "# Imported\n")

	imported, err := service.ImportSkillPackage(ctx, viewer, bytes.NewReader(packageBytes), int64(len(packageBytes)))
	if err != nil {
		t.Fatalf("ImportSkillPackage: %v", err)
	}
	if imported.SkillID != "skill.imported" || imported.Name != "imported" || !imported.SkillMDPresent {
		t.Fatalf("imported = %+v", imported)
	}
	if imported.ResourceCounts.Assets != 1 || imported.ResourceCounts.Resources != 1 || imported.ResourceCounts.Total != 2 {
		t.Fatalf("resource counts = %+v", imported.ResourceCounts)
	}
	if _, err := skillRegistry.Get(ctx, agentcore.SkillID("skill.imported")); err != nil {
		t.Fatalf("registry missing imported skill: %v", err)
	}
}

func TestCommandServiceCreatesSkillPackageFromFormFields(t *testing.T) {
	ctx := context.Background()
	skillRegistry := skillres.NewMemoryRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	service := NewCommandService(toolres.NewMemoryRegistry(), skillRegistry)
	service.SetSkillPackageStore(store)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}

	detail, err := service.CreateSkill(ctx, viewer, CreateSkillRequest{
		Name:         "docx-writer",
		Description:  "Build DOCX files",
		Version:      "1.2.0",
		TaskTypeTags: []string{"writing", "document"},
		Prompt:       "# Prompt\n\nUse the DOCX API.",
	})
	if err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}

	if detail.Skill.SkillID != "skill.docx-writer" || detail.Skill.Description != "Build DOCX files" {
		t.Fatalf("created skill = %+v", detail.Skill)
	}
	if !bytes.Contains([]byte(detail.SkillMD.Raw), []byte("task_type_tags:\n  - writing\n  - document")) ||
		!bytes.Contains([]byte(detail.SkillMD.Raw), []byte("# Prompt")) {
		t.Fatalf("skill md = %q", detail.SkillMD.Raw)
	}
	if _, err := skillRegistry.Get(ctx, agentcore.SkillID("skill.docx-writer")); err != nil {
		t.Fatalf("registry missing created skill: %v", err)
	}
}

func TestCommandServiceGetsSkillDetailAndIgnoresRegistryOnlySkillInPackageList(t *testing.T) {
	ctx := context.Background()
	skillRegistry := skillres.NewMemoryRegistry()
	if err := skillRegistry.Register(ctx, skillres.SkillDefinition{ID: "skill.registry-only", Name: "Registry Only", Version: "1.0.0", Enabled: true}); err != nil {
		t.Fatalf("register registry-only skill: %v", err)
	}
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	command := NewCommandService(toolres.NewMemoryRegistry(), skillRegistry)
	command.SetSkillPackageStore(store)
	query := NewQueryService(toolres.NewMemoryRegistry(), skillRegistry)
	query.SetSkillPackageStore(store)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}
	packageBytes := buildSkillPackage(t, "skill.imported", "Imported skill", "# Imported\n")
	if _, err := command.ImportSkillPackage(ctx, viewer, bytes.NewReader(packageBytes), int64(len(packageBytes))); err != nil {
		t.Fatalf("ImportSkillPackage: %v", err)
	}

	view, err := query.ListResources(ctx, viewer)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(view.Skills) != 1 || view.Skills[0].SkillID != "skill.imported" {
		t.Fatalf("skills = %+v", view.Skills)
	}
	if view.Skills[0].ResourceCounts.Assets != 1 || view.Skills[0].ResourceCounts.Resources != 1 || view.Skills[0].ResourceCounts.Total != 2 {
		t.Fatalf("list resource counts = %+v", view.Skills[0].ResourceCounts)
	}

	detail, err := query.GetSkillDetail(ctx, viewer, "skill.imported")
	if err != nil {
		t.Fatalf("GetSkillDetail: %v", err)
	}
	if !bytes.Contains([]byte(detail.SkillMD.Raw), []byte("# Imported")) {
		t.Fatalf("skill markdown = %q", detail.SkillMD.Raw)
	}
	if len(detail.Roots) != 5 {
		t.Fatalf("roots = %+v", detail.Roots)
	}

	if _, err := query.GetSkillDetail(ctx, viewer, "skill.registry-only"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("registry-only detail error = %v, want ErrNotFound", err)
	}
}

func TestQueryServiceDoesNotRehydratePackageRiskOnRead(t *testing.T) {
	ctx := context.Background()
	skillRegistry := skillres.NewMemoryRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	command := NewCommandService(toolres.NewMemoryRegistry(), skillRegistry)
	command.SetSkillPackageStore(store)
	query := NewQueryService(toolres.NewMemoryRegistry(), skillRegistry)
	query.SetSkillPackageStore(store)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}
	packageBytes := buildSkillPackageWithEntries(t, "skill.party-government-document-generator", "党政机关公文生成", "# 党政机关公文生成\n", map[string]string{
		"generate_document.exe":        "binary",
		"read_file.exe":                "binary",
		"scripts/generate_document.py": "print('generate')\n",
	})
	if _, err := command.ImportSkillPackage(ctx, viewer, bytes.NewReader(packageBytes), int64(len(packageBytes))); err != nil {
		t.Fatalf("ImportSkillPackage: %v", err)
	}
	stale, err := skillRegistry.Get(ctx, agentcore.SkillID("skill.party-government-document-generator"))
	if err != nil {
		t.Fatalf("registry get: %v", err)
	}
	stale.RiskLevel = skillres.RiskLow
	if err := skillRegistry.UpdateDefinition(ctx, stale); err != nil {
		t.Fatalf("make registry stale: %v", err)
	}

	view, err := query.ListResources(ctx, viewer)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(view.Skills) != 1 || view.Skills[0].RiskLevel != "low" {
		t.Fatalf("listed skill = %+v, want unchanged low risk", view.Skills)
	}
	detail, err := query.GetSkillDetail(ctx, viewer, "skill.party-government-document-generator")
	if err != nil {
		t.Fatalf("GetSkillDetail: %v", err)
	}
	if detail.Skill.RiskLevel != "low" {
		t.Fatalf("detail risk = %q, want unchanged low", detail.Skill.RiskLevel)
	}
	definition, err := skillRegistry.Get(ctx, agentcore.SkillID("skill.party-government-document-generator"))
	if err != nil {
		t.Fatalf("registry get after query: %v", err)
	}
	if definition.RiskLevel != skillres.RiskLow {
		t.Fatalf("registry risk after query = %q, want unchanged low", definition.RiskLevel)
	}
}

func TestCommandServiceSavesSkillMarkdownAndUpdatesRegistry(t *testing.T) {
	ctx := context.Background()
	skillRegistry := skillres.NewMemoryRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	service := NewCommandService(toolres.NewMemoryRegistry(), skillRegistry)
	service.SetSkillPackageStore(store)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}
	packageBytes := buildSkillPackage(t, "skill.imported", "Imported skill", "# Imported\n")
	if _, err := service.ImportSkillPackage(ctx, viewer, bytes.NewReader(packageBytes), int64(len(packageBytes))); err != nil {
		t.Fatalf("ImportSkillPackage: %v", err)
	}

	raw := "---\nname: imported-v2\ndescription: Better skill\nversion: 2.0.0\n---\n# Imported v2\n"
	detail, err := service.SaveSkillMarkdown(ctx, viewer, "skill.imported", raw)
	if err != nil {
		t.Fatalf("SaveSkillMarkdown: %v", err)
	}
	if detail.Skill.SkillID != "skill.imported-v2" || detail.Skill.Version != "2.0.0" {
		t.Fatalf("detail after save = %+v", detail.Skill)
	}
	if _, err := skillRegistry.Get(ctx, agentcore.SkillID("skill.imported")); !errors.Is(err, skillres.ErrNotFound) {
		t.Fatalf("old skill registry error = %v, want ErrNotFound", err)
	}
	if def, err := skillRegistry.Get(ctx, agentcore.SkillID("skill.imported-v2")); err != nil || def.Description != "Better skill" {
		t.Fatalf("updated registry def=%+v err=%v", def, err)
	}
}

func TestCommandServiceSavesImportedChineseSkillMarkdownWithoutRenamingToTempPackage(t *testing.T) {
	ctx := context.Background()
	skillRegistry := skillres.NewMemoryRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	service := NewCommandService(toolres.NewMemoryRegistry(), skillRegistry)
	service.SetSkillPackageStore(store)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}
	packageBytes := buildSkillPackageWithName(t, "party-government-document-generator", "党政机关公文生成", "中文 Skill")
	imported, err := service.ImportSkillPackage(ctx, viewer, bytes.NewReader(packageBytes), int64(len(packageBytes)))
	if err != nil {
		t.Fatalf("ImportSkillPackage: %v", err)
	}

	raw := "---\nname: 党政机关公文生成\ndescription: 中文 Skill 已更新\n---\n# 党政机关公文生成\n"
	detail, err := service.SaveSkillMarkdown(ctx, viewer, imported.SkillID, raw)
	if err != nil {
		t.Fatalf("SaveSkillMarkdown: %v", err)
	}
	if detail.Skill.SkillID != imported.SkillID || detail.Skill.Description != "中文 Skill 已更新" {
		t.Fatalf("detail after save = %+v, imported id %q", detail.Skill, imported.SkillID)
	}
	if _, err := skillRegistry.Get(ctx, agentcore.SkillID(imported.SkillID)); err != nil {
		t.Fatalf("registry missing saved skill: %v", err)
	}
}

func TestCommandServiceRejectsSkillMarkdownRenameIntoExistingPackage(t *testing.T) {
	ctx := context.Background()
	skillRegistry := skillres.NewMemoryRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	service := NewCommandService(toolres.NewMemoryRegistry(), skillRegistry)
	service.SetSkillPackageStore(store)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}
	first := buildSkillPackage(t, "skill.first", "First", "# First\n")
	second := buildSkillPackage(t, "skill.second", "Second", "# Second\n")
	if _, err := service.ImportSkillPackage(ctx, viewer, bytes.NewReader(first), int64(len(first))); err != nil {
		t.Fatalf("import first: %v", err)
	}
	if _, err := service.ImportSkillPackage(ctx, viewer, bytes.NewReader(second), int64(len(second))); err != nil {
		t.Fatalf("import second: %v", err)
	}

	raw := "---\nname: second\ndescription: Collision\nversion: 1.0.0\n---\n# Collision\n"
	if _, err := service.SaveSkillMarkdown(ctx, viewer, "skill.first", raw); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("rename collision error = %v, want ErrInvalidRequest", err)
	}
	detail, err := service.SaveSkillMarkdown(ctx, viewer, "skill.second", "---\nname: second\ndescription: Still second\nversion: 1.0.0\n---\n# Second kept\n")
	if err != nil {
		t.Fatalf("second package should remain editable: %v", err)
	}
	if detail.Skill.SkillID != "skill.second" || detail.Skill.Description != "Still second" {
		t.Fatalf("second detail = %+v", detail.Skill)
	}
}

func TestCommandServiceReadsAndWritesTextSkillFiles(t *testing.T) {
	ctx := context.Background()
	skillRegistry := skillres.NewMemoryRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	service := NewCommandService(toolres.NewMemoryRegistry(), skillRegistry)
	service.SetSkillPackageStore(store)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}
	packageBytes := buildSkillPackage(t, "skill.imported", "Imported skill", "# Imported\n")
	if _, err := service.ImportSkillPackage(ctx, viewer, bytes.NewReader(packageBytes), int64(len(packageBytes))); err != nil {
		t.Fatalf("ImportSkillPackage: %v", err)
	}

	preview, err := service.GetSkillFile(ctx, viewer, SkillFileRequest{SkillID: "skill.imported", Path: "resources/notes.txt"})
	if err != nil {
		t.Fatalf("GetSkillFile: %v", err)
	}
	if preview.Content != "notes" || preview.PreviewKind != "text" {
		t.Fatalf("preview = %+v", preview)
	}

	updated, err := service.SaveSkillFile(ctx, viewer, SkillFileWriteRequest{SkillID: "skill.imported", Path: "resources/notes.txt", Content: "updated notes"})
	if err != nil {
		t.Fatalf("SaveSkillFile: %v", err)
	}
	if updated.Content != "updated notes" {
		t.Fatalf("updated = %+v", updated)
	}

	if _, err := service.SaveSkillFile(ctx, viewer, SkillFileWriteRequest{SkillID: "skill.imported", Path: "assets/logo.png", Content: "not text"}); err == nil {
		t.Fatalf("expected binary edit rejection")
	}
	if _, err := service.SaveSkillFile(ctx, viewer, SkillFileWriteRequest{SkillID: "skill.imported", Path: "SKILL.md", Content: "# bypass"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("skill md bypass error = %v, want ErrInvalidRequest", err)
	}
}

func TestCommandServiceReclassifiesSkillRiskWhenPackageFilesChange(t *testing.T) {
	ctx := context.Background()
	skillRegistry := skillres.NewMemoryRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	service := NewCommandService(toolres.NewMemoryRegistry(), skillRegistry)
	service.SetSkillPackageStore(store)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}
	packageBytes := buildSkillPackage(t, "skill.imported", "Imported skill", "# Imported\n")
	imported, err := service.ImportSkillPackage(ctx, viewer, bytes.NewReader(packageBytes), int64(len(packageBytes)))
	if err != nil {
		t.Fatalf("ImportSkillPackage: %v", err)
	}
	if imported.RiskLevel != "low" {
		t.Fatalf("initial risk = %q, want low", imported.RiskLevel)
	}

	uploaded, err := service.UploadSkillFile(ctx, viewer, SkillFileUploadRequest{
		SkillID: "skill.imported",
		Path:    "scripts/run.ps1",
		Content: []byte("Write-Output run"),
	})
	if err != nil {
		t.Fatalf("UploadSkillFile: %v", err)
	}
	if uploaded.Path != "scripts/run.ps1" {
		t.Fatalf("uploaded = %+v", uploaded)
	}
	definition, err := skillRegistry.Get(ctx, agentcore.SkillID("skill.imported"))
	if err != nil {
		t.Fatalf("registry get after script upload: %v", err)
	}
	if definition.RiskLevel != skillres.RiskMedium {
		t.Fatalf("risk after script upload = %q, want medium", definition.RiskLevel)
	}

	if _, err := service.UploadSkillFile(ctx, viewer, SkillFileUploadRequest{
		SkillID: "skill.imported",
		Path:    "bin/tool.exe",
		Content: []byte("binary"),
	}); err != nil {
		t.Fatalf("UploadSkillFile executable: %v", err)
	}
	definition, err = skillRegistry.Get(ctx, agentcore.SkillID("skill.imported"))
	if err != nil {
		t.Fatalf("registry get after executable upload: %v", err)
	}
	if definition.RiskLevel != skillres.RiskHigh {
		t.Fatalf("risk after executable upload = %q, want high", definition.RiskLevel)
	}
}

func TestCommandServiceMapsSkillPackageStoreErrors(t *testing.T) {
	ctx := context.Background()
	skillRegistry := skillres.NewMemoryRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	service := NewCommandService(toolres.NewMemoryRegistry(), skillRegistry)
	service.SetSkillPackageStore(store)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}
	packageBytes := buildSkillPackage(t, "skill.imported", "Imported skill", "# Imported\n")
	if _, err := service.ImportSkillPackage(ctx, viewer, bytes.NewReader(packageBytes), int64(len(packageBytes))); err != nil {
		t.Fatalf("ImportSkillPackage: %v", err)
	}

	if _, err := service.GetSkillFile(ctx, viewer, SkillFileRequest{SkillID: "skill.imported", Path: "resources/missing.txt"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing file error = %v, want ErrNotFound", err)
	}
	if _, err := service.SaveSkillFile(ctx, viewer, SkillFileWriteRequest{SkillID: "skill.imported", Path: "../outside.txt", Content: "nope"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unsafe file write error = %v, want ErrInvalidRequest", err)
	}
	if _, err := service.ExportSkillPackage(ctx, viewer, "skill.missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing export error = %v, want ErrNotFound", err)
	}
}

func TestCommandServiceUploadsDownloadsAndDeletesSkillFiles(t *testing.T) {
	ctx := context.Background()
	skillRegistry := skillres.NewMemoryRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	service := NewCommandService(toolres.NewMemoryRegistry(), skillRegistry)
	service.SetSkillPackageStore(store)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}
	packageBytes := buildSkillPackage(t, "skill.imported", "Imported skill", "# Imported\n")
	if _, err := service.ImportSkillPackage(ctx, viewer, bytes.NewReader(packageBytes), int64(len(packageBytes))); err != nil {
		t.Fatalf("ImportSkillPackage: %v", err)
	}

	uploaded, err := service.UploadSkillFile(ctx, viewer, SkillFileUploadRequest{
		SkillID: "skill.imported",
		Path:    "resources/uploaded.txt",
		Content: []byte("uploaded notes"),
	})
	if err != nil {
		t.Fatalf("UploadSkillFile: %v", err)
	}
	if uploaded.Path != "resources/uploaded.txt" || uploaded.Content != "uploaded notes" {
		t.Fatalf("uploaded = %+v", uploaded)
	}

	download, err := service.DownloadSkillFile(ctx, viewer, SkillFileRequest{SkillID: "skill.imported", Path: "resources/uploaded.txt"})
	if err != nil {
		t.Fatalf("DownloadSkillFile: %v", err)
	}
	if download.FileName != "uploaded.txt" || download.ContentType == "" || string(download.Bytes) != "uploaded notes" {
		t.Fatalf("download = file=%q type=%q bytes=%q", download.FileName, download.ContentType, string(download.Bytes))
	}

	if err := service.DeleteSkillFile(ctx, viewer, SkillFileRequest{SkillID: "skill.imported", Path: "resources/uploaded.txt"}); err != nil {
		t.Fatalf("DeleteSkillFile: %v", err)
	}
	if _, err := service.GetSkillFile(ctx, viewer, SkillFileRequest{SkillID: "skill.imported", Path: "resources/uploaded.txt"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted file error = %v, want ErrNotFound", err)
	}
	if _, err := service.UploadSkillFile(ctx, viewer, SkillFileUploadRequest{SkillID: "skill.imported", Path: "SKILL.md", Content: []byte("# bypass")}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("upload skill md bypass error = %v, want ErrInvalidRequest", err)
	}
	if err := service.DeleteSkillFile(ctx, viewer, SkillFileRequest{SkillID: "skill.imported", Path: "SKILL.md"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("delete skill md bypass error = %v, want ErrInvalidRequest", err)
	}
}

func TestCommandServiceCreatesSkillFolders(t *testing.T) {
	ctx := context.Background()
	skillRegistry := skillres.NewMemoryRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	service := NewCommandService(toolres.NewMemoryRegistry(), skillRegistry)
	service.SetSkillPackageStore(store)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}
	packageBytes := buildSkillPackage(t, "skill.imported", "Imported skill", "# Imported\n")
	if _, err := service.ImportSkillPackage(ctx, viewer, bytes.NewReader(packageBytes), int64(len(packageBytes))); err != nil {
		t.Fatalf("ImportSkillPackage: %v", err)
	}

	if err := service.CreateSkillFolder(ctx, viewer, SkillFileRequest{SkillID: "skill.imported", Path: "resources/drafts"}); err != nil {
		t.Fatalf("CreateSkillFolder: %v", err)
	}

	query := NewQueryService(toolres.NewMemoryRegistry(), skillRegistry)
	query.SetSkillPackageStore(store)
	detail, err := query.GetSkillDetail(ctx, viewer, "skill.imported")
	if err != nil {
		t.Fatalf("GetSkillDetail: %v", err)
	}
	resources := findSkillRoot(t, detail.Roots, "resources")
	if len(resources.Children) != 2 || resources.Children[0].Name != "drafts" {
		t.Fatalf("resources root = %+v", resources.Children)
	}
}

func findSkillRoot(t *testing.T, roots []skillres.FileRoot, name string) skillres.FileRoot {
	t.Helper()
	for _, root := range roots {
		if root.Name == name {
			return root
		}
	}
	t.Fatalf("missing root %s in %+v", name, roots)
	return skillres.FileRoot{}
}
