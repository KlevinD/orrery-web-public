package httptransport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"orrery_v2/internal/app/operations"
)

const (
	defaultPermissionPageSize = 20
	maxPermissionPageSize     = 100
)

type permissionLayeredOperations interface {
	ListPermissionRoles(ctx context.Context, viewer operations.ViewerRef, query operations.PermissionRolePageQuery) (operations.PermissionRolePageDTO, error)
	GetPermissionRole(ctx context.Context, viewer operations.ViewerRef, roleID string) (operations.RoleDefinitionDTO, error)
	ApplyPermissionRoleDefinitions(ctx context.Context, viewer operations.ViewerRef, cmd operations.ApplyPermissionRoleDefinitionsCommand) (operations.ApplyPermissionRoleDefinitionsResult, error)
	ListPermissionEffectiveGrants(ctx context.Context, viewer operations.ViewerRef, query operations.PermissionEffectiveGrantQuery) (operations.PermissionEffectiveGrantPageDTO, error)
}

type PermissionRoleSummaryDTO struct {
	RoleID              string `json:"role_id"`
	LabelZh             string `json:"label_zh"`
	LabelEn             string `json:"label_en"`
	BuiltIn             bool   `json:"built_in"`
	GrantedPackageCount int    `json:"granted_package_count"`
}

type PermissionRolePageDTO struct {
	Items      []PermissionRoleSummaryDTO `json:"items"`
	NextCursor string                     `json:"next_cursor,omitempty"`
	Revision   int64                      `json:"revision"`
}

type PermissionPackageDTO struct {
	PackageID       string   `json:"package_id"`
	PageID          string   `json:"page_id"`
	PageLabelZh     string   `json:"page_label_zh"`
	PageLabelEn     string   `json:"page_label_en"`
	LabelZh         string   `json:"label_zh"`
	LabelEn         string   `json:"label_en"`
	AccessMode      string   `json:"access_mode"`
	Scopes          []string `json:"scopes"`
	CapabilityCount int      `json:"capability_count"`
	GrantState      string   `json:"grant_state"`
}

type PermissionPackagePageDTO struct {
	Items      []PermissionPackageDTO `json:"items"`
	NextCursor string                 `json:"next_cursor,omitempty"`
}

type PermissionPackageCapabilityDTO struct {
	PermissionCapabilityDTO
	Granted bool `json:"granted"`
}

type PermissionPackageCapabilityPageDTO struct {
	Items      []PermissionPackageCapabilityDTO `json:"items"`
	NextCursor string                           `json:"next_cursor,omitempty"`
}

type PermissionPolicyBatchRequestDTO struct {
	ExpectedRevision  int64                           `json:"expected_revision"`
	RoleCreates       []PermissionRoleCreateDTO       `json:"role_creates,omitempty"`
	PackageChanges    []PermissionPackageChangeDTO    `json:"package_changes,omitempty"`
	CapabilityChanges []PermissionCapabilityChangeDTO `json:"capability_changes,omitempty"`
}

type PermissionRoleCreateDTO struct {
	RoleID  string `json:"role_id"`
	LabelZh string `json:"label_zh"`
	LabelEn string `json:"label_en"`
}

type PermissionPackageChangeDTO struct {
	RoleID    string `json:"role_id"`
	PackageID string `json:"package_id"`
	Effect    string `json:"effect"`
}

type PermissionCapabilityChangeDTO struct {
	RoleID       string `json:"role_id"`
	CapabilityID string `json:"capability_id"`
	Effect       string `json:"effect"`
}

type PermissionPolicyBatchResponseDTO struct {
	Revision int64 `json:"revision"`
}

type PermissionEffectiveGrantPageResponseDTO struct {
	Items      []operations.PermissionEffectiveGrantDTO `json:"items"`
	NextCursor string                                   `json:"next_cursor,omitempty"`
}

type permissionEffectiveCursor struct {
	AssignmentID string `json:"assignment_id"`
	ScopeOffset  int    `json:"scope_offset"`
}

type permissionPackageDefinition struct {
	PermissionPackageDTO
	Capabilities []PermissionCapabilityDTO
}

func (r *Router) handleListPermissionRoles(w http.ResponseWriter, req *http.Request) {
	limit, after, err := permissionPage(req)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	viewer, err := permissionPolicyOperationsViewer(req, operations.PermissionPermissionsRead)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	page, err := r.listPermissionRoles(req, viewer, after, limit)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	packages := r.permissionPackages()
	items := make([]PermissionRoleSummaryDTO, 0, len(page.Items))
	for _, role := range page.Items {
		items = append(items, PermissionRoleSummaryDTO{
			RoleID: role.RoleID, LabelZh: firstNonEmpty(role.Name, role.RoleID),
			LabelEn: firstNonEmpty(role.Description, role.Name, role.RoleID), BuiltIn: role.BuiltIn,
			GrantedPackageCount: countGrantedPackages(role, packages),
		})
	}
	response := PermissionRolePageDTO{Items: items, Revision: page.Revision}
	if page.HasMore && len(page.Items) > 0 {
		response.NextCursor = encodePermissionCursor(page.Items[len(page.Items)-1].RoleID)
	}
	writeJSON(w, http.StatusOK, response)
}

func (r *Router) listPermissionRoles(req *http.Request, viewer operations.ViewerRef, after string, limit int) (operations.PermissionRolePageDTO, error) {
	if r == nil || r.operations == nil {
		return operations.PermissionRolePageDTO{}, operations.ErrServiceDependency
	}
	if service, ok := r.operations.(permissionLayeredOperations); ok {
		return service.ListPermissionRoles(req.Context(), viewer, operations.PermissionRolePageQuery{AfterRoleID: after, Limit: limit})
	}
	legacy, err := r.operations.ListRoleDefinitions(req.Context(), viewer)
	if err != nil {
		return operations.PermissionRolePageDTO{}, err
	}
	roles := append([]operations.RoleDefinitionDTO(nil), legacy.Items...)
	sort.SliceStable(roles, func(i, j int) bool { return roles[i].RoleID < roles[j].RoleID })
	filtered := roles[:0]
	for _, role := range roles {
		if role.RoleID > after {
			filtered = append(filtered, role)
		}
	}
	hasMore := len(filtered) > limit
	if hasMore {
		filtered = filtered[:limit]
	}
	return operations.PermissionRolePageDTO{Items: filtered, HasMore: hasMore}, nil
}

func (r *Router) handleListPermissionPackages(w http.ResponseWriter, req *http.Request) {
	limit, after, err := permissionPage(req)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	viewer, err := permissionPolicyOperationsViewer(req, operations.PermissionPermissionsRead)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	role, err := r.optionalPermissionRole(req, viewer, req.URL.Query().Get("role_id"))
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	packages := r.permissionPackages()
	search := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("q")))
	accessMode := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("access_mode")))
	filtered := make([]permissionPackageDefinition, 0, len(packages))
	for _, item := range packages {
		if item.PackageID <= after || (accessMode != "" && accessMode != "all" && item.AccessMode != accessMode) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(strings.Join([]string{item.PackageID, item.LabelZh, item.LabelEn, item.PageLabelZh, item.PageLabelEn, strings.Join(item.Scopes, " ")}, " ")), search) {
			continue
		}
		item.GrantState = permissionPackageGrantState(role, item)
		filtered = append(filtered, item)
	}
	hasMore := len(filtered) > limit
	if hasMore {
		filtered = filtered[:limit]
	}
	items := make([]PermissionPackageDTO, 0, len(filtered))
	for _, item := range filtered {
		items = append(items, item.PermissionPackageDTO)
	}
	response := PermissionPackagePageDTO{Items: items}
	if hasMore && len(items) > 0 {
		response.NextCursor = encodePermissionCursor(items[len(items)-1].PackageID)
	}
	writeJSON(w, http.StatusOK, response)
}

func (r *Router) handleListPermissionPackageCapabilities(w http.ResponseWriter, req *http.Request, packageID string) {
	limit, after, err := permissionPage(req)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	viewer, err := permissionPolicyOperationsViewer(req, operations.PermissionPermissionsRead)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	role, err := r.optionalPermissionRole(req, viewer, req.URL.Query().Get("role_id"))
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	var selected *permissionPackageDefinition
	packages := r.permissionPackages()
	for index := range packages {
		if packages[index].PackageID == strings.TrimSpace(packageID) {
			selected = &packages[index]
			break
		}
	}
	if selected == nil {
		writeMappedError(w, req, operations.ErrNotFound)
		return
	}
	grants := permissionRoleScopeSet(role)
	capabilities := make([]PermissionPackageCapabilityDTO, 0, len(selected.Capabilities))
	for _, capability := range selected.Capabilities {
		if capability.CapabilityID <= after {
			continue
		}
		_, granted := grants[capability.Scope]
		capabilities = append(capabilities, PermissionPackageCapabilityDTO{PermissionCapabilityDTO: capability, Granted: granted})
	}
	hasMore := len(capabilities) > limit
	if hasMore {
		capabilities = capabilities[:limit]
	}
	response := PermissionPackageCapabilityPageDTO{Items: capabilities}
	if hasMore && len(capabilities) > 0 {
		response.NextCursor = encodePermissionCursor(capabilities[len(capabilities)-1].CapabilityID)
	}
	writeJSON(w, http.StatusOK, response)
}

func (r *Router) handleApplyPermissionPolicyBatch(w http.ResponseWriter, req *http.Request) {
	var body PermissionPolicyBatchRequestDTO
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeMappedError(w, req, fmt.Errorf("%w: request body is invalid", operations.ErrInvalidRequest))
		return
	}
	if body.ExpectedRevision < 0 || len(body.RoleCreates)+len(body.PackageChanges)+len(body.CapabilityChanges) == 0 {
		writeMappedError(w, req, fmt.Errorf("%w: expected_revision and at least one change are required", operations.ErrInvalidRequest))
		return
	}
	viewer, err := permissionPolicyOperationsViewer(req, operations.PermissionPermissionsRead, operations.PermissionPermissionsManage, operations.PermissionRolesRead, operations.PermissionRolesManage)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	packages := map[string]permissionPackageDefinition{}
	for _, item := range r.permissionPackages() {
		packages[item.PackageID] = item
	}
	capabilities := map[string]PermissionCapabilityDTO{}
	for _, item := range r.permissions {
		capabilities[item.CapabilityID] = item
	}
	roles := map[string]operations.RoleDefinitionDTO{}
	for _, create := range body.RoleCreates {
		roleID := strings.ToLower(strings.TrimSpace(create.RoleID))
		if roleID == "" || strings.TrimSpace(create.LabelZh) == "" {
			writeMappedError(w, req, fmt.Errorf("%w: role_id and label_zh are required", operations.ErrInvalidRequest))
			return
		}
		if _, exists := roles[roleID]; exists {
			writeMappedError(w, req, fmt.Errorf("%w: duplicate role_id %s", operations.ErrInvalidRequest, roleID))
			return
		}
		roles[roleID] = operations.RoleDefinitionDTO{
			RoleID: roleID, Name: strings.TrimSpace(create.LabelZh), Description: firstNonEmpty(strings.TrimSpace(create.LabelEn), strings.TrimSpace(create.LabelZh)),
			Category: "custom", Status: "active", AssignableActorTypes: []string{"human", "agent", "squad"},
		}
	}
	loadRole := func(roleID string) (operations.RoleDefinitionDTO, error) {
		roleID = strings.ToLower(strings.TrimSpace(roleID))
		if role, ok := roles[roleID]; ok {
			return role, nil
		}
		role, err := r.getPermissionRole(req, viewer, roleID)
		if err != nil {
			return operations.RoleDefinitionDTO{}, err
		}
		if role.BuiltIn {
			return operations.RoleDefinitionDTO{}, fmt.Errorf("%w: built-in role %s is read-only", operations.ErrInvalidRequest, roleID)
		}
		roles[roleID] = role
		return role, nil
	}
	for _, change := range body.PackageChanges {
		item, ok := packages[strings.TrimSpace(change.PackageID)]
		if !ok {
			writeMappedError(w, req, fmt.Errorf("%w: unknown package_id %s", operations.ErrInvalidRequest, change.PackageID))
			return
		}
		role, err := loadRole(change.RoleID)
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		role.PermissionRefs, err = applyPermissionScopeEffect(role.PermissionRefs, item.Scopes, change.Effect)
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		roles[role.RoleID] = role
	}
	for _, change := range body.CapabilityChanges {
		item, ok := capabilities[strings.TrimSpace(change.CapabilityID)]
		if !ok {
			writeMappedError(w, req, fmt.Errorf("%w: unknown capability_id %s", operations.ErrInvalidRequest, change.CapabilityID))
			return
		}
		role, err := loadRole(change.RoleID)
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		role.PermissionRefs, err = applyPermissionScopeEffect(role.PermissionRefs, []string{item.Scope}, change.Effect)
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		roles[role.RoleID] = role
	}
	items := make([]operations.RoleDefinitionDTO, 0, len(roles))
	for _, role := range roles {
		items = append(items, role)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].RoleID < items[j].RoleID })
	revision, err := r.applyPermissionRoles(req, viewer, body.ExpectedRevision, items)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, PermissionPolicyBatchResponseDTO{Revision: revision})
}

func (r *Router) handleListEffectivePermissionGrants(w http.ResponseWriter, req *http.Request) {
	limit := defaultPermissionPageSize
	if raw := strings.TrimSpace(req.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > maxPermissionPageSize {
			writeMappedError(w, req, fmt.Errorf("%w: limit must be between 1 and 100", operations.ErrInvalidRequest))
			return
		}
		limit = parsed
	}
	cursor, err := decodePermissionEffectiveCursor(req.URL.Query().Get("cursor"))
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	viewer, err := permissionPolicyOperationsViewer(req, operations.PermissionPermissionsRead)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	if r == nil || r.operations == nil {
		writeMappedError(w, req, operations.ErrServiceDependency)
		return
	}
	service, ok := r.operations.(permissionLayeredOperations)
	if !ok {
		view, viewErr := r.authoritativePermissionPolicyView(req)
		if viewErr != nil {
			writeMappedError(w, req, viewErr)
			return
		}
		items := make([]operations.PermissionEffectiveGrantDTO, 0, limit)
		for _, grant := range view.Effective {
			if len(items) == limit {
				break
			}
			items = append(items, operations.PermissionEffectiveGrantDTO{
				PrincipalKind: grant.PrincipalKind, PrincipalID: grant.PrincipalID,
				Scope: grant.Scope, Source: grant.Source, SourceID: grant.SourceID,
			})
		}
		writeJSON(w, http.StatusOK, PermissionEffectiveGrantPageResponseDTO{Items: items})
		return
	}
	page, err := service.ListPermissionEffectiveGrants(req.Context(), viewer, operations.PermissionEffectiveGrantQuery{
		AssignmentID: cursor.AssignmentID, ScopeOffset: cursor.ScopeOffset, Limit: limit,
	})
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	response := PermissionEffectiveGrantPageResponseDTO{Items: page.Items}
	if page.HasMore {
		response.NextCursor = encodePermissionEffectiveCursor(permissionEffectiveCursor{AssignmentID: page.NextAssignmentID, ScopeOffset: page.NextScopeOffset})
	}
	writeJSON(w, http.StatusOK, response)
}

func (r *Router) optionalPermissionRole(req *http.Request, viewer operations.ViewerRef, roleID string) (operations.RoleDefinitionDTO, error) {
	if strings.TrimSpace(roleID) == "" {
		return operations.RoleDefinitionDTO{}, nil
	}
	return r.getPermissionRole(req, viewer, roleID)
}

func (r *Router) getPermissionRole(req *http.Request, viewer operations.ViewerRef, roleID string) (operations.RoleDefinitionDTO, error) {
	if r == nil || r.operations == nil {
		return operations.RoleDefinitionDTO{}, operations.ErrServiceDependency
	}
	if service, ok := r.operations.(permissionLayeredOperations); ok {
		return service.GetPermissionRole(req.Context(), viewer, strings.TrimSpace(roleID))
	}
	page, err := r.operations.ListRoleDefinitions(req.Context(), viewer)
	if err != nil {
		return operations.RoleDefinitionDTO{}, err
	}
	for _, role := range page.Items {
		if role.RoleID == strings.TrimSpace(roleID) {
			return role, nil
		}
	}
	return operations.RoleDefinitionDTO{}, operations.ErrNotFound
}

func (r *Router) applyPermissionRoles(req *http.Request, viewer operations.ViewerRef, expectedRevision int64, roles []operations.RoleDefinitionDTO) (int64, error) {
	if r == nil || r.operations == nil {
		return 0, operations.ErrServiceDependency
	}
	if service, ok := r.operations.(permissionLayeredOperations); ok {
		result, err := service.ApplyPermissionRoleDefinitions(req.Context(), viewer, operations.ApplyPermissionRoleDefinitionsCommand{ExpectedRevision: expectedRevision, Roles: roles})
		return result.Revision, err
	}
	_, err := r.operations.PutRoleDefinitions(req.Context(), viewer, operations.PutRoleDefinitionsCommand{Roles: roles})
	if err != nil {
		return 0, err
	}
	return expectedRevision + 1, nil
}

func (r *Router) permissionPackages() []permissionPackageDefinition {
	byID := map[string]*permissionPackageDefinition{}
	for _, capability := range r.permissions {
		pageID, pageZh, pageEn := permissionPackagePage(capability.Domain)
		kind := permissionPackageKind(capability)
		packageID := pageID + "." + kind
		item := byID[packageID]
		if item == nil {
			actionZh, actionEn := permissionPackageAction(kind)
			subjectZh, subjectEn := permissionPackageSubject(capability, pageZh, pageEn)
			item = &permissionPackageDefinition{PermissionPackageDTO: PermissionPackageDTO{
				PackageID: packageID, PageID: pageID, PageLabelZh: pageZh, PageLabelEn: pageEn,
				LabelZh: subjectZh + actionZh, LabelEn: strings.TrimSpace(subjectEn + " " + actionEn),
				AccessMode: capability.AccessMode,
			}}
			byID[packageID] = item
		}
		if capability.AccessMode != "read" {
			item.AccessMode = "write"
		}
		if !slices.Contains(item.Scopes, capability.Scope) {
			item.Scopes = append(item.Scopes, capability.Scope)
		}
		item.Capabilities = append(item.Capabilities, capability)
	}
	items := make([]permissionPackageDefinition, 0, len(byID))
	for _, item := range byID {
		sort.Strings(item.Scopes)
		sort.SliceStable(item.Capabilities, func(i, j int) bool { return item.Capabilities[i].CapabilityID < item.Capabilities[j].CapabilityID })
		item.CapabilityCount = len(item.Capabilities)
		items = append(items, *item)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].PackageID < items[j].PackageID })
	return items
}

func permissionPackagePage(domain string) (string, string, string) {
	switch strings.TrimSpace(domain) {
	case "mission", "project", "agent":
		return "mission", "任务", "Mission"
	case "approval":
		return "approval", "审批中心", "Approval center"
	case "permissions", "authorization", "identity":
		return "permission", "权限中心", "Permission center"
	case "organization", "workforce", "roles":
		return "organization", "组织中心", "Organization center"
	case "resource", "tool":
		return "resource", "资源中心", "Resource center"
	case "memory":
		return "memory", "记忆中心", "Memory center"
	case "knowledge":
		return "knowledge", "知识中心", "Knowledge center"
	case "settings":
		return "settings", "系统设置", "Settings"
	case "discussion":
		return "discussion", "聊天", "Chat"
	case "scheduler":
		return "scheduler", "自动化", "Scheduler"
	case "todo":
		return "todo", "待办", "Todo"
	case "idea":
		return "idea", "想法", "Ideas"
	case "audit":
		return "audit", "审计中心", "Audit center"
	case "operations":
		return "evaluation", "评估中心", "Evaluation center"
	case "ontology_scope", "ontology_schema", "ontology_context", "ontology_lineage", "ontology_governance", "ontology_projection":
		return "ontology", "本体中心", "Ontology center"
	default:
		return strings.TrimSpace(domain), strings.TrimSpace(domain), strings.TrimSpace(domain)
	}
}

func permissionPackageKind(capability PermissionCapabilityDTO) string {
	scope := strings.TrimSpace(capability.Scope)
	parts := strings.Split(scope, ".")
	last := parts[len(parts)-1]
	switch {
	case scope == "tool.call" || last == "call":
		return "call"
	case last == "decide":
		return "decide"
	case last == "run":
		return "run"
	case last == "review":
		return "review"
	case last == "distill":
		return "distill"
	case last == "read" || capability.AccessMode == "read":
		return "read"
	default:
		return "write"
	}
}

func permissionPackageAction(kind string) (string, string) {
	switch kind {
	case "call":
		return "调用", "Call"
	case "decide":
		return "处理", "Decision"
	case "run":
		return "运行", "Run"
	case "review":
		return "审核", "Review"
	case "distill":
		return "沉淀", "Distill"
	case "read":
		return "查看", "Read"
	default:
		return "编辑", "Edit"
	}
}

func permissionPackageSubject(capability PermissionCapabilityDTO, pageZh, pageEn string) (string, string) {
	zh := strings.TrimSuffix(strings.TrimSpace(capability.CategoryZh), "中心")
	en := strings.TrimSpace(capability.CategoryEn)
	if zh == "" {
		zh = strings.TrimSuffix(pageZh, "中心")
	}
	if en == "" {
		en = strings.TrimSuffix(pageEn, " center")
	}
	return zh, en
}

func permissionPackageGrantState(role operations.RoleDefinitionDTO, item permissionPackageDefinition) string {
	grants := permissionRoleScopeSet(role)
	granted := 0
	for _, capability := range item.Capabilities {
		if _, ok := grants[capability.Scope]; ok {
			granted++
		}
	}
	if granted == 0 {
		return "none"
	}
	if granted == len(item.Capabilities) {
		return "granted"
	}
	return "partial"
}

func countGrantedPackages(role operations.RoleDefinitionDTO, packages []permissionPackageDefinition) int {
	count := 0
	for _, item := range packages {
		if permissionPackageGrantState(role, item) != "none" {
			count++
		}
	}
	return count
}

func permissionRoleScopeSet(role operations.RoleDefinitionDTO) map[string]struct{} {
	out := map[string]struct{}{}
	for _, scope := range role.PermissionRefs {
		if scope = strings.TrimSpace(scope); scope != "" {
			out[scope] = struct{}{}
		}
	}
	return out
}

func applyPermissionScopeEffect(current []string, scopes []string, effect string) ([]string, error) {
	set := map[string]struct{}{}
	for _, scope := range current {
		if scope = strings.TrimSpace(scope); scope != "" {
			set[scope] = struct{}{}
		}
	}
	switch strings.ToLower(strings.TrimSpace(effect)) {
	case "grant":
		for _, scope := range scopes {
			if scope = strings.TrimSpace(scope); scope != "" {
				set[scope] = struct{}{}
			}
		}
	case "revoke":
		for _, scope := range scopes {
			delete(set, strings.TrimSpace(scope))
		}
	default:
		return nil, fmt.Errorf("%w: effect must be grant or revoke", operations.ErrInvalidRequest)
	}
	out := make([]string, 0, len(set))
	for scope := range set {
		out = append(out, scope)
	}
	sort.Strings(out)
	return out, nil
}

func permissionPage(req *http.Request) (int, string, error) {
	limit := defaultPermissionPageSize
	if raw := strings.TrimSpace(req.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > maxPermissionPageSize {
			return 0, "", fmt.Errorf("%w: limit must be between 1 and 100", operations.ErrInvalidRequest)
		}
		limit = parsed
	}
	after, err := decodePermissionCursor(req.URL.Query().Get("cursor"))
	if err != nil {
		return 0, "", err
	}
	return limit, after, nil
}

func encodePermissionCursor(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodePermissionCursor(cursor string) (string, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return "", nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(payload) == 0 || len(payload) > 512 {
		return "", fmt.Errorf("%w: cursor is invalid", operations.ErrInvalidRequest)
	}
	return string(payload), nil
}

func encodePermissionEffectiveCursor(cursor permissionEffectiveCursor) string {
	payload, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodePermissionEffectiveCursor(cursor string) (permissionEffectiveCursor, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return permissionEffectiveCursor{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(payload) == 0 || len(payload) > 1024 {
		return permissionEffectiveCursor{}, fmt.Errorf("%w: cursor is invalid", operations.ErrInvalidRequest)
	}
	var decoded permissionEffectiveCursor
	if err := json.Unmarshal(payload, &decoded); err != nil || decoded.AssignmentID == "" || decoded.ScopeOffset < 0 {
		return permissionEffectiveCursor{}, fmt.Errorf("%w: cursor is invalid", operations.ErrInvalidRequest)
	}
	return decoded, nil
}

func writeMappedError(w http.ResponseWriter, req *http.Request, err error) {
	status, body := MapError(err, correlationID(req))
	writeJSON(w, status, body)
}
