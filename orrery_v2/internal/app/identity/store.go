package identity

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type SubjectStore interface {
	SaveSubject(ctx context.Context, subject IdentitySubject) error
	GetSubject(ctx context.Context, provider string, subject string) (IdentitySubject, error)
	GetSubjectByAgentProfileID(ctx context.Context, provider string, agentProfileID string) (IdentitySubject, error)
}

var ErrSubjectNotFound = errors.New("identity subject not found")

type MemorySubjectStore struct {
	mu       sync.RWMutex
	subjects map[string]IdentitySubject
	byAgent  map[string]string
	now      func() time.Time
}

func NewMemorySubjectStore() *MemorySubjectStore {
	return &MemorySubjectStore{
		subjects: map[string]IdentitySubject{},
		byAgent:  map[string]string{},
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (s *MemorySubjectStore) SaveSubject(ctx context.Context, subject IdentitySubject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("identity subject store is required")
	}
	subject = NormalizeIdentitySubject(subject)
	if subject.Provider == "" || subject.Subject == "" {
		return fmt.Errorf("provider and subject are required")
	}
	now := s.now()
	if subject.ID == "" {
		subject.ID = subject.Provider + ":" + subject.Subject
	}
	if subject.CreatedAt.IsZero() {
		subject.CreatedAt = now
	}
	subject.UpdatedAt = now
	key := subjectKey(subject.Provider, subject.Subject)
	agentKey := subjectAgentKey(subject.Provider, subject.AgentProfileID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.subjects[key]; ok && existing.AgentProfileID != "" && existing.AgentProfileID != subject.AgentProfileID {
		delete(s.byAgent, subjectAgentKey(existing.Provider, existing.AgentProfileID))
	}
	s.subjects[key] = subject
	if subject.AgentProfileID != "" {
		s.byAgent[agentKey] = key
	}
	return nil
}

func (s *MemorySubjectStore) GetSubject(ctx context.Context, provider string, subject string) (IdentitySubject, error) {
	if err := ctx.Err(); err != nil {
		return IdentitySubject{}, err
	}
	if s == nil {
		return IdentitySubject{}, fmt.Errorf("identity subject store is required")
	}
	key := subjectKey(provider, subject)
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.subjects[key]
	if !ok {
		return IdentitySubject{}, ErrSubjectNotFound
	}
	return record, nil
}

func (s *MemorySubjectStore) GetSubjectByID(ctx context.Context, id string) (IdentitySubject, error) {
	if err := ctx.Err(); err != nil {
		return IdentitySubject{}, err
	}
	if s == nil {
		return IdentitySubject{}, fmt.Errorf("identity subject store is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return IdentitySubject{}, ErrSubjectNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.subjects {
		if record.ID == id {
			return record, nil
		}
	}
	return IdentitySubject{}, ErrSubjectNotFound
}

func (s *MemorySubjectStore) GetSubjectByAgentProfileID(ctx context.Context, provider string, agentProfileID string) (IdentitySubject, error) {
	if err := ctx.Err(); err != nil {
		return IdentitySubject{}, err
	}
	if s == nil {
		return IdentitySubject{}, fmt.Errorf("identity subject store is required")
	}
	agentKey := subjectAgentKey(provider, agentProfileID)
	s.mu.RLock()
	defer s.mu.RUnlock()
	key, ok := s.byAgent[agentKey]
	if !ok {
		return IdentitySubject{}, ErrSubjectNotFound
	}
	record, ok := s.subjects[key]
	if !ok {
		return IdentitySubject{}, ErrSubjectNotFound
	}
	return record, nil
}

func (s *MemorySubjectStore) ListSubjects(ctx context.Context, query SubjectQuery) (SubjectPage, error) {
	if err := ctx.Err(); err != nil {
		return SubjectPage{}, err
	}
	if s == nil {
		return SubjectPage{}, fmt.Errorf("identity subject store is required")
	}
	query.Provider = normalizeLower(query.Provider)
	query.Subject = strings.TrimSpace(query.Subject)
	query.AgentProfileID = strings.TrimSpace(query.AgentProfileID)
	query.Status = normalizeLower(query.Status)
	s.mu.RLock()
	items := make([]IdentitySubject, 0, len(s.subjects))
	for _, record := range s.subjects {
		record = NormalizeIdentitySubject(record)
		if query.Provider != "" && record.Provider != query.Provider {
			continue
		}
		if query.Subject != "" && record.Subject != query.Subject {
			continue
		}
		if query.AgentProfileID != "" && record.AgentProfileID != query.AgentProfileID {
			continue
		}
		if query.Status != "" && record.Status != query.Status {
			continue
		}
		items = append(items, record)
	}
	s.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool {
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].ID < items[j].ID
	})
	return subjectPage(items, query.Limit, query.Offset), nil
}

func subjectKey(provider string, subject string) string {
	external := NormalizeExternalIdentity(ExternalIdentity{Provider: provider, Subject: subject})
	return external.Provider + "\x00" + external.Subject
}

func subjectAgentKey(provider string, agentProfileID string) string {
	provider = normalizeLower(provider)
	return provider + "\x00" + strings.TrimSpace(agentProfileID)
}

func subjectPage(items []IdentitySubject, limit int, offset int) SubjectPage {
	total := len(items)
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	if limit <= 0 || offset+limit > total {
		limit = total - offset
	}
	pageItems := items[offset : offset+limit]
	return SubjectPage{
		Subjects: append([]IdentitySubject(nil), pageItems...),
		Limit:    limit,
		Offset:   offset,
		Total:    total,
		Returned: len(pageItems),
		HasMore:  offset+limit < total,
	}
}

var _ SubjectAdminStore = (*MemorySubjectStore)(nil)
