package discussionsession

import (
	"context"
	"sync"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

type capturingMissionToolApplier struct {
	called bool
	viewer missioncontrol.ViewerRef
	calls  []ToolCallResult
	links  []LinkedMissionDTO
	err    error
	errors []error
}

type blockingMissionToolApplier struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type capturingMissionMessenger struct {
	called         bool
	viewer         missioncontrol.ViewerRef
	conversationID string
	mission        LinkedMissionDTO
	content        string
	idempotencyKey string
	err            error
}

type capturingExecutionDeleter struct {
	deletedMissionIDs []string
	deletedTasks      []string
	err               error
}

func newBlockingMissionToolApplier() *blockingMissionToolApplier {
	return &blockingMissionToolApplier{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (a *capturingMissionToolApplier) Apply(_ context.Context, viewer missioncontrol.ViewerRef, conversationID string, calls []ToolCallResult, existingMissions ...LinkedMissionDTO) ([]LinkedMissionDTO, error) {
	a.called = true
	a.viewer = viewer
	a.calls = append([]ToolCallResult(nil), calls...)
	err := a.err
	if len(a.errors) > 0 {
		err = a.errors[0]
		a.errors = a.errors[1:]
	}
	return a.links, err
}

func (a *blockingMissionToolApplier) Apply(ctx context.Context, _ missioncontrol.ViewerRef, _ string, _ []ToolCallResult, _ ...LinkedMissionDTO) ([]LinkedMissionDTO, error) {
	a.once.Do(func() { close(a.entered) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.release:
		return nil, nil
	}
}

func (a *blockingMissionToolApplier) Release() {
	close(a.release)
}

func (m *capturingMissionMessenger) PostDiscussionMessage(_ context.Context, viewer missioncontrol.ViewerRef, conversationID string, mission LinkedMissionDTO, content string, idempotencyKey string) error {
	m.called = true
	m.viewer = viewer
	m.conversationID = conversationID
	m.mission = mission
	m.content = content
	m.idempotencyKey = idempotencyKey
	return m.err
}

func (d *capturingExecutionDeleter) DeleteMission(_ context.Context, _ missioncontrol.ViewerRef, missionID string) error {
	d.deletedMissionIDs = append(d.deletedMissionIDs, missionID)
	return d.err
}

func (d *capturingExecutionDeleter) DeleteMissionTask(_ context.Context, _ missioncontrol.ViewerRef, missionID string, taskID string) error {
	d.deletedTasks = append(d.deletedTasks, missionID+"|"+taskID)
	return d.err
}
