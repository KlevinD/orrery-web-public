package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestPackageStoreBuildsGroupedFileTree(t *testing.T) {
	store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	skillDir, err := store.SkillDirectory(agentcore.SkillID("skill.minimax-docx"))
	if err != nil {
		t.Fatalf("SkillDirectory: %v", err)
	}
	writePackageFile(t, skillDir, "SKILL.md", skillMarkdown("minimax-docx", "Build docx files"))
	writePackageFile(t, skillDir, "assets/logo.png", "png")
	writePackageFile(t, skillDir, "resources/template.md", "# Template\n")
	writePackageFile(t, skillDir, "references/guide.txt", "Guide")
	writePackageFile(t, skillDir, "scripts/build.ps1", "Write-Output build")
	writePackageFile(t, skillDir, "docs/readme.txt", "Read me")
	writePackageFile(t, skillDir, "README.md", "# Root readme\n")

	tree, err := store.BuildFileTree(context.Background(), agentcore.SkillID("skill.minimax-docx"))
	if err != nil {
		t.Fatalf("BuildFileTree: %v", err)
	}

	if got := rootNames(tree.Roots); !sameStrings(got, []string{"assets", "resources", "references", "scripts", "other"}) {
		t.Fatalf("roots = %v", got)
	}
	for _, root := range tree.Roots {
		if root.Children == nil {
			t.Fatalf("%s children = nil, want empty array for API contract", root.Name)
		}
	}
	if tree.Counts.Assets != 1 || tree.Counts.Resources != 1 || tree.Counts.References != 1 || tree.Counts.Scripts != 1 || tree.Counts.Other != 2 || tree.Counts.Total != 6 {
		t.Fatalf("counts = %+v", tree.Counts)
	}
	assertNodePreview(t, tree, "assets/logo.png", PreviewKindImage)
	assertNodePreview(t, tree, "resources/template.md", PreviewKindMarkdown)
	assertNodePreview(t, tree, "references/guide.txt", PreviewKindText)
	assertNodePreview(t, tree, "scripts/build.ps1", PreviewKindText)
	assertNodePreview(t, tree, "docs/readme.txt", PreviewKindText)
	assertNodePreview(t, tree, "README.md", PreviewKindMarkdown)
}

func TestPackageStoreBuildsEmptyRootsAsArrays(t *testing.T) {
	store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	skillDir, err := store.SkillDirectory(agentcore.SkillID("skill.empty"))
	if err != nil {
		t.Fatalf("SkillDirectory: %v", err)
	}
	writePackageFile(t, skillDir, "SKILL.md", skillMarkdown("empty", "No resources yet"))

	tree, err := store.BuildFileTree(context.Background(), agentcore.SkillID("skill.empty"))
	if err != nil {
		t.Fatalf("BuildFileTree: %v", err)
	}
	for _, root := range tree.Roots {
		if root.Children == nil {
			t.Fatalf("%s children = nil, want empty array for API contract", root.Name)
		}
	}
	if tree.Counts.Total != 0 {
		t.Fatalf("total count = %d, want 0", tree.Counts.Total)
	}
}

func TestPackageStoreCreatesEmptyResourceDirectoriesInFileTree(t *testing.T) {
	store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	skillDir, err := store.SkillDirectory(agentcore.SkillID("skill.empty"))
	if err != nil {
		t.Fatalf("SkillDirectory: %v", err)
	}
	writePackageFile(t, skillDir, "SKILL.md", skillMarkdown("empty", "No resources yet"))

	if err := store.CreateDirectory(context.Background(), agentcore.SkillID("skill.empty"), "resources/drafts"); err != nil {
		t.Fatalf("CreateDirectory: %v", err)
	}
	tree, err := store.BuildFileTree(context.Background(), agentcore.SkillID("skill.empty"))
	if err != nil {
		t.Fatalf("BuildFileTree: %v", err)
	}

	resources := findRoot(t, tree.Roots, "resources")
	if len(resources.Children) != 1 || resources.Children[0].Name != "drafts" || resources.Children[0].Type != "directory" {
		t.Fatalf("resources children = %+v", resources.Children)
	}
}

func TestPackageStorePreviewKindRejectsLargeTextFiles(t *testing.T) {
	store := NewPackageStoreWithLimits(filepath.Join(t.TempDir(), "skills"), PackageLimits{
		MaxFiles:        32,
		MaxTotalBytes:   1 << 20,
		MaxFileBytes:    1 << 20,
		MaxPreviewBytes: 8,
	})
	skillDir, err := store.SkillDirectory(agentcore.SkillID("skill.minimax-docx"))
	if err != nil {
		t.Fatalf("SkillDirectory: %v", err)
	}
	writePackageFile(t, skillDir, "SKILL.md", skillMarkdown("minimax-docx", "Build docx files"))
	writePackageFile(t, skillDir, "resources/large.txt", "larger than preview")

	tree, err := store.BuildFileTree(context.Background(), agentcore.SkillID("skill.minimax-docx"))
	if err != nil {
		t.Fatalf("BuildFileTree: %v", err)
	}
	assertNodePreview(t, tree, "resources/large.txt", PreviewKindNone)
}

func TestPackageStoreReadFileDoesNotReturnImageBytesAsTextPreview(t *testing.T) {
	store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	skillDir, err := store.SkillDirectory(agentcore.SkillID("skill.minimax-docx"))
	if err != nil {
		t.Fatalf("SkillDirectory: %v", err)
	}
	writePackageFile(t, skillDir, "SKILL.md", skillMarkdown("minimax-docx", "Build docx files"))
	writePackageFile(t, skillDir, "assets/logo.png", "png-binary")

	node, raw, err := store.ReadFile(context.Background(), agentcore.SkillID("skill.minimax-docx"), "assets/logo.png")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if node.PreviewKind != PreviewKindImage {
		t.Fatalf("preview kind = %s, want %s", node.PreviewKind, PreviewKindImage)
	}
	if raw != nil {
		t.Fatalf("raw image bytes = %q, want nil", string(raw))
	}
}

func writePackageFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func rootNames(roots []FileRoot) []string {
	names := make([]string, 0, len(roots))
	for _, root := range roots {
		names = append(names, root.Name)
	}
	return names
}

func assertNodePreview(t *testing.T, tree FileTree, path string, want PreviewKind) {
	t.Helper()
	node, ok := findNode(tree.Roots, path)
	if !ok {
		t.Fatalf("missing node %s", path)
	}
	if node.PreviewKind != want {
		t.Fatalf("%s preview = %s, want %s", path, node.PreviewKind, want)
	}
}

func findNode(roots []FileRoot, path string) (FileNode, bool) {
	for _, root := range roots {
		if node, ok := findNodeIn(root.Children, path); ok {
			return node, true
		}
	}
	return FileNode{}, false
}

func findRoot(t *testing.T, roots []FileRoot, name string) FileRoot {
	t.Helper()
	for _, root := range roots {
		if root.Name == name {
			return root
		}
	}
	t.Fatalf("missing root %s in %+v", name, roots)
	return FileRoot{}
}

func findNodeIn(nodes []FileNode, path string) (FileNode, bool) {
	for _, node := range nodes {
		if node.Path == path {
			return node, true
		}
		if found, ok := findNodeIn(node.Children, path); ok {
			return found, true
		}
	}
	return FileNode{}, false
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
