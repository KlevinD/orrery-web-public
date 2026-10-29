package developmentproject

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"orrery_v2/internal/app/authorization"
)

const (
	effectWriteProjectCreated  = "relation.write_project_created"
	effectWriteMember          = "relation.write_member"
	effectDeleteMember         = "relation.delete_member"
	effectWriteConnectionUser  = "relation.write_connection_user"
	effectDeleteConnectionUser = "relation.delete_connection_user"
	effectRecordAudit          = "audit.record"
)

func projectEffectID(project Project, kind, discriminator string) string {
	return fmt.Sprintf("development-project:%s:%s:v%d:%s:%s", normalizeTenantID(project.TenantID), project.DevelopmentProjectID, project.Version, kind, strings.TrimSpace(discriminator))
}

func projectCreatedEffect(project Project, owner authorization.SubjectRef) PendingEffect {
	return PendingEffect{
		EffectID: projectEffectID(project, effectWriteProjectCreated, owner.Kind+":"+owner.ID),
		Kind:     effectWriteProjectCreated, SubjectKind: owner.Kind, SubjectID: owner.ID,
	}
}

func projectMemberEffect(project Project, kind string, subject authorization.SubjectRef, relation string) PendingEffect {
	return PendingEffect{
		EffectID: projectEffectID(project, kind, subject.Kind+":"+subject.ID+":"+relation),
		Kind:     kind, SubjectKind: subject.Kind, SubjectID: subject.ID, Relation: relation,
	}
}

func projectConnectionEffect(project Project, kind, connectionID string) PendingEffect {
	return PendingEffect{
		EffectID: projectEffectID(project, kind, connectionID), Kind: kind, ConnectionID: connectionID,
	}
}

func (s *Service) projectAuditEffect(project Project, viewer ViewerRef, action, result, summary string, metadata map[string]string) PendingEffect {
	effectID := projectEffectID(project, effectRecordAudit, action)
	return PendingEffect{
		EffectID: effectID,
		Kind:     effectRecordAudit,
		Audit: &AuditEvent{
			AuditID: effectID, TenantID: project.TenantID, ActorID: viewerSubjectID(viewer), EntityID: project.DevelopmentProjectID,
			Action: action, Result: result, Summary: summary, Metadata: cloneMetadata(metadata), OccurredAt: s.now(),
		},
	}
}

func appendProjectEffects(project *Project, effects ...PendingEffect) {
	if project == nil {
		return
	}
	project.PendingEffects = append(project.PendingEffects, effects...)
}

func (s *Service) reconcileProjectEffects(ctx context.Context, tenantID, projectID string) (Project, error) {
	if s == nil || s.repo == nil || s.relations == nil || s.audit == nil {
		return Project{}, ErrDependency
	}
	for {
		project, err := s.repo.Get(ctx, tenantID, projectID)
		if err != nil {
			return Project{}, err
		}
		if len(project.PendingEffects) == 0 {
			return project, nil
		}
		effect := project.PendingEffects[0]
		if err := s.applyProjectEffect(ctx, project, effect); err != nil {
			return project, fmt.Errorf("apply development project effect %q: %w", effect.EffectID, err)
		}
		if err := s.clearProjectEffect(ctx, project.TenantID, project.DevelopmentProjectID, effect.EffectID); err != nil {
			return project, fmt.Errorf("clear development project effect %q: %w", effect.EffectID, err)
		}
	}
}

func (s *Service) applyProjectEffect(ctx context.Context, project Project, effect PendingEffect) error {
	subject := authorization.SubjectRef{Kind: effect.SubjectKind, ID: effect.SubjectID}
	switch effect.Kind {
	case effectWriteProjectCreated:
		return s.relations.WriteDevelopmentProjectCreated(ctx, subject, project.DevelopmentProjectID, project.OrganizationID, project.TenantID)
	case effectWriteMember:
		return s.relations.WriteDevelopmentProjectMember(ctx, subject, effect.Relation, project.DevelopmentProjectID, project.TenantID)
	case effectDeleteMember:
		return s.relations.DeleteDevelopmentProjectMember(ctx, subject, effect.Relation, project.DevelopmentProjectID, project.TenantID)
	case effectWriteConnectionUser:
		return s.relations.WriteSCMConnectionUser(ctx, authorization.SubjectRef{Kind: "development_project", ID: project.DevelopmentProjectID}, effect.ConnectionID, project.TenantID)
	case effectDeleteConnectionUser:
		return s.relations.DeleteSCMConnectionUser(ctx, authorization.SubjectRef{Kind: "development_project", ID: project.DevelopmentProjectID}, effect.ConnectionID, project.TenantID)
	case effectRecordAudit:
		if effect.Audit == nil {
			return fmt.Errorf("%w: audit effect payload is missing", ErrDependency)
		}
		return s.audit.RecordDevelopmentProjectAudit(ctx, cloneAuditEvent(*effect.Audit))
	default:
		return fmt.Errorf("%w: unsupported effect kind %q", ErrDependency, effect.Kind)
	}
}

func (s *Service) clearProjectEffect(ctx context.Context, tenantID, projectID, effectID string) error {
	for attempt := 0; attempt < 16; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		project, err := s.repo.Get(ctx, tenantID, projectID)
		if err != nil {
			return err
		}
		index := pendingProjectEffectIndex(project.PendingEffects, effectID)
		if index < 0 {
			return nil
		}
		project.PendingEffects = append(project.PendingEffects[:index:index], project.PendingEffects[index+1:]...)
		if err := s.repo.Update(ctx, project.TenantID, project, project.Version); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return ErrConflict
}

func (s *Service) ReconcilePendingEffects(ctx context.Context, limit int) error {
	if s == nil || s.repo == nil || s.relations == nil || s.audit == nil {
		return ErrDependency
	}
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	const pageSize = 200
	after := s.reconcileAt
	processed := 0
	var reconcileErrors []error
	for processed < limit {
		projects, hasMore, err := s.repo.ListCatalog(ctx, after, pageSize)
		if err != nil {
			return errors.Join(append(reconcileErrors, err)...)
		}
		if len(projects) == 0 {
			s.reconcileAt = ""
			break
		}
		for _, project := range projects {
			after = project.TenantID + "/" + project.DevelopmentProjectID
			s.reconcileAt = after
			if len(project.PendingEffects) == 0 {
				continue
			}
			processed++
			if _, err := s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID); err != nil {
				reconcileErrors = append(reconcileErrors, err)
			}
			if processed >= limit {
				break
			}
		}
		if !hasMore && processed < limit {
			s.reconcileAt = ""
			break
		}
	}
	return errors.Join(reconcileErrors...)
}

func pendingProjectEffectIndex(effects []PendingEffect, effectID string) int {
	for index := range effects {
		if effects[index].EffectID == effectID {
			return index
		}
	}
	return -1
}

func cloneAuditEvent(event AuditEvent) AuditEvent {
	event.Metadata = cloneMetadata(event.Metadata)
	return event
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}
