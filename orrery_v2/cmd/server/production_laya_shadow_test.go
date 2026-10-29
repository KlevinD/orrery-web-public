package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/decision/laya"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type serverLayaJudge struct{ states chan []string }

func (j serverLayaJudge) Choose(ctx context.Context, s []string, q laya.Question) ([]laya.Decision, error) {
	j.states <- s
	out := make([]laya.Decision, len(s))
	for i := range out {
		out[i].Choice = "insufficient"
	}
	return out, nil
}

func TestLayaRSIProductionWiringPersistsFullEvidenceAndAssessment(t *testing.T) {
	ctx := context.Background()
	registry, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: "memory://" + t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	var logs bytes.Buffer
	rt := &platform.Runtime{Databases: registry, Logger: logging.NewJSONLogger(&logs, logging.LevelInfo)}
	requests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		io.WriteString(w, `{"results":[{"answers":{"judgment":{"type":"choice","choice":"tool_usage","confidence":0.5,"answer_confidence":0.8,"probabilities":{"tool_usage":0.8,"environment":0.05,"skill_defect":0.05,"insufficient":0.1}}}}]}`)
	}))
	defer server.Close()
	keyPath := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyPath, []byte("test-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORRERY_LAYA_MODE", "shadow")
	t.Setenv("ORRERY_LAYA_URL", server.URL)
	t.Setenv("ORRERY_LAYA_API_KEY_FILE", keyPath)
	observer, closeObserver, err := newProductionLayaShadow(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	defer closeObserver()
	_, dispatcher := newProductionRuntimeTooling(rt, nil, nil, nil, nil, nil, observer)
	call := runtimecore.RuntimeToolCallRequest{TenantID: "tenant", AgentID: "agent", RunID: "run", CallID: "call", Name: "query_order", Input: map[string]any{"customer_id": "PRIVATE_INPUT"}, ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: "query_order", Alias: "query_order"}}}
	result := runtimecore.RuntimeToolCallResult{Output: map[string]any{"status": "failed", "error_code": "invalid_arguments", "error": "order_id is required " + strings.Repeat("x", 300) + "PRIVATE_TAIL", "details": map[string]any{"nested": "PRIVATE_DETAILS"}}}
	before, _ := json.Marshal(result)
	dispatcher.observeLayaFailure(ctx, call, result)
	select {
	case body := <-requests:
		if strings.Contains(body, "PRIVATE") {
			t.Fatal("full evidence reached model")
		}
	case <-time.After(time.Second):
		t.Fatal("missing inference request")
	}
	samples, err := productionRSISampleStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := resourceadmin.RSILearningScope("tenant", "agent")
	deadline := time.Now().Add(time.Second)
	for {
		page, err := samples.List(ctx, scope, "", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 1 && page.Items[0].Assessment != nil {
			item := page.Items[0]
			if item.Assessment.Decisions[0].Choice != "tool_usage" || !strings.Contains(string(item.Sample.Evidence), "PRIVATE_DETAILS") || !strings.Contains(string(item.Sample.Evidence), "PRIVATE_INPUT") || !strings.Contains(item.Sample.Summary, "PRIVATE_TAIL") {
				t.Fatalf("incomplete learning observation: %+v", item)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("assessment not persisted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	closeObserver()
	after, _ := json.Marshal(result)
	if !bytes.Equal(before, after) || strings.Contains(logs.String(), "PRIVATE") {
		t.Fatal("changed result or leaked evidence into logs")
	}
}

type layaGateway struct {
	result mcptransport.CallToolResult
	err    error
}

func TestLayaRSILiveCanary(t *testing.T) {
	keyFile := os.Getenv("ORRERY_LAYA_CANARY_KEY_FILE")
	if keyFile == "" {
		t.Skip("explicit local Laya canary credentials are required")
	}
	ctx := context.Background()
	dbURL := "file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "rsi-canary.db"))
	registry, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: dbURL})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	rt := &platform.Runtime{Databases: registry, Logger: logging.NewJSONLogger(io.Discard, logging.LevelInfo)}
	t.Setenv("ORRERY_LAYA_MODE", "shadow")
	t.Setenv("ORRERY_LAYA_URL", "http://127.0.0.1:18490")
	t.Setenv("ORRERY_LAYA_API_KEY_FILE", keyFile)
	observer, closeObserver, err := newProductionLayaShadow(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	defer closeObserver()
	gateway, dispatcher := newProductionRuntimeTooling(rt, nil, nil, nil, nil, nil, observer)
	gateway.gateway = layaGateway{err: errors.New("temporary network timeout")}
	call := runtimecore.RuntimeToolCallRequest{
		TenantID: "rsi-deployment-canary", AgentID: "rsi-canary-agent", RunID: "rsi-canary-run", CallID: "rsi-canary-call",
		Name: "Knowledge", Input: map[string]any{"query": "deployment-canary-evidence"},
		ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: toolstd.KnowledgeSearch, Alias: "Knowledge", GrantedPermissions: []string{toolstd.PermissionKnowledgeRead}}},
	}
	result, err := dispatcher.CallRuntimeTool(ctx, call)
	if err != nil || outputString(result.Output, "status") != "failed" {
		t.Fatalf("runtime failure boundary changed: result=%+v error=%v", result, err)
	}
	samples, err := productionRSISampleStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := resourceadmin.RSILearningScope(call.TenantID, call.AgentID.String())
	deadline := time.Now().Add(8 * time.Second)
	for {
		page, err := samples.List(ctx, scope, "", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 1 && page.Items[0].Assessment != nil {
			item := page.Items[0]
			if item.Assessment.Fallback != "" || len(item.Assessment.Decisions) != 1 || !strings.Contains(string(item.Sample.Evidence), "deployment-canary-evidence") {
				t.Fatalf("incomplete live classification: %+v", item.Assessment)
			}
			t.Logf("real Laya runtime canary persisted: observation=%s category=%s", item.ID, item.Assessment.Decisions[0].Choice)
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("live runtime classification did not persist")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (g layaGateway) CallTool(context.Context, mcptransport.CallToolRequest) (mcptransport.CallToolResult, error) {
	return g.result, g.err
}

func TestLayaModeOnlyAllowsExplicitShadow(t *testing.T) {
	t.Setenv("ORRERY_LAYA_MODE", "off")
	o, close, err := newProductionLayaShadow(context.Background(), nil)
	if err != nil || o != nil {
		t.Fatal("off mode initialized Laya")
	}
	close()
	t.Setenv("ORRERY_LAYA_MODE", "active")
	if _, _, err := newProductionLayaShadow(context.Background(), nil); err == nil {
		t.Fatal("accepted active decision mode")
	}
	t.Setenv("ORRERY_LAYA_MODE", "shadow")
	t.Setenv("ORRERY_LAYA_API_KEY_FILE", "")
	if _, _, err := newProductionLayaShadow(context.Background(), nil); err == nil {
		t.Fatal("accepted missing credentials")
	}
}

func TestLayaMCPShadowOnlySeesAuthorizedOutputAndPreservesIt(t *testing.T) {
	states := make(chan []string, 2)
	records := make(chan laya.Record, 2)
	o := laya.NewObserver(context.Background(), serverLayaJudge{states}, func(r laya.Record) { records <- r })
	defer o.Close()
	output := mcptransport.CallToolResult{Output: map[string]any{"results": []any{map[string]any{"document_id": "doc-1", "node_id": "node-1", "content": "authorized content"}}}}
	ref := &productionMCPGatewayRef{gateway: layaGateway{result: output}, layaShadow: o}
	req := mcptransport.CallToolRequest{TenantID: "tenant-a", PrincipalID: "agent-a", ToolID: websitePublicKnowledgeSearchToolID, Arguments: map[string]any{"query": "question"}}
	got, err := ref.CallTool(context.Background(), req)
	if err != nil || !reflect.DeepEqual(got, output) {
		t.Fatal("changed MCP output")
	}
	select {
	case <-states:
	case <-time.After(time.Second):
		t.Fatal("missing MCP observation")
	}
	<-records
	ref.gateway = layaGateway{result: output, err: errors.New("permission denied")}
	if _, err := ref.CallTool(context.Background(), req); err == nil {
		t.Fatal("lost authorization failure")
	}
	select {
	case <-states:
		t.Fatal("denied output reached model")
	default:
	}
}

func TestLayaRSIObservesBoundFailuresWithoutModifyingResult(t *testing.T) {
	states := make(chan []string, 1)
	records := make(chan laya.Record, 1)
	o := laya.NewObserver(context.Background(), serverLayaJudge{states}, func(r laya.Record) { records <- r })
	defer o.Close()
	d := productionRuntimeToolDispatcher{layaShadow: o}
	call := runtimecore.RuntimeToolCallRequest{TenantID: "tenant-a", AgentID: "agent-a", RunID: "run-a", CallID: "call-a", Name: "bound-tool", ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: "bound-tool", Alias: "bound-tool"}}}
	result := runtimecore.RuntimeToolCallResult{Output: map[string]any{"status": "failed", "error_code": "invalid_arguments", "error": "order_id is required"}}
	d.observeLayaFailure(context.Background(), call, result)
	select {
	case <-states:
	case <-time.After(time.Second):
		t.Fatal("failure not observed")
	}
	r := <-records
	if r.Kind != "rsi_sample" || r.Mode != "shadow" || len(r.Sources) != 1 || result.Output["status"] != "failed" {
		t.Fatalf("incorrect sample %v", r)
	}
	call.Name = "unbound-tool"
	d.observeLayaFailure(context.Background(), call, result)
	select {
	case <-states:
		t.Fatal("unbound tool reached RSI")
	default:
	}
}
