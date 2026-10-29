package pi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestDeterministicMarketingContinuationDispatchesApprovedDraftExactlyOnce(t *testing.T) {
	checkpoint, opts := marketingContinuationTestFixture(t)
	configureMarketingContinuationSigningKey(t)
	calls := make(chan runtimecore.RuntimeToolCallRequest, 2)
	dispatcher := runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		calls <- call
		return runtimecore.RuntimeToolCallResult{Output: map[string]any{
			"status": "created", "provider_media_id": "wechat-media-1", "receipt": "wechat-receipt-1",
		}}, nil
	})
	session := openDeterministicMarketingContinuationSession(context.Background(), NativeConfig{
		Runtime: Config{ToolDispatcher: dispatcher}, EventCapacity: 32, BackpressureWait: time.Second, Now: time.Now,
	}, Config{ToolDispatcher: dispatcher}, agentcore.SessionSpec{SessionID: "marketing-continuation-session"}, agentcore.SessionConfig{}, NativeWorkspace{}, opts)
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	receipt, err := session.Dispatch(context.Background(), piNativeSend("approved-draft", "client-approved-draft", "continue"))
	if err != nil || receipt.TurnGeneration != 1 {
		t.Fatalf("dispatch = %#v, %v", receipt, err)
	}
	events := receivePiTerminal(t, session.Events(), 1)
	terminal := piTerminal(events)
	if terminal == nil || terminal.IsError || !strings.Contains(terminal.ResultText, "wechat-media-1") {
		t.Fatalf("terminal = %#v, events = %#v", terminal, events)
	}
	select {
	case call := <-calls:
		if call.Name != "mcp.marketing.marketing_wechat_draft_create" || call.Input["intent_id"] != checkpoint.IntentID ||
			call.Input["share_thumbnail_path"] != checkpoint.ShareThumbnailPath {
			t.Fatalf("runtime call = %#v", call)
		}
		receiptMeta, ok := call.RequestMeta[marketingHILReceiptMetaKey].(map[string]any)
		if !ok || receiptMeta["signature"] == "" || receiptMeta["intent_id"] != checkpoint.IntentID {
			t.Fatalf("HIL receipt = %#v", call.RequestMeta[marketingHILReceiptMetaKey])
		}
	default:
		t.Fatal("approved draft tool was not dispatched")
	}
	if _, err := session.Dispatch(context.Background(), piNativeSend("approved-draft-replay", "client-replay", "continue again")); err == nil {
		t.Fatal("single-use approved continuation accepted a replay")
	}
	select {
	case replay := <-calls:
		t.Fatalf("unexpected replay call = %#v", replay)
	default:
	}
}

func TestDeterministicMarketingContinuationRejectsChangedApprovedMaterial(t *testing.T) {
	checkpoint, opts := marketingContinuationTestFixture(t)
	if err := os.WriteFile(checkpoint.ArticleMDPath, []byte("changed after approval"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := deterministicMarketingContinuationInput(opts)
	if err == nil || !strings.Contains(err.Error(), "material hash") {
		t.Fatalf("changed material error = %v", err)
	}
}

func TestDeterministicMarketingContinuationDoesNotClaimOrdinaryPiRuns(t *testing.T) {
	if isDeterministicMarketingContinuation(runtimecore.ExecOptions{ProviderSpecific: map[string]string{
		"approval_continuation": "true", "approval_decision": "approved",
	}}) {
		t.Fatal("ordinary PI continuation was claimed by deterministic marketing mode")
	}
}

func TestNativeConnectionClaimsApprovedMarketingContinuationBeforeLaunchingPi(t *testing.T) {
	_, opts := marketingContinuationTestFixture(t)
	request := agentcore.RuntimeRequest{
		TenantID: opts.TenantID, RunID: opts.RunID, AgentID: opts.AgentID, ToolBindings: opts.ToolBindings,
		RuntimeOptions: agentcore.RuntimeOptions{
			WorkingDirectory: opts.Cwd, WorkspacePolicy: opts.WorkspacePolicy,
			ProviderSpecific: opts.ProviderSpecific,
		},
	}
	connection, err := NewNativeConnection(NativeConfig{
		Runtime: Config{Executable: filepath.Join(t.TempDir(), "pi-must-not-launch")},
		ResolveWorkspace: NativeWorkspaceResolverFunc(func(context.Context, string) (NativeWorkspace, error) {
			return NativeWorkspace{Request: request}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	backend, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{
		Mode: agentcore.SessionOpenFresh, SessionID: "approved-marketing-open-session",
	}, agentcore.SessionConfig{WorkspaceRef: "approved-marketing-workspace"})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	session, ok := backend.(*NativeSession)
	if !ok || !session.deterministicMarketingContinuation || session.binding != nil {
		t.Fatalf("backend = %#v, want deterministic continuation without PI process", backend)
	}
}

func TestNativeConnectionFailsClosedForIncompleteMarketingContinuation(t *testing.T) {
	_, opts := marketingContinuationTestFixture(t)
	delete(opts.ProviderSpecific, "approval_action_id")
	request := agentcore.RuntimeRequest{
		TenantID: opts.TenantID, RunID: opts.RunID, AgentID: opts.AgentID, ToolBindings: opts.ToolBindings,
		RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: opts.Cwd, WorkspacePolicy: opts.WorkspacePolicy, ProviderSpecific: opts.ProviderSpecific},
	}
	connection, err := NewNativeConnection(NativeConfig{ResolveWorkspace: NativeWorkspaceResolverFunc(func(context.Context, string) (NativeWorkspace, error) {
		return NativeWorkspace{Request: request}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	_, err = connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "invalid-marketing-open-session"}, agentcore.SessionConfig{WorkspaceRef: "invalid-marketing-workspace"})
	if err == nil || !strings.Contains(err.Error(), "approved marketing continuation metadata") {
		t.Fatalf("OpenSession error = %v, want fail-closed metadata error", err)
	}
}

func marketingContinuationTestFixture(t *testing.T) (marketingContinuationCheckpoint, runtimecore.ExecOptions) {
	t.Helper()
	workspaceRoot := filepath.Join(t.TempDir(), "shared", "workspaces", "jordan")
	articleDir := filepath.Join(workspaceRoot, "wechat", "articles", "approved")
	checkpointDir := filepath.Join(workspaceRoot, "wechat", "publish-intents")
	if err := os.MkdirAll(filepath.Join(articleDir, "imgs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(checkpointDir, 0o700); err != nil {
		t.Fatal(err)
	}
	articlePath := filepath.Join(articleDir, "article.md")
	coverPath := filepath.Join(articleDir, "cover.png")
	bodyOne := filepath.Join(articleDir, "imgs", "one.png")
	bodyTwo := filepath.Join(articleDir, "imgs", "two.png")
	for path, content := range map[string]string{
		articlePath: "# Approved article\n\nFrozen content.", coverPath: "cover-image", bodyOne: "body-one", bodyTwo: "body-two",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	checkpoint := marketingContinuationCheckpoint{
		IntentID: "intent-approved", AccountID: "wechat-main", Status: "pending_review", Operation: "create_draft",
		IntentHash: strings.Repeat("a", 64), Title: "Approved title", Author: "Jordan", Digest: "Approved digest",
		ArticleMDPath: articlePath, CoverPath: coverPath, ShareThumbnailPath: coverPath,
		BodyImagePaths: []string{bodyTwo, bodyOne}, DraftOnly: true,
	}
	workspace, err := newMarketingContinuationWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	material, err := buildMarketingContinuationMaterial(workspace, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkpointDir, checkpoint.IntentID+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	binding := agentcore.RuntimeToolBinding{
		ToolID: "mcp.marketing.marketing_wechat_draft_create", Alias: "mcp_marketing_marketing_wechat_draft_create",
		Source: "external_mcp", ServerToolID: "mcp.marketing", DownstreamToolName: marketingContinuationTool,
	}
	return checkpoint, runtimecore.ExecOptions{
		TenantID: "system", RunID: "run-approved", AgentID: "jordan", ToolBindings: []agentcore.RuntimeToolBinding{binding},
		WorkspacePolicy: agentcore.RuntimeWorkspacePolicy{Root: workspaceRoot, WorkDir: workspaceRoot}, Cwd: workspaceRoot,
		ProviderSpecific: map[string]string{
			"approval_continuation": "true", "approval_decision": "approved", "approval_action_id": "approve_once",
			"approval_id": "approval-event-1", "approval_decided_by": "human-ding", "approval_decided_at": "2026-08-21T08:00:00Z",
			"hil_decision_id": "hil-decision-1", "hil_receipt_id": "hil-receipt-1", "hil_receipt_effect": "authorize",
			"approval_material_kind": "wechat_draft", "approval_material_hash": material.MaterialHash,
			"approval_material_account_id": checkpoint.AccountID, "approval_material_intent_id": checkpoint.IntentID,
			"approval_material_intent_hash":      checkpoint.IntentHash,
			runtimecore.ApprovalMetadataToolName: "mcp_marketing_marketing_wechat_draft_create",
		},
	}
}

func configureMarketingContinuationSigningKey(t *testing.T) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "marketing-hil.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(marketingHILSigningKeyIDEnv, "orrery-test")
	t.Setenv(marketingHILPrivateKeyPathEnv, path)
}
