package main

import (
	"context"
	"errors"
	"strings"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missionprojection "orrery_v2/internal/mission/projection"
)

type productionMissionLinkProjectionReader interface {
	GetMissionControlForTenant(context.Context, string, missioncore.MissionID) (missionprojection.ReadModel, error)
}

type productionDiscussionMissionLinkAvailability struct {
	missions productionMissionLinkProjectionReader
}

func newProductionDiscussionMissionLinkAvailability(missions productionMissionLinkProjectionReader) productionDiscussionMissionLinkAvailability {
	return productionDiscussionMissionLinkAvailability{missions: missions}
}

func (p productionDiscussionMissionLinkAvailability) MissionExists(ctx context.Context, tenantID, missionID string) (bool, error) {
	if p.missions == nil || strings.TrimSpace(missionID) == "" {
		return false, missioncontrol.ErrInvalidRequest
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		tenantID = "global"
	}
	_, err := p.missions.GetMissionControlForTenant(ctx, tenantID, missioncore.MissionID(strings.TrimSpace(missionID)))
	if errors.Is(err, missioncontrol.ErrProjectionNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
