package main

import (
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/app/missioncontrol"
	missiondiscussion "orrery_v2/internal/mission/discussion"
	missionevent "orrery_v2/internal/mission/event"
	missiontask "orrery_v2/internal/mission/task"
	missionteam "orrery_v2/internal/mission/team"
	"orrery_v2/internal/platform"
)

func newProductionMissionTaskServices(rt *platform.Runtime) (*missiontask.PlatformRepository, missiontask.EventSink, *missiontask.DeliveryService, *missiontask.QAService) {
	tasks := missiontask.NewPlatformRepository()
	events := improvement.TaskOutcomeEventSink{Next: missionevent.NewTaskEventSink(), Repository: tasks}
	delivery := missiontask.NewDeliveryService(rt.UnitOfWork, tasks, events, missiontask.DeliveryServiceOptions{Clock: rt.Clock, IDGenerator: rt.IDGenerator})
	qa := missiontask.NewQAService(rt.UnitOfWork, tasks, events, missiontask.QAServiceOptions{Clock: rt.Clock, IDGenerator: rt.IDGenerator})
	return tasks, events, delivery, qa
}

func newProductionMissionCollaborationServices(rt *platform.Runtime, profiles agentcore.AgentProfileRepository) (*missiondiscussion.Service, *missionteam.Service) {
	discussions := missiondiscussion.NewService(missiondiscussion.ServiceOptions{
		UnitOfWork: rt.UnitOfWork, Dispatcher: missioncontrol.NewMentionExecutionDispatcher(rt.ExecutionQueue),
		Clock: rt.Clock, IDGenerator: rt.IDGenerator, Logger: rt.Logger,
	})
	staffing := missionteam.NewService(missionteam.ServiceOptions{
		UnitOfWork: rt.UnitOfWork, ProfileRepo: profiles, Provisioner: agentcore.NewStaticInstanceProvisioner(nil),
		Clock: rt.Clock, IDGenerator: rt.IDGenerator, Logger: rt.Logger,
	})
	return discussions, staffing
}
