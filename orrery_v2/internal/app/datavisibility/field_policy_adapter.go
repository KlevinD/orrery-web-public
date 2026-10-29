package datavisibility

import (
	"context"
	"fmt"

	"orrery_v2/internal/app/identity"
)

type FieldEffect string

const (
	FieldAllow FieldEffect = "allow"
	FieldMask  FieldEffect = "mask"
)

type FieldClassifier interface {
	ClassifyField(context.Context, identity.Principal, string, ResourceDescriptor, string) (FieldEffect, error)
}

type FieldClassifierFunc func(context.Context, identity.Principal, string, ResourceDescriptor, string) (FieldEffect, error)

func (f FieldClassifierFunc) ClassifyField(ctx context.Context, principal identity.Principal, purpose string, resource ResourceDescriptor, fieldPath string) (FieldEffect, error) {
	return f(ctx, principal, purpose, resource, fieldPath)
}

type FieldPolicyAdapter struct {
	classifier FieldClassifier
	revisions  PolicyRevisionSource
}

func NewFieldPolicyAdapter(classifier FieldClassifier, revisions PolicyRevisionSource) *FieldPolicyAdapter {
	return &FieldPolicyAdapter{classifier: classifier, revisions: revisions}
}

func (a *FieldPolicyAdapter) Evaluate(ctx context.Context, req AdapterRequest) (AdapterDecision, error) {
	if a == nil || a.classifier == nil {
		return AdapterDecision{}, ErrDependencyUnavailable
	}
	revision, err := beginPolicyRevision(ctx, a.revisions, req.Principal)
	if err != nil {
		return AdapterDecision{}, err
	}
	out := AdapterDecision{Coverage: CoverageFields, Resources: make([]AdapterResourceDecision, 0, len(req.Resources))}
	for _, resource := range req.Resources {
		item := AdapterResourceDecision{Key: resource.Key}
		for _, fieldPath := range resource.FieldPaths {
			effect, err := a.classifier.ClassifyField(ctx, identity.NormalizePrincipal(req.Principal), req.Purpose, resource, fieldPath)
			if err != nil {
				return AdapterDecision{}, err
			}
			switch effect {
			case FieldAllow:
				item.AllowedFieldPaths = append(item.AllowedFieldPaths, fieldPath)
			case FieldMask:
				item.MaskedFieldPaths = append(item.MaskedFieldPaths, fieldPath)
			default:
				return AdapterDecision{}, fmt.Errorf("%w: classifier returned invalid effect for %s.%s", ErrIncompleteDecision, resource.Key, fieldPath)
			}
		}
		out.Resources = append(out.Resources, item)
	}
	if a.revisions != nil {
		revision, err = finishPolicyRevision(ctx, a.revisions, req.Principal, revision)
		if err != nil {
			return AdapterDecision{}, err
		}
		out.PolicyRevision = revision
	}
	return out, nil
}
