package missioncontrol

import (
	"testing"

	"orrery_v2/internal/mission/projection"
)

func TestMissionProjectionHidesMachineOnlyAgentText(t *testing.T) {
	raw := "<think>inspect internal state</think>\n```orrery-agent-result\n{\"run_id\":\"secret\"}\n```"

	discussion := mapDiscussion(projection.DiscussionView{
		Messages: []projection.MessageView{{
			MessageID: "message-1", AuthorID: "agent-1", AuthorType: "agent", Body: raw,
			Visibility: []string{"mission"},
		}},
	}, projection.TeamView{}, ViewerRef{ViewerKind: ViewerKindSystem, PermissionScope: []string{"mission"}})
	if len(discussion.Messages) != 1 || discussion.Messages[0].Body != "任务进展已记录到 Orrery。" {
		t.Fatalf("discussion message = %+v", discussion.Messages)
	}

	blockers := mapBlockers([]projection.BlockerView{{BlockerID: "blocker-1", Summary: raw}})
	if len(blockers) != 1 || blockers[0].Summary != "阻塞原因已记录到 Orrery。" {
		t.Fatalf("blockers = %+v", blockers)
	}

	progress := mapProgress(projection.ProgressGraphView{Nodes: []projection.ProgressNodeView{{ID: "node-1", Details: raw}}})
	if len(progress.Nodes) != 1 || progress.Nodes[0].Details != "进度详情已记录到 Orrery。" {
		t.Fatalf("progress = %+v", progress.Nodes)
	}

	runtime := mapRuntimeSessions([]projection.RuntimeSessionView{{
		Status: projection.RuntimeSessionFailed, ErrorSummary: raw,
	}})
	if len(runtime) != 1 || runtime[0].ErrorSummary != "任务执行失败，详细原因已记录到 Orrery。" {
		t.Fatalf("runtime = %+v", runtime)
	}
}

func TestMissionProjectionPreservesReadableAgentSummary(t *testing.T) {
	readable := "已完成价格核验，共发现 2 项需要人工确认。"
	blockers := mapBlockers([]projection.BlockerView{{BlockerID: "blocker-1", Summary: readable}})
	if len(blockers) != 1 || blockers[0].Summary != readable {
		t.Fatalf("readable blocker = %+v", blockers)
	}
}
