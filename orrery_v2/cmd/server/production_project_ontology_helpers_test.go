package main

import (
	"testing"
	"time"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/graphprotocol"
)

type fixedProjectClock struct {
	now time.Time
}

func (c fixedProjectClock) Now() time.Time {
	return c.now
}

type sequenceProjectIDs []string

func (g sequenceProjectIDs) NewIDContext() (string, error) {
	if len(g) == 0 {
		return "", nil
	}
	return g[0], nil
}

type steppingProjectClock struct {
	values []time.Time
}

func (c *steppingProjectClock) Now() time.Time {
	if len(c.values) == 0 {
		return time.Unix(0, 0).UTC()
	}
	value := c.values[0]
	c.values = c.values[1:]
	return value
}

func assertOntologyEvent(t *testing.T, events []graphprotocol.DomainEvent, eventID, eventType string) {
	t.Helper()
	for _, event := range events {
		if event.ID == eventID {
			if event.Type != eventType {
				t.Fatalf("ontology event %s type = %q, want %q", eventID, event.Type, eventType)
			}
			return
		}
	}
	t.Fatalf("ontology event %s missing in %+v", eventID, events)
}

func assertNoOntologyEvent(t *testing.T, events []graphprotocol.DomainEvent, eventID, eventType string) {
	t.Helper()
	for _, event := range events {
		if event.ID == eventID && event.Type == eventType {
			t.Fatalf("ontology event %s/%s unexpectedly present in %+v", eventID, eventType, events)
		}
	}
}

func assertOntologyEventType(t *testing.T, events []graphprotocol.DomainEvent, eventType string) graphprotocol.DomainEvent {
	t.Helper()
	for _, event := range events {
		if event.Type == eventType {
			return event
		}
	}
	t.Fatalf("ontology event type %s missing in %+v", eventType, events)
	return graphprotocol.DomainEvent{}
}

func assertOntologyEventPayload(t *testing.T, events []graphprotocol.DomainEvent, eventID, key string, want any) {
	t.Helper()
	for _, event := range events {
		if event.ID == eventID {
			if got := event.Payload[key]; got != want {
				t.Fatalf("ontology event %s payload[%s] = %#v, want %#v", eventID, key, got, want)
			}
			return
		}
	}
	t.Fatalf("ontology event %s missing in %+v", eventID, events)
}

func registrySnapshotObjectType(snapshot ontology.RegistrySnapshot, objectTypeID string) (ontology.ObjectType, bool) {
	for _, objectType := range snapshot.ObjectTypes {
		if objectType.ID == objectTypeID {
			return objectType, true
		}
	}
	return ontology.ObjectType{}, false
}

func registrySnapshotActionType(snapshot ontology.RegistrySnapshot, actionTypeID string) (ontology.ActionTypeSummary, bool) {
	for _, actionType := range snapshot.ActionTypes {
		if actionType.ID == actionTypeID {
			return actionType, true
		}
	}
	return ontology.ActionTypeSummary{}, false
}

func metadataEvidenceHasModelRun(refs []ontology.MetadataEvidenceRef, runID string) bool {
	for _, ref := range refs {
		if ref.Kind == "model_run" &&
			ref.ID == runID &&
			ref.Metadata["model"] != "" &&
			ref.Metadata["version"] != "" &&
			ref.Metadata["input_hash"] != "" &&
			ref.Metadata["prompt_hash"] != "" {
			return true
		}
	}
	return false
}

func productionFloatPtrEqual(value *float64, want float64) bool {
	return value != nil && *value == want
}
