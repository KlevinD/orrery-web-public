package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
)

func TestScheduledResultNormalizerKeepsAgentReportSeparateFromReceipt(t *testing.T) {
	const body = "今天核对了 3 份资料，发现 1 处价格过期。请按新版报价更新。"
	input := agentcore.AgentInput{RunID: "runtime-a", Task: agentcore.TaskInput{ID: "task-a", MissionID: "mission-a"}, ContextPack: agentcore.ContextPack{AgentID: "agent-a"}, RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{"run_source": schedulerAgentTaskRunSource}}}
	response := agentcore.RuntimeResponse{Status: agentcore.RuntimeStatusCompleted, Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: "private notes outside the public report\n" + scheduledFeishuPublicSummaryFence + "\n" + body + "\n```\n```orrery-agent-result\n{\"version\":1,\"outcome\":\"completed\",\"summary\":\"internal receipt\",\"deliverables\":[],\"evidence\":[]}\n```"}}}
	result, err := (missionTaskAgentResultNormalizer{}).NormalizeContext(context.Background(), input, response, time.Now())
	if err != nil || result.UserReport != body || result.Summary.Text != "internal receipt" {
		t.Fatalf("report lost or rewritten: %+v %v", result, err)
	}
	// A worker recovering a committed result has no runtime response. It must
	// still relay this report instead of regenerating or rerunning the task.
	run := runtimedaemon.AgentRun{RunID: input.RunID, AgentID: "agent-a", TaskID: "task-a", MissionID: "mission-a"}
	recovered, err := scheduledRunResultFromCommittedAgentResult(run, result)
	if err != nil || recovered.AgentReport == nil || recovered.AgentReport.Body != body {
		t.Fatalf("recovery lost Agent report: %+v %v", recovered, err)
	}
	got := scheduledAgentDeliveryRunResult(run, recovered, agentcore.RuntimeResponse{})
	if got.Summary != body {
		t.Fatal("restart replaced Agent report")
	}
}

func TestScheduledResultReportRequiresFinalPublicAgentOutput(t *testing.T) {
	for name, output := range map[string]string{"empty": "", "private": "<think>secret</think>", "machine": "```orrery-agent-result\n{}\n```", "unterminated": "```orrery-public-summary\nnot yet finished", "ambiguous": "```orrery-public-summary\none\n```\n```orrery-public-summary\ntwo\n```"} {
		t.Run(name, func(t *testing.T) {
			response := agentcore.RuntimeResponse{Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: output}}}
			if got := scheduledAgentFinalReport(response); got != "" {
				t.Fatalf("fabricated public report: %q", got)
			}
		})
	}
	response := agentcore.RuntimeResponse{Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Content: "unfinished intermediate explanation"}}}
	if got := scheduledAgentFinalReport(response); got != "" {
		t.Fatalf("intermediate text leaked: %q", got)
	}
}
