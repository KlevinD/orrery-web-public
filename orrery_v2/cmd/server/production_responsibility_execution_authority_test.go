package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	runtimesession "orrery_v2/internal/agent/runtime/session"
	"orrery_v2/internal/platform/execution"
)

func TestProductionResponsibilityExecutionAuthorityReadsOnlyLiveBoundSession(t *testing.T) {
	broker := runtimesession.NewWorkspaceBroker()
	registry := runtimesession.NewConnectionRegistryWithOptions(runtimesession.ConnectionRegistryOptions{Workspaces: broker})
	envelope := &agentcore.ExecutionEnvelope{TenantID: "tenant-1", RunID: "direct-run", AgentID: "agent-1", AgentProfileID: "profile-1", AllowedToolIDs: []string{"knowledge_search"}}
	if err := envelope.Seal(); err != nil {
		t.Fatal(err)
	}
	binding, err := broker.Bind(context.Background(), agentcore.RuntimeRequest{RunID: "direct-run", AgentID: "agent-1", TenantID: "tenant-1", ExecutionEnvelope: envelope, RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	authority := productionResponsibilityExecutionAuthority{jobs: staticResponsibilityExecutionJobs{}, sessions: registry}
	resolved, err := authority.ResolveResponsibilityExecution(context.Background(), "direct-run")
	if err != nil || resolved == nil || resolved.Envelope == nil || resolved.TenantID != "tenant-1" || resolved.AgentProfileID != "profile-1" || resolved.Envelope.Verify() != nil {
		t.Fatalf("live authority = %+v, %v", resolved, err)
	}
	resolved.Envelope.AllowedToolIDs[0] = "unauthorized_write"
	resolved, _ = authority.ResolveResponsibilityExecution(context.Background(), "direct-run")
	if resolved.Envelope.AllowedToolIDs[0] != "knowledge_search" {
		t.Fatal("caller changed bound authority")
	}
	unknown, err := authority.ResolveResponsibilityExecution(context.Background(), "other-run")
	if err != nil || unknown != nil {
		t.Fatalf("unknown run = %+v, %v", unknown, err)
	}
	broker.Release(binding.Ref)
	closed, err := authority.ResolveResponsibilityExecution(context.Background(), "direct-run")
	if err != nil || closed != nil {
		t.Fatalf("closed session still authorized: %+v, %v", closed, err)
	}
}

func TestProductionResponsibilityExecutionAuthorityRejectsAmbiguousSession(t *testing.T) {
	broker := runtimesession.NewWorkspaceBroker()
	registry := runtimesession.NewConnectionRegistryWithOptions(runtimesession.ConnectionRegistryOptions{Workspaces: broker})
	envelope := &agentcore.ExecutionEnvelope{TenantID: "tenant-1", RunID: "same-run", AgentID: "agent-1", AgentProfileID: "profile-1"}
	if err := envelope.Seal(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		_, err := broker.Bind(context.Background(), agentcore.RuntimeRequest{RunID: "same-run", AgentID: "agent-1", TenantID: "tenant-1", ExecutionEnvelope: envelope, RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: t.TempDir()}})
		if err != nil {
			t.Fatal(err)
		}
	}
	authority := productionResponsibilityExecutionAuthority{jobs: staticResponsibilityExecutionJobs{}, sessions: registry}
	resolved, err := authority.ResolveResponsibilityExecution(context.Background(), "same-run")
	if err != nil || resolved != nil {
		t.Fatalf("ambiguous authority = %+v, %v", resolved, err)
	}
}

func TestProductionResponsibilityExecutionAuthorityReadsCanonicalRunContext(t *testing.T) {
	run := runtimedaemon.AgentRun{
		RunID: "run-1", AgentID: "agent-1", AgentProfileID: "profile-1", AgentInstanceID: "instance-1",
		Request: agentcore.RuntimeRequest{TenantID: "tenant-1", ContextPack: agentcore.ContextPack{ResponsibilityContext: &agentcore.ResponsibilityContext{
			ContextID: "context-1", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 2,
			PolicyID: "policy-1", PolicyVersion: 4, AgentProfileID: "profile-1", AllowedTools: []string{"repo.read"},
		}}},
	}
	job, err := runtimedaemon.ExecutionJobFromAgentRun(run)
	if err != nil {
		t.Fatal(err)
	}
	authority := productionResponsibilityExecutionAuthority{jobs: staticResponsibilityExecutionJobs{job: job, found: true}}

	resolved, err := authority.ResolveResponsibilityExecution(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("ResolveResponsibilityExecution: %v", err)
	}
	if resolved == nil || resolved.RunID != "run-1" || resolved.TenantID != "tenant-1" || resolved.AgentID != "agent-1" || resolved.AgentProfileID != "profile-1" ||
		resolved.Context == nil || resolved.Context.ContextID != "context-1" || resolved.Context.PolicyVersion != 4 || len(resolved.Context.AllowedTools) != 1 || resolved.Context.AllowedTools[0] != "repo.read" {
		t.Fatalf("resolved responsibility = %+v", resolved)
	}
	resolved.Context.AllowedTools[0] = "repo.write"
	if run.Request.ContextPack.ResponsibilityContext.AllowedTools[0] != "repo.read" {
		t.Fatalf("resolved context aliases queued run: %+v", run.Request.ContextPack.ResponsibilityContext)
	}
}

func TestProductionResponsibilityExecutionAuthorityTreatsUnknownRunAsNonResponsibility(t *testing.T) {
	authority := productionResponsibilityExecutionAuthority{jobs: staticResponsibilityExecutionJobs{}}
	resolved, err := authority.ResolveResponsibilityExecution(context.Background(), "discussion-run")
	if err != nil || resolved != nil {
		t.Fatalf("ResolveResponsibilityExecution = %+v, %v", resolved, err)
	}
}

func TestProductionResponsibilityExecutionAuthorityTreatsQueuedRunWithoutContextAsNonResponsibility(t *testing.T) {
	run := runtimedaemon.AgentRun{
		RunID: "run-ordinary", AgentID: "agent-1", AgentProfileID: "profile-1", AgentInstanceID: "instance-1",
		Request: agentcore.RuntimeRequest{TenantID: "tenant-1"},
	}
	job, err := runtimedaemon.ExecutionJobFromAgentRun(run)
	if err != nil {
		t.Fatal(err)
	}
	authority := productionResponsibilityExecutionAuthority{jobs: staticResponsibilityExecutionJobs{job: job, found: true}}

	resolved, err := authority.ResolveResponsibilityExecution(context.Background(), "run-ordinary")
	if err != nil || resolved != nil {
		t.Fatalf("ResolveResponsibilityExecution = %+v, %v", resolved, err)
	}
}

type staticResponsibilityExecutionJobs struct {
	job   execution.Job
	found bool
	err   error
}

func (s staticResponsibilityExecutionJobs) Get(context.Context, string) (execution.Job, bool, error) {
	return s.job, s.found, s.err
}
