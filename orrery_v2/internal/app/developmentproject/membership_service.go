package developmentproject

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"orrery_v2/internal/app/authorization"
)

func (s *Service) ListMembers(ctx context.Context, viewer ViewerRef, projectID string) ([]Member, error) {
	project, err := s.getAuthorized(ctx, viewer, projectID, "development_project.view")
	if err != nil {
		return nil, err
	}
	members := append([]Member(nil), project.Members...)
	sort.Slice(members, func(i, j int) bool {
		if members[i].Role == members[j].Role {
			return members[i].MemberID < members[j].MemberID
		}
		return members[i].Role < members[j].Role
	})
	return members, nil
}

func (s *Service) AddMember(ctx context.Context, viewer ViewerRef, cmd AddMemberCommand) (Member, ProjectDTO, error) {
	if err := s.requireMutationDependencies(false); err != nil {
		return Member{}, ProjectDTO{}, err
	}
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.DevelopmentProjectID = strings.TrimSpace(cmd.DevelopmentProjectID)
	cmd.MemberID = strings.TrimSpace(cmd.MemberID)
	cmd.SubjectKind = strings.ToLower(strings.TrimSpace(cmd.SubjectKind))
	cmd.SubjectID = strings.TrimSpace(cmd.SubjectID)
	cmd.Role = strings.ToLower(strings.TrimSpace(cmd.Role))
	if cmd.IdempotencyKey != "" && !validIdempotencyKey(cmd.IdempotencyKey) {
		return Member{}, ProjectDTO{}, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidRequest)
	}
	hashCommand := cmd
	hashCommand.IdempotencyKey = ""
	requestHash, err := projectMutationRequestHash("member.add", hashCommand)
	if err != nil {
		return Member{}, ProjectDTO{}, err
	}
	project, err := s.getAuthorized(ctx, viewer, cmd.DevelopmentProjectID, "development_project.manage")
	if err != nil {
		return Member{}, ProjectDTO{}, err
	}
	if receipt, replay, err := projectMutationReplay(project, cmd.IdempotencyKey, "member.add", requestHash); err != nil {
		return Member{}, ProjectDTO{}, err
	} else if replay {
		project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
		if err != nil {
			return Member{}, ProjectDTO{}, err
		}
		if receipt.Member == nil {
			return Member{}, ProjectDTO{}, ErrDependency
		}
		dto, dtoErr := s.dto(ctx, viewer, project)
		return *receipt.Member, dto, dtoErr
	}
	if project.Status != StatusActive {
		return Member{}, ProjectDTO{}, ErrArchived
	}
	if cmd.ExpectedProjectVersion <= 0 || project.Version != cmd.ExpectedProjectVersion {
		return Member{}, ProjectDTO{}, ErrConflict
	}
	if err := validateMember(cmd.SubjectKind, cmd.SubjectID, cmd.Role); err != nil {
		return Member{}, ProjectDTO{}, err
	}
	for _, member := range project.Members {
		if member.SubjectKind == cmd.SubjectKind && member.SubjectID == cmd.SubjectID {
			return Member{}, ProjectDTO{}, ErrConflict
		}
	}
	memberID := strings.TrimSpace(cmd.MemberID)
	if memberID == "" {
		memberID, err = s.newID("member")
		if err != nil {
			return Member{}, ProjectDTO{}, err
		}
	}
	if !validResourceID(memberID) {
		return Member{}, ProjectDTO{}, fmt.Errorf("%w: member_id is invalid", ErrInvalidRequest)
	}
	now := s.now()
	member := Member{
		MemberID: memberID, SubjectKind: cmd.SubjectKind, SubjectID: cmd.SubjectID, Role: cmd.Role,
		CreatedBy: viewerSubjectID(viewer), CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	project.Members = append(project.Members, member)
	project.Version++
	project.UpdatedAt = now
	appendProjectMutationReceipt(&project, MutationReceipt{IdempotencyKey: cmd.IdempotencyKey, Operation: "member.add", RequestHash: requestHash, Member: &member})
	memberSubject := authorization.SubjectRef{Kind: member.SubjectKind, ID: member.SubjectID}
	appendProjectEffects(&project,
		projectMemberEffect(project, effectWriteMember, memberSubject, member.Role),
		s.projectAuditEffect(project, viewer, "development_project.member_added", "succeeded", "added development project member", map[string]string{"member_id": member.MemberID, "role": member.Role}),
	)
	if err := s.repo.Update(ctx, project.TenantID, project, cmd.ExpectedProjectVersion); err != nil {
		return Member{}, ProjectDTO{}, err
	}
	project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
	if err != nil {
		return Member{}, ProjectDTO{}, err
	}
	dto, err := s.dto(ctx, viewer, project)
	return member, dto, err
}

func (s *Service) UpdateMember(ctx context.Context, viewer ViewerRef, cmd UpdateMemberCommand) (Member, ProjectDTO, error) {
	if err := s.requireMutationDependencies(false); err != nil {
		return Member{}, ProjectDTO{}, err
	}
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.DevelopmentProjectID = strings.TrimSpace(cmd.DevelopmentProjectID)
	cmd.MemberID = strings.TrimSpace(cmd.MemberID)
	cmd.Role = strings.ToLower(strings.TrimSpace(cmd.Role))
	if cmd.IdempotencyKey != "" && !validIdempotencyKey(cmd.IdempotencyKey) {
		return Member{}, ProjectDTO{}, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidRequest)
	}
	hashCommand := cmd
	hashCommand.IdempotencyKey = ""
	requestHash, err := projectMutationRequestHash("member.update", hashCommand)
	if err != nil {
		return Member{}, ProjectDTO{}, err
	}
	project, err := s.getAuthorized(ctx, viewer, cmd.DevelopmentProjectID, "development_project.manage")
	if err != nil {
		return Member{}, ProjectDTO{}, err
	}
	if receipt, replay, err := projectMutationReplay(project, cmd.IdempotencyKey, "member.update", requestHash); err != nil {
		return Member{}, ProjectDTO{}, err
	} else if replay {
		project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
		if err != nil {
			return Member{}, ProjectDTO{}, err
		}
		if receipt.Member == nil {
			return Member{}, ProjectDTO{}, ErrDependency
		}
		dto, dtoErr := s.dto(ctx, viewer, project)
		return *receipt.Member, dto, dtoErr
	}
	if project.Status != StatusActive {
		return Member{}, ProjectDTO{}, ErrArchived
	}
	if cmd.ExpectedProjectVersion <= 0 || project.Version != cmd.ExpectedProjectVersion {
		return Member{}, ProjectDTO{}, ErrConflict
	}
	index := memberIndex(project.Members, cmd.MemberID)
	if index < 0 {
		return Member{}, ProjectDTO{}, ErrNotFound
	}
	current := project.Members[index]
	if cmd.ExpectedMemberVersion <= 0 || current.Version != cmd.ExpectedMemberVersion {
		return Member{}, ProjectDTO{}, ErrConflict
	}
	role := cmd.Role
	if err := validateMember(current.SubjectKind, current.SubjectID, role); err != nil {
		return Member{}, ProjectDTO{}, err
	}
	if current.Role == MemberRoleOwner && role != MemberRoleOwner && ownerCount(project.Members) == 1 {
		return Member{}, ProjectDTO{}, fmt.Errorf("%w: a project must retain an owner", ErrInvalidRequest)
	}
	if current.Role == role {
		appendProjectMutationReceipt(&project, MutationReceipt{IdempotencyKey: cmd.IdempotencyKey, Operation: "member.update", RequestHash: requestHash, Member: &current})
		if err := s.repo.Update(ctx, project.TenantID, project, cmd.ExpectedProjectVersion); err != nil {
			return Member{}, ProjectDTO{}, err
		}
		dto, dtoErr := s.dto(ctx, viewer, project)
		return current, dto, dtoErr
	}
	project.Members[index].Role = role
	project.Members[index].Version++
	project.Members[index].UpdatedAt = s.now()
	project.Version++
	project.UpdatedAt = s.now()
	updatedMember := project.Members[index]
	appendProjectMutationReceipt(&project, MutationReceipt{IdempotencyKey: cmd.IdempotencyKey, Operation: "member.update", RequestHash: requestHash, Member: &updatedMember})
	subject := authorization.SubjectRef{Kind: current.SubjectKind, ID: current.SubjectID}
	appendProjectEffects(&project,
		projectMemberEffect(project, effectDeleteMember, subject, current.Role),
		projectMemberEffect(project, effectWriteMember, subject, role),
		s.projectAuditEffect(project, viewer, "development_project.member_updated", "succeeded", "updated development project member", map[string]string{"member_id": current.MemberID, "role": role}),
	)
	if err := s.repo.Update(ctx, project.TenantID, project, cmd.ExpectedProjectVersion); err != nil {
		return Member{}, ProjectDTO{}, err
	}
	project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
	if err != nil {
		return Member{}, ProjectDTO{}, err
	}
	dto, err := s.dto(ctx, viewer, project)
	return project.Members[index], dto, err
}

func (s *Service) RemoveMember(ctx context.Context, viewer ViewerRef, cmd RemoveMemberCommand) (ProjectDTO, error) {
	if err := s.requireMutationDependencies(false); err != nil {
		return ProjectDTO{}, err
	}
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.DevelopmentProjectID = strings.TrimSpace(cmd.DevelopmentProjectID)
	cmd.MemberID = strings.TrimSpace(cmd.MemberID)
	if cmd.IdempotencyKey != "" && !validIdempotencyKey(cmd.IdempotencyKey) {
		return ProjectDTO{}, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidRequest)
	}
	hashCommand := cmd
	hashCommand.IdempotencyKey = ""
	requestHash, err := projectMutationRequestHash("member.remove", hashCommand)
	if err != nil {
		return ProjectDTO{}, err
	}
	project, err := s.getAuthorized(ctx, viewer, cmd.DevelopmentProjectID, "development_project.manage")
	if err != nil {
		return ProjectDTO{}, err
	}
	if _, replay, err := projectMutationReplay(project, cmd.IdempotencyKey, "member.remove", requestHash); err != nil {
		return ProjectDTO{}, err
	} else if replay {
		project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
		if err != nil {
			return ProjectDTO{}, err
		}
		return s.dto(ctx, viewer, project)
	}
	if project.Status != StatusActive {
		return ProjectDTO{}, ErrArchived
	}
	if cmd.ExpectedProjectVersion <= 0 || project.Version != cmd.ExpectedProjectVersion {
		return ProjectDTO{}, ErrConflict
	}
	index := memberIndex(project.Members, cmd.MemberID)
	if index < 0 {
		return ProjectDTO{}, ErrNotFound
	}
	member := project.Members[index]
	if cmd.ExpectedMemberVersion <= 0 || member.Version != cmd.ExpectedMemberVersion {
		return ProjectDTO{}, ErrConflict
	}
	if member.Role == MemberRoleOwner && ownerCount(project.Members) == 1 {
		return ProjectDTO{}, fmt.Errorf("%w: a project must retain an owner", ErrInvalidRequest)
	}
	project.Members = append(project.Members[:index:index], project.Members[index+1:]...)
	project.Version++
	project.UpdatedAt = s.now()
	appendProjectMutationReceipt(&project, MutationReceipt{IdempotencyKey: cmd.IdempotencyKey, Operation: "member.remove", RequestHash: requestHash})
	subject := authorization.SubjectRef{Kind: member.SubjectKind, ID: member.SubjectID}
	appendProjectEffects(&project,
		projectMemberEffect(project, effectDeleteMember, subject, member.Role),
		s.projectAuditEffect(project, viewer, "development_project.member_removed", "succeeded", "removed development project member", map[string]string{"member_id": member.MemberID, "role": member.Role}),
	)
	if err := s.repo.Update(ctx, project.TenantID, project, cmd.ExpectedProjectVersion); err != nil {
		return ProjectDTO{}, err
	}
	project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
	if err != nil {
		return ProjectDTO{}, err
	}
	return s.dto(ctx, viewer, project)
}

func validateMember(kind, subjectID, role string) error {
	if !validSubjectID(subjectID) || !oneOf(kind, "user", "agent") {
		return ErrInvalidRequest
	}
	switch role {
	case MemberRoleOwner, MemberRoleMaintainer, MemberRoleViewer, MemberRoleMerger:
		if kind != "user" {
			return fmt.Errorf("%w: role %s requires a user", ErrInvalidRequest, role)
		}
	case MemberRoleExecutor:
		if kind != "agent" {
			return fmt.Errorf("%w: executor role requires an agent", ErrInvalidRequest)
		}
	case MemberRoleContributor:
	default:
		return ErrInvalidRequest
	}
	return nil
}

func memberIndex(members []Member, memberID string) int {
	memberID = strings.TrimSpace(memberID)
	for index := range members {
		if members[index].MemberID == memberID {
			return index
		}
	}
	return -1
}

func ownerCount(members []Member) int {
	count := 0
	for _, member := range members {
		if member.Role == MemberRoleOwner {
			count++
		}
	}
	return count
}
