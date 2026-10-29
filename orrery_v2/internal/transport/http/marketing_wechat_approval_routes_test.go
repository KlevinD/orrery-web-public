package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"orrery_v2/internal/app/hil"
)

func TestMarketingWechatBusinessApprovalRouteRequiresDedicatedBearer(t *testing.T) {
	service := &recordingMarketingWechatApprovalHTTPService{}
	router := NewRouter(RouterOptions{
		MarketingWechatApproval: MarketingWechatApprovalRoute{Service: service, Authorization: "Bearer marketing-secret"},
	})
	for _, authorization := range []string{"", "Bearer wrong-secret"} {
		req := httptest.NewRequest(http.MethodPost, "/api/business-approvals/marketing-wechat-drafts", bytes.NewReader(validMarketingWechatApprovalBody(t)))
		req.Header.Set("Authorization", authorization)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("authorization %q status = %d body=%s", authorization, response.Code, response.Body.String())
		}
	}
	if service.calls != 0 {
		t.Fatalf("service calls = %d, want 0", service.calls)
	}
}

func TestMarketingWechatBusinessApprovalRouteRejectsUnknownFields(t *testing.T) {
	service := &recordingMarketingWechatApprovalHTTPService{}
	router := NewRouter(RouterOptions{
		MarketingWechatApproval: MarketingWechatApprovalRoute{Service: service, Authorization: "Bearer marketing-secret"},
	})
	body := map[string]any{}
	if err := json.Unmarshal(validMarketingWechatApprovalBody(t), &body); err != nil {
		t.Fatal(err)
	}
	body["approval_assignee_id"] = "attacker-selected"
	encoded, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/business-approvals/marketing-wechat-drafts", bytes.NewReader(encoded))
	req.Header.Set("Authorization", "Bearer marketing-secret")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, service.calls, response.Body.String())
	}
}

func TestMarketingWechatBusinessApprovalRouteCreatesFormalApproval(t *testing.T) {
	service := &recordingMarketingWechatApprovalHTTPService{result: hil.MarketingWechatApprovalResult{
		DecisionID: "decision-1", WorkflowInstanceID: "workflow-1", Status: hil.DecisionPending,
		MaterialHash: "sha256:material", Revision: 1,
	}}
	router := NewRouter(RouterOptions{
		MarketingWechatApproval: MarketingWechatApprovalRoute{Service: service, Authorization: "Bearer marketing-secret"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/business-approvals/marketing-wechat-drafts", bytes.NewReader(validMarketingWechatApprovalBody(t)))
	req.Header.Set("Authorization", "Bearer marketing-secret")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if service.calls != 1 || service.input.TenantID != "system" || service.input.RequesterID != "marketing" ||
		service.input.GenerationID != "scheduled-run-1" || service.input.ContentHash != "sha256:content" {
		t.Fatalf("service call=%d input=%+v", service.calls, service.input)
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["decision_id"] != "decision-1" || result["workflow_instance_id"] != "workflow-1" || result["status"] != "pending" {
		t.Fatalf("response = %+v", result)
	}
}

type recordingMarketingWechatApprovalHTTPService struct {
	calls  int
	input  hil.MarketingWechatApprovalInput
	result hil.MarketingWechatApprovalResult
	err    error
}

func (s *recordingMarketingWechatApprovalHTTPService) GetMarketingWechatApprovalStatus(_ context.Context, tenant, requester, decision string) (hil.MarketingWechatApprovalStatus, error) {
	s.calls++
	s.input = hil.MarketingWechatApprovalInput{TenantID: tenant, RequesterID: requester, GenerationID: decision}
	return hil.MarketingWechatApprovalStatus{DecisionID: decision, WorkflowInstanceID: "workflow-1", Status: "pending"}, s.err
}

func TestMarketingWechatStatusRouteUsesOwnerAndDedicatedCredential(t *testing.T) {
	service := &recordingMarketingWechatApprovalHTTPService{}
	router := NewRouter(RouterOptions{MarketingWechatApproval: MarketingWechatApprovalRoute{Service: service, Authorization: "Bearer marketing-secret"}})
	for _, authorized := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodGet, "/api/business-approvals/marketing-wechat-drafts/decision-1", nil)
		if authorized {
			req.Header.Set("Authorization", "Bearer marketing-secret")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if !authorized {
			if response.Code != 401 || service.calls != 0 {
				t.Fatalf("unauthorized=%d calls=%d", response.Code, service.calls)
			}
			continue
		}
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" || service.input.TenantID != "system" || service.input.RequesterID != "marketing" || service.input.GenerationID != "decision-1" {
			t.Fatalf("response=%d input=%+v", response.Code, service.input)
		}
	}
}

func (s *recordingMarketingWechatApprovalHTTPService) RequestMarketingWechatApproval(_ context.Context, input hil.MarketingWechatApprovalInput) (hil.MarketingWechatApprovalResult, error) {
	s.calls++
	s.input = input
	return s.result, s.err
}

func validMarketingWechatApprovalBody(t *testing.T) []byte {
	t.Helper()
	body := map[string]any{
		"generation_id": "scheduled-run-1", "account_id": "wechat-main", "content_hash": "sha256:content",
		"asset_hashes": []string{"sha256:cover"}, "evidence_refs": []string{"generation:scheduled-run-1"},
		"idempotency_key": "wechat-draft-approval:scheduled-run-1",
		"material": map[string]any{
			"language": "zh-CN", "title": "公众号文章草稿审批", "decision_request": "是否批准将本期文章写入公众号草稿箱？",
			"business_context": "文章和图片已经完成质量检查。", "proposed_action": "将冻结内容写入公众号草稿箱。",
			"expected_result": "生成一份可继续人工检查的公众号草稿。", "impact_scope": "公众号主账号草稿箱，不公开发布。",
			"risks": []map[string]string{{"description": "内容可能不准确。", "mitigation": "检查冻结预览，生成后仍不公开发布。"}},
		},
		"artifacts": []map[string]string{{"label": "文章预览", "url": "https://marketing.example.test/generations/scheduled-run-1", "kind": "document"}},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
