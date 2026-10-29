package discussionsession

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestRuntimeApprovalMaterialBuildsWechatSnapshot(t *testing.T) {
	root := t.TempDir()
	prepareWechatMaterialFiles(t, root)
	request := runtimeWechatApprovalRequest(t, root, map[string]any{
		"tools_root": "/srv/marketing/private-tools",
		"api_token":  "must-not-be-persisted",
	})

	material, err := buildRuntimeApprovalMaterial(request)
	if err != nil {
		t.Fatalf("build material: %v", err)
	}
	if material == nil {
		t.Fatal("material is nil")
	}
	if material.Version != 1 || material.Kind != "wechat_draft" || material.Title != "Reviewed title" || material.Summary != "Reviewed digest" {
		t.Fatalf("material identity = %+v", material)
	}
	if material.Content != "# Reviewed article\n" || material.ContentHash == "" || material.MaterialHash == "" {
		t.Fatalf("material content = %+v", material)
	}
	if material.BusinessApprovalMaterial == nil || material.BusinessApprovalMaterial.Title != "将公众号文章《Reviewed title》保存为草稿" ||
		!strings.Contains(material.BusinessApprovalMaterial.ExpectedResult, "不会直接发布文章") ||
		len(material.BusinessApprovalMaterial.Risks) != 2 {
		t.Fatalf("business approval material = %+v", material.BusinessApprovalMaterial)
	}
	if material.Fields["channel"] != "wechat" || material.Fields["operation"] != "draft_create" ||
		material.Fields["account_id"] != "wechat-main" || material.Fields["intent_id"] != "intent-1" ||
		material.Fields["intent_hash"] != strings.Repeat("a", 64) || material.Fields["author"] != "Jordan" ||
		material.Fields["article_md_path"] != "article.md" {
		t.Fatalf("material fields = %+v", material.Fields)
	}
	if len(material.Artifacts) != 3 {
		t.Fatalf("artifacts = %+v", material.Artifacts)
	}
	businessKey := runtimeApprovalBusinessKey(*material)
	if !strings.HasPrefix(businessKey, "marketing.wechat_draft.intent:sha256:") {
		t.Fatalf("business key = %q", businessKey)
	}
	changedPresentation := *material
	changedPresentation.Title = "A regenerated title"
	changedPresentation.MaterialHash = "sha256:regenerated-material"
	if got := runtimeApprovalBusinessKey(changedPresentation); got != businessKey {
		t.Fatalf("presentation-only change altered business key: %q != %q", got, businessKey)
	}
	changedIntent := *material
	changedIntent.Fields = cloneStringMap(material.Fields)
	changedIntent.Fields["intent_id"] = "intent-2"
	if got := runtimeApprovalBusinessKey(changedIntent); got == businessKey {
		t.Fatalf("changed intent retained business key %q", got)
	}
	for _, artifact := range material.Artifacts {
		if artifact.Path == "" || artifact.Role == "" || artifact.SizeBytes <= 0 || artifact.MediaType != "image/png" || artifact.SHA256 == "" {
			t.Fatalf("artifact = %+v", artifact)
		}
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"tools_root", "api_token", "must-not-be-persisted", "/srv/marketing/private-tools", root} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("material exposed %q: %s", forbidden, encoded)
		}
	}
}

func TestRuntimeApprovalMaterialRejectsUnsafeWechatFiles(t *testing.T) {
	t.Run("outside workspace", func(t *testing.T) {
		root := t.TempDir()
		prepareWechatMaterialFiles(t, root)
		outside := filepath.Join(t.TempDir(), "outside.md")
		if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
			t.Fatal(err)
		}
		request := runtimeWechatApprovalRequest(t, root, map[string]any{"article_md_path": outside})
		if _, err := buildRuntimeApprovalMaterial(request); err == nil {
			t.Fatal("outside-workspace Markdown was accepted")
		}
	})

	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		prepareWechatMaterialFiles(t, root)
		if err := os.Symlink(filepath.Join(root, "cover.png"), filepath.Join(root, "linked.png")); err != nil {
			if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
				t.Skip("current Windows account cannot create symbolic links")
			}
			t.Fatal(err)
		}
		request := runtimeWechatApprovalRequest(t, root, map[string]any{"cover_path": filepath.Join(root, "linked.png")})
		if _, err := buildRuntimeApprovalMaterial(request); err == nil {
			t.Fatal("symlinked image was accepted")
		}
	})

	t.Run("invalid utf8", func(t *testing.T) {
		root := t.TempDir()
		prepareWechatMaterialFiles(t, root)
		writeMaterialFixture(t, root, "article.md", []byte{0xff, 0xfe})
		if _, err := buildRuntimeApprovalMaterial(runtimeWechatApprovalRequest(t, root, nil)); err == nil {
			t.Fatal("invalid UTF-8 Markdown was accepted")
		}
	})

	t.Run("oversized markdown", func(t *testing.T) {
		root := t.TempDir()
		prepareWechatMaterialFiles(t, root)
		writeMaterialFixture(t, root, "article.md", []byte(strings.Repeat("a", 128*1024+1)))
		if _, err := buildRuntimeApprovalMaterial(runtimeWechatApprovalRequest(t, root, nil)); err == nil {
			t.Fatal("oversized Markdown was accepted")
		}
	})

	t.Run("too many images", func(t *testing.T) {
		root := t.TempDir()
		prepareWechatMaterialFiles(t, root)
		images := make([]any, 15)
		for i := range images {
			images[i] = filepath.Join(root, "body.png")
		}
		request := runtimeWechatApprovalRequest(t, root, map[string]any{"body_image_paths": images})
		if _, err := buildRuntimeApprovalMaterial(request); err == nil {
			t.Fatal("more than 16 image references were accepted")
		}
	})

	t.Run("oversized image", func(t *testing.T) {
		root := t.TempDir()
		prepareWechatMaterialFiles(t, root)
		if err := os.Truncate(filepath.Join(root, "cover.png"), 20*1024*1024+1); err != nil {
			t.Fatal(err)
		}
		if _, err := buildRuntimeApprovalMaterial(runtimeWechatApprovalRequest(t, root, nil)); err == nil {
			t.Fatal("oversized image was accepted")
		}
	})
}

func TestRuntimeApprovalMaterialRequiresWechatReviewFields(t *testing.T) {
	for _, field := range []string{"account_id", "intent_id", "intent_hash", "title", "digest", "article_md_path", "cover_path"} {
		t.Run(field, func(t *testing.T) {
			root := t.TempDir()
			prepareWechatMaterialFiles(t, root)
			request := runtimeWechatApprovalRequest(t, root, map[string]any{field: ""})
			if _, err := buildRuntimeApprovalMaterial(request); err == nil {
				t.Fatalf("missing %s was accepted", field)
			}
		})
	}

	root := t.TempDir()
	prepareWechatMaterialFiles(t, root)
	request := runtimeWechatApprovalRequest(t, root, map[string]any{"body_image_paths": nil, "cover_path": "", "share_thumbnail_path": ""})
	if _, err := buildRuntimeApprovalMaterial(request); err == nil {
		t.Fatal("missing asset manifest was accepted")
	}
}

func TestRuntimeApprovalMaterialIgnoresUnknownTools(t *testing.T) {
	material, err := buildRuntimeApprovalMaterial(runtimecore.ApprovalRequest{ToolName: "exec_command"})
	if err != nil || material != nil {
		t.Fatalf("generic approval material = %+v, err=%v", material, err)
	}
}

func runtimeWechatApprovalRequest(t *testing.T, root string, overrides map[string]any) runtimecore.ApprovalRequest {
	t.Helper()
	input := map[string]any{
		"account_id":           "wechat-main",
		"intent_id":            "intent-1",
		"intent_hash":          strings.Repeat("a", 64),
		"title":                "Reviewed title",
		"author":               "Jordan",
		"digest":               "Reviewed digest",
		"article_md_path":      filepath.Join(root, "article.md"),
		"cover_path":           filepath.Join(root, "cover.png"),
		"share_thumbnail_path": filepath.Join(root, "share.png"),
		"body_image_paths":     []any{filepath.Join(root, "body.png")},
	}
	for key, value := range overrides {
		input[key] = value
	}
	payload, err := json.Marshal(map[string]any{"input": input})
	if err != nil {
		t.Fatal(err)
	}
	return runtimecore.ApprovalRequest{
		ToolName:      "mcp_marketing_marketing_wechat_draft_create",
		WorkspaceRoot: root,
		Payload:       payload,
	}
}

func prepareWechatMaterialFiles(t *testing.T, root string) {
	t.Helper()
	writeMaterialFixture(t, root, "article.md", []byte("# Reviewed article\n"))
	writeMaterialFixture(t, root, "cover.png", []byte("cover"))
	writeMaterialFixture(t, root, "share.png", []byte("share"))
	writeMaterialFixture(t, root, "body.png", []byte("body"))
}

func writeMaterialFixture(t *testing.T, root, name string, content []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), content, 0o600); err != nil {
		t.Fatal(err)
	}
}
