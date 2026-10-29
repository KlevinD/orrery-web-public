package projectadmin

import (
	"errors"
	"time"
)

const (
	ProjectStatusActive   = "active"
	ProjectStatusArchived = "archived"
)

var (
	ErrServiceDependency = errors.New("project service dependency is not configured")
	ErrInvalidRequest    = errors.New("project request is invalid")
	ErrNotFound          = errors.New("project not found")
	ErrConflict          = errors.New("project version conflict")
)

const (
	PermissionProjectRead   = "project.read"
	PermissionProjectCreate = "project.create"
	PermissionProjectWrite  = "project.write"
	PermissionProjectManage = "project.manage"
)

type ViewerKind string

const (
	ViewerKindUser    ViewerKind = "user"
	ViewerKindAgent   ViewerKind = "agent"
	ViewerKindSystem  ViewerKind = "system"
	ViewerKindService ViewerKind = "service"
)

type ViewerRef struct {
	ViewerID               string     `json:"viewer_id"`
	ViewerKind             ViewerKind `json:"viewer_kind"`
	TenantID               string     `json:"-"`
	PermissionScope        []string   `json:"-"`
	AuthorizationSubjectID string     `json:"-"`
}

type Project struct {
	TenantID       string    `json:"-"`
	ProjectID      string    `json:"project_id"`
	OrganizationID string    `json:"organization_id,omitempty"`
	Title          string    `json:"title"`
	Status         string    `json:"status"`
	Version        int64     `json:"version"`
	CreatedBy      string    `json:"created_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ProjectGraphStatus struct {
	ProjectTenantProjected      bool `json:"project_tenant_projected"`
	OrganizationViewProjected   bool `json:"organization_view_projected"`
	OrganizationObjectProjected bool `json:"organization_object_projected"`
}

type ProjectView struct {
	Project      Project             `json:"project"`
	ContextGraph ProjectGraphStatus  `json:"context_graph"`
	Capabilities ProjectCapabilities `json:"capabilities"`
}

type ProjectCapabilities struct {
	CanView    bool `json:"can_view"`
	CanUpdate  bool `json:"can_update"`
	CanArchive bool `json:"can_archive"`
}

type ProjectPage struct {
	Projects []ProjectView `json:"projects"`
	Page     Page          `json:"page"`
}

type Page struct {
	Limit    int  `json:"limit,omitempty"`
	Offset   int  `json:"offset,omitempty"`
	Total    int  `json:"total"`
	Returned int  `json:"returned"`
	HasMore  bool `json:"has_more"`
}

type ListProjectsQuery struct {
	OrganizationID string
	Status         string
	Limit          int
	Offset         int
}

type CreateProjectCommand struct {
	ProjectID      string `json:"project_id,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`
	Title          string `json:"title"`
}

type UpdateProjectCommand struct {
	ProjectID       string `json:"project_id,omitempty"`
	Title           string `json:"title"`
	ExpectedVersion int64  `json:"expected_version,omitempty"`
}

type ArchiveProjectCommand struct {
	ProjectID       string `json:"project_id,omitempty"`
	ExpectedVersion int64  `json:"expected_version,omitempty"`
}
