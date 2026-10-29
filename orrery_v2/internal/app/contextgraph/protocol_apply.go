package contextgraph

import (
	"context"
	"fmt"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
)

type objectReferenceEnsurer interface {
	EnsureObjectReference(context.Context, Object) error
}

func ApplyProjectionResult(ctx context.Context, store WriterStore, result graphprotocol.ProjectionResult) error {
	return ApplyProjectionResults(ctx, store, []graphprotocol.ProjectionResult{result})
}

func ApplyProjectionResults(ctx context.Context, store WriterStore, results []graphprotocol.ProjectionResult) error {
	if store == nil {
		return fmt.Errorf("context graph store is required")
	}
	if platform, ok := store.(*PlatformStore); ok && platform != nil && platform.tx == nil {
		if platform.store == nil {
			return fmt.Errorf("context graph store is required")
		}
		return platform.store.WithTx(ctx, func(tx db.Tx) error {
			return applyProjectionResults(ctx, NewPlatformStoreTx(tx), results)
		})
	}
	return applyProjectionResults(ctx, store, results)
}

func applyProjectionResults(ctx context.Context, store WriterStore, results []graphprotocol.ProjectionResult) error {
	for _, result := range results {
		if err := applyProjectionResult(ctx, store, result); err != nil {
			return err
		}
	}
	return nil
}

func applyProjectionResult(ctx context.Context, store WriterStore, result graphprotocol.ProjectionResult) error {
	for _, assertion := range result.Assertions {
		switch typed := assertion.(type) {
		case graphprotocol.ObjectAssertion:
			if err := store.SaveEvidence(ctx, evidenceFromProvenance(typed.Object.TenantID, typed.Provenance)); err != nil {
				return err
			}
			if err := store.SaveObject(ctx, objectFromAssertion(typed)); err != nil {
				return err
			}
		case graphprotocol.LinkAssertion:
			if err := store.SaveEvidence(ctx, evidenceFromProvenance(typed.From.TenantID, typed.Provenance)); err != nil {
				return err
			}
			if ensurer, ok := store.(objectReferenceEnsurer); ok {
				for _, reference := range []graphprotocol.ObjectRef{typed.From, typed.To} {
					if err := ensurer.EnsureObjectReference(ctx, objectReferenceFromAssertion(reference, typed.Provenance)); err != nil {
						return err
					}
				}
			}
			if err := store.SaveLink(ctx, linkFromAssertion(typed)); err != nil {
				return err
			}
		case graphprotocol.FactAssertion:
			if err := store.SaveEvidence(ctx, evidenceFromProvenance(typed.Subject.TenantID, typed.Provenance)); err != nil {
				return err
			}
			if err := store.SaveFact(ctx, factFromAssertion(typed)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported graph assertion %T", assertion)
		}
	}
	return nil
}

func objectReferenceFromAssertion(reference graphprotocol.ObjectRef, provenance graphprotocol.ProvenanceRef) Object {
	return Object{
		ID:             reference.ID,
		TenantID:       reference.TenantID,
		ObjectType:     reference.Type,
		CanonicalKey:   "reference:" + reference.Type + ":" + reference.ID,
		Label:          reference.ID,
		Properties:     map[string]any{"_orrery_reference": "pending"},
		Status:         "reference_pending",
		Provenance:     Ref{Kind: "event", ID: provenance.SourceEventID},
		EvidenceRefs:   []Ref{{Kind: "event", ID: provenance.SourceEventID}},
		OwnerAccessRef: accessRefFromProtocol(provenance.SourceAccessRef),
	}
}

func objectFromAssertion(assertion graphprotocol.ObjectAssertion) Object {
	temporal := temporalFromProtocol(assertion.Provenance.Temporal)
	return Object{
		ID:             assertion.Object.ID,
		TenantID:       assertion.Object.TenantID,
		ObjectType:     assertion.Object.Type,
		CanonicalKey:   assertion.CanonicalKey,
		Label:          assertion.Label,
		Properties:     cloneMap(assertion.Properties),
		Provenance:     Ref{Kind: "event", ID: assertion.Provenance.SourceEventID},
		EvidenceRefs:   []Ref{{Kind: "event", ID: assertion.Provenance.SourceEventID}},
		OwnerAccessRef: accessRefFromProtocol(assertion.Provenance.SourceAccessRef),
		SourceStreamID: assertion.Provenance.SourceStreamID,
		SourceVersion:  assertion.Provenance.SourceVersion,
		Temporal:       temporal,
		UpdatedAt:      projectedUpdatedAt(assertion.Provenance),
	}
}

func linkFromAssertion(assertion graphprotocol.LinkAssertion) Link {
	id := assertion.LinkType + ":" + assertion.From.ID + ":" + assertion.To.ID
	if assertion.Identity != "" {
		id = assertion.LinkType + ":" + assertion.Identity
	}
	temporal := temporalFromProtocol(assertion.Provenance.Temporal)
	return Link{
		ID:              id,
		TenantID:        assertion.From.TenantID,
		LinkType:        assertion.LinkType,
		FromObjectID:    assertion.From.ID,
		ToObjectID:      assertion.To.ID,
		Properties:      cloneMap(assertion.Properties),
		Provenance:      Ref{Kind: "event", ID: assertion.Provenance.SourceEventID},
		EvidenceRefs:    []Ref{{Kind: "event", ID: assertion.Provenance.SourceEventID}},
		SourceAccessRef: accessRefFromProtocol(assertion.Provenance.SourceAccessRef),
		SourceStreamID:  assertion.Provenance.SourceStreamID,
		SourceVersion:   assertion.Provenance.SourceVersion,
		Temporal:        temporal,
		UpdatedAt:       projectedUpdatedAt(assertion.Provenance),
	}
}

func factFromAssertion(assertion graphprotocol.FactAssertion) Fact {
	temporal := temporalFromProtocol(assertion.Provenance.Temporal)
	return Fact{
		ID:              assertion.Subject.Type + ":" + assertion.Subject.ID + ":" + assertion.Predicate,
		TenantID:        assertion.Subject.TenantID,
		Subject:         Ref{Kind: assertion.Subject.Type, ID: assertion.Subject.ID},
		Predicate:       assertion.Predicate,
		Value:           assertion.Value,
		Properties:      cloneMap(assertion.Properties),
		Provenance:      Ref{Kind: "event", ID: assertion.Provenance.SourceEventID},
		EvidenceRefs:    []Ref{{Kind: "event", ID: assertion.Provenance.SourceEventID}},
		SourceAccessRef: accessRefFromProtocol(assertion.Provenance.SourceAccessRef),
		SourceStreamID:  assertion.Provenance.SourceStreamID,
		SourceVersion:   assertion.Provenance.SourceVersion,
		Temporal:        temporal,
		UpdatedAt:       projectedUpdatedAt(assertion.Provenance),
	}
}

func temporalFromProtocol(value graphprotocol.TemporalContext) TemporalMetadata {
	return TemporalMetadata{
		Mode:               value.Mode,
		BusinessValidFrom:  value.BusinessValidFrom,
		BusinessValidTo:    value.BusinessValidTo,
		SourceRecordedAt:   value.SourceRecordedAt,
		SourceSupersededAt: value.SourceSupersededAt,
		OrreryIngestedAt:   value.OrreryIngestedAt,
	}
}

func projectedUpdatedAt(provenance graphprotocol.ProvenanceRef) time.Time {
	if !provenance.Temporal.SourceRecordedAt.IsZero() {
		return provenance.Temporal.SourceRecordedAt
	}
	return provenance.ObservedAt
}

func evidenceFromProvenance(tenantID string, provenance graphprotocol.ProvenanceRef) Evidence {
	return Evidence{
		ID:       provenance.SourceEventID,
		TenantID: tenantID,
		SourceRef: Ref{
			Kind: provenance.SourceEventType,
			ID:   provenance.SourceEventID,
		},
		Summary:         provenance.SourceSystem,
		Confidence:      1,
		SourceAccessRef: accessRefFromProtocol(provenance.SourceAccessRef),
		ObservedAt:      provenance.ObservedAt,
	}
}

func accessRefFromProtocol(ref graphprotocol.AccessRef) AccessRef {
	return normalizedAccessRefOrEmpty(AccessRef{Kind: ref.Kind, ID: ref.ID, TenantID: ref.TenantID})
}
