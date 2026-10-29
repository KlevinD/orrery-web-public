package qa

import missiontask "orrery_v2/internal/mission/task"

type QAResultID = missiontask.QAResultID
type QAStatus = missiontask.QAStatus
type CheckStatus = missiontask.CheckStatus

const (
	QAStatusApproved    = missiontask.QAStatusApproved
	QAStatusRejected    = missiontask.QAStatusRejected
	QAStatusNeedsRework = missiontask.QAStatusNeedsRework
	QAStatusBlocked     = missiontask.QAStatusBlocked

	CheckStatusPassed  = missiontask.CheckStatusPassed
	CheckStatusFailed  = missiontask.CheckStatusFailed
	CheckStatusMissing = missiontask.CheckStatusMissing
	CheckStatusSkipped = missiontask.CheckStatusSkipped
)

type CheckedCriterion = missiontask.CheckedCriterion
type EvidenceCheckResult = missiontask.EvidenceCheckResult
type QAResult = missiontask.QAResultRecord
type AcceptanceReportID = missiontask.AcceptanceReportID
type AcceptanceReport = missiontask.AcceptanceReport
