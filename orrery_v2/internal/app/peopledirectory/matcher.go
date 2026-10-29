package peopledirectory

import (
	"fmt"
	"sort"
	"strings"
)

func MatchSnapshot(snapshot Snapshot, users []DirectoryUser, people []PersonCandidate) ([]MatchResult, error) {
	if !snapshot.Complete || snapshot.Status != SnapshotStatusComplete {
		return nil, ErrSnapshotIncomplete
	}
	employeeIndex := make(map[string][]PersonCandidate)
	emailIndex := make(map[string][]PersonCandidate)
	relationIndex := make(map[string][]PersonCandidate)
	for _, person := range people {
		if value := normalizeEmployeeNumber(person.EmployeeNumber); value != "" {
			employeeIndex[value] = append(employeeIndex[value], person)
		}
		if value := normalizeEmail(person.EnterpriseEmail); value != "" {
			emailIndex[value] = append(emailIndex[value], person)
		}
		if value := strings.TrimSpace(person.FeishuSubject); value != "" {
			relationIndex[value] = append(relationIndex[value], person)
		}
	}

	results := make([]MatchResult, 0, len(users))
	seenSourceRecords := make(map[string]struct{}, len(users))
	matchedPeople := make(map[string]struct{})
	for _, rawUser := range users {
		user := rawUser.normalized()
		seenSourceRecords[user.SourceRecordID] = struct{}{}
		result := matchDirectoryUser(user, employeeIndex, emailIndex, relationIndex)
		if result.Action == MatchActionIgnore {
			continue
		}
		if result.ProposedPersonID != "" && result.Action != MatchActionConflict {
			matchedPeople[result.ProposedPersonID] = struct{}{}
		}
		results = append(results, result)
	}

	for _, person := range people {
		if !person.Enabled || strings.TrimSpace(person.Source) != strings.TrimSpace(snapshot.Source) || strings.TrimSpace(person.SourceTenantID) != strings.TrimSpace(snapshot.TenantID) {
			continue
		}
		if _, ok := matchedPeople[strings.TrimSpace(person.PersonID)]; ok {
			continue
		}
		sourceRecordID := strings.TrimSpace(person.SourceRecordID)
		if sourceRecordID == "" {
			continue
		}
		if _, present := seenSourceRecords[sourceRecordID]; present {
			continue
		}
		results = append(results, MatchResult{
			SourceRecordID:   sourceRecordID,
			Action:           MatchActionDisable,
			MatchKind:        MatchByMissingFromSnapshot,
			ProposedPersonID: strings.TrimSpace(person.PersonID),
			ApplyStatus:      ApplyStatusPending,
		})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].SourceRecordID < results[j].SourceRecordID })
	return results, nil
}

func matchDirectoryUser(user DirectoryUser, employeeIndex, emailIndex, relationIndex map[string][]PersonCandidate) MatchResult {
	result := MatchResult{SourceRecordID: user.SourceRecordID, ApplyStatus: ApplyStatusPending}
	candidatesByKind := []struct {
		kind       MatchKind
		identifier string
		matches    []PersonCandidate
	}{
		{kind: MatchByEmployeeNumber, identifier: user.EmployeeNumber, matches: employeeIndex[user.EmployeeNumber]},
		{kind: MatchByEnterpriseEmail, identifier: user.EnterpriseEmail, matches: emailIndex[user.EnterpriseEmail]},
		{kind: MatchByFeishuRelation, identifier: user.SourceRecordID, matches: relationIndex[user.SourceRecordID]},
	}
	var selected *PersonCandidate
	var selectedKind MatchKind
	for _, lookup := range candidatesByKind {
		if lookup.identifier == "" || len(lookup.matches) == 0 {
			continue
		}
		if len(lookup.matches) > 1 {
			return conflictResult(user.SourceRecordID, fmt.Sprintf("%s matches multiple People records", lookup.kind))
		}
		if selected == nil {
			candidate := lookup.matches[0]
			selected = &candidate
			selectedKind = lookup.kind
			continue
		}
		if strings.TrimSpace(selected.PersonID) != strings.TrimSpace(lookup.matches[0].PersonID) {
			return conflictResult(user.SourceRecordID, fmt.Sprintf("%s and %s identify different People records", selectedKind, lookup.kind))
		}
	}
	if selected == nil {
		if !user.Active {
			result.Action = MatchActionIgnore
			return result
		}
		if user.EmployeeNumber == "" && user.EnterpriseEmail == "" {
			return conflictResult(user.SourceRecordID, "active directory user has no employee number or enterprise email and requires manual identity resolution")
		}
		result.Action = MatchActionCreate
		result.MatchKind = MatchByNewPerson
		return result
	}
	result.ProposedPersonID = strings.TrimSpace(selected.PersonID)
	result.MatchKind = selectedKind
	if user.Active {
		result.Action = MatchActionUpdate
	} else {
		result.Action = MatchActionDisable
	}
	return result
}

func conflictResult(sourceRecordID, reason string) MatchResult {
	return MatchResult{
		SourceRecordID: sourceRecordID,
		Action:         MatchActionConflict,
		MatchKind:      MatchByConflict,
		ConflictReason: reason,
		ApplyStatus:    ApplyStatusPendingReview,
	}
}
