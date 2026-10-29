package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/platform/config"
)

func TestAutomaticRepairRuntimeRequiresTrustedDispatcherAndManagedWorkspace(t *testing.T) {
	original := agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged, ConfigRef: agentCoreRuntimeProfileID}
	principal := identity.Principal{SubjectProvider: "transport", Subject: automaticRepairDispatcherID,
		UserID: automaticRepairDispatcherID, ViewerKind: identity.ViewerKindSystem, TenantID: "tenant-a"}
	root := t.TempDir()
	request := discussionsession.DiscussionAgentMentionRequest{
		ConversationKind: "direct", ConversationOwnerID: automaticRepairDispatcherID, ConversationTenantID: "tenant-a",
		SourceMessage: discussionsession.MessageDTO{AuthorID: automaticRepairDispatcherID},
		TargetAgent:   discussionsession.ConversationMemberDTO{ID: defaultOntologyStewardAgentID},
		Workspace:     discussionsession.DiscussionWorkspaceContext{RootDir: root, AgentWorkDir: filepath.Join(root, "agent"), AgentID: defaultOntologyStewardAgentID},
	}
	trusted := func(p identity.Principal) context.Context {
		return identity.ContextWithTrustedInternalService(identity.ContextWithPrincipal(context.Background(), p))
	}
	cfg := repairRuntimeTestConfig(t, "55m", "runtime-codex-datou")
	if got := automaticRepairRuntimeTimeout(trusted(principal), cfg, request, 20*time.Minute); got != 55*time.Minute {
		t.Fatalf("repair timeout = %v", got)
	}
	if got := automaticRepairRuntimeTimeout(trusted(principal), repairRuntimeTestConfig(t, "", "runtime-codex-datou"), request, 20*time.Minute); got != 20*time.Minute {
		t.Fatalf("unconfigured repair timeout = %v", got)
	}
	got := automaticRepairRuntimeSpec(trusted(principal), "runtime-codex-datou", request, original)
	if got.Kind != agentcore.RuntimeKindCodex || got.ConfigRef != "runtime-codex-datou" {
		t.Fatalf("repair runtime = %+v", got)
	}
	for _, name := range []string{"not-trusted", "user", "other-principal", "other-tenant", "other-owner", "other-author", "other-agent", "group", "other-workspace-agent", "outside-workspace", "no-workspace", "disabled"} {
		t.Run(name, func(t *testing.T) {
			p, req, ref := principal, request, "runtime-codex-datou"
			switch name {
			case "user":
				p.ViewerKind = identity.ViewerKindUser
			case "other-principal":
				p.Subject = "other"
			case "other-tenant":
				p.TenantID = "tenant-b"
			case "other-owner":
				req.ConversationOwnerID = "other"
			case "other-author":
				req.SourceMessage.AuthorID = "other"
			case "other-agent":
				req.TargetAgent.ID = "other"
			case "group":
				req.ConversationKind = "group"
			case "other-workspace-agent":
				req.Workspace.AgentID = "other"
			case "outside-workspace":
				req.Workspace.AgentWorkDir = filepath.Join(root, "..", "outside")
			case "no-workspace":
				req.Workspace.RootDir = ""
			case "disabled":
				ref = ""
			}
			ctx := trusted(p)
			if name == "not-trusted" {
				ctx = identity.ContextWithPrincipal(context.Background(), p)
			}
			if actual := automaticRepairRuntimeSpec(ctx, ref, req, original); !reflect.DeepEqual(actual, original) {
				t.Fatalf("ordinary request selected repair runtime: %+v", actual)
			}
			timeoutConfig := cfg
			if name == "disabled" {
				timeoutConfig = repairRuntimeTestConfig(t, "55m", "")
			}
			if actual := automaticRepairRuntimeTimeout(ctx, timeoutConfig, req, 20*time.Minute); actual != 20*time.Minute {
				t.Fatalf("ordinary request selected repair timeout: %v", actual)
			}
		})
	}
}

func repairRuntimeTestConfig(t *testing.T, timeout, ref string) config.Config {
	t.Helper()
	data, err := os.ReadFile("../../config/orrery.development.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "repair.yaml")
	extra := "\nmaintenance:\n  repair_runtime_config_ref: " + ref + "\n"
	if timeout != "" {
		extra += "  repair_timeout: " + timeout + "\n"
	}
	if err := os.WriteFile(path, append(data, []byte(extra)...), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadYAMLWithSecrets(path, config.SecretValues{DatabasePassword: "isolated-test-password"})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestAutomaticRepairRuntimeRejectsInvalidTimeoutConfig(t *testing.T) {
	data, err := os.ReadFile("../../config/orrery.development.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, timeout := range []string{"0s", "-1m", "91m", "invalid"} {
		t.Run(timeout, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "repair.yaml")
			if err := os.WriteFile(path, append(data, []byte("\nmaintenance:\n  repair_timeout: "+timeout+"\n")...), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := config.LoadYAMLWithSecrets(path, config.SecretValues{DatabasePassword: "isolated-test-password"}); err == nil {
				t.Fatal("invalid maintenance timeout was accepted")
			}
		})
	}
}

func TestAutomaticRepairRuntimeConfigAcceptsExplicitProfileReference(t *testing.T) {
	data, err := os.ReadFile("../../config/orrery.development.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "repair.yaml")
	if err := os.WriteFile(path, append(data, []byte("\nmaintenance:\n  repair_runtime_config_ref: runtime-codex-datou\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadYAMLWithSecrets(path, config.SecretValues{DatabasePassword: "isolated-test-password"})
	if err != nil {
		t.Fatal(err)
	}
	if got := configString(cfg, "maintenance.repair_runtime_config_ref"); got != "runtime-codex-datou" {
		t.Fatalf("repair config = %q", got)
	}
}
