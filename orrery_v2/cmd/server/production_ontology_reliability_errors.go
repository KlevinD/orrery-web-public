package main

import (
	"errors"

	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/app/reliability"
)

func ontologyReliabilityError(err error) error {
	switch {
	case errors.Is(err, reliability.ErrInvalidRequest), errors.Is(err, hil.ErrInvalidRequest):
		return ontologycontract.ErrInvalidRequest
	case errors.Is(err, reliability.ErrNotFound), errors.Is(err, hil.ErrNotFound):
		return ontologycontract.ErrUnavailable
	case errors.Is(err, reliability.ErrConflict), errors.Is(err, hil.ErrConflict), errors.Is(err, hil.ErrIdempotencyConflict):
		return ontologycontract.ErrStateConflict
	case errors.Is(err, hil.ErrParticipantRequired), errors.Is(err, hil.ErrDecisionDenied):
		return ontologyscope.ErrAccessDenied
	default:
		return err
	}
}
