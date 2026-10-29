package skill

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestPackageStoreImportsRootLevelSkillPackage(t *testing.T) {
	ctx := context.Background()
	store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	data := zipPackage(t, map[string]string{
		"SKILL.md":              skillMarkdown("minimax-docx", "Build docx files"),
		"assets/logo.png":       "png",
		"resources/template.md": "# Template\n",
		"references/guide.txt":  "Guide",
		"scripts/build.ps1":     "Write-Output build",
	})

	definition, err := store.ImportPackage(ctx, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ImportPackage: %v", err)
	}
	if definition.ID != agentcore.SkillID("skill.minimax-docx") {
		t.Fatalf("skill id = %q", definition.ID)
	}
	if definition.Description != "Build docx files" {
		t.Fatalf("description = %q", definition.Description)
	}
	skillDir, err := store.SkillDirectory(definition.ID)
	if err != nil {
		t.Fatalf("SkillDirectory: %v", err)
	}
	assertFile(t, filepath.Join(skillDir, "SKILL.md"), skillMarkdown("minimax-docx", "Build docx files"))
	assertFile(t, filepath.Join(skillDir, "assets", "logo.png"), "png")
	assertFile(t, filepath.Join(skillDir, "resources", "template.md"), "# Template\n")
	assertFile(t, filepath.Join(skillDir, "references", "guide.txt"), "Guide")
	assertFile(t, filepath.Join(skillDir, "scripts", "build.ps1"), "Write-Output build")
	if !strings.HasPrefix(definition.SourceRef, "file://") || !strings.Contains(definition.SourceRef, filepath.ToSlash(skillDir)) {
		t.Fatalf("source ref = %q, want file ref under %s", definition.SourceRef, skillDir)
	}
}

func TestPackageStoreImportsSingleFolderSkillPackage(t *testing.T) {
	ctx := context.Background()
	store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	data := zipPackage(t, map[string]string{
		"minimax-docx/SKILL.md":        skillMarkdown("minimax-docx", "Build docx files"),
		"minimax-docx/assets/logo.png": "png",
	})

	definition, err := store.ImportPackage(ctx, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ImportPackage: %v", err)
	}
	skillDir, err := store.SkillDirectory(definition.ID)
	if err != nil {
		t.Fatalf("SkillDirectory: %v", err)
	}
	assertFile(t, filepath.Join(skillDir, "SKILL.md"), skillMarkdown("minimax-docx", "Build docx files"))
	assertFile(t, filepath.Join(skillDir, "assets", "logo.png"), "png")
}

func TestPackageStoreImportsStandardPackageWithBinaryToolFiles(t *testing.T) {
	ctx := context.Background()
	store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	top := "skills-NanjingHJLP.party-government-document-generator-master-57ba4ba8ca1b8aea0769560ef90871cde7611282"
	data := zipPackage(t, map[string]string{
		top + "/SKILL.md":                      skillMarkdown("党政机关公文生成", "Generate party and government documents"),
		top + "/assets/document_template.json": `{}`,
		top + "/references/gbt-9704-2012.md":   "# GBT 9704\n",
		top + "/scripts/generate_document.py":  "print('generate')\n",
		top + "/generate_document.exe":         string(bytes.Repeat([]byte("g"), 17<<20)),
		top + "/read_file.exe":                 string(bytes.Repeat([]byte("r"), 31<<20)),
	})

	definition, err := store.ImportPackage(ctx, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ImportPackage: %v", err)
	}
	if definition.ID != agentcore.SkillID("skill.party-government-document-generator") {
		t.Fatalf("skill id = %q", definition.ID)
	}
	if definition.RiskLevel != RiskHigh {
		t.Fatalf("risk = %q, want high", definition.RiskLevel)
	}
	tree, err := store.BuildFileTree(ctx, definition.ID)
	if err != nil {
		t.Fatalf("BuildFileTree: %v", err)
	}
	if tree.Counts.Scripts != 1 || tree.Counts.Other != 2 || tree.Counts.Total != 5 {
		t.Fatalf("resource counts = %+v", tree.Counts)
	}
}

func TestPackageStoreClassifiesSkillRiskFromPackageFiles(t *testing.T) {
	tests := []struct {
		name string
		file string
		want RiskLevel
	}{
		{name: "executable", file: "bin/tool.exe", want: RiskHigh},
		{name: "script", file: "scripts/build.py", want: RiskMedium},
		{name: "text", file: "references/guide.md", want: RiskLow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
			data := zipPackage(t, map[string]string{
				"SKILL.md": skillMarkdown("risk-"+tc.name, "Risk case"),
				tc.file:    "content",
			})

			definition, err := store.ImportPackage(ctx, bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}
			if definition.RiskLevel != tc.want {
				t.Fatalf("risk = %q, want %q", definition.RiskLevel, tc.want)
			}
		})
	}
}

func TestPackageStoreRejectsLegacyManifestPackage(t *testing.T) {
	store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	data := zipPackage(t, map[string]string{
		"skill.json":     `{"format_version":1}`,
		"files/SKILL.md": skillMarkdown("legacy", "Legacy package"),
	})

	_, err := store.ImportPackage(context.Background(), bytes.NewReader(data), int64(len(data)))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "legacy") {
		t.Fatalf("expected legacy package rejection, got %v", err)
	}
}

func TestImportStandardPackageRejectsLegacyManifestPackage(t *testing.T) {
	data := zipPackage(t, map[string]string{
		"skill.json":     `{"format_version":1}`,
		"files/SKILL.md": skillMarkdown("legacy", "Legacy package"),
	})

	_, err := ImportStandardPackage(context.Background(), bytes.NewReader(data), int64(len(data)))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "legacy") {
		t.Fatalf("expected legacy package rejection, got %v", err)
	}
}

func TestPackageStoreRejectsPackagesPastLimits(t *testing.T) {
	store := NewPackageStoreWithLimits(filepath.Join(t.TempDir(), "skills"), PackageLimits{
		MaxFiles:      8,
		MaxTotalBytes: 16,
		MaxFileBytes:  64,
	})
	data := zipPackage(t, map[string]string{
		"SKILL.md": string(bytes.Repeat([]byte("x"), 32)),
	})

	_, err := store.ImportPackage(context.Background(), bytes.NewReader(data), int64(len(data)))
	if err == nil {
		t.Fatalf("expected package limit error")
	}
}

func TestPackageStoreRejectsUnsafePackagePaths(t *testing.T) {
	tests := []string{
		"../escape.txt",
		"minimax-docx/../escape.txt",
		"/absolute.txt",
		"C:/escape.txt",
		"minimax-docx\\escape.txt",
	}
	for _, unsafePath := range tests {
		t.Run(unsafePath, func(t *testing.T) {
			store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
			data := zipPackage(t, map[string]string{
				"SKILL.md": skillMarkdown("minimax-docx", "Build docx files"),
				unsafePath: "bad",
			})

			_, err := store.ImportPackage(context.Background(), bytes.NewReader(data), int64(len(data)))
			if err == nil {
				t.Fatalf("expected unsafe path rejection")
			}
		})
	}
}

func TestPackageStoreStagesPackageDeletionForRollbackOrCommit(t *testing.T) {
	ctx := context.Background()
	store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	data := zipPackage(t, map[string]string{
		"SKILL.md":        skillMarkdown("review", "Review package"),
		"assets/logo.txt": "logo",
	})
	definition, err := store.ImportPackage(ctx, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ImportPackage: %v", err)
	}

	staged, err := store.StagePackageDeletions(ctx, []agentcore.SkillID{definition.ID})
	if err != nil {
		t.Fatalf("StagePackageDeletions: %v", err)
	}
	if _, err := store.BuildFileTree(ctx, definition.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("staged package BuildFileTree error = %v, want ErrNotFound", err)
	}
	if err := staged.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := store.BuildFileTree(ctx, definition.ID); err != nil {
		t.Fatalf("rolled back package missing: %v", err)
	}

	staged, err = store.StagePackageDeletions(ctx, []agentcore.SkillID{definition.ID})
	if err != nil {
		t.Fatalf("StagePackageDeletions second pass: %v", err)
	}
	if err := staged.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, err := store.BuildFileTree(ctx, definition.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("committed package BuildFileTree error = %v, want ErrNotFound", err)
	}
}

func TestPackageStoreExportsDirectoryPackage(t *testing.T) {
	ctx := context.Background()
	store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	data := zipPackage(t, map[string]string{
		"SKILL.md":              skillMarkdown("minimax-docx", "Build docx files"),
		"assets/logo.png":       "png",
		"resources/template.md": "# Template\n",
		"references/guide.txt":  "Guide",
		"scripts/build.ps1":     "Write-Output build",
	})
	definition, err := store.ImportPackage(ctx, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ImportPackage: %v", err)
	}

	var exported bytes.Buffer
	if err := store.ExportPackage(ctx, definition.ID, &exported); err != nil {
		t.Fatalf("ExportPackage: %v", err)
	}

	files := readZipFiles(t, exported.Bytes())
	if _, exists := files["skill.json"]; exists {
		t.Fatalf("export should not contain legacy skill.json")
	}
	assertZipFile(t, files, "minimax-docx/SKILL.md", skillMarkdown("minimax-docx", "Build docx files"))
	assertZipFile(t, files, "minimax-docx/assets/logo.png", "png")
	assertZipFile(t, files, "minimax-docx/resources/template.md", "# Template\n")
	assertZipFile(t, files, "minimax-docx/references/guide.txt", "Guide")
	assertZipFile(t, files, "minimax-docx/scripts/build.ps1", "Write-Output build")
}

func skillMarkdown(name, description string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\nversion: 1.0.0\n---\n# " + name + "\n"
}

func zipPackage(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(raw) != want {
		t.Fatalf("%s = %q, want %q", path, raw, want)
	}
}

func assertZipFile(t *testing.T, files map[string][]byte, name, want string) {
	t.Helper()
	raw, exists := files[name]
	if !exists {
		t.Fatalf("zip missing %s; entries: %v", name, mapKeys(files))
	}
	if string(raw) != want {
		t.Fatalf("%s = %q, want %q", name, raw, want)
	}
}

func mapKeys(values map[string][]byte) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func readZipFiles(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	files := map[string][]byte{}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		content, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", file.Name, err)
		}
		files[file.Name] = content
	}
	return files
}
