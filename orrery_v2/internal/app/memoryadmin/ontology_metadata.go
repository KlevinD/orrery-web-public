package memoryadmin

import (
	"context"
	"fmt"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/graphprotocol"
)

type OntologyMemoryRecordMetadataSink struct {
	Runner *ontology.MappingRuleProjectionRunner
}

func (s OntologyMemoryRecordMetadataSink) PublishMemoryRecordMetadata(ctx context.Context, event graphprotocol.DomainEvent) error {
	if s.Runner == nil {
		return fmt.Errorf("%w: ontology metadata runner is required", ErrServiceDependency)
	}
	_, err := s.Runner.ApplyEvent(ctx, event)
	return err
}
