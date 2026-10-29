package hil

import (
	"fmt"
	"strings"
)

type MarketingWechatApprovalResolution struct {
	TenantID           string   `json:"tenant_id"`
	GenerationID       string   `json:"generation_id"`
	AccountID          string   `json:"account_id"`
	DecisionID         string   `json:"decision_id"`
	WorkflowInstanceID string   `json:"workflow_instance_id"`
	ReceiptID          string   `json:"receipt_id"`
	Outcome            string   `json:"outcome"`
	ArgsHash           string   `json:"args_hash"`
	MaterialHash       string   `json:"material_hash"`
	ContentHash        string   `json:"content_hash"`
	AssetHashes        []string `json:"asset_hashes"`
	Actor              ActorRef `json:"actor"`
	Reason             string   `json:"reason,omitempty"`
	SubmissionID       string   `json:"submission_id,omitempty"`
	ProjectID          string   `json:"project_id,omitempty"`
	ContentRevision    int      `json:"content_revision,omitempty"`
	BundleHash         string   `json:"bundle_hash,omitempty"`
}

func BuildMarketingWechatApprovalResolution(resolution DecisionResolution) (MarketingWechatApprovalResolution, error) {
	preview := resolution.Decision.PayloadPreview
	generationID := strings.TrimSpace(resolution.Decision.Resource.ID)
	if value, _ := preview["generation_id"].(string); strings.TrimSpace(value) != generationID || generationID == "" {
		return MarketingWechatApprovalResolution{}, fmt.Errorf("Marketing WeChat approval generation binding is invalid")
	}
	outcome := "rejected"
	if resolution.Decision.Status == DecisionDenied && resolution.Receipt.Effect == EffectDeny && resolution.Receipt.ResolverSummary["lifecycle_outcome"] == "superseded" {
		outcome = "superseded"
	} else if resolution.Decision.Status == DecisionResolved && resolution.Receipt.Effect == EffectAuthorize && resolution.Receipt.SingleUse {
		outcome = "approved"
	} else if resolution.Decision.Status == DecisionPending && resolution.Receipt.Effect == EffectRequiresApproval && !resolution.Receipt.SingleUse {
		outcome = "changes_requested"
	} else if resolution.Decision.Status != DecisionDenied || resolution.Receipt.Effect != EffectDeny {
		return MarketingWechatApprovalResolution{}, fmt.Errorf("Marketing WeChat approval resolution is not terminal")
	} else if resolution.Actor.Kind == ActorService && strings.Contains(strings.ToLower(resolution.Reason), "deadline") {
		outcome = "expired"
	}
	accountID, _ := preview["account_id"].(string)
	contentHash, _ := preview["content_hash"].(string)
	assetHashes := productionMarketingWechatApprovalStrings(preview["asset_hashes"])
	if strings.TrimSpace(accountID) == "" || strings.TrimSpace(contentHash) == "" || resolution.Receipt.ArgsHash != resolution.Decision.ArgsHash {
		return MarketingWechatApprovalResolution{}, fmt.Errorf("Marketing WeChat approval resolution hashes are invalid")
	}
	return MarketingWechatApprovalResolution{
		TenantID: resolution.Decision.TenantID, GenerationID: generationID, AccountID: strings.TrimSpace(accountID),
		DecisionID: resolution.Decision.ID, WorkflowInstanceID: resolution.Receipt.WorkflowInstanceID, ReceiptID: resolution.Receipt.ID,
		Outcome: outcome, ArgsHash: resolution.Receipt.ArgsHash, MaterialHash: resolution.Receipt.MaterialHash,
		ContentHash: strings.TrimSpace(contentHash), AssetHashes: assetHashes, Actor: resolution.Actor, Reason: strings.TrimSpace(resolution.Reason),
		SubmissionID: marketingPreviewText(preview, "submission_id"), ProjectID: marketingPreviewText(preview, "project_id"),
		ContentRevision: marketingPreviewInt(preview["content_revision"]), BundleHash: marketingPreviewText(preview, "bundle_hash"),
	}, nil
}

func marketingPreviewText(preview map[string]any, key string) string {
	value, _ := preview[key].(string)
	return value
}
func marketingPreviewInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return 0
	}
}

func productionMarketingWechatApprovalStrings(value any) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
	}
}
