package rebac

import "time"

type Ref struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type RelationTuple struct {
	ID        string     `json:"id"`
	TenantID  string     `json:"tenant_id"`
	Subject   Ref        `json:"subject"`
	Relation  string     `json:"relation"`
	Object    Ref        `json:"object"`
	Source    Ref        `json:"source,omitempty"`
	ValidFrom *time.Time `json:"valid_from,omitempty"`
	ValidTo   *time.Time `json:"valid_to,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type RelationCheck struct {
	TenantID string
	Subject  Ref
	Relation string
	Object   Ref
	At       time.Time
}
