package httptransport

import (
	"net/http"

	resourceadmin "orrery_v2/internal/app/resourceadmin"
)

func resourceViewerFromRequest(r *http.Request) (resourceadmin.ViewerRef, error) {
	viewer, err := ViewerFromRequest(r)
	if err != nil {
		return resourceadmin.ViewerRef{}, resourceadmin.ErrInvalidViewer
	}
	return resourceadmin.ViewerRef{
		ViewerID:        viewer.ViewerID,
		PermissionScope: viewer.PermissionScope,
	}, nil
}
