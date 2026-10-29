package httptransport

import (
	"encoding/json"
	"io"
	"net/http"

	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

const maxOntologyMutationBodyBytes = 64 << 10

func decodeOntologyMutationBody(req *http.Request, target any) error {
	if req == nil || req.Body == nil || target == nil {
		return ontologycontract.ErrInvalidRequest
	}
	limited := &io.LimitedReader{R: req.Body, N: maxOntologyMutationBodyBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ontologycontract.ErrInvalidRequest
	}
	if limited.N <= 0 {
		return ontologycontract.ErrInvalidRequest
	}
	return nil
}

func ontologyMutationOriginAllowed(req *http.Request) bool {
	if authCredentialFromRequest(req).source != authCredentialCookie {
		return true
	}
	return cookieMutationOriginAllowed(req)
}
