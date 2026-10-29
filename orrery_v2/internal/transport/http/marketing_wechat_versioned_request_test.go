package httptransport

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/hil"
)

func TestMarketingWechatVersionedRequestPreservesFrozenBinding(t *testing.T) {
	hash := "sha256:" + strings.Repeat("a", 64)
	service := &recordingMarketingWechatApprovalHTTPService{result: hil.MarketingWechatApprovalResult{
		DecisionID: "decision-versioned", WorkflowInstanceID: "workflow-versioned",
		Status: hil.DecisionPending, MaterialHash: hash, ArgsHash: hash, Revision: 1,
	}}
	router := NewRouter(RouterOptions{MarketingWechatApproval: MarketingWechatApprovalRoute{
		Service: service, Authorization: "Bearer marketing-secret",
	}})
	var body map[string]any
	if err := json.Unmarshal(validMarketingWechatApprovalBody(t), &body); err != nil {
		t.Fatal(err)
	}
	body["submission_id"] = "submission:immutable-1"
	body["project_id"] = "content-project:wechat:1"
	body["content_revision"] = 3
	body["bundle_hash"] = hash
	body["supersedes_decision_id"] = "decision-prior"
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/business-approvals/marketing-wechat-drafts", bytes.NewReader(encoded))
	req.Header.Set("Authorization", "Bearer marketing-secret")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if service.calls != 1 || service.input.TenantID != "system" || service.input.RequesterID != "marketing" ||
		service.input.SubmissionID != "submission:immutable-1" || service.input.ProjectID != "content-project:wechat:1" ||
		service.input.ContentRevision != 3 || service.input.BundleHash != hash || service.input.SupersedesDecisionID != "decision-prior" {
		t.Fatalf("lost version binding: %+v", service.input)
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["args_hash"] != hash {
		t.Fatalf("missing material argument binding: %+v", result)
	}
}
