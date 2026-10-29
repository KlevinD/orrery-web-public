package discussionsession

import (
	"context"
	"strings"
	"time"
)

type EnsureMissionConversationCommand struct {
	MissionID                        string
	Title                            string
	Status                           string
	CreatedAt                        time.Time
	PersonalAssistantOriginOwnerID   string
	PersonalAssistantOriginSubjectID string
}

func (s *Service) EnsureMissionConversation(ctx context.Context, viewer ViewerRef, cmd EnsureMissionConversationCommand) (ConversationDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	missionID := strings.TrimSpace(cmd.MissionID)
	if strings.TrimSpace(viewer.ViewerID) == "" || missionID == "" {
		return ConversationDTO{}, ErrInvalidRequest
	}
	if (strings.TrimSpace(cmd.PersonalAssistantOriginOwnerID) == "") != (strings.TrimSpace(cmd.PersonalAssistantOriginSubjectID) == "") {
		return ConversationDTO{}, ErrInvalidRequest
	}
	if subjectID := strings.TrimSpace(cmd.PersonalAssistantOriginSubjectID); subjectID != "" && subjectID != strings.TrimSpace(viewer.ViewerID) {
		return ConversationDTO{}, ErrForbidden
	}
	existing, ok, err := s.findConversationByMissionID(ctx, viewer.TenantID, missionID)
	if err != nil {
		return ConversationDTO{}, err
	}
	now := s.now()
	if ok {
		if changed, err := applyMissionPersonalAssistantOrigin(&existing, viewer, cmd, now); err != nil {
			return ConversationDTO{}, err
		} else if changed {
			if err := s.repo.SaveConversation(ctx, existing.ConversationRecord); err != nil {
				return ConversationDTO{}, err
			}
		}
		if err := s.ensureConversationHasViewerMember(ctx, existing, viewer, now); err != nil {
			return ConversationDTO{}, err
		}
		if err := s.ensureMissionLinkMetadata(ctx, existing.ConversationID, cmd, now); err != nil {
			return ConversationDTO{}, err
		}
		updated, err := s.repo.GetConversation(ctx, existing.ConversationID)
		if err != nil {
			return ConversationDTO{}, err
		}
		s.writeConversationAuthorization(ctx, existing.ConversationID)
		return mapConversationDTO(updated), nil
	}

	conversationID, err := s.nextConversationID(cmd)
	if err != nil {
		return ConversationDTO{}, err
	}
	title := firstNonBlank(strings.TrimSpace(cmd.Title), "Mission "+missionID)
	createdAt := firstTime(cmd.CreatedAt, now)
	conversation := ConversationRecord{
		ConversationID:                   conversationID,
		TenantID:                         firstNonBlank(strings.TrimSpace(viewer.TenantID), "global"),
		Title:                            title,
		Status:                           "completed",
		CreatedBy:                        strings.TrimSpace(viewer.ViewerID),
		OwnerID:                          strings.TrimSpace(cmd.PersonalAssistantOriginOwnerID),
		PersonalAssistantOriginOwnerID:   strings.TrimSpace(cmd.PersonalAssistantOriginOwnerID),
		PersonalAssistantOriginSubjectID: strings.TrimSpace(cmd.PersonalAssistantOriginSubjectID),
		CreatedAt:                        createdAt,
		UpdatedAt:                        createdAt,
		ViewedAt:                         createdAt,
	}
	members, err := s.initialConversationMembers(ctx, conversation, viewer, createdAt)
	if err != nil {
		return ConversationDTO{}, err
	}
	for index := range members {
		if strings.TrimSpace(members[index].MemberKind) == "agent" {
			members[index] = grantConversationMemberManagedSource(members[index], missionConversationBootstrapManagedSource(conversation.TenantID, missionID))
		}
	}
	if name := conversationMemberName(members, conversationViewerMemberKind(viewer.ViewerKind), viewer.ViewerID); name != "" {
		conversation.CreatedByName = name
	}
	if err := s.repo.SaveConversation(ctx, conversation); err != nil {
		return ConversationDTO{}, err
	}
	if err := s.saveConversationMembers(ctx, members); err != nil {
		return ConversationDTO{}, err
	}
	if err := s.repo.SaveMissionLink(ctx, missionLinkRecord(conversationID, cmd, createdAt)); err != nil {
		return ConversationDTO{}, err
	}
	created, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	s.writeConversationAuthorization(ctx, conversationID)
	return mapConversationDTO(created), nil
}

// applyMissionPersonalAssistantOrigin makes the PA provenance immutable while
// allowing a validated first retry to backfill a Mission created before the
// provenance field existed. The production API performs the source-conversation
// access check before this command reaches the service.
func applyMissionPersonalAssistantOrigin(view *ConversationRecordView, viewer ViewerRef, cmd EnsureMissionConversationCommand, now time.Time) (bool, error) {
	if view == nil {
		return false, ErrInvalidRequest
	}
	cmdOwner := strings.TrimSpace(cmd.PersonalAssistantOriginOwnerID)
	cmdSubject := strings.TrimSpace(cmd.PersonalAssistantOriginSubjectID)
	if (cmdOwner == "") != (cmdSubject == "") {
		return false, ErrInvalidRequest
	}
	existingOwner := strings.TrimSpace(view.PersonalAssistantOriginOwnerID)
	existingSubject := strings.TrimSpace(view.PersonalAssistantOriginSubjectID)
	if existingOwner != "" || existingSubject != "" {
		if cmdOwner == "" && cmdSubject == "" {
			return false, nil
		}
		if existingOwner != cmdOwner || existingSubject != cmdSubject {
			return false, ErrRevisionConflict
		}
		return false, nil
	}
	if cmdOwner == "" {
		return false, nil
	}
	viewerID := strings.TrimSpace(viewer.ViewerID)
	if cmdSubject != viewerID {
		return false, ErrForbidden
	}
	if createdBy := strings.TrimSpace(view.CreatedBy); createdBy != "" && createdBy != cmdSubject {
		return false, ErrForbidden
	}
	if ownerID := strings.TrimSpace(view.OwnerID); ownerID != "" && ownerID != cmdOwner {
		return false, ErrForbidden
	}
	view.PersonalAssistantOriginOwnerID = cmdOwner
	view.PersonalAssistantOriginSubjectID = cmdSubject
	if strings.TrimSpace(view.OwnerID) == "" {
		view.OwnerID = cmdOwner
	}
	view.UpdatedAt = firstTime(now, view.UpdatedAt)
	return true, nil
}

func missionConversationBootstrapManagedSource(tenantID, missionID string) string {
	return "mission-bootstrap:" + strings.TrimSpace(tenantID) + "/" + strings.TrimSpace(missionID)
}

func (s *Service) findConversationByMissionID(ctx context.Context, tenantID, missionID string) (ConversationRecordView, bool, error) {
	conversations, err := s.repo.ListConversations(ctx)
	if err != nil {
		return ConversationRecordView{}, false, err
	}
	tenantID = firstNonBlank(strings.TrimSpace(tenantID), "global")
	for _, conversation := range conversations {
		if strings.TrimSpace(conversation.ConversationKind) == "direct" {
			continue
		}
		// Records created before tenant-aware discussions did not persist a
		// tenant. They belong to the historical global tenant only.
		if firstNonBlank(strings.TrimSpace(conversation.TenantID), "global") != tenantID {
			continue
		}
		for _, link := range conversation.LinkedMissions {
			if strings.TrimSpace(link.MissionID) == missionID {
				view, err := s.repo.GetConversation(ctx, conversation.ConversationID)
				if err != nil {
					return ConversationRecordView{}, false, err
				}
				return view, true, nil
			}
		}
	}
	return ConversationRecordView{}, false, nil
}

func (s *Service) ensureConversationHasViewerMember(ctx context.Context, view ConversationRecordView, viewer ViewerRef, now time.Time) error {
	if viewer.ViewerKind == ViewerKindSystem {
		return nil
	}
	kind := conversationViewerMemberKind(viewer.ViewerKind)
	viewerID := strings.TrimSpace(viewer.ViewerID)
	if conversationMemberName(view.Members, kind, viewerID) != "" {
		return nil
	}
	var member ConversationMemberRecord
	var err error
	if kind == "agent" {
		member, err = s.resolveAgentConversationMember(ctx, view.ConversationID, viewerID, "话题成员", now)
	} else {
		member, err = s.currentViewerConversationMember(ctx, view.ConversationID, viewer, "话题成员", now)
	}
	if err != nil {
		return err
	}
	if err := s.repo.SaveMember(ctx, member); err != nil {
		return err
	}
	s.writeConversationAuthorization(ctx, view.ConversationID)
	return nil
}

func (s *Service) currentViewerConversationMember(ctx context.Context, conversationID string, viewer ViewerRef, role string, createdAt time.Time) (ConversationMemberRecord, error) {
	if s == nil {
		return ConversationMemberRecord{}, ErrServiceDependency
	}
	viewerID := strings.TrimSpace(viewer.ViewerID)
	switch viewer.ViewerKind {
	case ViewerKindAgent:
		return s.resolveAgentConversationMember(ctx, conversationID, viewerID, role, createdAt)
	case ViewerKindSystem:
		return ConversationMemberRecord{}, ErrConversationMember
	}
	if s.humanProfiles == nil {
		return ConversationMemberRecord{}, ErrServiceDependency
	}
	humanProfile, err := s.humanProfiles.EnsureDiscussionHumanProfile(ctx, viewer)
	if err != nil {
		return ConversationMemberRecord{}, err
	}
	name, err := strictConversationDisplayName(viewerID, humanProfile.DisplayName, humanProfile.RealName)
	if err != nil {
		return ConversationMemberRecord{}, err
	}
	return ConversationMemberRecord{
		ConversationID: strings.TrimSpace(conversationID),
		MemberKind:     "user",
		MemberID:       viewerID,
		DisplayName:    name,
		Role:           strings.TrimSpace(role),
		CreatedAt:      createdAt,
	}, nil
}

func (s *Service) ensureMissionLinkMetadata(ctx context.Context, conversationID string, cmd EnsureMissionConversationCommand, now time.Time) error {
	return s.repo.SaveMissionLink(ctx, missionLinkRecord(conversationID, cmd, now))
}

func (s *Service) nextConversationID(cmd EnsureMissionConversationCommand) (string, error) {
	if s.generator != nil {
		return s.generator.NewIDContext()
	}
	missionID := strings.TrimSpace(cmd.MissionID)
	if missionID == "" {
		return "", ErrInvalidRequest
	}
	return "discussion-" + missionID, nil
}

func missionLinkRecord(conversationID string, cmd EnsureMissionConversationCommand, createdAt time.Time) LinkedMissionRecord {
	return LinkedMissionRecord{
		ConversationID: strings.TrimSpace(conversationID),
		MissionID:      strings.TrimSpace(cmd.MissionID),
		Title:          strings.TrimSpace(cmd.Title),
		Status:         strings.TrimSpace(cmd.Status),
		CreatedAt:      firstTime(cmd.CreatedAt, createdAt),
	}
}
