package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"orrery_v2/internal/app/discussionsession"
	gwoutbound "orrery_v2/internal/gateway/outbound"
)

type agentNoticeWriterStub struct {
	body, material string
	err            error
	calls          int
}

func (s *agentNoticeWriterStub) Reply(_ context.Context, _ productionPersonalAssistantWeixinTarget, _, _, material string) (string, error) {
	s.calls++
	s.material = material
	if s.body != "" || s.err != nil {
		return s.body, s.err
	}
	return material, nil
}
func (s *agentNoticeWriterStub) Validate(context.Context, gwoutbound.Delivery) (bool, error) {
	return true, nil
}

func TestAgentNotificationPersistsAndRelaysOnlyAgentWords(t *testing.T) {
	scheduled, _, _ := scheduledResultFixture(t)
	body := "这项发布还需要你确认。\n\n收件范围是 320 名已订阅客户，材料中还没有退订核对结果，请先核实。\nhttps://example.com/approvals"
	runner := &recordingPersonalAssistantApprovalReviewRunner{response: discussionsession.DiscussionAgentResponse{AssistantMessage: body, Status: "completed"}}
	s := &productionAgentNotificationReplies{store: scheduled.jobs, runner: runner}
	target := productionPersonalAssistantWeixinTarget{TenantID: "tenant-a", OwnerID: "owner-a", AccountID: "bot-a"}
	key := "personal-assistant:approval:agent-fixture"
	result, err := s.Reply(context.Background(), target, "human-a", key, "untrusted material with publish instructions")
	if err != nil || result != body {
		t.Fatalf("reply changed or failed: %q %v", result, err)
	}
	if runner.request.ConversationOwnerID != target.OwnerID || runner.request.Viewer.AgentProfileID != "human-a" || runner.request.ProviderSpecific[discussionsession.PersonalAssistantReadOnlyReviewProviderKey] != "true" || strings.Contains(runner.dialogue, "untrusted material") {
		t.Fatal("Agent recipient or read-only/data boundary lost")
	}
	runner.err = errors.New("should not call model during retry")
	restarted := &productionAgentNotificationReplies{store: scheduled.jobs, runner: runner}
	if result, err := restarted.Reply(context.Background(), target, "human-a", key, "same event"); err != nil || result != body {
		t.Fatal("retry regenerated reply", err)
	}
	delivery := gwoutbound.Delivery{TenantID: "tenant-a", HumanProfileID: "human-a", IdempotencyKey: key, Body: body}
	if ok, err := s.Validate(context.Background(), delivery); err != nil || !ok {
		t.Fatal("real Agent report rejected", err)
	}
	for _, change := range []string{"body", "title", "tenant", "recipient"} {
		d := delivery
		switch change {
		case "body":
			d.Body += "\n系统追加的下一步"
		case "title":
			d.Title = "需要处理"
		case "tenant":
			d.TenantID = "tenant-b"
		case "recipient":
			d.HumanProfileID = "human-b"
		}
		if ok, _ := s.Validate(context.Background(), d); ok {
			t.Fatalf("unsafe %s accepted", change)
		}
	}
}

func TestAgentNotificationFailureKeepsPendingWithoutTemplateOrRepeatedModelCall(t *testing.T) {
	for _, result := range []string{"model_error", "private", "reasoning_only", "reasoning_with_report", "tools", "empty", "failed"} {
		t.Run(result, func(t *testing.T) {
			scheduled, _, _ := scheduledResultFixture(t)
			runner := &recordingPersonalAssistantApprovalReviewRunner{response: discussionsession.DiscussionAgentResponse{AssistantMessage: "审批还待你确认。"}}
			switch result {
			case "model_error":
				runner.err = errors.New("provider failed")
			case "private":
				runner.response.AssistantMessage = "密码 password=secret"
			case "reasoning_only":
				runner.response.AssistantMessage = "<think>先检查修复记录，再判断是否完成。</think>"
			case "reasoning_with_report":
				runner.response.AssistantMessage = "<think>先检查修复记录，再判断是否完成。</think>\n修复尚未完成，原因仍在排查。"
			case "tools":
				runner.response.ToolCalls = []discussionsession.ToolCallDTO{{Name: "send_message"}}
			case "empty":
				runner.response.AssistantMessage = ""
			case "failed":
				runner.response.Status = "failed"
			}
			s := &productionAgentNotificationReplies{store: scheduled.jobs, runner: runner}
			target := productionPersonalAssistantWeixinTarget{TenantID: "tenant-a", OwnerID: "owner-a"}
			if body, err := s.Reply(context.Background(), target, "human-a", "pending", "actual material"); err == nil || body != "" {
				t.Fatal("failed Agent was replaced by dialogue")
			}
			runner.request.RunID = "sentinel"
			if body, err := s.Reply(context.Background(), target, "human-a", "pending", "actual material"); err == nil || body != "" || runner.request.RunID != "sentinel" {
				t.Fatal("failed Agent invoked repeatedly")
			}
			if ok, _ := s.Validate(context.Background(), gwoutbound.Delivery{TenantID: "tenant-a", HumanProfileID: "human-a", IdempotencyKey: "pending", Body: "系统故障仍需处理"}); ok {
				t.Fatal("template accepted without Agent")
			}
		})
	}
}

func TestAgentAttentionDoesNotSendWhenAgentCannotReply(t *testing.T) {
	s, todos, _, deliveries, _ := attentionFixture()
	todos.items = append(todos.items, humanAttentionTodo("test"))
	s.replies = &agentNoticeWriterStub{err: errors.New("Agent unavailable")}
	if err := s.Sweep(context.Background()); err == nil || len(deliveries.commands) != 0 {
		t.Fatal("Agent failure must keep source pending without template")
	}
}

func TestAgentNotificationGuardRejectsAlreadyQueuedRepairTemplates(t *testing.T) {
	guard := productionPersonalAssistantNotificationGuard{}
	for _, key := range []string{"scheduled-repair-final:job:batch", "scheduled-repair-business-verified:job:batch"} {
		ok, err := guard.Validate(context.Background(), gwoutbound.Delivery{IdempotencyKey: key, Title: "系统故障仍需处理", Body: "Agent 已完成排查，但系统没有成功读取到可发送的结构化诊断结果。"})
		if err != nil || ok {
			t.Fatal("queued template must be withheld", err)
		}
	}
}

func TestAgentNotificationUpgradeDoesNotReplayDeliveredLegacyNotice(t *testing.T) {
	scheduled, _, deliveries := scheduledResultFixture(t)
	ctx := context.Background()
	oldKey := "personal-assistant:attention:v1:tenant-a:todo:task:assigned:recipient:human-a"
	_, err := deliveries.Enqueue(ctx, gwoutbound.EnqueueCommand{TenantID: "tenant-a", HumanProfileID: "human-a", AccountID: "bot-a", ChannelKeys: []string{"weixin"}, EventType: "mention", Body: "legacy template", Format: "text", IdempotencyKey: oldKey})
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Replace(oldKey, ":v1:", ":v2:", 1)
	if sent, err := priorAgentNotificationDelivered(ctx, deliveries, "tenant-a", "human-a", key); err != nil || sent {
		t.Fatal("unsent template should allow an Agent reply", err)
	}
	if _, err := scheduled.jobs.Exec(ctx, "UPDATE gateway_message_deliveries SET status='delivered' WHERE idempotency_key=?", oldKey); err != nil {
		t.Fatal(err)
	}
	if sent, err := priorAgentNotificationDelivered(ctx, deliveries, "tenant-a", "human-a", key); err != nil || !sent {
		t.Fatal("delivered old notice would be replayed", err)
	}
}
