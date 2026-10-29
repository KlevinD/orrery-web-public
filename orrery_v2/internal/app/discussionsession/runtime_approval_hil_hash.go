package discussionsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func runtimeHILWechatMaterialArgsHash(req runtimecore.ApprovalRequest, metadata map[string]string) string {
	if strings.TrimSpace(req.ToolName) != wechatDraftApprovalTool {
		return ""
	}
	businessKey := strings.TrimSpace(metadata[approvalBusinessKeyMetadataKey])
	materialHash := strings.TrimSpace(metadata["approval_material_hash"])
	if businessKey == "" || !strings.HasPrefix(materialHash, "sha256:") || !validRuntimeApprovalSHA256(strings.TrimPrefix(materialHash, "sha256:")) {
		return ""
	}
	payload, err := json.Marshal(struct {
		Version      int    `json:"version"`
		Tool         string `json:"tool"`
		BusinessKey  string `json:"business_key"`
		MaterialHash string `json:"material_hash"`
	}{Version: 1, Tool: wechatDraftApprovalTool, BusinessKey: businessKey, MaterialHash: materialHash})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:])
}
