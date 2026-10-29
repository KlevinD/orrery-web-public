package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

const schedulerMissionWorkerAuthorityScope = "mission_collaboration"

type schedulerMissionTaskSource interface {
	ListTasks(context.Context, bool) ([]schedulerapp.ScheduledTask, error)
}

type schedulerMissionProfileStore interface {
	GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error)
	SaveProfile(context.Context, agentcore.AgentProfile) error
}

type schedulerMissionProfileIssue struct {
	AgentProfileID string `json:"agent_profile_id"`
	Reason         string `json:"reason"`
}

type schedulerMissionProfileReport struct {
	Applied          bool                           `json:"applied"`
	ScannedTasks     int                            `json:"scanned_tasks"`
	TargetProfiles   int                            `json:"target_profiles"`
	EligibleProfiles int                            `json:"eligible_profiles"`
	PendingProfiles  int                            `json:"pending_profiles"`
	UpdatedProfiles  int                            `json:"updated_profiles"`
	Issues           []schedulerMissionProfileIssue `json:"issues,omitempty"`
}

type repeatedStringFlag []string

func (values *repeatedStringFlag) String() string {
	return strings.Join(*values, ",")
}

func (values *repeatedStringFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("profile ID cannot be blank")
	}
	*values = append(*values, value)
	return nil
}

func runSchedulerMissionProfiles(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || (args[0] != "plan" && args[0] != "apply" && args[0] != "verify") {
		return fmt.Errorf("usage: resourcectl scheduler-mission-profiles <plan|apply|verify> --db <url> [--profile <id>] [--apply]")
	}
	action := args[0]
	fs := flag.NewFlagSet("scheduler-mission-profiles "+action, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbURL := fs.String("db", "", "Orrery core database URL")
	confirmed := fs.Bool("apply", false, "confirm mutation")
	var explicitProfiles repeatedStringFlag
	fs.Var(&explicitProfiles, "profile", "additional Agent profile ID to inspect (repeatable)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if strings.TrimSpace(*dbURL) == "" {
		return fmt.Errorf("--db is required")
	}
	if action == "apply" && !*confirmed {
		return fmt.Errorf("scheduler-mission-profiles apply requires explicit --apply")
	}
	registry, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: *dbURL})
	if err != nil {
		return err
	}
	defer registry.Close()
	core, err := registry.Store(db.RoleCore)
	if err != nil {
		return err
	}
	report, err := reconcileSchedulerMissionProfilesForTargets(
		ctx,
		schedulerapp.NewRepository(db.NewRouter(registry)),
		agentcore.NewPlatformProfileRepository(uow.New(core)),
		action == "apply",
		explicitProfiles,
	)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return err
	}
	if action == "verify" && (report.PendingProfiles != 0 || len(report.Issues) != 0) {
		return fmt.Errorf("scheduler Mission profile verification failed: pending=%d issues=%d", report.PendingProfiles, len(report.Issues))
	}
	return nil
}

func reconcileSchedulerMissionProfiles(ctx context.Context, tasks schedulerMissionTaskSource, profiles schedulerMissionProfileStore, apply bool) (schedulerMissionProfileReport, error) {
	return reconcileSchedulerMissionProfilesForTargets(ctx, tasks, profiles, apply, nil)
}

func reconcileSchedulerMissionProfilesForTargets(ctx context.Context, tasks schedulerMissionTaskSource, profiles schedulerMissionProfileStore, apply bool, explicitProfiles []string) (schedulerMissionProfileReport, error) {
	report := schedulerMissionProfileReport{Applied: apply}
	if tasks == nil || profiles == nil {
		return report, fmt.Errorf("scheduler Mission profile dependencies are required")
	}
	storedTasks, err := tasks.ListTasks(ctx, true)
	if err != nil {
		return report, err
	}
	targets := map[string]struct{}{}
	for _, id := range explicitProfiles {
		if id = strings.TrimSpace(id); id != "" {
			targets[id] = struct{}{}
		}
	}
	for _, task := range storedTasks {
		if task.Target.Mode != schedulerapp.TargetModeAgentTask {
			continue
		}
		report.ScannedTasks++
		agentID := strings.TrimSpace(task.Target.AgentID)
		if agentID == "" {
			report.Issues = append(report.Issues, schedulerMissionProfileIssue{Reason: "agent_task target has no Agent profile"})
			continue
		}
		targets[agentID] = struct{}{}
	}
	ids := make([]string, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	report.TargetProfiles = len(ids)
	for _, id := range ids {
		profile, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(id))
		if err != nil {
			report.Issues = append(report.Issues, schedulerMissionProfileIssue{AgentProfileID: id, Reason: err.Error()})
			continue
		}
		if reason := schedulerMissionProfileRepairBlocker(profile); reason != "" {
			report.Issues = append(report.Issues, schedulerMissionProfileIssue{AgentProfileID: id, Reason: reason})
			continue
		}
		needsRole := !schedulerMissionProfileHasRole(profile, "worker")
		needsAuthority := strings.TrimSpace(profile.OrgIdentity.AuthorityScope) == ""
		if !needsRole && !needsAuthority {
			report.EligibleProfiles++
			continue
		}
		report.PendingProfiles++
		if !apply {
			continue
		}
		if needsRole {
			profile.OrgIdentity.RoleRefs = append(profile.OrgIdentity.RoleRefs, agentcore.RoleRef{ID: "worker"})
		}
		if needsAuthority {
			profile.OrgIdentity.AuthorityScope = schedulerMissionWorkerAuthorityScope
		}
		if err := profiles.SaveProfile(ctx, profile); err != nil {
			return report, fmt.Errorf("save scheduler Agent profile %s: %w", id, err)
		}
		report.UpdatedProfiles++
		report.EligibleProfiles++
	}
	return report, nil
}

func schedulerMissionProfileRepairBlocker(profile agentcore.AgentProfile) string {
	if profile.ID.IsZero() {
		return "Agent profile ID is empty"
	}
	if profile.AgentKind != agentcore.AgentKindAI {
		return "scheduled Mission target is not an AI Agent"
	}
	if !profile.Enabled {
		return "scheduled Mission target profile is disabled"
	}
	if profile.OrgIdentity.OrganizationRef.ID == "" || profile.OrgIdentity.DepartmentRef.ID == "" || profile.OrgIdentity.PositionRef.ID == "" {
		return "scheduled Mission target organization identity is incomplete"
	}
	return ""
}

func schedulerMissionProfileHasRole(profile agentcore.AgentProfile, role string) bool {
	for _, ref := range profile.OrgIdentity.RoleRefs {
		if strings.EqualFold(strings.TrimSpace(string(ref.ID)), strings.TrimSpace(role)) {
			return true
		}
	}
	return false
}
