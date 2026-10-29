package notification

import (
	"context"
	"testing"

	"orrery_v2/internal/app/scheduler"
)

func TestReportPushPreservesAgentTextWithoutPlatformPrefixOrLink(t *testing.T) {
	s, tasks, _, gateway, _, v := fixture(t)
	publishRule(t, s, v, "all")
	if _, err := s.SavePreferences(context.Background(), v, Preferences{Channels: []string{"weixin", "feishu"}}); err != nil {
		t.Fatal(err)
	}
	addRun(s, tasks, "partial", scheduler.RunStatusFailed, true)
	const body = "已核对 3 个来源，其中 1 个无法读取；当前结论只覆盖另外 2 个。"
	tasks.runs[0].Result.AgentReport.Body = body
	tasks.runs[0].Result.Summary = "platform internal receipt"
	later(s)
	mustSweep(t, s)
	if len(gateway.requests) == 0 {
		t.Fatal("Agent report was not queued")
	}
	for _, request := range gateway.requests {
		if request.Body != body || request.Title != "" {
			t.Fatalf("Agent report changed: %+v", request)
		}
	}
	for _, delivery := range gateway.deliveries {
		delivery.Body = "platform substitute"
		if allowed, _ := s.ValidateDelivery(context.Background(), delivery); allowed {
			t.Fatal("rewritten content passed send guard")
		}
	}
}

func TestReportPushWithholdsSystemSummaryAndKeepsPersonalAssistantAsOnlyWeixinRelay(t *testing.T) {
	for _, missingReport := range []bool{true, false} {
		t.Run(map[bool]string{true: "missing_report", false: "one_weixin_relay"}[missingReport], func(t *testing.T) {
			s, tasks, _, gateway, _, v := fixture(t)
			publishRule(t, s, v, "all")
			if _, err := s.SavePreferences(context.Background(), v, Preferences{Channels: []string{"weixin", "feishu"}}); err != nil {
				t.Fatal(err)
			}
			addRun(s, tasks, "report", scheduler.RunStatusCompleted, false)
			if missingReport {
				tasks.runs[0].Result.AgentReport = nil
			} else {
				s.SetExternalReportRelay(func(event Event, human, channel string) bool {
					return event.OriginTenant == v.TenantID && human == v.HumanID && channel == "weixin"
				})
			}
			later(s)
			mustSweep(t, s)
			for _, request := range gateway.requests {
				if missingReport || (request.HumanProfileID == v.HumanID && request.ChannelKeys[0] == "weixin") {
					t.Fatalf("unexpected duplicate/system message: %+v", request)
				}
			}
			if !missingReport && mustInbox(t, s, v).Total != 1 {
				t.Fatal("reserving external relay removed in-app report")
			}
			if !missingReport && (len(gateway.requests) != 1 || gateway.requests[0].ChannelKeys[0] != "feishu") {
				t.Fatal("reserving Weixin relay blocked an unrelated channel")
			}
		})
	}
}
