package resourceadmin

type ViewerRef struct {
	ViewerID        string
	PermissionScope []string
	RequestID       string
	IdempotencyKey  string
}

const (
	PermissionResourceRead   = "resource.read"
	PermissionResourceManage = "resource.manage"
)

func (v ViewerRef) Validate() error {
	if v.ViewerID == "" {
		return ErrInvalidViewer
	}
	return nil
}

func (v ViewerRef) Can(permission string) bool {
	for _, granted := range v.PermissionScope {
		if granted == permission || granted == PermissionResourceManage {
			return true
		}
	}
	return false
}

func requirePermission(viewer ViewerRef, permission string) error {
	if err := viewer.Validate(); err != nil {
		return err
	}
	if !viewer.Can(permission) {
		return ErrPermissionDenied
	}
	return nil
}
