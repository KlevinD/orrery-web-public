package hil

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMarketingWechatVersionedSubmissionReplaysExactBundleAndReplacesOldApproval(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "marketing-versioned-request")
	defer closeStore()
	ids := &sequenceHILIDs{}
	now := testRepositoryNow
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: ids, Clock: func() time.Time { return now }, Assignees: fakeWorkflowAssigneeResolver{results: map[string][]ActorRef{
		"requester_manager:": {{Kind: ActorHuman, ID: "manager"}}, "people_role:approval.reviewer": {{Kind: ActorHuman, ID: "reviewer"}},
	}}})
	installDistinctApprovalTemplate(t, ctx, workflow, "tenant-1", MarketingWechatDraftApprovalActionType)
	creator := NewService(repo, fixedContextProvider{snapshot: OntologyContextSnapshot{BundleID: "bundle-1", Complete: true}}, ids, WithClock(func() time.Time { return now }), WithWorkflowService(workflow))
	service := NewMarketingWechatApprovalService(creator, repo, func() time.Time { return now })
	input := MarketingWechatApprovalInput{TenantID: "tenant-1", RequesterID: "marketing", GenerationID: "run-1", AccountID: "account-1", ContentHash: "sha256:content", AssetHashes: []string{"sha256:cover", "sha256:body1", "sha256:body2"}, Material: completeBusinessApprovalMaterial(), IdempotencyKey: "legacy-1"}
	old, err := service.RequestMarketingWechatApproval(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	input.SubmissionID = "submission-2"
	input.ProjectID = "project-1"
	input.ContentRevision = 2
	input.BundleHash = "sha256:" + strings.Repeat("a", 64)
	input.SupersedesDecisionID = old.DecisionID
	input.IdempotencyKey = "submission-2"
	revised, err := service.RequestMarketingWechatApproval(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if revised.DecisionID == old.DecisionID {
		t.Fatal("new content reused the old decision")
	}
	now = now.Add(time.Minute)
	replay, err := service.RequestMarketingWechatApproval(ctx, input)
	if err != nil || replay.DecisionID != revised.DecisionID {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	input.BundleHash = "sha256:" + strings.Repeat("b", 64)
	if _, err := service.RequestMarketingWechatApproval(ctx, input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed material was not rejected: %v", err)
	}
	prior, err := repo.GetWorkflowInstanceByDecisionID(ctx, "tenant-1", old.DecisionID)
	if err != nil || prior.Instance.Status != WorkflowSuperseded {
		t.Fatalf("old state: %+v %v", prior, err)
	}
	latest, err := repo.GetWorkflowInstanceByDecisionID(ctx, "tenant-1", revised.DecisionID)
	if err != nil || latest.Instance.Status != WorkflowRunning || latest.Decision.PayloadPreview["bundle_hash"] != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("new state: %+v %v", latest, err)
	}
	page, err := repo.ListWorkflowInstances(ctx, WorkflowListQuery{TenantID: "tenant-1"})
	if err != nil || page.Total != 2 {
		t.Fatalf("duplicate workflows: %+v %v", page, err)
	}
}

func TestMarketingWechatApprovalServiceCreatesFixedBusinessWorkflow(t *testing.T) {
	creator := &recordingMarketingWechatApprovalCreator{result: CreateDecisionResult{Decision: DecisionRequest{
		ID: "decision-marketing-1", Status: DecisionPending, Route: RouteDecision{Mode: ModeApproval},
	}}}
	reader := &recordingMarketingWechatApprovalWorkflowReader{detail: WorkflowInstanceDetail{
		Instance: WorkflowInstance{ID: "workflow-marketing-1", Revision: 1},
	}}
	service := NewMarketingWechatApprovalService(creator, reader, func() time.Time { return testRepositoryNow })

	result, err := service.RequestMarketingWechatApproval(context.Background(), MarketingWechatApprovalInput{
		TenantID: "system", RequesterID: "marketing", GenerationID: "scheduled-run-1", AccountID: "wechat-main",
		ContentHash: "sha256:content", AssetHashes: []string{" sha256:z-cover ", "sha256:a-inline"},
		Material: BusinessApprovalMaterial{
			Title: " 公众号文章草稿审批 ", DecisionRequest: "是否批准将本期文章写入公众号草稿箱？",
			BusinessContext: "文章和图片已经完成质量检查。", ProposedAction: "将冻结内容写入公众号草稿箱。",
			ExpectedResult: "审批后生成一份可继续人工检查的公众号草稿。", ImpactScope: "公众号主账号草稿箱，不公开发布。",
			Risks: []BusinessApprovalRisk{{Description: "内容表述可能不准确。", Mitigation: "审批前检查冻结预览，草稿生成后仍不公开发布。"}},
		},
		Artifacts:    []BusinessApprovalArtifact{{Label: "文章预览", URL: "https://marketing.example.test/generations/scheduled-run-1", Kind: "document"}},
		EvidenceRefs: []string{"generation:scheduled-run-1"}, IdempotencyKey: "wechat-draft-approval:scheduled-run-1",
	})
	if err != nil {
		t.Fatalf("RequestMarketingWechatApproval: %v", err)
	}
	if creator.calls != 1 || reader.calls != 1 {
		t.Fatalf("creator calls=%d reader calls=%d", creator.calls, reader.calls)
	}
	input := creator.input
	if input.Action != MarketingWechatDraftApprovalActionType || input.ActionTypeID != MarketingWechatDraftApprovalActionType ||
		input.Requester != (ActorRef{Kind: ActorService, ID: "marketing"}) ||
		input.Resource != (ObjectRef{Kind: MarketingWechatGenerationResourceKind, ID: "scheduled-run-1"}) {
		t.Fatalf("fixed approval identity = %+v", input)
	}
	if !input.ApprovalRequired || input.HILRequired || input.CouncilRequired || input.RiskLevel != RiskHigh ||
		input.ExpiresAt != testRepositoryNow.Add(7*24*time.Hour) {
		t.Fatalf("fixed approval policy = %+v", input)
	}
	if input.ArgsHash == "" || input.BusinessKey != "marketing-wechat-draft-approval:scheduled-run-1" {
		t.Fatalf("approval binding = %+v", input)
	}
	material, ok := input.PayloadPreview["business_approval_material"].(map[string]any)
	if !ok || material["title"] != "公众号文章草稿审批" || input.PayloadPreview["generation_id"] != "scheduled-run-1" ||
		input.PayloadPreview["content_hash"] != "sha256:content" {
		t.Fatalf("approval payload = %#v", input.PayloadPreview)
	}
	assetHashes, ok := input.PayloadPreview["asset_hashes"].([]string)
	if !ok || len(assetHashes) != 2 || assetHashes[0] != "sha256:z-cover" || assetHashes[1] != "sha256:a-inline" {
		t.Fatalf("ordered asset hashes = %#v", input.PayloadPreview["asset_hashes"])
	}
	if result.DecisionID != "decision-marketing-1" || result.WorkflowInstanceID != "workflow-marketing-1" ||
		result.Status != DecisionPending || result.MaterialHash == "" || result.Revision != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestMarketingWechatApprovalServiceRejectsUnboundHashes(t *testing.T) {
	creator := &recordingMarketingWechatApprovalCreator{}
	service := NewMarketingWechatApprovalService(creator, &recordingMarketingWechatApprovalWorkflowReader{}, nil)
	_, err := service.RequestMarketingWechatApproval(context.Background(), MarketingWechatApprovalInput{
		TenantID: "system", RequesterID: "marketing", GenerationID: "scheduled-run-1", AccountID: "wechat-main",
		ContentHash: "content-without-algorithm", Material: completeBusinessApprovalMaterial(), IdempotencyKey: "approval-1",
	})
	if err == nil || creator.calls != 0 {
		t.Fatalf("error=%v creator calls=%d", err, creator.calls)
	}
}

type recordingMarketingWechatApprovalCreator struct {
	calls  int
	input  CreateDecisionInput
	result CreateDecisionResult
}

func (c *recordingMarketingWechatApprovalCreator) CreateDecision(_ context.Context, input CreateDecisionInput) (CreateDecisionResult, error) {
	c.calls++
	c.input = input
	return c.result, nil
}

type recordingMarketingWechatApprovalWorkflowReader struct {
	calls  int
	detail WorkflowInstanceDetail
}

func (r *recordingMarketingWechatApprovalWorkflowReader) GetWorkflowInstanceByDecisionID(_ context.Context, tenantID, decisionID string) (WorkflowInstanceDetail, error) {
	r.calls++
	return r.detail, nil
}
