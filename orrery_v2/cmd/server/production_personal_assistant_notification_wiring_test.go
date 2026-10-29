package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
	devicedirectory "orrery_v2/internal/device/directory"
	"orrery_v2/internal/platform/testkit"
)

func TestPersonalAssistantNotificationWiringConnectsDelegationAndContext(t *testing.T) {
	for _, scenario := range []string{"enabled", "no_grants", "workers_disabled", "invalid_grants"} {
		t.Run(scenario, func(t *testing.T) {
			e, _, _, _, auth := delegatedApprovalFixture(t)
			raw, _ := json.Marshal(e.authority.grants)
			if scenario == "no_grants" {
				raw = nil
			}
			if scenario == "invalid_grants" {
				raw = []byte(`[{"allow_all":true}]`)
			}
			t.Setenv("ORRERY_PERSONAL_ASSISTANT_APPROVAL_DELEGATIONS", string(raw))
			t.Setenv("ORRERY_PERSONAL_ASSISTANT_ATTENTION_RECIPIENTS", "")
			rt := testkit.NewTestRuntime(t)
			runner := newProductionPersonalAssistantDiscussionAgentRunner(nil, nil, nil)
			w := productionPersonalAssistantNotificationWiring{
				runtime: rt, core: e.core, profiles: e.authority.profiles, accounts: auth, scopes: auth,
				discussions: e.notices, runner: runner, hil: e.decisions, workflow: hil.NewPlatformRepository(e.core),
			}
			err := w.Prepare(context.Background(), scenario == "workers_disabled")
			if scenario == "invalid_grants" {
				if err == nil {
					t.Fatal("unsafe configuration accepted at startup")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (w.delegations != nil) != (scenario == "enabled") {
				t.Fatal("delegation enablement not wired")
			}
			provider, ok := runner.notificationContext.(*productionPersonalAssistantNotificationContext)
			if !ok || provider.jobs == nil || provider.approvals == nil || provider.delegations != w.delegations {
				t.Fatal("private notification context not connected to production runner")
			}
			if w.delegations != nil && (w.delegations.completion == nil || w.delegations.settings == nil || w.delegations.decisions != e.decisions) {
				t.Fatal("delegation reviewer or HIL service not connected")
			}
		})
	}
}

type assistantNotificationContextFunc func(context.Context, discussionsession.DiscussionAgentRequest) (string, error)

func (f assistantNotificationContextFunc) NotificationContext(ctx context.Context, request discussionsession.DiscussionAgentRequest) (string, error) {
	return f(ctx, request)
}

func TestPersonalAssistantNotificationContextReachesManagedAndDeviceRuntimes(t *testing.T) {
	for _, mode := range []string{"managed", "device"} {
		t.Run(mode, func(t *testing.T) {
			managed, device := &recordingPersonalAssistantRunner{}, &recordingPersonalAssistantRunner{}
			selected := managed
			view := devicedirectory.PersonalAssistantView{AssistantEnabled: true, HostingMode: devicedirectory.HostingModeManaged}
			if mode == "device" {
				view, selected = ownedPersonalAssistantView(devicedirectory.AgentStatusOnline, false), device
			}
			runner := newProductionPersonalAssistantDiscussionAgentRunner(managed, device, &recordingPersonalAssistantDirectory{view: view}, &recordingPersonalAssistantRunnerResolver{runner: selected})
			calls := 0
			runner.setNotificationContext(assistantNotificationContextFunc(func(ctx context.Context, request discussionsession.DiscussionAgentRequest) (string, error) {
				calls++
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 4*time.Second || request.ConversationKind != discussionsession.ConversationKindPersonalAssistant {
					t.Fatal("context lookup is unbounded or entered a group")
				}
				if calls == 1 {
					return "notice-current-pending", nil
				}
				if calls == 2 {
					return "notice-current-done", nil
				}
				return "", context.DeadlineExceeded
			}))
			request := personalAssistantRuntimeRequest()
			if _, err := runner.RunDiscussion(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if selected.request.PersonalAssistantNotificationContext != "notice-current-pending" {
				t.Fatal("notification context missing on first turn")
			}
			resumed := selected.request
			if _, err := runner.RunDiscussion(context.Background(), resumed); err != nil {
				t.Fatal(err)
			}
			if selected.request.PersonalAssistantNotificationContext != "notice-current-done" {
				t.Fatal("resumed route reused stale notification state")
			}
			if _, err := runner.RunDiscussion(context.Background(), resumed); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(selected.request.PersonalAssistantNotificationContext, "pending") || !strings.Contains(selected.request.PersonalAssistantNotificationContext, "暂时无法读取") {
				t.Fatal("context error reused stale state")
			}
			group := personalAssistantRuntimeRequest()
			group.ConversationKind = discussionsession.ConversationKindGroup
			if _, err := runner.RunDiscussion(context.Background(), group); err != nil {
				t.Fatal(err)
			}
			if calls != 3 || managed.request.PersonalAssistantNotificationContext != "" {
				t.Fatal("private notification context leaked into group")
			}
		})
	}
}

func TestPersonalAssistantNotificationContextReadOncePerPreparedExecution(t *testing.T) {
	for _, mode := range []string{"managed", "device"} {
		for _, material := range []string{"pending", "", "lookup_error"} {
			for _, streaming := range []bool{false, true} {
				t.Run(mode+"/"+material+"/stream="+fmt.Sprint(streaming), func(t *testing.T) {
					selected := &recordingPersonalAssistantRunner{}
					view := devicedirectory.PersonalAssistantView{AssistantEnabled: true, HostingMode: devicedirectory.HostingModeManaged}
					if mode == "device" {
						view = ownedPersonalAssistantView(devicedirectory.AgentStatusOnline, false)
					}
					runner := newProductionPersonalAssistantDiscussionAgentRunner(selected, selected, &recordingPersonalAssistantDirectory{view: view}, &recordingPersonalAssistantRunnerResolver{runner: selected})
					calls := 0
					runner.setNotificationContext(assistantNotificationContextFunc(func(context.Context, discussionsession.DiscussionAgentRequest) (string, error) {
						calls++
						if material == "lookup_error" {
							return "", context.DeadlineExceeded
						}
						return material, nil
					}))
					request := personalAssistantRuntimeRequest()
					// Preparation and execution both route this request in production.
					for i := 0; i < 2; i++ {
						if err := runner.RouteDiscussionAgentRequest(context.Background(), &request); err != nil {
							t.Fatal(err)
						}
					}
					execute := func(request discussionsession.DiscussionAgentRequest) {
						t.Helper()
						if streaming {
							events, err := runner.RunDiscussionStream(context.Background(), request)
							if err != nil {
								t.Fatal(err)
							}
							for range events {
							}
						} else if _, err := runner.RunDiscussion(context.Background(), request); err != nil {
							t.Fatal(err)
						}
					}
					execute(request)
					if calls != 1 {
						t.Fatalf("prepared execution read notifications %d times", calls)
					}
					want := material
					if material == "lookup_error" {
						want = "通知状态暂时无法读取"
					}
					if !strings.Contains(selected.request.PersonalAssistantNotificationContext, want) {
						t.Fatal("prepared notification material did not reach runtime")
					}
					// A continuation must refresh even if a provider retains the same run ID.
					execute(selected.request)
					if calls != 2 {
						t.Fatalf("continuation reused notification state: calls=%d", calls)
					}
				})
			}
		}
	}
}

func TestPersonalAssistantPreparedNotificationScopeCannotCrossIdentityOrTransport(t *testing.T) {
	mutations := map[string]func(*discussionsession.DiscussionAgentRequest){
		"run":    func(r *discussionsession.DiscussionAgentRequest) { r.RunID = "run-2" },
		"owner":  func(r *discussionsession.DiscussionAgentRequest) { r.ConversationOwnerID = "user-2" },
		"tenant": func(r *discussionsession.DiscussionAgentRequest) { r.ConversationTenantID = "tenant-b" },
		"viewer": func(r *discussionsession.DiscussionAgentRequest) { r.Viewer.ViewerID = "user-2" },
		"permissions": func(r *discussionsession.DiscussionAgentRequest) {
			r.Viewer.PermissionScope = []string{"discussion.read"}
		},
		"transport": func(r *discussionsession.DiscussionAgentRequest) {
			raw, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var decoded discussionsession.DiscussionAgentRequest
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			*r = decoded
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			selected := &recordingPersonalAssistantRunner{}
			runner := newProductionPersonalAssistantDiscussionAgentRunner(selected, selected, &recordingPersonalAssistantDirectory{view: devicedirectory.PersonalAssistantView{AssistantEnabled: true}}, &recordingPersonalAssistantRunnerResolver{runner: selected})
			calls := 0
			runner.setNotificationContext(assistantNotificationContextFunc(func(context.Context, discussionsession.DiscussionAgentRequest) (string, error) {
				calls++
				return fmt.Sprint(calls), nil
			}))
			request := personalAssistantRuntimeRequest()
			if err := runner.RouteDiscussionAgentRequest(context.Background(), &request); err != nil {
				t.Fatal(err)
			}
			mutate(&request)
			if err := runner.RouteDiscussionAgentRequest(context.Background(), &request); err != nil {
				t.Fatal(err)
			}
			if calls != 2 || request.PersonalAssistantNotificationContext != "2" {
				t.Fatal("notification preparation survived an identity change or transport round trip")
			}
		})
	}
}
