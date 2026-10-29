package main

import (
	"context"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

type capturingAgentResultCommands struct {
	calls  int
	viewer missioncontrol.ViewerRef
	cmd    missioncontrol.SubmitResultCommand
}

func (c *capturingAgentResultCommands) SubmitAgentResult(_ context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.SubmitResultCommand) (missioncontrol.CommandReceiptDTO, error) {
	c.calls++
	c.viewer = viewer
	c.cmd = cmd
	return missioncontrol.CommandReceiptDTO{CommandID: "submission-1", Status: "accepted"}, nil
}
