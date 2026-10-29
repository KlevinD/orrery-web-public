package datapolicy

import "time"

const (
	EffectAllow = "allow"
	EffectMask  = "mask"
	EffectDeny  = "deny"
)

type FieldPolicy struct {
	ID         string
	TenantID   string
	Consumer   string
	Purpose    string
	ObjectType string
	FieldPath  string
	Effect     string
	Reason     string
	Enabled    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type PurposePolicy struct {
	ID         string
	TenantID   string
	Consumer   string
	Purpose    string
	ObjectType string
	Effect     string
	Reason     string
	Enabled    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
