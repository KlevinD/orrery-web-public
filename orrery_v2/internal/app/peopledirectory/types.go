package peopledirectory

import (
	"errors"
	"strings"
)

const (
	SourceFeishu = "people.feishu"

	SnapshotStatusScanning = "scanning"
	SnapshotStatusComplete = "complete"
	SnapshotStatusFailed   = "failed"

	ApplyStatusPending       = "pending"
	ApplyStatusPendingReview = "pending_review"
	ApplyStatusApplied       = "applied"
)

var (
	ErrSnapshotNotFound   = errors.New("People directory snapshot not found")
	ErrSnapshotIncomplete = errors.New("People directory snapshot is incomplete")
	ErrMatchConflict      = errors.New("People directory match state conflicts")
)

type Snapshot struct {
	ID        string
	Source    string
	TenantID  string
	Status    string
	Complete  bool
	PageCount int
	LastError string
}

type DirectoryUser struct {
	SourceRecordID  string
	EmployeeNumber  string
	Name            string
	Email           string
	EnterpriseEmail string
	Active          bool
}

func (u DirectoryUser) normalized() DirectoryUser {
	u.SourceRecordID = strings.TrimSpace(u.SourceRecordID)
	u.EmployeeNumber = normalizeEmployeeNumber(u.EmployeeNumber)
	u.Name = strings.TrimSpace(u.Name)
	u.Email = normalizeEmail(u.Email)
	u.EnterpriseEmail = normalizeEmail(u.EnterpriseEmail)
	return u
}

type DirectoryPage struct {
	Users         []DirectoryUser
	HasMore       bool
	NextPageToken string
}

type PersonCandidate struct {
	PersonID        string
	EmployeeNumber  string
	EnterpriseEmail string
	FeishuSubject   string
	Source          string
	SourceRecordID  string
	SourceTenantID  string
	Enabled         bool
}

type MatchAction string

const (
	MatchActionIgnore   MatchAction = "ignore"
	MatchActionCreate   MatchAction = "create"
	MatchActionUpdate   MatchAction = "update"
	MatchActionDisable  MatchAction = "disable"
	MatchActionConflict MatchAction = "conflict"
)

type MatchKind string

const (
	MatchByEmployeeNumber      MatchKind = "employee_number"
	MatchByEnterpriseEmail     MatchKind = "enterprise_email"
	MatchByFeishuRelation      MatchKind = "kratos_feishu_relation"
	MatchByNewPerson           MatchKind = "new_person"
	MatchByMissingFromSnapshot MatchKind = "missing_from_snapshot"
	MatchByConflict            MatchKind = "conflict"
)

type MatchResult struct {
	SourceRecordID   string
	Action           MatchAction
	MatchKind        MatchKind
	ProposedPersonID string
	ConflictReason   string
	ApplyStatus      string
}

func normalizeEmployeeNumber(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
