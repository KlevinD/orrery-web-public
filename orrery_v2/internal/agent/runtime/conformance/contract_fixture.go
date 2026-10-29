package conformance

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/session"
)

type ContractFixture struct {
	Contract string         `json:"contract"`
	Name     string         `json:"name"`
	Initial  session.State  `json:"initial"`
	Steps    []ContractStep `json:"steps"`
}

type ContractStep struct {
	Event    agentcore.SessionEvent `json:"event"`
	Expected session.State          `json:"expected"`
}

func DecodeContractFixture(reader io.Reader) (ContractFixture, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var fixture ContractFixture
	if err := decoder.Decode(&fixture); err != nil {
		return ContractFixture{}, err
	}
	if strings.TrimSpace(fixture.Contract) == "" || strings.TrimSpace(fixture.Name) == "" || len(fixture.Steps) == 0 {
		return ContractFixture{}, fmt.Errorf("contract fixture requires contract, name, and steps")
	}
	if err := fixture.Initial.Validate(); err != nil {
		return ContractFixture{}, fmt.Errorf("initial state: %w", err)
	}
	for i, step := range fixture.Steps {
		if err := step.Event.Validate(); err != nil {
			return ContractFixture{}, fmt.Errorf("step %d event: %w", i, err)
		}
		if err := step.Expected.Validate(); err != nil {
			return ContractFixture{}, fmt.Errorf("step %d expected state: %w", i, err)
		}
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return ContractFixture{}, fmt.Errorf("contract fixture contains trailing JSON")
	}
	return fixture, nil
}

// RunContractFixture checks each event against an explicit Orrery contract
// expectation, including intermediate states. No external engine is executed.
func RunContractFixture(fixture ContractFixture) error {
	state := fixture.Initial.Clone()
	for i, fixtureStep := range fixture.Steps {
		step, err := session.Step(state, fixtureStep.Event)
		if err != nil {
			return fmt.Errorf("step %d reducer error: %w", i, err)
		}
		if !reflect.DeepEqual(step.State, fixtureStep.Expected) {
			return fmt.Errorf("step %d state mismatch: got %#v, want %#v", i, step.State, fixtureStep.Expected)
		}
		state = step.State
	}
	return nil
}
