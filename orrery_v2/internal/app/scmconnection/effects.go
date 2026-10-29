package scmconnection

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"orrery_v2/internal/app/authorization"
)

const (
	effectWriteConnectionCreated = "relation.write_connection_created"
	effectRecordAudit            = "audit.record"
)

func connectionEffectID(connection Connection, kind, discriminator string) string {
	return fmt.Sprintf("scm-connection:%s:%s:v%d:%s:%s", normalizeTenantID(connection.TenantID), connection.ConnectionID, connection.Version, kind, strings.TrimSpace(discriminator))
}

func connectionCreatedEffect(connection Connection, owner authorization.SubjectRef) PendingEffect {
	return PendingEffect{
		EffectID: connectionEffectID(connection, effectWriteConnectionCreated, owner.Kind+":"+owner.ID),
		Kind:     effectWriteConnectionCreated, SubjectKind: owner.Kind, SubjectID: owner.ID,
	}
}

func (s *Service) connectionAuditEffect(connection Connection, viewer ViewerRef, action, result, summary string, metadata map[string]string) PendingEffect {
	effectID := connectionEffectID(connection, effectRecordAudit, action)
	return PendingEffect{
		EffectID: effectID,
		Kind:     effectRecordAudit,
		Audit: &AuditEvent{
			AuditID: effectID, TenantID: connection.TenantID, ActorID: viewerSubjectID(viewer), EntityID: connection.ConnectionID,
			Action: action, Result: result, Summary: summary, Metadata: cloneEffectMetadata(metadata), OccurredAt: s.now(),
		},
	}
}

func appendConnectionEffects(connection *Connection, effects ...PendingEffect) {
	if connection != nil {
		connection.PendingEffects = append(connection.PendingEffects, effects...)
	}
}

func (s *Service) reconcileConnectionEffects(ctx context.Context, tenantID, connectionID string) (Connection, error) {
	if s == nil || s.repo == nil || s.relations == nil || s.audit == nil {
		return Connection{}, ErrDependency
	}
	for {
		connection, err := s.repo.Get(ctx, tenantID, connectionID)
		if err != nil {
			return Connection{}, err
		}
		if len(connection.PendingEffects) == 0 {
			return connection, nil
		}
		effect := connection.PendingEffects[0]
		if err := s.applyConnectionEffect(ctx, connection, effect); err != nil {
			return connection, fmt.Errorf("apply SCM connection effect %q: %w", effect.EffectID, err)
		}
		if err := s.clearConnectionEffect(ctx, connection.TenantID, connection.ConnectionID, effect.EffectID); err != nil {
			return connection, fmt.Errorf("clear SCM connection effect %q: %w", effect.EffectID, err)
		}
	}
}

func (s *Service) applyConnectionEffect(ctx context.Context, connection Connection, effect PendingEffect) error {
	switch effect.Kind {
	case effectWriteConnectionCreated:
		return s.relations.WriteSCMConnectionCreated(ctx, authorization.SubjectRef{Kind: effect.SubjectKind, ID: effect.SubjectID}, connection.ConnectionID, connection.OrganizationID, connection.TenantID)
	case effectRecordAudit:
		if effect.Audit == nil {
			return fmt.Errorf("%w: audit effect payload is missing", ErrDependency)
		}
		return s.audit.RecordSCMConnectionAudit(ctx, cloneEffectAuditEvent(*effect.Audit))
	default:
		return fmt.Errorf("%w: unsupported effect kind %q", ErrDependency, effect.Kind)
	}
}

func (s *Service) clearConnectionEffect(ctx context.Context, tenantID, connectionID, effectID string) error {
	for attempt := 0; attempt < 16; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		connection, err := s.repo.Get(ctx, tenantID, connectionID)
		if err != nil {
			return err
		}
		index := pendingConnectionEffectIndex(connection.PendingEffects, effectID)
		if index < 0 {
			return nil
		}
		connection.PendingEffects = append(connection.PendingEffects[:index:index], connection.PendingEffects[index+1:]...)
		if err := s.repo.Update(ctx, connection.TenantID, connection, connection.Version); err != nil {
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
		connections, hasMore, err := s.repo.ListCatalog(ctx, after, pageSize)
		if err != nil {
			return errors.Join(append(reconcileErrors, err)...)
		}
		if len(connections) == 0 {
			s.reconcileAt = ""
			break
		}
		for _, connection := range connections {
			after = connection.TenantID + "/" + connection.ConnectionID
			s.reconcileAt = after
			if len(connection.PendingEffects) == 0 {
				continue
			}
			processed++
			if _, err := s.reconcileConnectionEffects(ctx, connection.TenantID, connection.ConnectionID); err != nil {
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

func pendingConnectionEffectIndex(effects []PendingEffect, effectID string) int {
	for index := range effects {
		if effects[index].EffectID == effectID {
			return index
		}
	}
	return -1
}

func cloneEffectAuditEvent(event AuditEvent) AuditEvent {
	event.Metadata = cloneEffectMetadata(event.Metadata)
	return event
}

func cloneEffectMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}
