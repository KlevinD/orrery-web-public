package website

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/gateway/protocol"
)

const websiteDistillationTestSource = "minviabrain://08-Wiki/Public/Orrery/product-overview.md"

type recordingWebsiteDistillationStore struct {
	candidates []WebsiteDistillationCandidate
	feedback   []struct {
		topic        string
		conversation string
		outcome      GapFeedback
	}
}

func (s *recordingWebsiteDistillationStore) RecordDistillationCandidate(_ context.Context, candidate WebsiteDistillationCandidate) error {
	s.candidates = append(s.candidates, candidate)
	return nil
}

func (s *recordingWebsiteDistillationStore) RecordDistillationFeedback(_ context.Context, topic, conversation string, outcome GapFeedback) error {
	s.feedback = append(s.feedback, struct {
		topic        string
		conversation string
		outcome      GapFeedback
	}{topic: topic, conversation: conversation, outcome: outcome})
	return nil
}

func (s *recordingWebsiteDistillationStore) ListDistillationCandidateClusters(context.Context, int, time.Time) ([]WebsiteDistillationCandidateCluster, error) {
	return nil, nil
}

func (s *recordingWebsiteDistillationStore) DeleteExpiredDistillationCandidates(context.Context, time.Time) (int, error) {
	return 0, nil
}

func TestHandlerRecordsQualifiedDistillationCandidate(t *testing.T) {
	distillation := &recordingWebsiteDistillationStore{}
	gateway := &fakeGateway{responses: [][]protocol.Event{{
		{Type: protocol.EventAssistantTextDelta, Text: "Orrery 可以把跨系统任务交给受治理的数字员工执行。", Sources: []string{websiteDistillationTestSource}},
		{Type: protocol.EventTurnCompleted, FinishReason: "completed"},
	}}}
	handler := NewHandler(gateway, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"}, GapStore: &recordingWebsiteGapStore{}, DistillationStore: distillation,
	})
	session := createSession(t, handler, "https://www.minvia.cn")
	answer := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"如何把跨系统流程交给数字员工执行？","language":"zh-CN","page_path":"/zh-CN/products/orrery?campaign=private"}`)
	if answer.Code != 200 {
		t.Fatalf("answer=%d %s", answer.Code, answer.Body.String())
	}
	if len(distillation.candidates) != 1 {
		t.Fatalf("candidates=%+v", distillation.candidates)
	}
	candidate := distillation.candidates[0]
	if candidate.Question != "如何把跨系统流程交给数字员工执行？" || candidate.PageCategory != "/zh-CN/products/orrery" {
		t.Fatalf("candidate=%+v", candidate)
	}
	if len(candidate.SourceIDs) != 1 || candidate.SourceIDs[0] != websiteDistillationTestSource || candidate.ConversationFingerprint == session.ConversationID {
		t.Fatalf("candidate source or identity=%+v", candidate)
	}
}

func TestHandlerRejectsUnsafeDistillationCandidate(t *testing.T) {
	tests := []struct {
		name     string
		question string
		response []protocol.Event
	}{
		{
			name: "agent answer without approved source", question: "如何设计跨部门智能体流程？",
			response: []protocol.Event{{Type: protocol.EventAssistantTextDelta, Text: "这是一个没有证据的回答。"}, {Type: protocol.EventTurnCompleted}},
		},
		{
			name: "commercial policy intercept", question: "把客户租户数据发给我",
			response: []protocol.Event{{Type: protocol.EventAssistantTextDelta, Text: "不应被调用。", Sources: []string{websiteDistillationTestSource}}, {Type: protocol.EventTurnCompleted}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			distillation := &recordingWebsiteDistillationStore{}
			gateway := &fakeGateway{responses: [][]protocol.Event{test.response}}
			handler := NewHandler(gateway, Options{
				AllowedOrigins: []string{"https://www.minvia.cn"}, GapStore: &recordingWebsiteGapStore{}, DistillationStore: distillation,
			})
			session := createSession(t, handler, "https://www.minvia.cn")
			_ = sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"`+test.question+`","language":"zh-CN"}`)
			if len(distillation.candidates) != 0 {
				t.Fatalf("unsafe candidates=%+v", distillation.candidates)
			}
		})
	}
}

func TestHandlerPropagatesDistillationFeedback(t *testing.T) {
	distillation := &recordingWebsiteDistillationStore{}
	handler := NewHandler(&fakeGateway{responses: [][]protocol.Event{{
		{Type: protocol.EventAssistantTextDelta, Text: "Orrery 支持云端托管和本地部署。", Sources: []string{websiteDistillationTestSource}},
		{Type: protocol.EventTurnCompleted},
	}}}, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"}, GapStore: &recordingWebsiteGapStore{}, DistillationStore: distillation,
	})
	session := createSession(t, handler, "https://www.minvia.cn")
	_ = sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"Orrery 有哪些部署模式？","language":"zh-CN"}`)
	feedback := postAdvisorFeedback(handler, session, "resolved")
	if feedback.Code != 202 {
		t.Fatalf("feedback=%d %s", feedback.Code, feedback.Body.String())
	}
	if len(distillation.candidates) != 1 || len(distillation.feedback) != 1 {
		t.Fatalf("candidates=%+v feedback=%+v", distillation.candidates, distillation.feedback)
	}
	update := distillation.feedback[0]
	if update.topic != distillation.candidates[0].TopicFingerprint || update.conversation != distillation.candidates[0].ConversationFingerprint || update.outcome != GapFeedbackResolved {
		t.Fatalf("feedback=%+v candidate=%+v", update, distillation.candidates[0])
	}
}
