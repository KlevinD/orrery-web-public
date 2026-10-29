package irisadapter

import (
	"fmt"
	"strings"

	"orrery_v2/internal/app/actioncontract"
	contextgraph "orrery_v2/internal/app/contextgraph"
)

type Adapter struct{}

func NewAdapter() Adapter {
	return Adapter{}
}

type Snapshot struct {
	SnapshotID            string                              `json:"snapshot_id"`
	TenantID              string                              `json:"tenant_id"`
	Purpose               string                              `json:"purpose"`
	ActorRef              contextgraph.Ref                    `json:"actor_ref"`
	Schema                contextgraph.SchemaDictionary       `json:"schema,omitempty"`
	Nodes                 []Node                              `json:"nodes"`
	Edges                 []Edge                              `json:"edges"`
	Facts                 []Fact                              `json:"facts"`
	Evidence              []Evidence                          `json:"evidence"`
	PermissionConstraints []contextgraph.PermissionConstraint `json:"permission_constraints,omitempty"`
	Confidence            float64                             `json:"confidence,omitempty"`
}

type Node struct {
	ID         string             `json:"id"`
	Kind       string             `json:"kind"`
	Label      string             `json:"label"`
	Properties map[string]any     `json:"properties,omitempty"`
	Evidence   []contextgraph.Ref `json:"evidence_refs,omitempty"`
}

type Edge struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	FromID string `json:"from_id"`
	ToID   string `json:"to_id"`
}

type Fact struct {
	ID        string           `json:"id"`
	Subject   contextgraph.Ref `json:"subject"`
	Predicate string           `json:"predicate"`
	Value     any              `json:"value"`
}

type Evidence struct {
	ID        string           `json:"id"`
	SourceRef contextgraph.Ref `json:"source_ref"`
	Summary   string           `json:"summary"`
}

type ActionActor struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type AcceptedAction struct {
	RunID        string             `json:"run_id"`
	TenantID     string             `json:"tenant_id"`
	WorkspaceID  string             `json:"workspace_id,omitempty"`
	ActionTypeID string             `json:"action_type_id"`
	Actor        ActionActor        `json:"actor"`
	RuntimeID    string             `json:"runtime_id"`
	HumanIntent  contextgraph.Ref   `json:"human_intent_ref,omitempty"`
	ObjectRefs   []contextgraph.Ref `json:"object_refs,omitempty"`
	ArgsHash     string             `json:"args_hash"`
	EvidenceRefs []contextgraph.Ref `json:"evidence_refs,omitempty"`
}

func (a Adapter) ContextBundleToSnapshot(bundle contextgraph.ContextBundle) Snapshot {
	snapshot := Snapshot{
		SnapshotID:            bundle.BundleID,
		TenantID:              bundle.TenantID,
		Purpose:               bundle.Purpose,
		ActorRef:              bundle.ActorRef,
		Schema:                cloneSchemaDictionary(bundle.Schema),
		PermissionConstraints: append([]contextgraph.PermissionConstraint(nil), bundle.PermissionConstraints...),
		Confidence:            bundle.Confidence,
	}
	for _, object := range bundle.Objects {
		snapshot.Nodes = append(snapshot.Nodes, Node{
			ID:         object.ID,
			Kind:       object.ObjectType,
			Label:      object.Label,
			Properties: cloneMap(object.Properties),
			Evidence:   append([]contextgraph.Ref(nil), object.EvidenceRefs...),
		})
	}
	for _, link := range bundle.Links {
		snapshot.Edges = append(snapshot.Edges, Edge{
			ID:     link.ID,
			Kind:   link.LinkType,
			FromID: link.FromObjectID,
			ToID:   link.ToObjectID,
		})
	}
	for _, fact := range bundle.Facts {
		snapshot.Facts = append(snapshot.Facts, Fact{
			ID:        fact.ID,
			Subject:   fact.Subject,
			Predicate: fact.Predicate,
			Value:     fact.Value,
		})
	}
	for _, evidence := range bundle.Evidence {
		snapshot.Evidence = append(snapshot.Evidence, Evidence{
			ID:        evidence.ID,
			SourceRef: evidence.SourceRef,
			Summary:   evidence.Summary,
		})
	}
	return snapshot
}

func (a Adapter) AcceptedActionToStartRun(action AcceptedAction) (actioncontract.StartRunRequest, error) {
	if strings.TrimSpace(action.RunID) == "" || strings.TrimSpace(action.TenantID) == "" || strings.TrimSpace(action.ActionTypeID) == "" || strings.TrimSpace(action.Actor.ID) == "" || strings.TrimSpace(action.RuntimeID) == "" || strings.TrimSpace(action.ArgsHash) == "" {
		return actioncontract.StartRunRequest{}, fmt.Errorf("run_id, tenant_id, action_type_id, actor, runtime_id, and args_hash are required")
	}
	return actioncontract.StartRunRequest{
		RunID:        actioncontract.ActionRunID(strings.TrimSpace(action.RunID)),
		TenantID:     strings.TrimSpace(action.TenantID),
		WorkspaceID:  strings.TrimSpace(action.WorkspaceID),
		ActionTypeID: actioncontract.ActionTypeID(strings.TrimSpace(action.ActionTypeID)),
		Actor: actioncontract.ActionSubject{
			Kind: subjectKind(action.Actor.Kind),
			ID:   strings.TrimSpace(action.Actor.ID),
		},
		Runtime: actioncontract.RuntimeRef{
			Kind: actioncontract.RuntimeIRIS,
			ID:   strings.TrimSpace(action.RuntimeID),
		},
		HumanIntent:  refToActionRef(action.HumanIntent),
		ObjectRefs:   refsToObjectRefs(action.ObjectRefs),
		ArgsHash:     strings.TrimSpace(action.ArgsHash),
		EvidenceRefs: refsToActionRefs(action.EvidenceRefs),
	}, nil
}

func subjectKind(kind string) actioncontract.SubjectKind {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "agent":
		return actioncontract.SubjectAgent
	case "service":
		return actioncontract.SubjectService
	case "worker":
		return actioncontract.SubjectWorker
	default:
		return actioncontract.SubjectHuman
	}
}

func refsToObjectRefs(refs []contextgraph.Ref) []actioncontract.ObjectRef {
	out := make([]actioncontract.ObjectRef, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.ID) == "" {
			continue
		}
		out = append(out, actioncontract.ObjectRef{Kind: strings.TrimSpace(ref.Kind), ID: strings.TrimSpace(ref.ID)})
	}
	return out
}

func refsToActionRefs(refs []contextgraph.Ref) []actioncontract.Ref {
	out := make([]actioncontract.Ref, 0, len(refs))
	for _, ref := range refs {
		if converted := refToActionRef(ref); converted.ID != "" {
			out = append(out, converted)
		}
	}
	return out
}

func refToActionRef(ref contextgraph.Ref) actioncontract.Ref {
	return actioncontract.Ref{Kind: strings.TrimSpace(ref.Kind), ID: strings.TrimSpace(ref.ID)}
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneSchemaDictionary(src contextgraph.SchemaDictionary) contextgraph.SchemaDictionary {
	return contextgraph.SchemaDictionary{
		ObjectTypes:   append([]contextgraph.SchemaObjectType(nil), src.ObjectTypes...),
		LinkTypes:     append([]contextgraph.SchemaLinkType(nil), src.LinkTypes...),
		PropertyTypes: append([]contextgraph.SchemaPropertyType(nil), src.PropertyTypes...),
		ActionTypes:   append([]contextgraph.SchemaActionType(nil), src.ActionTypes...),
		VersionHash:   src.VersionHash,
	}
}
