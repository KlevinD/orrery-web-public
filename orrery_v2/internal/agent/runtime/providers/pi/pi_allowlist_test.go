package pi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestBuildArgsDisablesNativeToolsForExplicitAllowlist(t *testing.T) {
	for _, allowed := range []string{`["website_faq_catalog_list"]`, `[]`, `null`, `invalid`} {
		t.Run(allowed, func(t *testing.T) {
			args := BuildArgs("", runtimecore.ExecOptions{ProviderSpecific: map[string]string{
				agentcore.RuntimeAllowedToolIDsProviderKey: allowed,
			}})
			if !slices.Contains(args, "--no-builtin-tools") {
				t.Fatalf("native tools can bypass allowlist: %v", args)
			}
		})
	}
	if slices.Contains(BuildArgs("", runtimecore.ExecOptions{}), "--no-builtin-tools") {
		t.Fatal("unrestricted runs must retain their existing native tools")
	}
}

func TestPiAllowlistDoesNotExposeOrExecuteImplicitPlanTools(t *testing.T) {
	opts := runtimecore.ExecOptions{Cwd: t.TempDir(), Environment: map[string]string{"PI_CODING_AGENT_DIR": filepath.Join(t.TempDir(), "pi-home")},
		ToolBindings:     []agentcore.RuntimeToolBinding{{ToolID: "website_faq_catalog_list", Alias: "website_faq_catalog"}},
		ProviderSpecific: map[string]string{agentcore.RuntimeAllowedToolIDsProviderKey: `["website_faq_catalog_list"]`},
	}
	dispatcher := runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		t.Fatal("implicit plan tool reached the dispatcher")
		return runtimecore.RuntimeToolCallResult{}, nil
	})
	prepared, cleanup, err := prepareBridge(context.Background(), dispatcher, nil, opts)
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	var config piBridgeConfig
	if err := json.Unmarshal([]byte(readBridgeFile(t, prepared.ConfigPath)), &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Tools) != 1 || config.Tools[0].SourceToolID != "website_faq_catalog_list" {
		t.Fatalf("bridge implicitly exposed non-allowlisted tools: %+v", config.Tools)
	}
	bridge := newBridgeServer(context.Background(), dispatcher, nil, opts)
	request := httptest.NewRequest(http.MethodPost, "/tool", strings.NewReader(`{"tool":"plan_create","input":{}}`))
	response := httptest.NewRecorder()
	bridge.handleToolCall(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("implicit plan tool call status = %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/approve", strings.NewReader(`{"tool":"plan_create","input":{}}`))
	response = httptest.NewRecorder()
	bridge.handleApproval(response, request)
	if !strings.Contains(response.Body.String(), `"allow":false`) {
		t.Fatalf("implicit plan tool was approved: %s", response.Body.String())
	}
}

func TestPiAllowlistPreservesExplicitlyAuthorizedPlanTools(t *testing.T) {
	opts := runtimecore.ExecOptions{ProviderSpecific: map[string]string{
		agentcore.RuntimeAllowedToolIDsProviderKey: `["plan.create","plan.update","plan.complete"]`,
	}}
	for _, name := range []string{"plan_create", "plan_update", "plan_complete"} {
		if !piPlanToolAllowed(name, opts) {
			t.Fatalf("explicit plan binding %s was denied", name)
		}
	}
	opts.ProviderSpecific[agentcore.RuntimeAllowedToolIDsProviderKey] = `["plan.create"]`
	if piPlanToolAllowed("plan_update", opts) || piPlanToolAllowed("plan_complete", opts) {
		t.Fatal("one plan binding implicitly authorized other plan tools")
	}
}
