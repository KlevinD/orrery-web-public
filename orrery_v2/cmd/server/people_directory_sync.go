package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/peopleaccount"
	"orrery_v2/internal/app/peopledirectory"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/idgen"
)

const peopleDirectorySystemViewerID = "system:people-feishu-directory"

type peopleDirectoryPlacement struct {
	OrganizationID string
	DepartmentID   string
	PositionID     string
}

type peopleDirectoryApplyStore interface {
	Snapshot(ctx context.Context, snapshotID string) (peopledirectory.Snapshot, error)
	StagedUsers(ctx context.Context, snapshotID string) ([]peopledirectory.DirectoryUser, error)
	MatchResults(ctx context.Context, snapshotID string) ([]peopledirectory.MatchResult, error)
	ReservePersonID(ctx context.Context, snapshotID, sourceRecordID, personID string) error
	MarkApplied(ctx context.Context, snapshotID, sourceRecordID, personID string) error
}

type peopleDirectoryWorkforcePort interface {
	NewWorkforceMemberID() (string, error)
	UpsertWorkforceMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.UpsertWorkforceMemberCommand) (operations.WorkforceMemberDTO, error)
	SetWorkforceMemberEnabled(ctx context.Context, viewer operations.ViewerRef, cmd operations.SetWorkforceMemberEnabledCommand) error
}

type peopleDirectorySnapshotApplier struct {
	store     peopleDirectoryApplyStore
	workforce peopleDirectoryWorkforcePort
	placement peopleDirectoryPlacement
}

type peopleDirectoryApplyReport struct {
	SnapshotID         string                                         `json:"snapshot_id"`
	Created            int                                            `json:"created"`
	Updated            int                                            `json:"updated"`
	Disabled           int                                            `json:"disabled"`
	Conflicts          int                                            `json:"conflicts"`
	Skipped            int                                            `json:"skipped"`
	InitialCredentials []peopleAccountReconciliationInitialCredential `json:"initial_credentials,omitempty"`
}

func newPeopleDirectorySnapshotApplier(store peopleDirectoryApplyStore, workforce peopleDirectoryWorkforcePort, placement peopleDirectoryPlacement) *peopleDirectorySnapshotApplier {
	return &peopleDirectorySnapshotApplier{store: store, workforce: workforce, placement: placement}
}

func (a *peopleDirectorySnapshotApplier) ApplySnapshot(ctx context.Context, snapshotID string) (peopleDirectoryApplyReport, error) {
	if a == nil || a.store == nil || a.workforce == nil {
		return peopleDirectoryApplyReport{}, fmt.Errorf("People directory snapshot applier is not configured")
	}
	snapshot, err := a.store.Snapshot(ctx, snapshotID)
	if err != nil {
		return peopleDirectoryApplyReport{}, err
	}
	if !snapshot.Complete || snapshot.Status != peopledirectory.SnapshotStatusComplete {
		return peopleDirectoryApplyReport{}, peopledirectory.ErrSnapshotIncomplete
	}
	users, err := a.store.StagedUsers(ctx, snapshotID)
	if err != nil {
		return peopleDirectoryApplyReport{}, err
	}
	usersBySourceID := make(map[string]peopledirectory.DirectoryUser, len(users))
	for _, user := range users {
		usersBySourceID[strings.TrimSpace(user.SourceRecordID)] = user
	}
	matches, err := a.store.MatchResults(ctx, snapshotID)
	if err != nil {
		return peopleDirectoryApplyReport{}, err
	}
	viewer := peopleDirectorySystemViewer()
	report := peopleDirectoryApplyReport{SnapshotID: snapshot.ID}
	for _, match := range matches {
		if match.ApplyStatus == peopledirectory.ApplyStatusApplied {
			report.Skipped++
			continue
		}
		switch match.Action {
		case peopledirectory.MatchActionConflict:
			report.Conflicts++
			continue
		case peopledirectory.MatchActionIgnore:
			report.Skipped++
			continue
		case peopledirectory.MatchActionDisable:
			personID := strings.TrimSpace(match.ProposedPersonID)
			if personID == "" {
				return report, fmt.Errorf("disable match %q has no person id", match.SourceRecordID)
			}
			if err := a.workforce.SetWorkforceMemberEnabled(ctx, viewer, operations.SetWorkforceMemberEnabledCommand{AgentProfileID: personID, Enabled: false}); err != nil {
				return report, err
			}
			if err := a.store.MarkApplied(ctx, snapshot.ID, match.SourceRecordID, personID); err != nil {
				return report, err
			}
			report.Disabled++
			continue
		case peopledirectory.MatchActionCreate, peopledirectory.MatchActionUpdate:
		default:
			return report, fmt.Errorf("unsupported People directory match action %q", match.Action)
		}

		user, ok := usersBySourceID[strings.TrimSpace(match.SourceRecordID)]
		if !ok {
			return report, fmt.Errorf("staged directory user %q is missing", match.SourceRecordID)
		}
		personID := strings.TrimSpace(match.ProposedPersonID)
		if match.Action == peopledirectory.MatchActionCreate && personID == "" {
			personID = strings.ToUpper(strings.TrimSpace(user.EmployeeNumber))
			if personID == "" {
				personID, err = a.workforce.NewWorkforceMemberID()
				if err != nil {
					return report, err
				}
			}
			if err := a.store.ReservePersonID(ctx, snapshot.ID, match.SourceRecordID, personID); err != nil {
				return report, err
			}
		}
		if personID == "" {
			return report, fmt.Errorf("directory match %q has no person id", match.SourceRecordID)
		}
		command := peopleDirectoryUpsertCommand(snapshot, user, personID, match.Action, a.placement)
		member, err := a.workforce.UpsertWorkforceMember(ctx, viewer, command)
		if err != nil {
			return report, err
		}
		if password := strings.TrimSpace(member.AccountProjection.InitialPassword); password != "" {
			report.InitialCredentials = append(report.InitialCredentials, peopleAccountReconciliationInitialCredential{
				PersonID: personID, Username: member.UserAccount.Username, Password: password,
			})
		}
		if err := a.store.MarkApplied(ctx, snapshot.ID, match.SourceRecordID, personID); err != nil {
			return report, err
		}
		if match.Action == peopledirectory.MatchActionCreate {
			report.Created++
		} else {
			report.Updated++
		}
	}
	return report, nil
}

func peopleDirectoryUpsertCommand(snapshot peopledirectory.Snapshot, user peopledirectory.DirectoryUser, personID string, action peopledirectory.MatchAction, placement peopleDirectoryPlacement) operations.UpsertWorkforceMemberCommand {
	displayName := strings.TrimSpace(user.Name)
	if displayName == "" {
		displayName = firstNonBlank(strings.TrimSpace(user.EnterpriseEmail), strings.TrimSpace(user.EmployeeNumber), strings.TrimSpace(user.SourceRecordID))
	}
	command := operations.UpsertWorkforceMemberCommand{
		AgentProfileID: personID,
		RealName:       displayName,
		DisplayName:    displayName,
		Email:          strings.TrimSpace(user.EnterpriseEmail),
		PersonSource: operations.WorkforcePersonSourceFactsDTO{
			EmployeeNumber: strings.TrimSpace(user.EmployeeNumber),
			Source:         strings.TrimSpace(snapshot.Source),
			SourceRecordID: strings.TrimSpace(user.SourceRecordID),
			SourceTenantID: strings.TrimSpace(snapshot.TenantID),
		},
		Enabled: user.Active,
	}
	if action == peopledirectory.MatchActionUpdate {
		command.OrganizationID = strings.TrimSpace(placement.OrganizationID)
		command.DepartmentID = strings.TrimSpace(placement.DepartmentID)
		command.PositionID = strings.TrimSpace(placement.PositionID)
		command.UpdateMask = []string{"display_name", "real_name", "email", "person_source", "enabled", "organization_id", "department_id", "position_id"}
		return command
	}
	command.AgentKind = string(agentcore.AgentKindHuman)
	command.EmploymentType = string(agentcore.EmploymentTypeHuman)
	command.OrganizationID = strings.TrimSpace(placement.OrganizationID)
	command.DepartmentID = strings.TrimSpace(placement.DepartmentID)
	command.PositionID = strings.TrimSpace(placement.PositionID)
	command.RoleIDs = []string{operations.RoleIDBasicUser, "member"}
	command.UserAccount = operations.WorkforceUserAccountDTO{
		Username:       peopleDirectoryUsername(user),
		Nickname:       displayName,
		Email:          strings.TrimSpace(user.EnterpriseEmail),
		AccountEnabled: user.Active,
	}
	command.Persona = operations.WorkforcePersonaDTO{Backstory: "People managed employee."}
	return command
}

func peopleDirectoryUsername(user peopledirectory.DirectoryUser) string {
	value := strings.TrimSpace(user.EmployeeNumber)
	if value == "" {
		value = strings.TrimSpace(user.EnterpriseEmail)
	}
	if value == "" {
		value = "feishu-" + strings.TrimSpace(user.SourceRecordID)
	}
	return strings.ToLower(value)
}

func peopleDirectorySystemViewer() operations.ViewerRef {
	return operations.ViewerRef{
		ViewerID:   peopleDirectorySystemViewerID,
		ViewerKind: "system",
		PermissionScope: []string{
			operations.PermissionWorkforceManage,
			operations.PermissionRolesManage,
			operations.PermissionPermissionsManage,
		},
	}
}

type peopleDirectoryProfileStore interface {
	QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

type peopleDirectoryKratosLookup interface {
	FindIdentityByUsername(ctx context.Context, username string) (peopleaccount.KratosIdentity, bool, error)
}

type productionPeopleDirectorySync struct {
	scanner      peopleDirectoryScanner
	store        peopleDirectorySyncStore
	profiles     peopleDirectoryProfileStore
	kratos       peopleDirectoryKratosLookup
	source       string
	tenantID     string
	syncInterval time.Duration
	retention    peopledirectory.SnapshotRetentionPolicy
	now          func() time.Time
}

type peopleDirectorySyncReport struct {
	SnapshotID string `json:"snapshot_id"`
	Users      int    `json:"users"`
	Creates    int    `json:"creates"`
	Updates    int    `json:"updates"`
	Disables   int    `json:"disables"`
	Conflicts  int    `json:"conflicts"`
	Ignored    int    `json:"ignored"`
}

func (s *productionPeopleDirectorySync) ScanAndMatch(ctx context.Context) (peopleDirectorySyncReport, error) {
	if s == nil || s.scanner == nil || s.store == nil || s.profiles == nil {
		return peopleDirectorySyncReport{}, fmt.Errorf("production People directory sync is not configured")
	}
	snapshotID, err := idgen.NewRandom("people-directory-snapshot").NewIDContext()
	if err != nil {
		return peopleDirectorySyncReport{}, err
	}
	if err := s.scanner.Scan(ctx, snapshotID); err != nil {
		return peopleDirectorySyncReport{}, err
	}
	snapshot, err := s.store.Snapshot(ctx, snapshotID)
	if err != nil {
		return peopleDirectorySyncReport{}, err
	}
	users, err := s.store.StagedUsers(ctx, snapshotID)
	if err != nil {
		return peopleDirectorySyncReport{}, err
	}
	people, err := s.peopleCandidates(ctx)
	if err != nil {
		return peopleDirectorySyncReport{}, err
	}
	matches, err := peopledirectory.MatchSnapshot(snapshot, users, people)
	if err != nil {
		return peopleDirectorySyncReport{}, err
	}
	if err := s.store.SaveMatchResults(ctx, snapshotID, matches); err != nil {
		return peopleDirectorySyncReport{}, err
	}
	report := peopleDirectorySyncReport{SnapshotID: snapshotID, Users: len(users)}
	for _, match := range matches {
		switch match.Action {
		case peopledirectory.MatchActionCreate:
			report.Creates++
		case peopledirectory.MatchActionUpdate:
			report.Updates++
		case peopledirectory.MatchActionDisable:
			report.Disables++
		case peopledirectory.MatchActionConflict:
			report.Conflicts++
		case peopledirectory.MatchActionIgnore:
			report.Ignored++
		}
	}
	return report, nil
}

func (s *productionPeopleDirectorySync) peopleCandidates(ctx context.Context) ([]peopledirectory.PersonCandidate, error) {
	page, err := s.profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
		AgentKind: agentcore.AgentKindHuman, Placement: agentcore.TalentPlacementFilterAll, IncludeHumans: true,
	})
	if err != nil {
		return nil, err
	}
	candidates := make([]peopledirectory.PersonCandidate, 0, len(page.Entries))
	for _, entry := range page.Entries {
		profile, err := s.profiles.GetProfile(ctx, entry.AgentProfileID)
		if err != nil {
			return nil, err
		}
		candidate := peopledirectory.PersonCandidate{
			PersonID: profile.ID.String(), EmployeeNumber: profile.PersonSource.EmployeeNumber,
			EnterpriseEmail: firstNonBlank(profile.UserAccount.Email, profile.Email),
			Source:          profile.PersonSource.Source, SourceRecordID: profile.PersonSource.SourceRecordID,
			SourceTenantID: profile.PersonSource.SourceTenantID, Enabled: profile.Enabled,
		}
		if s.kratos != nil && strings.TrimSpace(profile.UserAccount.Username) != "" {
			identity, found, err := s.kratos.FindIdentityByUsername(ctx, profile.UserAccount.Username)
			if err != nil {
				return nil, err
			}
			if found {
				candidate.FeishuSubject = identity.FeishuSubject
			}
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func configureProductionPeopleDirectorySync(rt *platform.Runtime, core *db.Store, profiles *agentcore.PlatformProfileRepository, auth identityAuthConfig) error {
	if rt == nil {
		return fmt.Errorf("production runtime is not configured")
	}
	enabled, _ := rt.Config.Bool("auth.feishu.directory_sync_enabled")
	if !enabled {
		return nil
	}
	syncer, err := newProductionPeopleDirectorySync(rt, core, profiles, auth)
	if err != nil {
		return err
	}
	return rt.RegisterSweeper("people.feishu_directory", syncer)
}

func newProductionPeopleDirectorySync(rt *platform.Runtime, core *db.Store, profiles *agentcore.PlatformProfileRepository, auth identityAuthConfig) (*productionPeopleDirectorySync, error) {
	if auth.IdentityMode != identityModePeopleSSO || auth.KratosAdmin == nil || core == nil || profiles == nil {
		return nil, fmt.Errorf("Feishu directory sync requires people_sso mode and People account projection")
	}
	appID, _ := rt.Config.String("auth.feishu.app_id")
	appSecret, _ := rt.Config.String("auth.feishu.app_secret")
	tenantID, _ := rt.Config.String("auth.feishu.allowed_tenant_key")
	if strings.TrimSpace(appID) == "" || strings.TrimSpace(appSecret) == "" || strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("auth.feishu.app_id, app_secret, and allowed_tenant_key are required for directory sync")
	}
	store := peopledirectory.NewStore(core)
	return &productionPeopleDirectorySync{
		scanner: peopledirectory.NewFeishuScanner(appID, appSecret, tenantID, store), store: store,
		profiles: profiles, kratos: auth.KratosAdmin,
		source: peopledirectory.SourceFeishu, tenantID: strings.TrimSpace(tenantID),
		syncInterval: productionPeopleDirectorySyncInterval,
		retention: peopledirectory.SnapshotRetentionPolicy{
			KeepLatest: productionPeopleDirectoryKeepLatest,
			MinimumAge: productionPeopleDirectorySnapshotMinimumAge,
			Limit:      productionPeopleDirectoryPruneLimit,
		},
		now: rt.Clock.Now,
	}, nil
}

func configureProductionPeopleWorkforce(ctx context.Context, rt *platform.Runtime, core *db.Store, profiles *agentcore.PlatformProfileRepository, workforce *operations.CommandService, auth identityAuthConfig) error {
	if err := workforce.ReconcileWorkforceRoleAssignments(ctx, "system:workforce-role-reconcile"); err != nil {
		return fmt.Errorf("reconcile workforce role assignments: %w", err)
	}
	return configureProductionPeopleDirectorySync(rt, core, profiles, auth)
}
