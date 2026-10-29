package responsibility

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const peopleResponsibilityMaxResponseBytes = 4 << 20

type PeopleResponsibilitySource interface {
	ResolveResponsibilities(context.Context, string) (PeopleResponsibilityResolution, error)
}

type HTTPPeopleResponsibilitySource struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewHTTPPeopleResponsibilitySource(baseURL, token string, client *http.Client) (*HTTPPeopleResponsibilitySource, error) {
	baseURL, token = strings.TrimSpace(baseURL), strings.TrimSpace(token)
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || token == "" {
		return nil, fmt.Errorf("%w: People responsibility source configuration", ErrInvalidContract)
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPPeopleResponsibilitySource{baseURL: strings.TrimRight(baseURL, "/"), token: token, client: client}, nil
}

func (s *HTTPPeopleResponsibilitySource) ResolveResponsibilities(ctx context.Context, subjectID string) (PeopleResponsibilityResolution, error) {
	subjectID = strings.TrimSpace(subjectID)
	if s == nil || s.client == nil || subjectID == "" {
		return PeopleResponsibilityResolution{}, ErrInvalidContract
	}
	requestURL := s.baseURL + "/api/v2/subjects/" + url.PathEscape(subjectID) + "/responsibilities"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return PeopleResponsibilityResolution{}, fmt.Errorf("create People responsibility request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+s.token)
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return PeopleResponsibilityResolution{}, fmt.Errorf("people_source_unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return PeopleResponsibilityResolution{}, fmt.Errorf("people_source_unavailable: status %d", response.StatusCode)
	}
	var resolution PeopleResponsibilityResolution
	decoder := json.NewDecoder(io.LimitReader(response.Body, peopleResponsibilityMaxResponseBytes))
	if err := decoder.Decode(&resolution); err != nil {
		return PeopleResponsibilityResolution{}, fmt.Errorf("decode People responsibility response: %w", err)
	}
	if strings.TrimSpace(resolution.SubjectID) != subjectID {
		return PeopleResponsibilityResolution{}, fmt.Errorf("People responsibility subject mismatch: got %q, want %q", resolution.SubjectID, subjectID)
	}
	return resolution, nil
}
