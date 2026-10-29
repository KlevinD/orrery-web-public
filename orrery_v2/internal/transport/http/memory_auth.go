package httptransport

import (
	"net/http"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/memoryadmin"
	"orrery_v2/internal/app/memorydistill"
)

func memoryViewerFromRequest(r *http.Request) (memoryadmin.ViewerRef, error) {
	viewer, err := ViewerFromRequest(r)
	if err != nil {
		return memoryadmin.ViewerRef{}, memoryadmin.ErrInvalidViewer
	}
	result := memoryadmin.ViewerRef{
		ViewerID:        viewer.ViewerID,
		ViewerKind:      string(viewer.ViewerKind),
		PermissionScope: viewer.PermissionScope,
		RequestID:       correlationID(r),
		IdempotencyKey:  idempotencyKey(r),
	}
	if internalViewerAuthorizationMatches(r) {
		result.AgentID = memoryIdentityID(r, "X-Agent-ID")
		result.OrganizationIDs = memoryIdentityIDs(r, "X-Organization-ID", "X-Organization-IDs")
		result.DepartmentIDs = memoryIdentityIDs(r, "X-Department-ID", "X-Department-IDs")
		result.ProjectIDs = memoryIdentityIDs(r, "X-Project-ID", "X-Project-IDs")
		result.MissionIDs = memoryIdentityIDs(r, "X-Mission-ID", "X-Mission-IDs")
		result.DiscussionIDs = memoryIdentityIDs(r, "X-Discussion-ID", "X-Discussion-IDs")
	}
	return result, nil
}

func (r *Router) memoryViewerFromRequest(req *http.Request) (memoryadmin.ViewerRef, error) {
	viewer, err := memoryViewerFromRequest(req)
	if err != nil || req == nil {
		return viewer, err
	}
	principal, ok := identity.PrincipalFromContext(req.Context())
	if !ok || strings.TrimSpace(principal.AgentProfileID) == "" {
		return viewer, nil
	}
	viewer.AgentID = strings.TrimSpace(principal.AgentProfileID)
	var profile agentcore.AgentProfile
	if r != nil && r.agentProfiles != nil {
		profile, err = r.agentProfiles.GetProfile(req.Context(), agentcore.AgentProfileID(viewer.AgentID))
	} else if r != nil && r.knowledgeProfiles != nil {
		profile, err = r.knowledgeProfiles.GetProfile(req.Context(), agentcore.AgentProfileID(viewer.AgentID))
	} else {
		return viewer, nil
	}
	if err != nil {
		return viewer, nil
	}
	if orgID := strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID)); orgID != "" {
		viewer.OrganizationIDs = appendUniqueMemoryIDs(viewer.OrganizationIDs, []string{orgID})
	}
	if departmentID := strings.TrimSpace(string(profile.OrgIdentity.DepartmentRef.ID)); departmentID != "" {
		viewer.DepartmentIDs = appendUniqueMemoryIDs(viewer.DepartmentIDs, []string{departmentID})
	}
	return viewer, nil
}

func (r *Router) memoryDistillViewerFromRequest(req *http.Request) (memorydistill.ViewerRef, error) {
	viewer, err := r.memoryViewerFromRequest(req)
	if err != nil {
		return memorydistill.ViewerRef{}, memorydistill.ErrInvalidViewer
	}
	return memorydistill.ViewerRef{
		ViewerID:        viewer.ViewerID,
		ViewerKind:      viewer.ViewerKind,
		AgentID:         viewer.AgentID,
		OrganizationIDs: append([]string(nil), viewer.OrganizationIDs...),
		DepartmentIDs:   append([]string(nil), viewer.DepartmentIDs...),
		ProjectIDs:      append([]string(nil), viewer.ProjectIDs...),
		MissionIDs:      append([]string(nil), viewer.MissionIDs...),
		DiscussionIDs:   append([]string(nil), viewer.DiscussionIDs...),
		PermissionScope: append([]string(nil), viewer.PermissionScope...),
		Language:        memoryRequestLanguage(req),
		RequestID:       viewer.RequestID,
		IdempotencyKey:  viewer.IdempotencyKey,
	}, nil
}

func memoryRequestLanguage(req *http.Request) string {
	if req == nil {
		return ""
	}
	for _, preference := range strings.Split(req.Header.Get("Accept-Language"), ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(preference, ";", 2)[0]))
		switch {
		case strings.HasPrefix(tag, "en"):
			return "en-US"
		case strings.HasPrefix(tag, "zh"):
			return "zh-CN"
		}
	}
	return ""
}

func memoryIdentityID(req *http.Request, header string) string {
	if req == nil {
		return ""
	}
	return strings.TrimSpace(req.Header.Get(header))
}

func memoryIdentityIDs(req *http.Request, singleHeader, listHeader string) []string {
	if req == nil {
		return nil
	}
	return appendUniqueMemoryIDs(splitHeader(req.Header.Get(singleHeader)), splitHeader(req.Header.Get(listHeader)))
}

func appendUniqueMemoryIDs(groups ...[]string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, group := range groups {
		for _, id := range group {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}
