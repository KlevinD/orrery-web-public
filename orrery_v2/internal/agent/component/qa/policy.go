package qa

import missiontask "orrery_v2/internal/mission/task"

type CheckMode = missiontask.CheckMode
type EvidenceStrictness = missiontask.EvidenceStrictness

const (
	CheckModeAutomated = missiontask.CheckModeAutomated
	CheckModeHuman     = missiontask.CheckModeHuman
	CheckModeHybrid    = missiontask.CheckModeHybrid

	EvidenceStrictnessBestEffort = missiontask.EvidenceStrictnessBestEffort
	EvidenceStrictnessRequired   = missiontask.EvidenceStrictnessRequired
	EvidenceStrictnessStrict     = missiontask.EvidenceStrictnessStrict
)

type QAPolicy = missiontask.QAPolicy
