package main

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	emailchannel "orrery_v2/internal/gateway/channel/email"
	gwconfig "orrery_v2/internal/gateway/config"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type fakeProductionEmailConfigLoader struct {
	record gwconfig.Record
	err    error
}

func (f fakeProductionEmailConfigLoader) LoadOrDefault() (gwconfig.Record, error) {
	return f.record, f.err
}

type fakeProductionEmailProfileStore struct {
	profile agentcore.AgentProfile
	err     error
}

func (f fakeProductionEmailProfileStore) GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	return f.profile, f.err
}

type recordingProductionEmailSender struct {
	options emailchannel.Options
	message emailchannel.Message
	count   int
	receipt emailchannel.Receipt
	err     error
}

func (s *recordingProductionEmailSender) Send(_ context.Context, options emailchannel.Options, message emailchannel.Message) (emailchannel.Receipt, error) {
	s.options = options
	s.message = message
	s.count++
	return s.receipt, s.err
}

func TestProductionEmailToolSendsFromAgentMailboxAndDeduplicates(t *testing.T) {
	sender := &recordingProductionEmailSender{receipt: emailchannel.Receipt{MessageID: "message-1@example.test"}}
	executor := newProductionEmailToolExecutor(
		fakeProductionEmailConfigLoader{record: configuredEmailRecord()},
		fakeProductionEmailProfileStore{profile: configuredEmailProfile()},
		sender,
	)
	req := mcptransport.EmailToolRequest{
		TenantID: "org-1", PrincipalID: "agent-1", AgentID: "agent-1", ProfileID: "profile-1",
		To: []string{" user@example.test ", "user@example.test"}, Cc: []string{"reviewer@example.test"},
		Subject: " Status ", Text: "Plain body", HTML: "<p>HTML body</p>", IdempotencyKey: "mail-1",
	}
	first, err := executor.ExecuteEmailTool(context.Background(), "email_send", req)
	if err != nil {
		t.Fatalf("ExecuteEmailTool: %v", err)
	}
	second, err := executor.ExecuteEmailTool(context.Background(), "email_send", req)
	if err != nil {
		t.Fatalf("ExecuteEmailTool duplicate: %v", err)
	}
	if sender.count != 1 {
		t.Fatalf("send count = %d, want 1", sender.count)
	}
	if sender.message.From != "agent@example.test" || len(sender.message.To) != 1 || sender.message.Subject != "Status" {
		t.Fatalf("message = %+v", sender.message)
	}
	if sender.options.Username != "smtp-user" || sender.options.Password != "smtp-password" {
		t.Fatalf("SMTP options = %+v", sender.options)
	}
	if first.Output["message_id"] != "message-1@example.test" || second.Output["message_id"] != first.Output["message_id"] {
		t.Fatalf("results = first:%+v second:%+v", first.Output, second.Output)
	}
}

func TestProductionEmailToolRejectsMissingMailboxAndTenantMismatch(t *testing.T) {
	base := configuredEmailProfile()
	for name, profile := range map[string]agentcore.AgentProfile{
		"missing mailbox": func() agentcore.AgentProfile { p := base; p.Email = ""; return p }(),
		"missing tenant":  base,
		"tenant mismatch": base,
	} {
		t.Run(name, func(t *testing.T) {
			sender := &recordingProductionEmailSender{}
			executor := newProductionEmailToolExecutor(fakeProductionEmailConfigLoader{record: configuredEmailRecord()}, fakeProductionEmailProfileStore{profile: profile}, sender)
			tenantID := "org-1"
			if name == "missing tenant" {
				tenantID = ""
			} else if name == "tenant mismatch" {
				tenantID = "org-2"
			}
			_, err := executor.ExecuteEmailTool(context.Background(), "email_send", mcptransport.EmailToolRequest{
				TenantID: tenantID, ProfileID: "profile-1", To: []string{"user@example.test"}, Subject: "Status", Text: "Body",
			})
			if err == nil {
				t.Fatal("ExecuteEmailTool error = nil")
			}
			if (name == "missing tenant" || name == "tenant mismatch") && !errors.Is(err, mcptransport.ErrPermissionDenied) {
				t.Fatalf("error = %v, want permission denied", err)
			}
			if sender.count != 0 {
				t.Fatalf("send count = %d, want 0", sender.count)
			}
		})
	}
}

func configuredEmailRecord() gwconfig.Record {
	return gwconfig.Record{Config: gwconfig.Config{Adapters: gwconfig.AdaptersConfig{Email: gwconfig.PlatformAdapterConfig{
		Enabled: true, APIKey: "smtp-user", Token: "smtp-password",
		Extra: map[string]any{"smtpHost": "smtp.example.test", "smtpPort": 587, "smtpTLS": "starttls"},
	}}}}
}

func configuredEmailProfile() agentcore.AgentProfile {
	return agentcore.AgentProfile{
		ID: "profile-1", Email: "agent@example.test", Enabled: true,
		OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "org-1"}},
	}
}
