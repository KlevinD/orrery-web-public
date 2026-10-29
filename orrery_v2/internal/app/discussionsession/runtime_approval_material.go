package discussionsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/hil"
)

const (
	approvalMaterialMetadataKey          = "approval_material_json"
	approvalBusinessKeyMetadataKey       = "approval_business_key"
	wechatDraftApprovalTool              = "mcp_marketing_marketing_wechat_draft_create"
	maxApprovalMarkdownBytes       int64 = 128 * 1024
	maxApprovalImageBytes          int64 = 20 * 1024 * 1024
	maxApprovalImageReferences           = 16
)

type runtimeApprovalMaterial struct {
	Version                  int                           `json:"material_version"`
	Kind                     string                        `json:"material_kind"`
	Title                    string                        `json:"title"`
	Summary                  string                        `json:"summary"`
	Content                  string                        `json:"content"`
	ContentHash              string                        `json:"content_hash"`
	MaterialHash             string                        `json:"material_hash"`
	Fields                   map[string]string             `json:"fields"`
	Artifacts                []runtimeApprovalArtifact     `json:"artifacts"`
	BusinessApprovalMaterial *hil.BusinessApprovalMaterial `json:"business_approval_material,omitempty"`
}

type runtimeApprovalArtifact struct {
	Role      string `json:"role"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	MediaType string `json:"media_type"`
	SHA256    string `json:"sha256"`
}

type runtimeApprovalWorkspace struct {
	logicalRoot  string
	resolvedRoot string
}

func putRuntimeApprovalMaterialBinding(metadata map[string]string, material runtimeApprovalMaterial) {
	if metadata == nil {
		return
	}
	putRuntimeApprovalMetadata(metadata, "approval_material_kind", material.Kind)
	putRuntimeApprovalMetadata(metadata, "approval_material_hash", material.MaterialHash)
	for _, key := range []string{"account_id", "intent_id", "intent_hash"} {
		putRuntimeApprovalMetadata(metadata, "approval_material_"+key, material.Fields[key])
	}
	putRuntimeApprovalMetadata(metadata, approvalBusinessKeyMetadataKey, runtimeApprovalBusinessKey(material))
}

func runtimeApprovalBusinessKey(material runtimeApprovalMaterial) string {
	if material.Kind != "wechat_draft" {
		return ""
	}
	accountID := strings.TrimSpace(material.Fields["account_id"])
	intentID := strings.TrimSpace(material.Fields["intent_id"])
	intentHash := strings.TrimSpace(material.Fields["intent_hash"])
	if accountID == "" || intentID == "" || !validRuntimeApprovalSHA256(intentHash) {
		return ""
	}
	payload, err := json.Marshal(struct {
		Version    int    `json:"version"`
		Tool       string `json:"tool"`
		AccountID  string `json:"account_id"`
		IntentID   string `json:"intent_id"`
		IntentHash string `json:"intent_hash"`
	}{
		Version: 1, Tool: wechatDraftApprovalTool, AccountID: accountID, IntentID: intentID, IntentHash: intentHash,
	})
	if err != nil {
		return ""
	}
	return "marketing.wechat_draft.intent:" + runtimeApprovalHash(payload)
}

func runtimeApprovalContinuationProviderSpecific(metadata map[string]string, continuation runtimecore.ApprovalContinuation) map[string]string {
	providerSpecific := runtimecore.ApprovalContinuationProviderSpecific(metadata, continuation)
	for _, key := range []string{
		"approval_id", "approval_mode", "approval_decided_by", "approval_decided_at",
		"hil_decision_id", "hil_receipt_id", "hil_receipt_effect",
		"approval_material_kind", "approval_material_hash", "approval_material_account_id",
		"approval_material_intent_id", "approval_material_intent_hash",
		approvalBusinessKeyMetadataKey,
	} {
		putRuntimeApprovalMetadata(providerSpecific, key, metadata[key])
	}
	if providerSpecific["approval_material_intent_id"] != "" && providerSpecific["approval_material_intent_hash"] != "" {
		return providerSpecific
	}
	var material runtimeApprovalMaterial
	if err := json.Unmarshal([]byte(strings.TrimSpace(metadata[approvalMaterialMetadataKey])), &material); err == nil {
		putRuntimeApprovalMaterialBinding(providerSpecific, material)
	}
	return providerSpecific
}

func buildRuntimeApprovalMaterial(request runtimecore.ApprovalRequest) (*runtimeApprovalMaterial, error) {
	if strings.TrimSpace(request.ToolName) != wechatDraftApprovalTool {
		return nil, nil
	}
	workspace, err := newRuntimeApprovalWorkspace(request.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	input, err := runtimeApprovalInput(request.Payload)
	if err != nil {
		return nil, err
	}
	accountID, err := requiredRuntimeApprovalString(input, "account_id")
	if err != nil {
		return nil, err
	}
	intentID, err := requiredRuntimeApprovalString(input, "intent_id")
	if err != nil {
		return nil, err
	}
	intentHash, err := requiredRuntimeApprovalString(input, "intent_hash")
	if err != nil {
		return nil, err
	}
	if !validRuntimeApprovalSHA256(intentHash) {
		return nil, fmt.Errorf("approval material intent_hash must be a lowercase SHA-256")
	}
	title, err := requiredRuntimeApprovalString(input, "title")
	if err != nil {
		return nil, err
	}
	digest, err := requiredRuntimeApprovalString(input, "digest")
	if err != nil {
		return nil, err
	}
	articlePath, err := requiredRuntimeApprovalString(input, "article_md_path")
	if err != nil {
		return nil, err
	}
	content, relativeArticlePath, err := workspace.readMarkdown(articlePath)
	if err != nil {
		return nil, fmt.Errorf("approval material article_md_path: %w", err)
	}

	imageRefs, err := runtimeApprovalImageReferences(input)
	if err != nil {
		return nil, err
	}
	if len(imageRefs) == 0 {
		return nil, fmt.Errorf("approval material asset manifest is required")
	}
	if len(imageRefs) > maxApprovalImageReferences {
		return nil, fmt.Errorf("approval material has %d image references; maximum is %d", len(imageRefs), maxApprovalImageReferences)
	}
	artifacts := make([]runtimeApprovalArtifact, 0, len(imageRefs))
	for _, ref := range imageRefs {
		artifact, err := workspace.imageArtifact(ref.role, ref.path)
		if err != nil {
			return nil, fmt.Errorf("approval material %s: %w", ref.role, err)
		}
		artifacts = append(artifacts, artifact)
	}
	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].Path == artifacts[j].Path {
			return artifacts[i].Role < artifacts[j].Role
		}
		return artifacts[i].Path < artifacts[j].Path
	})

	material := &runtimeApprovalMaterial{
		Version:     1,
		Kind:        "wechat_draft",
		Title:       title,
		Summary:     digest,
		Content:     content,
		ContentHash: runtimeApprovalHash([]byte(content)),
		Fields: map[string]string{
			"channel":         "wechat",
			"operation":       "draft_create",
			"account_id":      accountID,
			"intent_id":       intentID,
			"intent_hash":     intentHash,
			"article_md_path": relativeArticlePath,
		},
		Artifacts: artifacts,
	}
	if author := runtimeApprovalString(input, "author"); author != "" {
		material.Fields["author"] = author
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return nil, fmt.Errorf("encode approval material: %w", err)
	}
	material.MaterialHash = runtimeApprovalHash(encoded)
	businessMaterial := runtimeWechatBusinessApprovalMaterial(title, accountID)
	material.BusinessApprovalMaterial = &businessMaterial
	return material, nil
}

func runtimeWechatBusinessApprovalMaterial(title, accountID string) hil.BusinessApprovalMaterial {
	return hil.BusinessApprovalMaterial{
		Language:        "zh-CN",
		Title:           fmt.Sprintf("将公众号文章《%s》保存为草稿", strings.TrimSpace(title)),
		DecisionRequest: "是否允许营销智能体把这篇文章写入指定公众号的草稿箱？",
		BusinessContext: "文章正文和配图已经准备完成。写入公众号属于外部系统变更，需要在执行前确认内容和目标账号。",
		ProposedAction:  fmt.Sprintf("在公众号账号“%s”中创建草稿《%s》。", strings.TrimSpace(accountID), strings.TrimSpace(title)),
		ExpectedResult:  "草稿创建后可以继续编辑或由运营人员发布；本次审批不会直接发布文章。",
		ImpactScope:     fmt.Sprintf("只影响账号“%s”的草稿箱，不会向关注者推送，也不会修改其他文章。", strings.TrimSpace(accountID)),
		Risks: []hil.BusinessApprovalRisk{
			{Description: "文章内容或配图可能存在不准确、不完整或不适合对外的表述。", Mitigation: "批准前检查标题、正文、事实、链接和配图；需要修改时先拒绝并退回营销任务。"},
			{Description: "目标公众号账号选择错误会把草稿写入错误的账号。", Mitigation: "批准前核对页面中显示的公众号账号。"},
		},
	}
}

func runtimeApprovalInput(raw json.RawMessage) (map[string]any, error) {
	var payload struct {
		Input map[string]any `json:"input"`
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("approval material payload is required")
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode approval material payload: %w", err)
	}
	if payload.Input == nil {
		return nil, fmt.Errorf("approval material input is required")
	}
	return payload.Input, nil
}

func requiredRuntimeApprovalString(input map[string]any, key string) (string, error) {
	value := runtimeApprovalString(input, key)
	if value == "" {
		return "", fmt.Errorf("approval material %s is required", key)
	}
	return value, nil
}

func runtimeApprovalString(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return strings.TrimSpace(value)
}

type runtimeApprovalImageReference struct {
	role string
	path string
}

func runtimeApprovalImageReferences(input map[string]any) ([]runtimeApprovalImageReference, error) {
	refs := make([]runtimeApprovalImageReference, 0, 3)
	cover, err := requiredRuntimeApprovalString(input, "cover_path")
	if err != nil {
		return nil, err
	}
	refs = append(refs, runtimeApprovalImageReference{role: "cover", path: cover})
	if path := runtimeApprovalString(input, "share_thumbnail_path"); path != "" {
		refs = append(refs, runtimeApprovalImageReference{role: "share_thumbnail", path: path})
	}
	if raw := input["body_image_paths"]; raw != nil {
		values, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("approval material body_image_paths must be an array")
		}
		for _, rawPath := range values {
			path, ok := rawPath.(string)
			path = strings.TrimSpace(path)
			if !ok || path == "" {
				return nil, fmt.Errorf("approval material body_image_paths must contain non-empty strings")
			}
			refs = append(refs, runtimeApprovalImageReference{role: "body_image", path: path})
		}
	}
	return refs, nil
}

func newRuntimeApprovalWorkspace(root string) (runtimeApprovalWorkspace, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return runtimeApprovalWorkspace{}, fmt.Errorf("approval material workspace root is required")
	}
	logicalRoot, err := filepath.Abs(root)
	if err != nil {
		return runtimeApprovalWorkspace{}, fmt.Errorf("resolve approval material workspace root: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(logicalRoot)
	if err != nil {
		return runtimeApprovalWorkspace{}, fmt.Errorf("resolve approval material workspace root: %w", err)
	}
	info, err := os.Stat(resolvedRoot)
	if err != nil {
		return runtimeApprovalWorkspace{}, fmt.Errorf("stat approval material workspace root: %w", err)
	}
	if !info.IsDir() {
		return runtimeApprovalWorkspace{}, fmt.Errorf("approval material workspace root is not a directory")
	}
	return runtimeApprovalWorkspace{logicalRoot: filepath.Clean(logicalRoot), resolvedRoot: filepath.Clean(resolvedRoot)}, nil
}

func (workspace runtimeApprovalWorkspace) resolveFile(path string) (string, string, os.FileInfo, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", nil, fmt.Errorf("path is required")
	}
	logicalPath := path
	if !filepath.IsAbs(logicalPath) {
		logicalPath = filepath.Join(workspace.logicalRoot, logicalPath)
	}
	logicalPath = filepath.Clean(logicalPath)
	relativePath, err := filepath.Rel(workspace.logicalRoot, logicalPath)
	if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) || filepath.IsAbs(relativePath) {
		return "", "", nil, fmt.Errorf("path is outside the runtime workspace")
	}
	expectedPath := filepath.Join(workspace.resolvedRoot, relativePath)
	info, err := os.Lstat(expectedPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("stat path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", "", nil, fmt.Errorf("symlinked paths are not allowed")
	}
	resolvedPath, err := filepath.EvalSymlinks(expectedPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("resolve path: %w", err)
	}
	if filepath.Clean(resolvedPath) != filepath.Clean(expectedPath) {
		return "", "", nil, fmt.Errorf("symlinked paths are not allowed")
	}
	resolvedRelative, err := filepath.Rel(workspace.resolvedRoot, resolvedPath)
	if err != nil || resolvedRelative == ".." || strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(resolvedRelative) {
		return "", "", nil, fmt.Errorf("path is outside the runtime workspace")
	}
	if !info.Mode().IsRegular() {
		return "", "", nil, fmt.Errorf("path is not a regular file")
	}
	return resolvedPath, filepath.ToSlash(resolvedRelative), info, nil
}

func (workspace runtimeApprovalWorkspace) readMarkdown(path string) (string, string, error) {
	resolvedPath, relativePath, info, err := workspace.resolveFile(path)
	if err != nil {
		return "", "", err
	}
	if info.Size() > maxApprovalMarkdownBytes {
		return "", "", fmt.Errorf("Markdown exceeds %d bytes", maxApprovalMarkdownBytes)
	}
	file, err := os.Open(resolvedPath)
	if err != nil {
		return "", "", fmt.Errorf("open Markdown: %w", err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxApprovalMarkdownBytes+1))
	if err != nil {
		return "", "", fmt.Errorf("read Markdown: %w", err)
	}
	if int64(len(content)) > maxApprovalMarkdownBytes {
		return "", "", fmt.Errorf("Markdown exceeds %d bytes", maxApprovalMarkdownBytes)
	}
	if !utf8.Valid(content) {
		return "", "", fmt.Errorf("Markdown must be valid UTF-8")
	}
	return string(content), relativePath, nil
}

func (workspace runtimeApprovalWorkspace) imageArtifact(role, path string) (runtimeApprovalArtifact, error) {
	resolvedPath, relativePath, info, err := workspace.resolveFile(path)
	if err != nil {
		return runtimeApprovalArtifact{}, err
	}
	if info.Size() > maxApprovalImageBytes {
		return runtimeApprovalArtifact{}, fmt.Errorf("image exceeds %d bytes", maxApprovalImageBytes)
	}
	mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(relativePath)))
	if separator := strings.IndexByte(mediaType, ';'); separator >= 0 {
		mediaType = mediaType[:separator]
	}
	if !strings.HasPrefix(mediaType, "image/") {
		return runtimeApprovalArtifact{}, fmt.Errorf("artifact is not a recognized image")
	}
	file, err := os.Open(resolvedPath)
	if err != nil {
		return runtimeApprovalArtifact{}, fmt.Errorf("open image: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maxApprovalImageBytes+1))
	if err != nil {
		return runtimeApprovalArtifact{}, fmt.Errorf("hash image: %w", err)
	}
	if written > maxApprovalImageBytes {
		return runtimeApprovalArtifact{}, fmt.Errorf("image exceeds %d bytes", maxApprovalImageBytes)
	}
	return runtimeApprovalArtifact{
		Role:      role,
		Path:      relativePath,
		SizeBytes: written,
		MediaType: mediaType,
		SHA256:    "sha256:" + hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func validRuntimeApprovalSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func runtimeApprovalHash(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
