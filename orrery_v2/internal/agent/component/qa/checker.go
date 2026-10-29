package qa

import missiontask "orrery_v2/internal/mission/task"

type Checker struct {
	contract QAContract
	policy   QAPolicy
}

type QACheckInput = missiontask.QACheckInput

func NewChecker(contract QAContract, policy QAPolicy) Checker {
	return Checker{contract: contract, policy: policy}
}

func (c Checker) Check(input QACheckInput) QAResult {
	return missiontask.CheckQA(c.contract, c.policy, input, QAResult{Status: QAStatusApproved})
}

func (c Checker) Normalize(result QAResult) QAResult {
	return missiontask.CheckQA(c.contract, c.policy, QACheckInput{TaskID: result.TaskID, SubmissionID: result.SubmissionID, CheckedBy: result.CheckedBy}, result)
}
