package httptransport

import missioncontrol "orrery_v2/internal/app/missioncontrol"

func errServiceDependency() error {
	return missioncontrol.ErrServiceDependency
}
